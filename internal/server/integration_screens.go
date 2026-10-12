package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/audit"
	"pocketadmin/internal/integration"
	"pocketadmin/internal/permission"
)

// registerIntegrationRoutes mounts the provider screens.
func (s *Server) registerIntegrationRoutes(panel *gin.RouterGroup) {
	s.readGuarded(panel, http.MethodGet, "/integrations", permission.IntegrationProviderList, s.handleIntegrationList)
	s.readGuarded(panel, http.MethodGet, "/integrations/new", permission.IntegrationProviderCreate, s.handleIntegrationForm)
	s.readGuarded(panel, http.MethodGet, "/integrations/:id/edit", permission.IntegrationProviderUpdate, s.handleIntegrationForm)
	s.readGuarded(panel, http.MethodGet, "/integrations/logs", permission.IntegrationLogList, s.handleIntegrationLogs)

	s.guarded(panel, http.MethodPost, "/integrations/save", permission.IntegrationProviderCreate, s.handleIntegrationSave)
	s.guarded(panel, http.MethodPost, "/integrations/:id/default", permission.IntegrationProviderUpdate, s.handleIntegrationDefault)
	s.guarded(panel, http.MethodPost, "/integrations/:id/test", permission.IntegrationProviderTest, s.handleIntegrationTest)
	s.guarded(panel, http.MethodPost, "/integrations/:id/status", permission.IntegrationProviderUpdate, s.handleIntegrationStatus)
	s.guarded(panel, http.MethodPost, "/integrations/:id/delete", permission.IntegrationProviderDelete, s.handleIntegrationDelete)
}

// handleIntegrationList renders the providers grouped by type.
func (s *Server) handleIntegrationList(c *gin.Context) {
	records, err := s.app.FindRecordsByFilter(integration.CollectionProviders,
		"deletedAt = ''", "type,-isDefault,priority", 200, 0)
	if err != nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Could not load the providers", err.Error())
		return
	}

	groups := make([]integrationGroup, 0, len(integration.Types()))
	byType := map[string][]*core.Record{}
	for _, rec := range records {
		byType[rec.GetString("type")] = append(byType[rec.GetString("type")], rec)
	}

	for _, name := range integration.Types() {
		group := integrationGroup{Type: name, Label: integration.TypeLabel(integration.Type(name))}
		for _, rec := range byType[name] {
			driver, _ := integration.LookupDriver(rec.GetString("driver"))
			row := integrationRow{
				Record:     rec,
				Name:       rec.GetString("name"),
				Code:       rec.GetString("code"),
				Driver:     driver.Name,
				DriverCode: rec.GetString("driver"),
				Status:     rec.GetString("status"),
				IsDefault:  rec.GetBool("isDefault"),
				Priority:   rec.GetInt("priority"),
				LastTest:   rec.GetString("lastTestResult"),
				LastTestAt: rec.GetString("lastTestMessage"),
				Testable:   driver.Testable,
				Sandbox:    rec.GetBool("sandbox"),
				SecretKeys: integration.SortedKeys(integration.DecryptSecrets(s.app, rec.GetString("secrets"))),
			}
			if at := rec.GetDateTime("lastTestAt"); !at.IsZero() {
				row.LastTestWhen = relativeTime(at)
			}
			group.Rows = append(group.Rows, row)
		}
		groups = append(groups, group)
	}

	actor := s.actor(c)
	s.render(c, http.StatusOK, "integrations.html", integrationsPage{
		base:      s.newBase(c, "Integrations", "integrations"),
		Groups:    groups,
		CanCreate: actor.Can(permission.IntegrationProviderCreate),
		CanUpdate: actor.Can(permission.IntegrationProviderUpdate),
		CanTest:   actor.Can(permission.IntegrationProviderTest),
		CanDelete: actor.Can(permission.IntegrationProviderDelete),
	})
}

