package server

import (
	"log/slog"

	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"
)

// Audit actions recorded by the identity module.
const (
	auditImpersonationStart = "impersonation.start"
	auditImpersonationStop  = "impersonation.stop"
	auditStatusChange       = "account.status_change"
	auditPasswordReset      = "account.password_reset"
	auditSoftDelete         = "account.soft_delete"
	auditRestore            = "account.restore"
	auditSessionRevoke      = "session.revoke"
	auditSessionRevokeAll   = "session.revoke_all"
	auditProfileAttach      = "profile.attach"
	auditProfileDetach      = "profile.detach"
	auditRoleAssign         = "role.assign"

	auditModuleIdentity = "identity"
)

// auditEntry is one security-relevant event.
type auditEntry struct {
	// Action is a dotted verb such as "impersonation.start".
	Action string

	// Module owns the action.
	Module string

	// Resource and ResourceID name what was acted on.
	Resource   string
	ResourceID string

	// Result is success, failure or denied.
	Result string

	// Risk ranks the entry for review: low, medium or high.
	Risk string

	// Summary is a human-readable line; Detail carries structured context.
	Summary string
	Detail  map[string]any

	// SubjectID is the account acted on, when it differs from the actor.
	SubjectID    string
	SubjectEmail string
}

// audit writes a security event.
//
// Unlike a sign-in attempt, an audit event that cannot be written is a real
// problem: the action has already happened and the trail of it is missing. A
// failure is logged at error level so it cannot pass quietly, which is how the
// missing-value bug in the sign-in history went unnoticed.
func (s *Server) audit(c *gin.Context, entry auditEntry) {
	if entry.Module == "" {
		entry.Module = auditModuleIdentity
	}
	if entry.Result == "" {
		entry.Result = "success"
	}
	if entry.Risk == "" {
		entry.Risk = "low"
	}

	col, err := s.app.FindCollectionByNameOrId("audit_logs")
	if err != nil {
		slog.Error("the audit log collection is missing; a security event was not recorded",
			"action", entry.Action, "error", err)
		return
	}

	rec := core.NewRecord(col)

	actor := s.actor(c)
	if actor != nil && !actor.IsSuperuser {
		rec.Set("user", actor.UserID)
		rec.Set("actorEmail", actor.Email)
	} else if actor != nil {
		// A break-glass superuser has no `users` row, so only the address is known.
		rec.Set("actorEmail", actor.Email)
	}

	if entry.SubjectID != "" {
		rec.Set("identifier", entry.SubjectEmail)
	}

	rec.Set("context", sessionContextAdmin)
	rec.Set("module", entry.Module)
	rec.Set("action", entry.Action)
	rec.Set("resource", entry.Resource)
	rec.Set("resourceId", entry.ResourceID)
	rec.Set("result", entry.Result)
	rec.Set("riskLevel", entry.Risk)
	rec.Set("summary", truncate(entry.Summary, 500))
	rec.Set("ip", c.ClientIP())
	rec.Set("userAgent", truncate(c.Request.UserAgent(), sessionUserAgentMaxLen))

	if len(entry.Detail) > 0 {
		rec.Set("detail", entry.Detail)
	}

	if err := s.app.Save(rec); err != nil {
		slog.Error("could not write an audit entry; a security event was not recorded",
			"action", entry.Action, "error", err)
	}
}

// logLogin writes a sign-in attempt.
//
// A failure here is logged at error level rather than swallowed: the sign-in
// history is evidence, and an unnoticed gap in it is worse than a noisy log.
func (s *Server) logLogin(c *gin.Context, userID, identifier, result, reason, risk string) {
	col, err := s.app.FindCollectionByNameOrId("login_logs")
	if err != nil {
		slog.Error("the sign-in history collection is missing; an attempt was not recorded",
			"result", result, "error", err)
		return
	}

	rec := core.NewRecord(col)
	if userID != "" {
		rec.Set("user", userID)
	}
	rec.Set("context", sessionContextAdmin)
	rec.Set("identifier", truncate(identifier, 255))
	rec.Set("ip", c.ClientIP())
	rec.Set("userAgent", truncate(c.Request.UserAgent(), sessionUserAgentMaxLen))
	rec.Set("device", deviceLabel(c.Request.UserAgent()))
	rec.Set("result", result)
	rec.Set("reason", truncate(reason, 255))
	rec.Set("riskLevel", risk)

	if err := s.app.Save(rec); err != nil {
		slog.Error("could not write a sign-in history entry",
			"identifier", identifier, "result", result, "error", err)
	}
}
