package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// Collection names.
const (
	CollectionJobs = "scheduled_jobs"
	CollectionRuns = "job_runs"
)

// Store reads and writes scheduling state.
type Store struct {
	app core.App
}

// NewStore returns a store bound to an application.
func NewStore(app core.App) *Store { return &Store{app: app} }

// EnsureRows creates a scheduled row for every registered handler.
//
// It runs at startup rather than in a migration because the handler catalogue is
// registered in code and registration happens after migrations have run. Doing it
// here also means the rows follow the code: a release that adds a handler gets its
// row, and one that removes a handler leaves the old row inert rather than
// deleting configuration somebody may still be reading.
//
// An existing row is never overwritten. An operator who changed a schedule owns
// that change, and a deploy must not silently revert it.
func (s *Store) EnsureRows() error {
	col, err := s.app.FindCollectionByNameOrId(CollectionJobs)
	if err != nil || col == nil {
		return fmt.Errorf("jobs: the %s collection is missing", CollectionJobs)
	}

	for _, handler := range All() {
		// PocketBase reports "no rows" as an error rather than as a nil record, so
		// a missing row and a database fault arrive the same way. They are told
		// apart here rather than both being treated as not-found, because a real
		// fault silently creating a duplicate row would be worse than a failed
		// startup.
		existing, err := s.app.FindFirstRecordByFilter(CollectionJobs,
			"code = {:c}", map[string]any{"c": handler.Code})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("jobs: look up %s: %w", handler.Code, err)
		}
		if existing != nil {
			// Keep the display fields current; leave the schedule alone.
			changed := false
			if existing.GetString("name") != handler.Name && handler.Name != "" {
				existing.Set("name", handler.Name)
				changed = true
			}
			if existing.GetString("description") != handler.Description {
				existing.Set("description", handler.Description)
				changed = true
			}
			if changed {
				if err := s.app.Save(existing); err != nil {
					return err
				}
			}
			continue
		}

		rec := core.NewRecord(col)
		rec.Set("code", handler.Code)
		rec.Set("name", handler.Name)
		rec.Set("description", handler.Description)
		rec.Set("cron", handler.DefaultCron)
		rec.Set("status", "enabled")
		rec.Set("concurrency", "forbid")
		rec.Set("timeoutSeconds", 300)
		rec.Set("maxRetries", 0)
		rec.Set("retryBackoffSeconds", 60)
		rec.Set("notifyOnFailure", false)

		if schedule, err := ParseSchedule(handler.DefaultCron); err == nil {
			if next, ok := schedule.Next(time.Now()); ok {
				rec.Set("nextRunAt", next.UTC())
			}
		}

		if err := s.app.Save(rec); err != nil {
			return fmt.Errorf("jobs: create the row for %s: %w", handler.Code, err)
		}
	}

	return nil
}

// Due returns the enabled jobs whose next run has arrived.
func (s *Store) Due(now time.Time, limit int) ([]*core.Record, error) {
	records, err := s.app.FindRecordsByFilter(CollectionJobs,
		"status = 'enabled' && nextRunAt != '' && nextRunAt <= {:now}",
		"nextRunAt", limit, 0, map[string]any{"now": now.UTC()})
	if err != nil {
		return nil, fmt.Errorf("jobs: find due jobs: %w", err)
	}
	return records, nil
}

// OptionsFor reads a job's execution policy.
func OptionsFor(rec *core.Record) RunOptions {
	concurrency := Concurrency(rec.GetString("concurrency"))
	if concurrency == "" {
		concurrency = ConcurrencyForbid
	}

	return RunOptions{
		Timeout:     time.Duration(rec.GetInt("timeoutSeconds")) * time.Second,
		MaxRetries:  rec.GetInt("maxRetries"),
		Backoff:     time.Duration(rec.GetInt("retryBackoffSeconds")) * time.Second,
		Concurrency: concurrency,
	}
}

// Params reads a job's parameters.
func Params(rec *core.Record) map[string]any {
	raw := rec.Get("params")
	if raw == nil {
		return nil
	}
	decoded, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	return decoded
}