// handleIntegrationForm renders the editor for a new or existing provider.
func (s *Server) handleIntegrationForm(c *gin.Context) {
	view := integrationForm{
		base:       s.newBase(c, "New provider", "integrations"),
		IsNew:      true,
		Types:      integration.Types(),
		TypeLabels: map[string]string{},
		Drivers:    s.driverOptionsByType(),
	}
	for _, t := range integration.Types() {
		view.TypeLabels[t] = integration.TypeLabel(integration.Type(t))
	}

	if id := c.Param("id"); id != "" {
		rec, err := s.app.FindRecordById(integration.CollectionProviders, id)
		if err != nil || rec == nil {
			s.renderErrorPage(c, http.StatusNotFound, "Not found", "That provider does not exist.")
			return
		}
		view.IsNew = false
		view.Record = rec
		view.Name = rec.GetString("name")
		view.Code = rec.GetString("code")
		view.Type = rec.GetString("type")
		view.DriverCode = rec.GetString("driver")
		view.Description = rec.GetString("description")
		view.Sandbox = rec.GetBool("sandbox")
		view.Priority = rec.GetInt("priority")
		view.Config = integration.SortedKeys(integration.ResolveConfig(s.app, rec))
		view.base = s.newBase(c, "Edit provider", "integrations")

		// The secret field names and the mask, never the values. The browser is
		// told which credentials exist so the form can draw them, and nothing
		// about what they are.
		stored := integration.DecryptSecrets(s.app, rec.GetString("secrets"))
		view.SecretValues = integration.SecretsForBrowser(stored)

		if driver, ok := integration.LookupDriver(view.DriverCode); ok {
			view.Fields = driverFields(driver, integration.ResolveConfig(s.app, rec), view.SecretValues)
		}
	} else {
		view.Priority = 100
		if driver, ok := integration.LookupDriver("smtp"); ok {
			view.DriverCode = driver.Code
			view.Fields = driverFields(driver, nil, nil)
		}
	}

	s.render(c, http.StatusOK, "integration_form.html", view)
}

