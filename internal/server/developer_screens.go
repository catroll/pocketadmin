package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/audit"
	"pocketadmin/internal/developer"
	"pocketadmin/internal/permission"
)

// registerDeveloperRoutes mounts the API key screens.
//
// The machine-facing API is registered separately, in registerDeveloperAPIRoutes,
// because it authenticates with a key rather than a session.
func (s *Server) registerDeveloperRoutes(panel *gin.RouterGroup) {
	s.readGuarded(panel, http.MethodGet, "/developer/keys", permission.DeveloperKeyList, s.handleAPIKeyList)
	s.readGuarded(panel, http.MethodGet, "/developer/api", permission.DeveloperAPIView, s.handleAPIReference)

	s.guarded(panel, http.MethodPost, "/developer/keys", permission.DeveloperKeyCreate, s.handleAPIKeyCreate)
	s.guarded(panel, http.MethodPost, "/developer/keys/:id/revoke", permission.DeveloperKeyRevoke, s.handleAPIKeyRevoke)
}

// handleAPIKeyList renders the keys.
func (s *Server) handleAPIKeyList(c *gin.Context) {
	records, err := s.app.FindRecordsByFilter(developer.CollectionAPIKeys,
		"", "-created", 200, 0)
	if err != nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Could not load the keys", err.Error())
		return
	}

	rows := make([]apiKeyRow, 0, len(records))
	for _, rec := range records {
		row := apiKeyRow{
			Record:     rec,
			Name:       rec.GetString("name"),
			Prefix:     rec.GetString("keyPrefix"),
			Status:     rec.GetString("status"),
			Usage:      rec.GetInt("usageCount"),
			LastUsedIP: rec.GetString("lastUsedIp"),
			Scopes:     len(rec.GetStringSlice("scopes")),
			RateLimit:  rec.GetInt("rateLimitPerMinute"),
			AllowList:  rec.GetString("ipAllowList"),
		}
		if owner, err := s.app.FindRecordById("users", rec.GetString("owner")); err == nil && owner != nil {
			row.Owner = owner.Email()
		}
		if at := rec.GetDateTime("lastUsedAt"); !at.IsZero() {
			row.LastUsed = relativeTime(at)
		}
		if at := rec.GetDateTime("expiresAt"); !at.IsZero() {
			row.Expires = formatDate(at)
		}
		if at := rec.GetDateTime("revokedAt"); !at.IsZero() {
			row.Revoked = formatDate(at)
		}
		rows = append(rows, row)
	}

	// A newly created key is shown once and is not stored anywhere it could be
	// read again, so it is passed through the flash for exactly this render.
	plaintext := s.takeFlashSecret(c)

	s.render(c, http.StatusOK, "developer_keys.html", apiKeysPage{
		base:        s.newBase(c, "API keys", "developer"),
		Rows:        rows,
		Plaintext:   plaintext,
		CanCreate:   s.actor(c).Can(permission.DeveloperKeyCreate),
		CanRevoke:   s.actor(c).Can(permission.DeveloperKeyRevoke),
		Permissions: s.permissionOptions(),
		Users:       s.administratorOptions(),
	})
}

