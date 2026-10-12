package server

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/audit"
	"pocketadmin/internal/jobs"
)

// jobHandlerOnce guards registration: Register panics on a duplicate code, which
// is the right guard against a genuine mistake but would fire when a Server is
// built more than once in one process, as tests do.
var jobHandlerOnce sync.Once

// RegisterJobHandlers adds this platform's tasks to the catalogue.
//
// The catalogue is registered in code and fixed at build time, so this is where a
// task's behaviour is defined -- an operator can change when it runs and whether
// it runs, and nothing else.
func (s *Server) RegisterJobHandlers() {
	jobHandlerOnce.Do(func() {
		s.registerAuditRetentionJob()
		s.registerSessionCleanupJob()
	})
}

// registerAuditRetentionJob prunes the logs according to the retention settings.
//
// This is the second seam from docs/decisions.md D-012 being paid off: the audit
// module could define the pruning but not schedule it, because the jobs module is
// a later wave. Both exist now, so it runs.
func (s *Server) registerAuditRetentionJob() {
	jobs.Register(jobs.Handler{
		Code:        "audit.retention",
		Name:        "Audit log retention",
		Description: "Deletes log entries older than the retention periods in the back-office settings. Zero means keep forever.",
		DefaultCron: "30 3 * * *",
		Run: func(ctx context.Context, _ map[string]any) (string, error) {
			retention := s.auditRetention()

			deleted, err := audit.Prune(s.app, retention, time.Now())
			if err != nil {
				return "", err
			}
			if len(deleted) == 0 {
				// "Nothing was old enough" and "nothing is configured to expire"
				// are different situations and an operator reading the history
				// needs to tell them apart: the first is normal, the second means
				// the logs will grow forever.
				if audit.NothingConfigured(retention) {
					return "nothing pruned: every log is set to be kept forever", nil
				}
				return "nothing pruned: no entries were older than their retention period", nil
			}

			// The count per log goes into the run history, because "pruned 12000
			// entries" is the number an operator wants and a bare success is not.
			summary := "pruned"
			for name, count := range deleted {
				summary += fmt.Sprintf(" %s=%d", name, count)
			}

			// A deletion is a security-relevant act, so it is recorded even though
			// it is routine: the audit log cannot be the only record that it was
			// pruned.
			s.auditSystem(auditEntry{
				Event:  audit.EventRetentionPruned,
				Module: "audit",
				// Pruning removes evidence, so it is recorded at warning rather
				// than info even though it is routine.
				Severity: audit.SeverityWarning,
				Summary:  "Audit retention removed entries: " + summary,
				Detail:   map[string]any{"deleted": deleted},
			})

			return summary, nil
		},
	})
}

// registerSessionCleanupJob removes session rows that can no longer be used.
//
// A revoked or expired row is kept for a while because the session list shows it,
// but keeping it forever means the table grows with rows nothing can act on.
func (s *Server) registerSessionCleanupJob() {
	jobs.Register(jobs.Handler{
		Code:        "identity.sessions.cleanup",
		Name:        "Expired session cleanup",
		Description: "Deletes session records that expired or were revoked more than 30 days ago.",
		DefaultCron: "0 4 * * *",
		Run: func(ctx context.Context, _ map[string]any) (string, error) {
			cutoff := time.Now().Add(-30 * 24 * time.Hour)

			records, err := s.app.FindRecordsByFilter("sessions",
				"(revokedAt != '' && revokedAt < {:cutoff}) || (expiresAt != '' && expiresAt < {:cutoff})",
				"", 0, 0, map[string]any{"cutoff": cutoff})
			if err != nil {
				return "", err
			}

			deleted := 0
			for _, rec := range records {
				select {
				case <-ctx.Done():
					return fmt.Sprintf("deleted %d before being cancelled", deleted), ctx.Err()
				default:
				}

				if err := s.app.Delete(rec); err != nil {
					return fmt.Sprintf("deleted %d before failing", deleted), err
				}
				deleted++
			}

			return fmt.Sprintf("deleted %d session record(s)", deleted), nil
		},
	})
}

// ---------------------------------------------------------------------------
// System-originated audit entries
// ---------------------------------------------------------------------------

// auditSystem writes a security event that has no request behind it.
//
// The ordinary audit path needs a gin context for the actor, the address and the
// request id. A scheduled task has none of those, and inventing a fake request
// would put information in the log that is not true.
func (s *Server) auditSystem(entry auditEntry) {
	if !audit.Valid(entry.Event) {
		slog.Error("refusing to record an undeclared audit event", "event", entry.Event)
		return
	}

	col, err := s.app.FindCollectionByNameOrId("security_audit_logs")
	if err != nil {
		slog.Error("the security audit log collection is missing", "error", err)
		return
	}

	severity := entry.Severity
	if severity == "" {
		severity = audit.SeverityInfo
	}

	rec := core.NewRecord(col)
	rec.Set("event", string(entry.Event))
	rec.Set("severity", string(severity))
	rec.Set("module", entry.Module)
	rec.Set("actorType", string(audit.ActorSystem))
	rec.Set("targetType", entry.TargetType)
	rec.Set("targetId", entry.TargetID)
	rec.Set("summary", truncate(entry.Summary, 500))

	if len(entry.Detail) > 0 {
		rec.Set("detail", audit.Redact(entry.Detail))
	}

	if err := s.app.Save(rec); err != nil {
		slog.Error("could not write a system audit entry", "event", entry.Event, "error", err)
	}
}

// StartScheduler registers the task catalogue, ensures every handler has a row,
// moves missed occurrences to their next future time and starts the loop.
//
// The returned function stops the scheduler and waits for in-flight work, so a
// shutdown does not interrupt a task halfway through a write.
func (s *Server) StartScheduler() func() {
	s.RegisterJobHandlers()

	// Before anything can write an event, make sure the stored catalogue covers
	// this build's. An upgraded database keeps the enum it was created with, and
	// a missing value means an audit entry that cannot be recorded.
	if err := audit.EnsureEventEnum(s.app); err != nil {
		slog.Error("could not widen the audit event catalogue", "error", err)
	}

	store := jobs.NewStore(s.app)

	if err := store.EnsureRows(); err != nil {
		slog.Error("could not ensure the scheduled task rows", "error", err)
	}

	// A process that was stopped has missed occurrences. They are not replayed:
	// an hourly task that missed a day should run once, not twenty-four times.
	if moved, err := store.CatchUp(time.Now()); err != nil {
		slog.Error("could not catch up the schedule", "error", err)
	} else if moved > 0 {
		slog.Info("moved missed task occurrences to their next run", "count", moved)
	}

	scheduler := jobs.NewScheduler(store)
	scheduler.Start(s.app)
	s.scheduler = scheduler

	return scheduler.Stop
}
