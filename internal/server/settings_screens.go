package server

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/audit"
	"pocketadmin/internal/permission"
	"pocketadmin/internal/settings"
)

// registerSettingsRoutes mounts both settings surfaces.
//
// Each surface has its own page and its own permission, and each writes through
// its own registry. The isolation is not a check in these handlers: Save walks a
// Registry, and a Registry only contains its own surface's keys, so the admin page
// has no way to name a product key.
func (s *Server) registerSettingsRoutes(panel *gin.RouterGroup) {
	s.readGuarded(panel, http.MethodGet, "/settings/admin",
		permission.SettingsAdminView, s.settingsForm(settings.Admin()))
	s.guarded(panel, http.MethodPost, "/settings/admin",
		permission.SettingsAdminUpdate, s.settingsSave(settings.Admin()))

	s.readGuarded(panel, http.MethodGet, "/settings/product",
		permission.SettingsProductView, s.settingsForm(settings.Product()))
	s.guarded(panel, http.MethodPost, "/settings/product",
		permission.SettingsProductUpdate, s.settingsSave(settings.Product()))
}

// settingsForm renders one surface's settings, grouped by category.
func (s *Server) settingsForm(reg *settings.Registry) gin.HandlerFunc {
	return func(c *gin.Context) {
		values := s.settings.Values(reg)

		s.render(c, http.StatusOK, "settings.html", settingsPage{
			base:       s.newBase(c, settingsTitle(reg), settingsActive(reg)),
			Surface:    reg.Surface(),
			BasePath:   settingsBasePath(reg),
			Groups:     s.settingsGroups(c, reg, values),
			CanUpdate:  s.actor(c).Can(settingsUpdatePerm(reg)),
			Categories: len(reg.Categories()),
		})
	}
}

// settingsSave validates and stores one category of one surface.
func (s *Server) settingsSave(reg *settings.Registry) gin.HandlerFunc {
	return func(c *gin.Context) {
		category := strings.TrimSpace(c.PostForm("category"))
		if category == "" {
			s.setFlash(c, "danger", "No settings category was submitted.")
			c.Redirect(http.StatusSeeOther, settingsBasePath(reg))
			return
		}

		// Only this category's declared keys are read from the form, so the
		// submission cannot reach a key the page did not render.
		submitted := make(map[string]any, 8)
		for _, key := range reg.KeysIn(category) {
			switch key.Type {
			case settings.TypeBool:
				// An unchecked checkbox is absent from the submission, and absence
				// means false for a checkbox. Every other type distinguishes absent
				// from empty, so booleans are collected unconditionally here.
				submitted[key.Key] = c.PostForm(key.Key) != ""
			default:
				value, present := c.GetPostForm(key.Key)
				if !present {
					continue
				}
				submitted[key.Key] = value
			}
		}

		var actor *core.Record
		if a := s.actor(c); a != nil && !a.IsSuperuser {
			actor = s.ownAccountQuiet(c)
		}

		result, err := s.settings.Save(actor, reg, category, submitted)
		if err != nil {
			s.setFlash(c, "danger", err.Error())
			c.Redirect(http.StatusSeeOther, settingsBasePath(reg)+"#"+category)
			return
		}

		// A settings change is exactly the kind of thing a reviewer wants to see
		// the before and after of, so the changed keys and their values are
		// recorded rather than a bare "settings changed".
		if len(result.Changed) > 0 {
			changes := make(map[string]any, len(result.Changed))
			for _, key := range result.Changed {
				changes[key.Key] = result.Values[key.Key]
			}
			s.audit(c, auditEntry{
				Event:      auditEffectiveEvent(reg),
				Module:     string(reg.Surface()),
				TargetType: "settings",
				TargetID:   string(reg.Surface()) + "." + category,
				Severity:   auditSettingsSeverity(result.Changed),
				Summary:    "Changed " + string(reg.Surface()) + " settings in " + category,
				Detail:     changes,
			})
			s.setFlash(c, "success", "Saved.")
		} else {
			s.setFlash(c, "info", "Nothing changed.")
		}

		c.Redirect(http.StatusSeeOther, settingsBasePath(reg)+"#"+category)
	}
}

// settingsGroups builds the per-category view models.
func (s *Server) settingsGroups(c *gin.Context, reg *settings.Registry, values map[string]any) []settingsGroup {
	groups := make([]settingsGroup, 0, len(reg.Categories()))

	for _, category := range reg.Categories() {
		group := settingsGroup{
			Category:    category,
			Title:       humanize(strings.ReplaceAll(category, ".", " ")),
			Description: settingsCategoryHelp(reg.Surface(), category),
		}

		for _, key := range reg.KeysIn(category) {
			group.Fields = append(group.Fields, settingsFieldVM(key, values[key.QualifiedName()]))
		}
		groups = append(groups, group)
	}
	return groups
}

