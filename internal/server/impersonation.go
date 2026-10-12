package server

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/permission"
	"pocketadmin/internal/rbac"
)

// Impersonation session keys.
//
// Starting an impersonation stashes the operator's own identity in the session
// and switches it to the target account, so the panel renders what that account
// would see. Stopping restores the stash, which means the operator keeps the
// exact session they arrived with, including its expiry and its revocation state.
const (
	sessionImpersonatorKey      = "imp_uid"
	sessionImpersonatorKindKey  = "imp_kind"
	sessionImpersonatorTokenKey = "imp_sid"
)

// ctxImpersonator carries the operator's account id for the current request.
const ctxImpersonator = "impersonator"

// registerImpersonationRoutes mounts starting and stopping impersonation.
//
// The stop route is deliberately not permission-guarded beyond authentication:
// ending an impersonation must always be possible, and requiring a permission
// would mean an operator whose rights were revoked mid-session could be trapped
// inside the account they were inspecting.
func (s *Server) registerImpersonationRoutes(panel *gin.RouterGroup) {
	s.guarded(panel, http.MethodPost, "/identity/accounts/:id/impersonate",
		permission.IdentityImpersonate, s.handleImpersonateStart)
	s.selfGuarded(panel, http.MethodPost, "/impersonation/stop", s.handleImpersonateStop)
}

// impersonatorID returns the operator's account id when the request is an
// impersonation, or "".
func (s *Server) impersonatorID(c *gin.Context) string {
	raw, ok := c.Get(ctxImpersonator)
	if !ok {
		return ""
	}
	id, _ := raw.(string)
	return id
}

// handleImpersonateStart switches the session to the target account.
func (s *Server) handleImpersonateStart(c *gin.Context) {
	operator := s.actor(c)
	if operator == nil {
		s.redirectToLogin(c)
		return
	}

	// Nested impersonation would make the audit trail ambiguous and could be used
	// to launder a session through several accounts.
	if s.impersonatorID(c) != "" {
		s.setFlash(c, "danger", "Stop the current impersonation before starting another.")
		c.Redirect(http.StatusSeeOther, "/identity/accounts")
		return
	}

	target, ok := s.findAccount(c)
	if !ok {
		return
	}

	if target.Id == operator.UserID {
		s.setFlash(c, "danger", "You are already signed in as that account.")
		c.Redirect(http.StatusSeeOther, "/identity/accounts/"+target.Id)
		return
	}
	if !target.GetDateTime("deletedAt").IsZero() {
		s.setFlash(c, "danger", "A deleted account cannot be impersonated.")
		c.Redirect(http.StatusSeeOther, "/identity/accounts/"+target.Id)
		return
	}

	sess := sessions.Default(c)

	// Stash the operator's own identity before switching.
	uid, _ := sess.Get(sessionUserKey).(string)
	kind, _ := sess.Get(sessionKindKey).(string)
	token, _ := sess.Get(sessionTokenKey).(string)
	sess.Set(sessionImpersonatorKey, uid)
	sess.Set(sessionImpersonatorKindKey, kind)
	sess.Set(sessionImpersonatorTokenKey, token)

	// The target gets its own revocable session, marked as impersonated and
	// attributed to the operator, so it is visible in the session list rather than
	// being an untraceable shadow.
	impersonationToken, err := s.createImpersonationSession(c, target, operator)
	if err != nil {
		slog.Error("could not create an impersonation session", "target", target.Id, "error", err)
		s.setFlash(c, "danger", "Could not start the impersonation: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/identity/accounts/"+target.Id)
		return
	}

	sess.Set(sessionUserKey, target.Id)
	sess.Set(sessionKindKey, sessionKindUser)
	sess.Set(sessionTokenKey, impersonationToken)

	if err := sess.Save(); err != nil {
		slog.Error("could not persist the impersonation session", "target", target.Id, "error", err)
		s.setFlash(c, "danger", "Could not start the impersonation.")
		c.Redirect(http.StatusSeeOther, "/identity/accounts/"+target.Id)
		return
	}

	s.logImpersonation(c, target, operator.Email, auditImpersonationStart, "Impersonation started")
	s.setFlash(c, "warning", "You are viewing the panel as "+target.Email()+
		". Writes are blocked; use the banner to stop.")

	c.Redirect(http.StatusSeeOther, "/dashboard")
}