// handleAPIKeyCreate issues a key and shows it once.
func (s *Server) handleAPIKeyCreate(c *gin.Context) {
	col, err := s.app.FindCollectionByNameOrId(developer.CollectionAPIKeys)
	if err != nil || col == nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Schema missing", "The collection does not exist.")
		return
	}

	name := strings.TrimSpace(c.PostForm("name"))
	ownerID := strings.TrimSpace(c.PostForm("owner"))
	if name == "" || ownerID == "" {
		s.setFlash(c, "danger", "A key needs a name and an owning account.")
		c.Redirect(http.StatusSeeOther, "/developer/keys")
		return
	}

	// The owner must hold an active administrator profile. A key is a way for a
	// person to act, and it has to be a person who can act at all.
	if !s.isActiveAdministrator(ownerID) {
		s.setFlash(c, "danger", "The owning account does not have an active administrator profile.")
		c.Redirect(http.StatusSeeOther, "/developer/keys")
		return
	}

	generated, err := developer.GenerateKey()
	if err != nil {
		s.setFlash(c, "danger", "Could not generate a key: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/developer/keys")
		return
	}

	// Scopes are validated against the permission catalogue before they are
	// stored. A key cannot be scoped to an authority that does not exist.
	scopes, unknown := s.validScopes(c.PostFormArray("scopes"))
	if len(unknown) > 0 {
		s.setFlash(c, "danger", "Unknown permission: "+strings.Join(unknown, ", "))
		c.Redirect(http.StatusSeeOther, "/developer/keys")
		return
	}
	if len(scopes) == 0 {
		s.setFlash(c, "danger", "A key with no scopes can do nothing, so it was not created.")
		c.Redirect(http.StatusSeeOther, "/developer/keys")
		return
	}

	// Scoping beyond the owner's own authority is refused rather than silently
	// narrowed. Storing a scope the owner does not hold would produce a key that
	// looks more powerful than it is, and the difference would only surface as a
	// puzzling 403.
	if excess := s.scopesBeyondOwner(ownerID, scopes); len(excess) > 0 {
		s.setFlash(c, "danger",
			"The owner does not hold: "+strings.Join(excess, ", ")+
				". A key cannot be given authority its owner lacks.")
		c.Redirect(http.StatusSeeOther, "/developer/keys")
		return
	}

	rec := core.NewRecord(col)
	rec.Set("name", name)
	rec.Set("owner", ownerID)
	// Only the prefix and the hash are stored. The key itself exists in this
	// response and nowhere else, which is the property that makes a database dump
	// not a set of working credentials.
	rec.Set("keyPrefix", generated.Prefix)
	rec.Set("keyHash", generated.Hash)
	rec.Set("scopes", scopes)
	rec.Set("status", developer.StatusActive)

	if limit := strings.TrimSpace(c.PostForm("rateLimitPerMinute")); limit != "" {
		rec.Set("rateLimitPerMinute", limit)
	}
	rec.Set("ipAllowList", strings.TrimSpace(c.PostForm("ipAllowList")))

	if raw := strings.TrimSpace(c.PostForm("expiresAt")); raw != "" {
		at, err := time.ParseInLocation("2006-01-02", raw, time.Local)
		if err != nil {
			s.setFlash(c, "danger", "The expiry date could not be read.")
			c.Redirect(http.StatusSeeOther, "/developer/keys")
			return
		}
		// The end of the chosen day, so "expires on the 5th" means usable through
		// the 5th rather than until midnight at its start.
		rec.Set("expiresAt", at.Add(24*time.Hour-time.Nanosecond).UTC())
	}

	if err := s.app.Save(rec); err != nil {
		s.setFlash(c, "danger", "Could not save the key: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/developer/keys")
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventAPIKeyCreated,
		Module:     "developer",
		TargetType: "api_key",
		TargetID:   rec.Id,
		Severity:   severityWarning,
		Summary:    "Issued API key \"" + name + "\" with " + itoa(len(scopes)) + " scope(s)",
		Detail:     map[string]any{"prefix": generated.Prefix, "owner": ownerID, "scopes": scopes},
	})

	// Held for exactly one render. It is not written to the database, not logged
	// and not put in the URL.
	s.setFlashSecret(c, generated.Plaintext)
	s.setFlash(c, "success", "Key created. Copy it now: it is not stored and cannot be shown again.")
	c.Redirect(http.StatusSeeOther, "/developer/keys")
}

// handleAPIKeyRevoke withdraws a key.
func (s *Server) handleAPIKeyRevoke(c *gin.Context) {
	rec, err := s.app.FindRecordById(developer.CollectionAPIKeys, c.Param("id"))
	if err != nil || rec == nil {
		s.renderErrorPage(c, http.StatusNotFound, "Not found", "That key does not exist.")
		return
	}

	var byUserID string
	if account := s.ownAccountQuiet(c); account != nil {
		byUserID = account.Id
	}

	if err := developer.Revoke(s.app, rec, byUserID); err != nil {
		s.setFlash(c, "danger", "Could not revoke: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/developer/keys")
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventAPIKeyRevoked,
		Module:     "developer",
		TargetType: "api_key",
		TargetID:   rec.Id,
		Severity:   severityCritical,
		Summary:    "Revoked API key \"" + rec.GetString("name") + "\" (" + rec.GetString("keyPrefix") + ")",
	})
	s.setFlash(c, "success", "Revoked. The key stops working immediately.")
	c.Redirect(http.StatusSeeOther, "/developer/keys")
}

// handleAPIReference describes the machine-facing API.
func (s *Server) handleAPIReference(c *gin.Context) {
	c.Request.URL.Path = "/developer/api"
	s.render(c, http.StatusOK, "developer_api.html", apiReferencePage{
		base:     s.newBase(c, "API reference", "developer.api"),
		BaseURL:  "/developer/api/v1",
		Endpoint: s.apiEndpointTable(),
	})
}