// settingsFieldVM renders one key as a form control.
func settingsFieldVM(key settings.Key, value any) settingsField {
	field := settingsField{
		Name:    key.Key,
		Label:   humanize(strings.ReplaceAll(key.Key, ".", " ")),
		Help:    key.Description,
		Value:   scalarString(value),
		Options: key.Options,
		Min:     key.Min,
		Max:     key.Max,
		Type:    string(key.Type),
		Public:  key.Public,
	}

	switch key.Type {
	case settings.TypeBool:
		field.Input = "switch"
		field.Checked, _ = value.(bool)
	case settings.TypeSelect:
		field.Input = "select"
	case settings.TypeNumber:
		field.Input = "number"
		if key.Min != nil && key.Max != nil && *key.Max <= 1 {
			// A bound of at most one is a fraction, not a count.
			field.Step = "0.01"
		}
	case settings.TypeJSON:
		field.Input = "json"
	case settings.TypeFile:
		field.Input = "text"
	default:
		field.Input = "text"
	}

	if key.Pattern != "" {
		field.Input = "text"
		field.Pattern = key.Pattern
	}

	return field
}

// scalarString renders a value for a form control.
//
// A collection or object is rendered as JSON so it can be edited; a boolean is not
// rendered at all, since a switch carries its own state.
func scalarString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case bool:
		return ""
	default:
		return settings.RenderJSON(value)
	}
}

// auditEffectiveEvent returns the audit event for a settings change.
//
// Both surfaces record the same event: it is one kind of thing that happened, and
// the surface is carried in the module field, which is what a filter would use.
func auditEffectiveEvent(_ *settings.Registry) audit.Event {
	return audit.EventSettingsChanged
}

// auditSettingsSeverity ranks a settings change.
//
// Touching sign-in policy is worth more attention than changing a footer, so the
// two are not recorded at the same level. It is the category of the changed key
// that decides, not the surface: both surfaces carry a security category.
func auditSettingsSeverity(changed []settings.Key) audit.Severity {
	for _, key := range changed {
		switch key.Category {
		case "security", "auth", "registration", "commerce":
			return audit.SeverityWarning
		}
	}
	return audit.SeverityInfo
}

// settingsBasePath returns the URL of a surface's page.
func settingsBasePath(reg *settings.Registry) string {
	if reg.Surface() == settings.SurfaceProduct {
		return "/settings/product"
	}
	return "/settings/admin"
}

// settingsActive returns the sidebar key for a surface.
func settingsActive(reg *settings.Registry) string {
	if reg.Surface() == settings.SurfaceProduct {
		return "settings.product"
	}
	return "settings.admin"
}

// settingsTitle names a surface's page.
func settingsTitle(reg *settings.Registry) string {
	if reg.Surface() == settings.SurfaceProduct {
		return "Storefront settings"
	}
	return "Back-office settings"
}

// settingsUpdatePerm returns the permission that guards a surface's writes.
func settingsUpdatePerm(reg *settings.Registry) string {
	if reg.Surface() == settings.SurfaceProduct {
		return permission.SettingsProductUpdate
	}
	return permission.SettingsAdminUpdate
}

// settingsCategoryHelp describes a category on its card.
func settingsCategoryHelp(surface settings.Surface, category string) string {
	help := map[string]string{
		"console":      "Branding and defaults for the panel itself.",
		"security":     "Sign-in policy for back-office accounts.",
		"notification": "Which events notify administrators, and where.",
		"banner":       "A message shown above the panel content.",

		"site":         "Identity of the storefront, including how it appears in search results.",
		"auth":         "How customers sign in.",
		"registration": "Whether customers may create their own accounts.",
		"announcement": "How announcements are presented on the storefront.",
		"feature":      "Which product capabilities are switched on.",
		"commerce":     "Money handling: currency, tax, order and refund policy.",
	}
	if text, ok := help[category]; ok {
		return text
	}
	return ""
}

// settingsPage is one surface's settings screen.
type settingsPage struct {
	base
	Surface    settings.Surface
	BasePath   string
	Groups     []settingsGroup
	Categories int
	CanUpdate  bool
}

// settingsGroup is one category's card.
type settingsGroup struct {
	Category    string
	Title       string
	Description string
	Fields      []settingsField
}

// settingsField is one rendered control.
type settingsField struct {
	Name    string
	Label   string
	Help    string
	Input   string
	Value   string
	Checked bool
	Options []string
	Type    string
	Public  bool
	Pattern string
	Step    string
	Min     *float64
	Max     *float64
}