// handleIntegrationSave creates or updates a provider.
func (s *Server) handleIntegrationSave(c *gin.Context) {
	id := strings.TrimSpace(c.PostForm("id"))

	col, err := s.app.FindCollectionByNameOrId(integration.CollectionProviders)
	if err != nil || col == nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Schema missing", "The collection does not exist.")
		return
	}

	var rec *core.Record
	if id == "" {
		rec = core.NewRecord(col)
	} else {
		rec, err = s.app.FindRecordById(integration.CollectionProviders, id)
		if err != nil || rec == nil {
			s.renderErrorPage(c, http.StatusNotFound, "Not found", "That provider does not exist.")
			return
		}
	}

	driverCode := strings.TrimSpace(c.PostForm("driver"))
	driver, ok := integration.LookupDriver(driverCode)
	if !ok {
		s.setFlash(c, "danger", "That driver is not registered in this build.")
		c.Redirect(http.StatusSeeOther, "/integrations")
		return
	}

	name := strings.TrimSpace(c.PostForm("name"))
	code := strings.TrimSpace(c.PostForm("code"))
	if name == "" || code == "" {
		s.setFlash(c, "danger", "A provider needs a name and a code.")
		c.Redirect(http.StatusSeeOther, "/integrations")
		return
	}

	// Collected from the driver's own declaration, so a field the driver does not
	// declare cannot be submitted and cannot be stored.
	config := map[string]any{}
	for _, field := range driver.Config {
		switch field.Kind {
		case integration.FieldBool:
			config[field.Name] = c.PostForm(field.Name) != ""
		default:
			if value, present := c.GetPostForm(field.Name); present {
				config[field.Name] = strings.TrimSpace(value)
			}
		}
	}

	submittedSecrets := map[string]any{}
	for _, field := range driver.Secrets {
		if value, present := c.GetPostForm("secret_" + field.Name); present {
			submittedSecrets[field.Name] = value
		}
	}

	// What is stored keeps every credential the form did not replace. An empty
	// field and the mask both mean "unchanged", which is what lets the form avoid
	// ever sending a stored secret back to the browser.
	storedSecrets := integration.DecryptSecrets(s.app, rec.GetString("secrets"))
	mergedSecrets := integration.MergeSecrets(storedSecrets, submittedSecrets)

	if problems := driver.Validate(config, mergedSecrets); len(problems) > 0 {
		s.setFlash(c, "danger", strings.Join(problems, "; "))
		c.Redirect(http.StatusSeeOther, "/integrations")
		return
	}

	encrypted, err := integration.EncryptSecrets(s.app, mergedSecrets)
	if err != nil {
		// The message names the cause. A refusal to store credentials without an
		// encryption key is deliberate, and an operator needs to know that is what
		// happened rather than seeing a generic failure.
		s.setFlash(c, "danger", err.Error())
		c.Redirect(http.StatusSeeOther, "/integrations")
		return
	}

	rec.Set("name", name)
	rec.Set("code", code)
	rec.Set("type", string(driver.Type))
	rec.Set("driver", driver.Code)
	rec.Set("config", config)
	rec.Set("secrets", encrypted)
	rec.Set("sandbox", c.PostForm("sandbox") != "")
	rec.Set("description", strings.TrimSpace(c.PostForm("description")))

	if raw := strings.TrimSpace(c.PostForm("priority")); raw != "" {
		rec.Set("priority", raw)
	}
	if rec.IsNew() {
		rec.Set("status", integration.StatusEnabled)
		rec.Set("lastTestResult", integration.TestUntested)
		if account := s.ownAccountQuiet(c); account != nil {
			rec.Set("createdBy", account.Id)
		}
	}

	if err := s.app.Save(rec); err != nil {
		s.setFlash(c, "danger", "Could not save: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/integrations")
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "integrations",
		TargetType: "integration_provider",
		TargetID:   rec.Id,
		Severity:   severityCritical,
		Summary:    "Provider \"" + name + "\" (" + driver.Code + ") saved",
		// The credential names are recorded, never the values: an audit log that
		// says a key was rotated is useful, and one that contains the key is a
		// second copy of it.
		Detail: map[string]any{"driver": driver.Code, "type": driver.Type,
			"credentials": integration.SortedKeys(mergedSecrets)},
	})

	s.setFlash(c, "success", "Saved.")
	c.Redirect(http.StatusSeeOther, "/integrations")
}

// handleIntegrationDefault makes a provider the default for its type.
func (s *Server) handleIntegrationDefault(c *gin.Context) {
	rec, err := s.app.FindRecordById(integration.CollectionProviders, c.Param("id"))
	if err != nil || rec == nil {
		s.renderErrorPage(c, http.StatusNotFound, "Not found", "That provider does not exist.")
		return
	}

	if err := integration.MakeDefault(s.app, rec); err != nil {
		s.setFlash(c, "danger", "Could not set the default: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/integrations")
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "integrations",
		TargetType: "integration_provider",
		TargetID:   rec.Id,
		Severity:   severityWarning,
		Summary:    "Provider \"" + rec.GetString("name") + "\" is now the default for " + rec.GetString("type"),
	})
	s.setFlash(c, "success", "It is now the default for "+integration.TypeLabel(integration.Type(rec.GetString("type")))+".")
	c.Redirect(http.StatusSeeOther, "/integrations")
}

