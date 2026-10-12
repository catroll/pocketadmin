package server

import (
	"log/slog"

	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/audit"
)

// defaultAuditModule is recorded when an entry does not name one.
const defaultAuditModule = "identity"

// auditEntry is one security-relevant event.
type auditEntry struct {
	// Event is a declared member of the audit catalogue. It is a typed constant
	// so an undeclared name does not compile: the collection stores a closed
	// select, and a value outside it is rejected at write time, which is how the
	// account-lifecycle trail was silently lost before it was typed.
	Event audit.Event

	// Module owns the action, for grouping and filtering.
	Module string

	// TargetType and TargetID name what was acted on.
	TargetType string
	TargetID   string

	// Severity ranks the entry for review. Empty means info.
	Severity audit.Severity

	// Summary is a human-readable line; Detail carries structured context.
	Summary string
	Detail  map[string]any

	// SubjectID and SubjectEmail name the account acted on, when that differs
	// from the actor.
	SubjectID    string
	SubjectEmail string
}

// audit writes a security event.
//
// Unlike a sign-in attempt, an audit event that cannot be written is a real
// problem: the action has already happened and the record of it is missing. A
// failure is logged at error level and names the event, so it cannot pass
// quietly -- which is exactly how the missing-value bug in the sign-in history
// went unnoticed.
func (s *Server) audit(c *gin.Context, entry auditEntry) {
	if !audit.Valid(entry.Event) {
		// A programming error rather than a runtime one, and one that would
		// otherwise be swallowed as a rejected write.
		slog.Error("refusing to record an undeclared audit event", "event", entry.Event)
		return
	}

	if entry.Module == "" {
		entry.Module = defaultAuditModule
	}
	if entry.Severity == "" {
		entry.Severity = audit.SeverityInfo
	}

	col, err := s.app.FindCollectionByNameOrId("security_audit_logs")
	if err != nil {
		slog.Error("the security audit log collection is missing; a security event was not recorded",
			"event", entry.Event, "error", err)
		return
	}

	rec := core.NewRecord(col)
	rec.Set("event", string(entry.Event))
	rec.Set("severity", string(entry.Severity))
	rec.Set("module", entry.Module)
	rec.Set("targetType", entry.TargetType)
	rec.Set("targetId", entry.TargetID)
	rec.Set("summary", truncate(entry.Summary, 500))

	actorType, actorID, actorEmail := s.auditActor(c)
	rec.Set("actorType", string(actorType))
	if actorID != "" {
		rec.Set("actor", actorID)
	}
	rec.Set("actorEmail", actorEmail)

	rec.Set("ip", c.ClientIP())
	rec.Set("userAgent", truncate(c.Request.UserAgent(), sessionUserAgentMaxLen))
	rec.Set("requestId", requestIDFrom(c))

	// Structured detail can carry anything a caller passes, so it goes through
	// the redactor before it is stored: a secret that reaches a log has leaked.
	if len(entry.Detail) > 0 {
		rec.Set("detail", audit.Redact(entry.Detail))
	}

	if err := app_save(s, rec); err != nil {
		slog.Error("could not write an audit entry; a security event was not recorded",
			"event", entry.Event, "error", err)
	}
}

// app_save is a seam so the write path has one place to change.
func app_save(s *Server, rec *core.Record) error { return s.app.Save(rec) }

// auditActor describes who is acting.
//
// A break-glass superuser has no `users` row, so it is recorded as a system actor
// with its address rather than losing the entry or attributing it to nobody.
func (s *Server) auditActor(c *gin.Context) (audit.ActorType, string, string) {
	actor := s.actor(c)
	switch {
	case actor == nil:
		return audit.ActorSystem, "", ""
	case actor.IsSuperuser:
		return audit.ActorSystem, "", actor.Email
	default:
		return audit.ActorUser, actor.UserID, actor.Email
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
