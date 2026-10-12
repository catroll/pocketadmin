package audit_test

import (
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/audit"
	"pocketadmin/internal/testsupport"
)

// seedOperations writes an operation log entry and dates it.
func seedOperations(t *testing.T, app core.App, when time.Time) *core.Record {
	t.Helper()

	col, err := app.FindCollectionByNameOrId("operation_logs")
	if err != nil {
		t.Fatalf("operation_logs: %v", err)
	}

	rec := core.NewRecord(col)
	rec.Set("module", "probe")
	rec.Set("action", "seed")
	rec.Set("method", "POST")
	rec.Set("path", "/probe")
	rec.Set("result", "success")
	rec.Set("statusCode", 200)

	if err := app.Save(rec); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := audit.Backdate(app, "operation_logs", rec.Id, when); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	return rec
}

// countOperations returns how many probe entries remain.
func countOperations(t *testing.T, app core.App) int {
	t.Helper()

	records, err := app.FindRecordsByFilter("operation_logs", "module = 'probe'", "", 0, 0)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	return len(records)
}

// TestPruneDeletesOnlyWhatIsExpired is the behaviour the retention feature exists
// for, and it is tested by writing an entry and genuinely dating it rather than by
// trusting the query.
func TestPruneDeletesOnlyWhatIsExpired(t *testing.T) {
	app := testsupport.NewApp(t)

	seedOperations(t, app, time.Now().Add(-400*24*time.Hour)) // well past 90 days
	seedOperations(t, app, time.Now().Add(-10*24*time.Hour))  // inside 90 days
	seedOperations(t, app, time.Now().Add(-200*24*time.Hour)) // past 90, inside 180

	// Only the operation log expires; the others are kept forever, which is also
	// what the defaults say.
	retention := audit.Retention{
		OperationLog: 90 * 24 * time.Hour,
		LoginLog:     180 * 24 * time.Hour,
		SecurityLog:  0,
		ExceptionLog: 30 * 24 * time.Hour,
	}

	deleted, err := audit.Prune(app, retention, time.Now())
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}

	if deleted["operation_logs"] != 2 {
		t.Errorf("deleted %d operation entries, want 2", deleted["operation_logs"])
	}
	if remaining := countOperations(t, app); remaining != 1 {
		t.Errorf("%d probe entries remain, want the one inside the window", remaining)
	}
}

// TestPruneKeepsEverythingWhenNoPeriodIsSet: zero means keep forever, and the
// default has to be the safe direction.
func TestPruneKeepsEverythingWhenNoPeriodIsSet(t *testing.T) {
	app := testsupport.NewApp(t)

	seedOperations(t, app, time.Now().Add(-3650*24*time.Hour))

	deleted, err := audit.Prune(app, audit.Retention{}, time.Now())
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(deleted) != 0 {
		t.Errorf("a zero retention deleted %v", deleted)
	}
	if remaining := countOperations(t, app); remaining != 1 {
		t.Errorf("%d entries remain, want all of them", remaining)
	}
	if !audit.NothingConfigured(audit.Retention{}) {
		t.Error("an all-zero retention was not reported as unconfigured")
	}
}

// TestNothingConfiguredDistinguishesTheTwoCases: a task's history has to say
// whether a quiet run was quiet or whether the logs will grow forever.
func TestNothingConfiguredDistinguishesTheTwoCases(t *testing.T) {
	if !audit.NothingConfigured(audit.Retention{}) {
		t.Error("all zeros should read as unconfigured")
	}

	one := audit.Retention{SecurityLog: time.Hour}
	if audit.NothingConfigured(one) {
		t.Error("a retention with one period set should not read as unconfigured")
	}
}

// TestBackdateRefusesAnUnknownCollection: the table name is interpolated into the
// statement, so it must come from this package's own list and not from a caller.
func TestBackdateRefusesAnUnknownCollection(t *testing.T) {
	app := testsupport.NewApp(t)

	if err := audit.Backdate(app, "users", "someid", time.Now()); err == nil {
		t.Error("backdating a collection that is not a log was allowed")
	}
	if err := audit.Backdate(app, "operation_logs; DROP TABLE users", "x", time.Now()); err == nil {
		t.Error("a collection name with a statement in it was allowed")
	}
}
