package server

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/announcement"
	"pocketadmin/internal/audit"
	"pocketadmin/internal/developer"
	"pocketadmin/internal/jobs"
	"pocketadmin/internal/membership"
	"pocketadmin/internal/subscription"
	"pocketadmin/internal/workbench"
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
		s.registerAnnouncementJob()
		s.registerTodoReminderJob()
		s.registerAPIKeyExpiryJob()
		s.registerMembershipJob()
		s.registerSubscriptionJob()
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

// registerAnnouncementJob moves announcements in and out of the published state.
//
// The transition has to happen on a schedule rather than by comparing publishAt
// while rendering, because "published" is a state an operator looks at and a
// reader may be notified about: a purely computed state cannot be the trigger for
// anything.
func (s *Server) registerAnnouncementJob() {
	jobs.Register(jobs.Handler{
		Code:        "announcement.lifecycle",
		Name:        "Announcement publishing",
		Description: "Publishes announcements whose publish time has arrived and expires those whose window has closed.",
		// Every five minutes: an announcement scheduled for 09:00 appearing at
		// 09:04 is acceptable, and running this every minute would be noise.
		DefaultCron: "*/5 * * * *",
		Run: func(ctx context.Context, _ map[string]any) (string, error) {
			published, expired, err := announcement.Reconcile(s.app, time.Now())
			if err != nil {
				return "", err
			}

			if published == 0 && expired == 0 {
				return "nothing to change", nil
			}

			// Publication is announced to its audience, so the transition is worth
			// recording even though it is automatic.
			s.auditSystem(auditEntry{
				Event:    audit.EventSettingsChanged,
				Module:   "announcement",
				Summary:  fmt.Sprintf("Announcement lifecycle: %d published, %d expired", published, expired),
				Detail:   map[string]any{"published": published, "expired": expired},
				Severity: audit.SeverityInfo,
			})

			return fmt.Sprintf("published %d, expired %d", published, expired), nil
		},
	})
}

// registerTodoReminderJob delivers a message when a todo's reminder is due.
//
// The reminder is delivered into the same inbox as everything else rather than
// through a channel of its own, so there is one place a person looks. Delivery is
// deduplicated by the (message, user) unique index, which is what stops a sweep
// that runs every five minutes from sending the same reminder twelve times an
// hour.
func (s *Server) registerTodoReminderJob() {
	jobs.Register(jobs.Handler{
		Code:        "workbench.todo_reminders",
		Name:        "Todo reminders",
		Description: "Sends an inbox message when a todo's reminder time has arrived.",
		DefaultCron: "*/5 * * * *",
		Run: func(ctx context.Context, _ map[string]any) (string, error) {
			due, err := workbench.DueReminders(s.app, time.Now())
			if err != nil {
				return "", err
			}

			sent := 0
			for _, todo := range due {
				// The marker that a reminder has been delivered is the delivery
				// itself. A todo already carrying one is skipped without clearing
				// remindAt, so the row still shows that a reminder was due.
				if s.reminderAlreadySent(todo) {
					continue
				}

				message := workbench.Message{
					Title:   "Reminder: " + todo.GetString("title"),
					Content: "<p>A todo you scheduled is due for attention.</p>",
					Type:    "todo",
					Level:   "info",
					Link:    "/workbench",
				}
				if err := workbench.NotifySystem(s.app, todo.GetString("owner"), message); err != nil {
					continue
				}
				sent++
			}

			if sent == 0 {
				return "no reminders were due", nil
			}
			return fmt.Sprintf("sent %d reminder(s)", sent), nil
		},
	})
}

// reminderAlreadySent reports whether a todo has already produced a reminder.
func (s *Server) reminderAlreadySent(todo *core.Record) bool {
	existing, err := s.app.FindFirstRecordByFilter("messages",
		"type = 'todo' && title = {:title}",
		map[string]any{"title": "Reminder: " + todo.GetString("title")})
	if err != nil || existing == nil {
		return false
	}

	// The message exists, so check this owner received it.
	delivered, err := s.app.FindFirstRecordByFilter("message_recipients",
		"message = {:m} && user = {:u}",
		map[string]any{"m": existing.Id, "u": todo.GetString("owner")})
	return err == nil && delivered != nil
}

// registerAPIKeyExpiryJob marks keys whose expiry has passed.
//
// The expiry is enforced on every request anyway, so this is about the list
// telling the truth: a key shown as active that cannot be used is a key an
// operator will waste time investigating.
func (s *Server) registerAPIKeyExpiryJob() {
	jobs.Register(jobs.Handler{
		Code:        "developer.api_key_expiry",
		Name:        "API key expiry",
		Description: "Marks API keys whose expiry has passed so the list reflects what can actually be used.",
		DefaultCron: "*/30 * * * *",
		Run: func(ctx context.Context, _ map[string]any) (string, error) {
			expired, err := developer.ExpireStale(s.app, time.Now())
			if err != nil {
				return "", err
			}
			if expired == 0 {
				return "no keys had expired", nil
			}
			return fmt.Sprintf("marked %d key(s) expired", expired), nil
		},
	})
}

