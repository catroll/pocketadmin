package server

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/rbac"
)

// Preference contexts. A person may want different settings in the back office
// than in a product surface, so preferences are stored per context rather than
// per account alone.
const (
	prefContextAdmin   = "admin"
	prefContextProduct = "product"
)

// preferencesPage is the self-service settings screen.
type preferencesPage struct {
	base

	// Preferences is the stored record, or nil when the account has none yet.
	Preferences *core.Record

	Theme            string
	Language         string
	PageSize         int
	Timezone         string
	SidebarCollapsed bool
	NotifyEmail      bool
	NotifySms        bool

	// Languages and Timezones are the offered choices; Timezones is deliberately
	// short and common rather than exhaustive.
	Languages []formOption
	Timezones []formOption
	PageSizes []int

	// MFA is the two-factor enrolment state for the signed-in account.
	MFA mfaEnrolment
}

// registerPreferenceRoutes mounts the self-service preferences screen.
//
// These routes require a signed-in account and nothing more: the screen reads
// and writes only the caller's own preferences, so it is the one write path that
// carries no permission. That is expressed with selfGuarded rather than by
// leaving the route unguarded, so the startup self-check can still tell a
// deliberate choice from an oversight.
func (s *Server) registerPreferenceRoutes(panel *gin.RouterGroup) {
	s.safe(panel, http.MethodGet, "/preferences", s.handlePreferencesForm)
	s.selfGuarded(panel, http.MethodPost, "/preferences", s.handlePreferencesSave)
	s.selfGuarded(panel, http.MethodPost, "/preferences/password", s.handlePasswordChange)
}

// handlePasswordChange lets an account change its own password.
//
// Administrators can reset any password from the account screen; this is the
// holder doing it themselves, which is the only path that does not require
// someone else to know or set a credential. Every other session is revoked,
// because a password change is how someone responds to a suspected compromise of
// their own account -- but the session performing the change is kept, so the
// person is not thrown out of the screen they are standing on.
func (s *Server) handlePasswordChange(c *gin.Context) {
	actor := s.actor(c)
	if actor == nil || actor.IsSuperuser {
		// A break-glass superuser has no `users` row to change a password on; they
		// use PocketBase's own dashboard.
		s.setFlash(c, "warning", "Change this password from the PocketBase dashboard.")
		c.Redirect(http.StatusSeeOther, "/preferences")
		return
	}

	rec, err := s.app.FindRecordById(rbac.CollectionUsers, actor.UserID)
	if err != nil || rec == nil {
		s.setFlash(c, "danger", "Could not load your account.")
		c.Redirect(http.StatusSeeOther, "/preferences")
		return
	}

	current := c.PostForm("currentPassword")
	next := c.PostForm("newPassword")
	confirm := c.PostForm("newPasswordConfirm")

	if !rec.ValidatePassword(current) {
		s.audit(c, auditEntry{
			Action: auditPasswordChange, Resource: "account", ResourceID: rec.Id,
			Result: "failure", Risk: "medium",
			Summary:   "Password change refused: the current password was wrong.",
			SubjectID: rec.Id, SubjectEmail: rec.Email(),
		})
		s.setFlash(c, "danger", "Your current password is not correct.")
		c.Redirect(http.StatusSeeOther, "/preferences")
		return
	}
	if len(next) < 8 {
		s.setFlash(c, "danger", "The new password must be at least 8 characters.")
		c.Redirect(http.StatusSeeOther, "/preferences")
		return
	}
	if next != confirm {
		s.setFlash(c, "danger", "The two new passwords do not match.")
		c.Redirect(http.StatusSeeOther, "/preferences")
		return
	}
	if next == current {
		s.setFlash(c, "warning", "The new password is the same as the current one.")
		c.Redirect(http.StatusSeeOther, "/preferences")
		return
	}

	rec.SetPassword(next)
	rec.Set("passwordChangedAt", time.Now().UTC())
	if err := s.app.Save(rec); err != nil {
		s.setFlash(c, "danger", "Could not change the password: "+strings.Join(validationMessages(err), "; "))
		c.Redirect(http.StatusSeeOther, "/preferences")
		return
	}

	// Revoke every other session but keep this one.
	currentToken, _ := sessions.Default(c).Get(sessionTokenKey).(string)
	if err := s.revokeOtherSessions(rec.Id, currentToken); err != nil {
		slog.Warn("could not revoke the other sessions after a password change",
			"user", rec.Id, "error", err)
	}

	s.audit(c, auditEntry{
		Action: auditPasswordChange, Resource: "account", ResourceID: rec.Id,
		Risk:      "medium",
		Summary:   "Password changed by the account holder; other sessions revoked.",
		SubjectID: rec.Id, SubjectEmail: rec.Email(),
	})
	s.setFlash(c, "success", "Password changed. Your other sessions were signed out.")
	c.Redirect(http.StatusSeeOther, "/preferences")
}

