package audit

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// Retention says how long each log is kept.
//
// Zero means "keep forever", which is the safe default: a retention period that
// silently defaults to something short would delete evidence nobody meant to
// discard, and the whole point of an append-only log is that its entries are
// still there when they are needed.
type Retention struct {
	OperationLog time.Duration
	LoginLog     time.Duration
	SecurityLog  time.Duration
	ExceptionLog time.Duration
}

// LogCollections maps a log to the field used for its age.
var LogCollections = []struct {
	Name     string
	Duration func(Retention) time.Duration
}{
	{"operation_logs", func(r Retention) time.Duration { return r.OperationLog }},
	{"login_logs", func(r Retention) time.Duration { return r.LoginLog }},
	{"security_audit_logs", func(r Retention) time.Duration { return r.SecurityLog }},
	{"exception_logs", func(r Retention) time.Duration { return r.ExceptionLog }},
}

// Prune deletes log entries older than the configured period and reports how many
// went.
//
// This is the only deletion path, which is what "append-only through the
// application" means: nothing updates or removes an entry as part of normal
// operation, and the one thing that does is a scheduled job an operator has to
// configure. Per docs/decisions.md D-012 the scheduling belongs to the jobs
// module (wave 4); the function lives here so that module has something to call
// and so its behaviour can be tested now.
func Prune(app core.App, retention Retention, now time.Time) (map[string]int, error) {
	deleted := map[string]int{}

	for _, log := range LogCollections {
		period := log.Duration(retention)
		if period <= 0 {
			continue
		}

		cutoff := now.Add(-period)

		records, err := app.FindRecordsByFilter(log.Name,
			"created < {:cutoff}", "", 0, 0, map[string]any{"cutoff": cutoff})
		if err != nil {
			return deleted, fmt.Errorf("audit: read %s for pruning: %w", log.Name, err)
		}

		for _, rec := range records {
			if err := app.Delete(rec); err != nil {
				return deleted, fmt.Errorf("audit: prune %s: %w", log.Name, err)
			}
			deleted[log.Name]++
		}
	}

	if len(deleted) > 0 {
		slog.Info("pruned audit logs", "deleted", deleted, "now", now.Format(time.RFC3339))
	}
	return deleted, nil
}

// NothingConfigured reports whether every retention period is zero.
//
// It exists so a caller can say "nothing is set to expire" rather than "nothing
// was old enough": an operator reading a task's history cannot otherwise tell a
// normal quiet run from a log that will grow without bound.
func NothingConfigured(retention Retention) bool {
	for _, log := range LogCollections {
		if log.Duration(retention) > 0 {
			return false
		}
	}
	return true
}

// Backdate moves a log entry's creation time.
//
// It exists for tests and for an operator repairing an import: the API refuses to
// change `created`, because accepting one from a request would let a caller place
// an entry anywhere in the timeline of an append-only log. The statement is
// parameterised, and the table name comes from this package's own list rather than
// from the caller.
func Backdate(app core.App, collection, id string, when time.Time) error {
	known := false
	for _, log := range LogCollections {
		if log.Name == collection {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("audit: %q is not one of the log collections", collection)
	}

	_, err := app.DB().NewQuery("UPDATE " + collection + " SET created = {:when} WHERE id = {:id}").
		Bind(map[string]any{
			"when": when.UTC().Format("2006-01-02 15:04:05.000Z"),
			"id":   id,
		}).Execute()
	return err
}