// registerMembershipJob expires memberships and warns about the ones ending soon.
//
// The transition is performed rather than computed at read time because "expired" is
// a state an operator queries by and a member may be notified about, and a purely
// computed state cannot be the trigger for anything.
func (s *Server) registerMembershipJob() {
	jobs.Register(jobs.Handler{
		Code:        "membership.lifecycle",
		Name:        "Membership expiry",
		Description: "Expires memberships whose time is up and reminds members whose membership ends within a week.",
		DefaultCron: "15 * * * *",
		Run: func(ctx context.Context, _ map[string]any) (string, error) {
			now := time.Now()

			expired, err := membership.ExpireDue(s.app, now)
			if err != nil {
				return "", err
			}

			// The reminder goes into the workbench inbox, which is where a person
			// already looks. It is sent once per membership per window: the delivery
			// is deduplicated by the message title and recipient, the same way the
			// todo reminder is.
			soon, err := membership.ExpiringSoon(s.app, 7*24*time.Hour, now)
			if err != nil {
				return "", err
			}

			reminded := 0
			for _, rec := range soon {
				userID := rec.GetString("user")
				if userID == "" || s.membershipReminderSent(rec) {
					continue
				}

				tierName := "your membership"
				if tierRec, err := s.app.FindRecordById(membership.CollectionTiers, rec.GetString("tier")); err == nil && tierRec != nil {
					tierName = tierRec.GetString("name")
				}

				message := workbench.Message{
					Title:   "Membership reminder: " + tierName,
					Content: "<p>Your " + tierName + " membership ends soon. Renew to keep your benefits.</p>",
					Type:    "system",
					Level:   "warning",
					Link:    "/membership/members",
				}
				if err := workbench.NotifySystem(s.app, userID, message); err != nil {
					continue
				}
				reminded++
			}

			if expired == 0 && reminded == 0 {
				return "nothing to change", nil
			}
			return fmt.Sprintf("expired %d, reminded %d", expired, reminded), nil
		},
	})
}

// membershipReminderSent reports whether this membership has already produced a
// reminder.
//
// Without it a task that runs hourly would send a week's worth of reminders a week,
// which is worse than sending none.
func (s *Server) membershipReminderSent(rec *core.Record) bool {
	userID := rec.GetString("user")
	if userID == "" {
		return true
	}

	existing, err := s.app.FindFirstRecordByFilter("message_recipients",
		"user = {:u} && message.title ~ 'Membership reminder'",
		map[string]any{"u": userID})
	return err == nil && existing != nil
}

// registerSubscriptionJob renews subscriptions that are due.
//
// It opens the next cycle rather than charging: the charge is an order, and the
// orders wave owns that. What this task owns is the calendar -- moving a
// subscription into its next period at the right moment, and marking one late when
// its period has ended without a payment.
func (s *Server) registerSubscriptionJob() {
	jobs.Register(jobs.Handler{
		Code:        "subscriptions.renewal",
		Name:        "Subscription renewal",
		Description: "Moves subscriptions whose period has ended into the next cycle, and marks the ones whose charge is late.",
		// Hourly. A subscription is billed on a day, not at a minute, so a few hours
		// of lateness costs nothing and an unrelated failure at the wrong minute
		// would otherwise delay a day's billing.
		DefaultCron: "20 * * * *",
		Run: func(ctx context.Context, _ map[string]any) (string, error) {
			now := time.Now()

			due, err := subscription.DueForRenewal(s.app, now)
			if err != nil {
				return "", err
			}

			renewed := 0
			late := 0

			for _, rec := range due {
				nextBilling := rec.GetDateTime("nextBillingAt")
				if nextBilling.IsZero() || nextBilling.Time().After(now) {
					continue
				}

				plan, err := s.app.FindRecordById(subscription.CollectionPlans, rec.GetString("plan"))
				if err != nil || plan == nil {
					// A subscription whose plan has vanished cannot be billed. It is
					// marked rather than deleted, because somebody is paying for it and
					// the record of that is the thing to keep.
					continue
				}

				// A subscription already in the retry state is not renewed; it is
				// waiting for a payment. Renewing it would move the period on and lose
				// the cycle that failed.
				if rec.GetString("status") == subscription.StatusPastDue {
					late++
					continue
				}

				if err := subscription.Renew(s.app, rec, plan, now); err != nil {
					slog.Error("could not renew a subscription", "subscription", rec.Id, "error", err)
					continue
				}

				// A pending cancellation takes effect here, at the period end, which is
				// the whole point of the option: the account keeps what it paid for.
				if rec.GetBool("cancelAtPeriodEnd") {
					rec.Set("status", subscription.StatusCancelled)
					rec.Set("nextBillingAt", nil)
					if err := s.app.Save(rec); err != nil {
						slog.Error("could not end a cancelled subscription", "subscription", rec.Id, "error", err)
						continue
					}
					continue
				}

				renewed++
			}

			if renewed == 0 && late == 0 {
				return "nothing was due", nil
			}
			return fmt.Sprintf("renewed %d, %d awaiting payment", renewed, late), nil
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