// handleIntegrationStatus enables or disables a provider.
func (s *Server) handleIntegrationStatus(c *gin.Context) {
	rec, err := s.app.FindRecordById(integration.CollectionProviders, c.Param("id"))
	if err != nil || rec == nil {
		s.renderErrorPage(c, http.StatusNotFound, "Not found", "That provider does not exist.")
		return
	}

	status := c.PostForm("status")
	if status != integration.StatusEnabled && status != integration.StatusDisabled {
		s.setFlash(c, "danger", "Unrecognised state.")
		c.Redirect(http.StatusSeeOther, "/integrations")
		return
	}

	rec.Set("status", status)
	if err := s.app.Save(rec); err != nil {
		s.setFlash(c, "danger", "Could not change the state: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/integrations")
		return
	}

	// Disabling the default leaves the type with no default, which is a real state
	// and is reported rather than silently prevented: an operator disabling a
	// provider on purpose should not be overruled, but they should be told.
	if status == integration.StatusDisabled && rec.GetBool("isDefault") {
		s.setFlash(c, "warning",
			"That was the default for "+rec.GetString("type")+
				", which now has no working provider. Enable another or make one the default.")
		c.Redirect(http.StatusSeeOther, "/integrations")
		return
	}

	s.setFlash(c, "success", "Provider "+status+".")
	c.Redirect(http.StatusSeeOther, "/integrations")
}

// handleIntegrationTest runs a connectivity test.
func (s *Server) handleIntegrationTest(c *gin.Context) {
	rec, err := s.app.FindRecordById(integration.CollectionProviders, c.Param("id"))
	if err != nil || rec == nil {
		s.renderErrorPage(c, http.StatusNotFound, "Not found", "That provider does not exist.")
		return
	}

	driver, ok := integration.LookupDriver(rec.GetString("driver"))
	if !ok {
		s.setFlash(c, "danger", "That driver is not registered in this build.")
		c.Redirect(http.StatusSeeOther, "/integrations")
		return
	}

	// A test that cannot be implemented says so rather than reporting success.
	if !driver.Testable {
		s.setFlash(c, "warning",
			"No connectivity test is implemented for "+driver.Name+", so nothing was checked.")
		c.Redirect(http.StatusSeeOther, "/integrations")
		return
	}

	provider := &integration.Provider{
		Record:  rec,
		Driver:  driver,
		Config:  integration.ResolveConfig(s.app, rec),
		Secrets: integration.DecryptSecrets(s.app, rec.GetString("secrets")),
	}

	// Bounded, because a provider that never answers must not hold a panel request
	// open until the browser gives up.
	ctx, cancel := contextWithTimeout(20 * time.Second)
	defer cancel()

	result := integration.Test(ctx, s.app, provider)

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "integrations",
		TargetType: "integration_provider",
		TargetID:   rec.Id,
		Severity:   severityInfo,
		Summary:    "Tested provider \"" + rec.GetString("name") + "\": " + map[bool]string{true: "success", false: "failure"}[result.Success],
	})

	if result.Success {
		s.setFlash(c, "success", result.Message)
	} else {
		s.setFlash(c, "danger", result.Message)
	}
	c.Redirect(http.StatusSeeOther, "/integrations")
}

// handleIntegrationDelete soft-deletes a provider.
func (s *Server) handleIntegrationDelete(c *gin.Context) {
	rec, err := s.app.FindRecordById(integration.CollectionProviders, c.Param("id"))
	if err != nil || rec == nil {
		s.renderErrorPage(c, http.StatusNotFound, "Not found", "That provider does not exist.")
		return
	}

	// A soft delete, because files.provider and payment_transactions name the
	// provider by code and a deleted row would leave them naming nothing. The row
	// is the record of where things were sent.
	rec.Set("deletedAt", time.Now().UTC())
	if err := s.app.Save(rec); err != nil {
		s.setFlash(c, "danger", "Could not delete: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/integrations")
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "integrations",
		TargetType: "integration_provider",
		TargetID:   rec.Id,
		Severity:   severityCritical,
		Summary:    "Provider \"" + rec.GetString("name") + "\" removed",
	})
	s.setFlash(c, "success", "Removed.")
	c.Redirect(http.StatusSeeOther, "/integrations")
}