// revokeOtherSessions ends every live session for an account except the one
// identified by keepToken.
func (s *Server) revokeOtherSessions(userID, keepToken string) error {
	records, err := s.app.FindRecordsByFilter(sessionCollectionName,
		"user = {:user} && revokedAt = ''", "", 0, 0, map[string]any{"user": userID})
	if err != nil {
		return err
	}

	keep := hashToken(keepToken)
	for _, rec := range records {
		if rec.GetString("tokenHash") == keep {
			continue
		}
		if err := s.revokeSessionRecord(rec, sessionRevokedPassword); err != nil {
			return err
		}
	}
	return nil
}

// handlePreferencesForm renders the account's preferences.
func (s *Server) handlePreferencesForm(c *gin.Context) {
	rec := s.preferencesFor(c)

	pageSize := s.cfg.PerPage
	theme := "light"
	if rec != nil {
		if v := rec.GetInt("pageSize"); v > 0 {
			pageSize = v
		}
		if v := rec.GetString("theme"); v != "" {
			theme = v
		}
	}

	s.render(c, http.StatusOK, "preferences.html", preferencesPage{
		base:             s.newBase(c, "Preferences", "preferences"),
		Preferences:      rec,
		Theme:            theme,
		Language:         prefString(rec, "language"),
		PageSize:         pageSize,
		Timezone:         prefString(rec, "timezone"),
		SidebarCollapsed: rec != nil && rec.GetBool("sidebarCollapsed"),
		NotifyEmail:      rec != nil && rec.GetBool("notifyEmail"),
		NotifySms:        rec != nil && rec.GetBool("notifySms"),
		Languages:        preferenceLanguages(),
		Timezones:        preferenceTimezones(),
		PageSizes:        []int{10, 20, 25, 50, 100},
		MFA:              s.mfaStateFor(s.ownAccountQuiet(c), s.cfg.AppName),
	})
}

// handlePreferencesSave writes the account's preferences, creating the record on
// first save.
func (s *Server) handlePreferencesSave(c *gin.Context) {
	actor := s.actor(c)
	if actor == nil {
		s.redirectToLogin(c)
		return
	}

	col, err := s.app.FindCollectionByNameOrId("user_preferences")
	if err != nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Schema missing", err.Error())
		return
	}

	rec := s.preferencesFor(c)
	if rec == nil {
		rec = core.NewRecord(col)
		rec.Set("user", actor.UserID)
		rec.Set("context", prefContextAdmin)
	}

	rec.Set("theme", firstOrEmpty([]string{c.PostForm("theme")}))
	rec.Set("language", strings.TrimSpace(c.PostForm("language")))
	rec.Set("timezone", strings.TrimSpace(c.PostForm("timezone")))
	rec.Set("sidebarCollapsed", c.PostForm("sidebarCollapsed") != "")
	rec.Set("notifyEmail", c.PostForm("notifyEmail") != "")
	rec.Set("notifySms", c.PostForm("notifySms") != "")

	if size, err := strconv.Atoi(c.PostForm("pageSize")); err == nil && size > 0 {
		rec.Set("pageSize", size)
	} else {
		rec.Set("pageSize", s.cfg.PerPage)
	}

	if err := s.app.Save(rec); err != nil {
		s.setFlash(c, "danger", "Could not save your preferences: "+strings.Join(validationMessages(err), "; "))
		c.Redirect(http.StatusSeeOther, "/preferences")
		return
	}

	s.setFlash(c, "success", "Preferences saved.")
	c.Redirect(http.StatusSeeOther, "/preferences")
}