// StartRun records that a run is beginning and returns the history row.
func (s *Store) StartRun(rec *core.Record, triggeredBy, userID string) (*core.Record, error) {
	col, err := s.app.FindCollectionByNameOrId(CollectionRuns)
	if err != nil || col == nil {
		return nil, fmt.Errorf("jobs: the %s collection is missing", CollectionRuns)
	}

	run := core.NewRecord(col)
	run.Set("job", rec.Id)
	run.Set("startedAt", time.Now().UTC())
	run.Set("status", StatusRunning)
	run.Set("triggeredBy", triggeredBy)
	run.Set("attempt", 1)
	if userID != "" {
		run.Set("triggeredByUser", userID)
	}

	if err := s.app.Save(run); err != nil {
		return nil, fmt.Errorf("jobs: record the start of a run: %w", err)
	}
	return run, nil
}

// FinishRun closes a history row and updates the job's own mirror of its state.
func (s *Store) FinishRun(run, rec *core.Record, result Result) error {
	now := time.Now().UTC()

	run.Set("finishedAt", now)
	run.Set("durationMs", result.Duration.Milliseconds())
	run.Set("status", result.Status)
	run.Set("attempt", result.Attempts)
	if result.Output != "" {
		run.Set("output", truncate(result.Output, 20000))
	}
	if result.Error != "" {
		run.Set("error", truncate(result.Error, 20000))
	}

	if err := s.app.Save(run); err != nil {
		return fmt.Errorf("jobs: close a run: %w", err)
	}

	if rec == nil {
		return nil
	}

	rec.Set("lastRunAt", now)
	rec.Set("lastStatus", result.Status)

	// The next occurrence is computed from now rather than from the scheduled
	// time, so a task that was delayed -- because the process was down, or the
	// previous run overran -- does not fire repeatedly to catch up. Running an
	// hourly task twenty-four times after a day of downtime is worse than missing
	// the day.
	schedule, err := ParseSchedule(rec.GetString("cron"))
	if err != nil {
		return nil
	}
	if next, ok := schedule.Next(now); ok {
		rec.Set("nextRunAt", next.UTC())
	} else {
		// The expression can never match again; disable rather than spin.
		rec.Set("status", "disabled")
		rec.Set("nextRunAt", nil)
		slog.Warn("a scheduled task has no future occurrence and was disabled",
			"code", rec.GetString("code"), "cron", rec.GetString("cron"))
	}

	return s.app.Save(rec)
}

// Reschedule recomputes a job's next occurrence from its expression.
//
// Called when an operator changes the schedule, and at startup for any job whose
// next run is in the past -- which is what happens after downtime.
func (s *Store) Reschedule(rec *core.Record, from time.Time) error {
	schedule, err := ParseSchedule(rec.GetString("cron"))
	if err != nil {
		return err
	}

	if rec.GetString("status") != "enabled" {
		rec.Set("nextRunAt", nil)
		return s.app.Save(rec)
	}

	if next, ok := schedule.Next(from); ok {
		rec.Set("nextRunAt", next.UTC())
	} else {
		rec.Set("nextRunAt", nil)
	}
	return s.app.Save(rec)
}

// CatchUp recomputes the next run of every job whose scheduled time has passed.
//
// A process that was stopped has missed occurrences. Running them all on startup
// would be a thundering herd of stale work, so each job is moved to its next
// future occurrence and the missed ones are simply not run.
func (s *Store) CatchUp(now time.Time) (int, error) {
	records, err := s.app.FindRecordsByFilter(CollectionJobs,
		"status = 'enabled' && (nextRunAt = '' || nextRunAt <= {:now})",
		"nextRunAt", 0, 0, map[string]any{"now": now.UTC()})
	if err != nil {
		return 0, err
	}

	moved := 0
	for _, rec := range records {
		if err := s.Reschedule(rec, now); err != nil {
			slog.Warn("could not reschedule a task", "code", rec.GetString("code"), "error", err)
			continue
		}
		moved++
	}
	return moved, nil
}

// RetryRun starts a new run for a job, used by the failure-retry path.
func (s *Store) RetryRun(rec *core.Record, userID string) (*core.Record, Result, error) {
	handler, ok := Lookup(rec.GetString("code"))
	if !ok {
		return nil, Result{}, fmt.Errorf("no handler is registered for %q", rec.GetString("code"))
	}

	run, err := s.StartRun(rec, "retry", userID)
	if err != nil {
		return nil, Result{}, err
	}

	result := Run(context.Background(), handler, Params(rec), OptionsFor(rec))
	if err := s.FinishRun(run, rec, result); err != nil {
		return run, result, err
	}
	return run, result, nil
}

// truncate shortens a string to a byte limit.
func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