// handleIntegrationLogs renders the integration log.
func (s *Server) handleIntegrationLogs(c *gin.Context) {
	filter := ""
	params := map[string]any{}
	if channel := strings.TrimSpace(c.Query("channel")); channel != "" {
		filter = "channel = {:channel}"
		params["channel"] = channel
	}
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		if filter != "" {
			filter += " && "
		}
		filter += "status = {:status}"
		params["status"] = status
	}

	records, err := s.app.FindRecordsByFilter(integration.CollectionLogs,
		filter, "-created", 200, 0, params)
	if err != nil {
		records = nil
	}

	s.render(c, http.StatusOK, "integration_logs.html", integrationLogsPage{
		base:     s.newBase(c, "Integration log", "integrations"),
		Rows:     records,
		Channel:  c.Query("channel"),
		Status:   c.Query("status"),
		Channels: []string{"email", "sms", "storage", "auth", "payment", "webhook"},
	})
}

// driverFields builds the form fields for a driver.
func driverFields(driver integration.Driver, config map[string]any, secrets map[string]string) []integrationField {
	out := make([]integrationField, 0, len(driver.Config)+len(driver.Secrets))

	for _, field := range driver.Config {
		value := ""
		if config != nil {
			if raw, ok := config[field.Name]; ok && raw != nil {
				value = integration.ScalarString(raw)
			}
		}
		if value == "" {
			value = field.Default
		}
		out = append(out, integrationField{
			Name: field.Name, Label: field.Label, Kind: string(field.Kind),
			Value: value, Required: field.Required, Options: field.Options,
			Help: field.Help, Secret: false,
			Checked: value == "true" || value == "1",
		})
	}

	for _, field := range driver.Secrets {
		out = append(out, integrationField{
			Name: "secret_" + field.Name, Label: field.Label, Kind: string(field.Kind),
			Value: secrets[field.Name], Required: field.Required,
			Help: field.Help, Secret: true,
		})
	}
	return out
}

// driverOptionsByType groups the drivers for the type-aware picker.
func (s *Server) driverOptionsByType() map[string][]integrationDriverOption {
	out := map[string][]integrationDriverOption{}
	for _, t := range integration.Types() {
		for _, driver := range integration.DriversOfType(integration.Type(t)) {
			out[t] = append(out[t], integrationDriverOption{
				Code: driver.Code, Name: driver.Name, Description: driver.Description,
			})
		}
	}
	return out
}

// integrationGroup is one provider type on the list.
type integrationGroup struct {
	Type  string
	Label string
	Rows  []integrationRow
}

// integrationRow is one provider.
type integrationRow struct {
	Record       *core.Record
	Name         string
	Code         string
	Driver       string
	DriverCode   string
	Status       string
	IsDefault    bool
	Priority     int
	Sandbox      bool
	LastTest     string
	LastTestAt   string
	LastTestWhen string
	Testable     bool
	SecretKeys   []string
}

// integrationsPage is the provider list.
type integrationsPage struct {
	base
	Groups    []integrationGroup
	CanCreate bool
	CanUpdate bool
	CanTest   bool
	CanDelete bool
}

// integrationForm is the provider editor.
type integrationForm struct {
	base
	Record       *core.Record
	IsNew        bool
	Name         string
	Code         string
	Type         string
	DriverCode   string
	Description  string
	Sandbox      bool
	Priority     int
	Config       []string
	SecretValues map[string]string
	Fields       []integrationField
	Types        []string
	TypeLabels   map[string]string
	Drivers      map[string][]integrationDriverOption
}

// integrationField is one rendered control.
type integrationField struct {
	Name     string
	Label    string
	Kind     string
	Value    string
	Required bool
	Options  []string
	Help     string
	Secret   bool
	Checked  bool
}

// integrationDriverOption is one selectable driver.
type integrationDriverOption struct {
	Code        string
	Name        string
	Description string
}

// integrationLogsPage is the integration log.
type integrationLogsPage struct {
	base
	Rows     []*core.Record
	Channel  string
	Status   string
	Channels []string
}