// ownAccountQuiet loads the signed-in account without side effects, for pages
// that only need to read its state.
func (s *Server) ownAccountQuiet(c *gin.Context) *core.Record {
	actor := s.actor(c)
	if actor == nil || actor.IsSuperuser {
		return nil
	}
	rec, err := s.app.FindRecordById(rbac.CollectionUsers, actor.UserID)
	if err != nil {
		return nil
	}
	return rec
}

// preferencesFor loads the signed-in account's admin-context preferences.
//
// A break-glass superuser has no `users` record, so it has none; the screen then
// shows defaults rather than failing.
func (s *Server) preferencesFor(c *gin.Context) *core.Record {
	actor := s.actor(c)
	if actor == nil || actor.IsSuperuser {
		return nil
	}

	rec, err := s.app.FindFirstRecordByFilter("user_preferences",
		"user = {:u} && context = {:c}",
		map[string]any{"u": actor.UserID, "c": prefContextAdmin})
	if err != nil || rec == nil {
		return nil
	}
	return rec
}

// currentPageSize returns the signed-in account's preferred page size, falling
// back to the configured default.
func (s *Server) currentPageSize(c *gin.Context) int {
	if rec := s.preferencesFor(c); rec != nil {
		if v := rec.GetInt("pageSize"); v > 0 && v <= 500 {
			return v
		}
	}
	return s.cfg.PerPage
}

// prefString reads a string preference, tolerating a missing record.
func prefString(rec *core.Record, field string) string {
	if rec == nil {
		return ""
	}
	return rec.GetString(field)
}

// preferenceLanguages lists the offered interface languages.
//
// Only English ships today; the list exists so the stored value is a closed set
// from the start rather than free text that later needs migrating.
func preferenceLanguages() []formOption {
	return []formOption{
		{Value: "en", Label: "English"},
		{Value: "zh-CN", Label: "简体中文"},
	}
}

// preferenceTimezones lists common zones for display purposes.
func preferenceTimezones() []formOption {
	return []formOption{
		{Value: "", Label: "— use the server default —"},
		{Value: "UTC", Label: "UTC"},
		{Value: "Asia/Shanghai", Label: "Asia/Shanghai (UTC+8)"},
		{Value: "Asia/Tokyo", Label: "Asia/Tokyo (UTC+9)"},
		{Value: "Asia/Singapore", Label: "Asia/Singapore (UTC+8)"},
		{Value: "Europe/London", Label: "Europe/London"},
		{Value: "Europe/Berlin", Label: "Europe/Berlin"},
		{Value: "America/New_York", Label: "America/New_York"},
		{Value: "America/Los_Angeles", Label: "America/Los_Angeles"},
	}
}

// preferenceTheme returns the signed-in account's theme for the layout, so the
// stored choice is applied rather than only recorded.
func (s *Server) preferenceTheme(c *gin.Context) string {
	if rec := s.preferencesFor(c); rec != nil {
		if theme := rec.GetString("theme"); theme != "" {
			return theme
		}
	}
	return "light"
}

// preferenceSidebarCollapsed reports whether the account asked for a collapsed
// sidebar.
func (s *Server) preferenceSidebarCollapsed(c *gin.Context) bool {
	rec := s.preferencesFor(c)
	return rec != nil && rec.GetBool("sidebarCollapsed")
}