// apiEndpointRow is one documented operation.
type apiEndpointRow struct {
	Method      string
	Path        string
	Permission  string
	Description string
}

// apiEndpointTable is the reference's content.
//
// It is a hand-written table rather than a generated specification, and the drift
// test beside it asserts that every route registered under the API prefix appears
// here. A generated document would be complete and unreadable; this is readable
// and tested for completeness, which is the property that matters.
func (s *Server) apiEndpointTable() []apiEndpointRow {
	return []apiEndpointRow{
		{Method: "GET", Path: "/whoami", Permission: "(key only)",
			Description: "What this key is scoped for and what it actually carries. The two differ when the owner has lost a permission."},
		{Method: "GET", Path: "/announcements", Permission: permission.AnnouncementList,
			Description: "Published announcements, filtered to the audiences the key's owner may see."},
		{Method: "GET", Path: "/jobs", Permission: permission.JobsJobList,
			Description: "Scheduled tasks with their schedule, state and last and next run."},
	}
}

// isActiveAdministrator reports whether an account may act.
func (s *Server) isActiveAdministrator(userID string) bool {
	admin, err := s.app.FindFirstRecordByFilter("admins",
		"user = {:u} && status = 'active'", map[string]any{"u": userID})
	if err != nil || admin == nil {
		return false
	}
	return true
}

// validScopes resolves submitted permission ids, reporting any that do not exist.
func (s *Server) validScopes(ids []string) (valid []string, unknown []string) {
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		rec, err := s.app.FindRecordById("permissions", id)
		if err != nil || rec == nil {
			unknown = append(unknown, id)
			continue
		}
		valid = append(valid, id)
	}
	return valid, unknown
}

// scopesBeyondOwner reports scopes the owner does not hold.
func (s *Server) scopesBeyondOwner(ownerID string, scopeIDs []string) []string {
	owner, err := s.app.FindRecordById("users", ownerID)
	if err != nil || owner == nil {
		return nil
	}

	actor, err := s.resolver.Resolve(owner)
	if err != nil || actor == nil {
		return nil
	}

	var excess []string
	for _, id := range scopeIDs {
		rec, err := s.app.FindRecordById("permissions", id)
		if err != nil || rec == nil {
			continue
		}
		code := rec.GetString("code")
		if !actor.Can(code) {
			excess = append(excess, code)
		}
	}
	return excess
}

// permissionOptions lists the permission points for the scope picker.
func (s *Server) permissionOptions() []permissionOption {
	records, err := s.app.FindRecordsByFilter("permissions", "", "module,resource,action", 0, 0)
	if err != nil {
		return nil
	}

	out := make([]permissionOption, 0, len(records))
	for _, rec := range records {
		out = append(out, permissionOption{
			ID:     rec.Id,
			Code:   rec.GetString("code"),
			Module: rec.GetString("module"),
			Title:  rec.GetString("title"),
		})
	}
	return out
}

// administratorOptions lists the accounts a key may belong to.
func (s *Server) administratorOptions() []accountOption {
	admins, err := s.app.FindRecordsByFilter("admins", "status = 'active'", "", 0, 0)
	if err != nil {
		return nil
	}

	out := make([]accountOption, 0, len(admins))
	for _, admin := range admins {
		user, err := s.app.FindRecordById("users", admin.GetString("user"))
		if err != nil || user == nil {
			continue
		}
		out = append(out, accountOption{ID: user.Id, Email: user.Email()})
	}
	return out
}

// apiKeyRow is one key on the list.
type apiKeyRow struct {
	Record     *core.Record
	Name       string
	Prefix     string
	Owner      string
	Status     string
	Scopes     int
	Usage      int
	RateLimit  int
	AllowList  string
	LastUsed   string
	LastUsedIP string
	Expires    string
	Revoked    string
}

// apiKeysPage is the key list.
type apiKeysPage struct {
	base
	Rows        []apiKeyRow
	Plaintext   string
	CanCreate   bool
	CanRevoke   bool
	Permissions []permissionOption
	Users       []accountOption
}

// apiReferencePage is the API reference.
type apiReferencePage struct {
	base
	BaseURL  string
	Endpoint []apiEndpointRow
}

// permissionOption is one selectable scope.
type permissionOption struct {
	ID     string
	Code   string
	Module string
	Title  string
}

// accountOption is one selectable owner.
type accountOption struct {
	ID    string
	Email string
}