// handleImpersonateStop restores the operator's own session.
func (s *Server) handleImpersonateStop(c *gin.Context) {
	sess := sessions.Default(c)

	operatorID, _ := sess.Get(sessionImpersonatorKey).(string)
	if operatorID == "" {
		// Not impersonating; nothing to restore.
		c.Redirect(http.StatusSeeOther, "/dashboard")
		return
	}
	operatorEmail := s.impersonatorEmail(c)

	// End the impersonation session rather than leaving a live one behind.
	if token, _ := sess.Get(sessionTokenKey).(string); token != "" {
		if rec, err := s.validateSessionRecord(token); err == nil && rec != nil {
			if err := s.revokeSessionRecord(rec, "impersonation_ended"); err != nil {
				slog.Warn("could not revoke the impersonation session", "error", err)
			}
		}
	}

	target, err := s.app.FindRecordById(rbac.CollectionUsers, s.actor(c).UserID)
	if err == nil && target != nil {
		s.logImpersonation(c, target, operatorEmail, auditImpersonationStop, "Impersonation stopped")
	}

	// Restore the operator's identity exactly as it was.
	sess.Set(sessionUserKey, operatorID)
	sess.Set(sessionKindKey, sess.Get(sessionImpersonatorKindKey))
	if token, _ := sess.Get(sessionImpersonatorTokenKey).(string); token != "" {
		sess.Set(sessionTokenKey, token)
	} else {
		sess.Delete(sessionTokenKey)
	}

	sess.Delete(sessionImpersonatorKey)
	sess.Delete(sessionImpersonatorKindKey)
	sess.Delete(sessionImpersonatorTokenKey)

	if err := sess.Save(); err != nil {
		slog.Error("could not restore the operator session", "operator", operatorID, "error", err)
		s.setFlash(c, "danger", "Could not stop the impersonation. Sign out and back in.")
		c.Redirect(http.StatusSeeOther, "/dashboard")
		return
	}

	s.setFlash(c, "success", "Impersonation ended.")
	c.Redirect(http.StatusSeeOther, "/identity/accounts")
}

// createImpersonationSession mints a session record for the impersonated account.
func (s *Server) createImpersonationSession(c *gin.Context, target *core.Record, operator *rbac.Actor) (string, error) {
	col, err := s.app.FindCollectionByNameOrId(sessionCollectionName)
	if err != nil {
		return "", err
	}

	raw := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)

	rec := core.NewRecord(col)
	rec.Set("user", target.Id)
	rec.Set("context", sessionContextAdmin)
	rec.Set("tokenHash", hashToken(token))
	rec.Set("ip", c.ClientIP())
	rec.Set("userAgent", truncate(c.Request.UserAgent(), sessionUserAgentMaxLen))
	rec.Set("device", deviceLabel(c.Request.UserAgent()))
	rec.Set("impersonated", true)
	// A break-glass superuser has no `users` row, so the attribution is only
	// recorded when there is one to point at.
	if !operator.IsSuperuser {
		rec.Set("impersonatedBy", operator.UserID)
	}
	rec.Set("lastSeenAt", time.Now().UTC())
	// An impersonation is a support action, not a working session, so it is
	// deliberately short-lived.
	rec.Set("expiresAt", time.Now().UTC().Add(impersonationTTL))

	if err := s.app.Save(rec); err != nil {
		return "", err
	}
	return token, nil
}

// impersonationTTL is how long an impersonated session lasts.
const impersonationTTL = 30 * time.Minute

// logImpersonation writes the audit entry.
//
// Impersonation is recorded in the sign-in history rather than a separate log,
// because that is where an investigator will look and because it already carries
// the actor, the target, the address and the device.
func (s *Server) logImpersonation(c *gin.Context, target *core.Record, operatorEmail, action, summary string) {
	s.audit(c, auditEntry{
		Action:     action,
		Resource:   "account",
		ResourceID: target.Id,
		Risk:       "high",
		Summary:    summary + " by " + operatorEmail + ".",
		Detail:     map[string]any{"operator": operatorEmail, "target": target.Email()},
		SubjectID:  target.Id, SubjectEmail: target.Email(),
	})
}

// blockWritesWhileImpersonating refuses state-changing requests during an
// impersonation.
//
// Support access is meant to look, not to act: an operator who could write while
// wearing someone else's account would leave a trail naming the wrong person. The
// stop route is exempt, since ending the impersonation is itself a write.
func (s *Server) blockWritesWhileImpersonating() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.impersonatorID(c) == "" || !isUnsafeMethod(c.Request.Method) {
			c.Next()
			return
		}
		if strings.HasPrefix(c.Request.URL.Path, "/impersonation/") {
			c.Next()
			return
		}

		s.setFlash(c, "warning", "Writes are blocked while impersonating. Stop the impersonation to make changes.")
		s.renderErrorPage(c, http.StatusForbidden, "Read-only while impersonating",
			"You are viewing the panel as another account. Changes are disabled until you stop impersonating.")
		c.Abort()
	}
}

// impersonatorEmail resolves the operator's address for the banner.
func (s *Server) impersonatorEmail(c *gin.Context) string {
	id := s.impersonatorID(c)
	if id == "" {
		return ""
	}
	if rec, err := s.app.FindRecordById(rbac.CollectionUsers, id); err == nil && rec != nil {
		return rec.Email()
	}
	return id
}
