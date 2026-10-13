package jobs_test

import (
	"context"

	"github.com/pocketbase/pocketbase/core"
	"sync/atomic"
	"testing"
	"time"

	"pocketadmin/internal/jobs"
	"pocketadmin/internal/testsupport"
)

// The test's own handler, registered under a code no other test uses. The catalogue is
// package-level, so a code that collides would replace another test's handler.
const tickTestCode = "test.scheduler.tick"

var tickTestRuns atomic.Int64

func init() {
	jobs.Register(jobs.Handler{
		Code:        tickTestCode,
		Name:        "Scheduler tick test",
		Description: "Runs on a schedule the test sets.",
		DefaultCron: "0 3 * * *",
		Run: func(ctx context.Context, _ map[string]any) (string, error) {
			tickTestRuns.Add(1)
			return "ran", nil
		},
	})
}

// createJob writes the row for the test handler directly.
//
// It is written here rather than through Store.EnsureRows because the handler catalogue
// is package-level and shared with every other test in this package: EnsureRows walks
// all of it, so one unrelated fixture with an incomplete handler would fail this test.
// A test that depends on the whole registry is a test that breaks when a neighbour
// changes.
func createJob(t *testing.T, app core.App, cron string) *core.Record {
	t.Helper()

	col, err := app.FindCollectionByNameOrId("scheduled_jobs")
	if err != nil || col == nil {
		t.Fatalf("the scheduled_jobs collection is missing: %v", err)
	}

	rec := core.NewRecord(col)
	rec.Set("code", tickTestCode)
	rec.Set("name", "Scheduler tick test")
	rec.Set("description", "Runs on a schedule the test sets.")
	rec.Set("cron", cron)
	rec.Set("status", "enabled")
	rec.Set("concurrency", "forbid")
	if err := app.Save(rec); err != nil {
		t.Fatalf("create the test job: %v", err)
	}
	return rec
}

// TestTheSchedulerTicksEverySecond is the liveness property the seconds field needs.
//
// The tick is what makes a seconds-granularity schedule possible at all: a twenty-second
// tick, which is what it was, cannot run a ten-second task more than once per twenty
// seconds. The tick also feeds the monitoring screen's "scheduler last tick" reading,
// so a stalled loop shows up as a stale reading rather than as silently late work.
func TestTheSchedulerTicksEverySecond(t *testing.T) {
	app := testsupport.NewApp(t)

	scheduler := jobs.NewScheduler(jobs.NewStore(app))
	scheduler.Start(app)
	defer scheduler.Stop()

	// The ticker's first tick is a second away, so an immediate check would see
	// nothing even on a healthy loop.
	time.Sleep(1200 * time.Millisecond)

	first := scheduler.LastTick()
	if first.IsZero() {
		t.Fatal("the scheduler has never ticked")
	}

	time.Sleep(1200 * time.Millisecond)
	last := scheduler.LastTick()
	if time.Since(last) > time.Second {
		t.Errorf("the last tick was %v ago, so the loop is not ticking every second", time.Since(last))
	}
	if !last.After(first) {
		t.Error("the tick reading did not advance, so the loop is not running")
	}
}

// TestADueJobRunsPromptly is the property a seconds-granularity schedule depends on.
//
// The job is due now, and the loop has to notice within its tick rather than at the
// next rescan. That is the difference between a schedule that means something and one
// that means "sometime in the next half minute".
func TestADueJobRunsPromptly(t *testing.T) {
	app := testsupport.NewApp(t)

	store := jobs.NewStore(app)
	rec := createJob(t, app, "*/10 * * * * *")

	// Already due, and enabled.
	rec.Set("status", "enabled")
	rec.Set("nextRunAt", time.Now().Add(-time.Second).UTC())
	if err := app.Save(rec); err != nil {
		t.Fatalf("schedule the test job: %v", err)
	}

	before := tickTestRuns.Load()

	scheduler := jobs.NewScheduler(store)
	scheduler.Start(app)
	defer scheduler.Stop()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if tickTestRuns.Load() > before {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Error("a job that was already due did not run within ten seconds")
}

// TestTheNextOccurrenceFollowsTheSecondsField: the row is rescheduled on the
// expression's own granularity, so a seconds job does not wait a minute to run again.
func TestTheNextOccurrenceFollowsTheSecondsField(t *testing.T) {
	app := testsupport.NewApp(t)

	store := jobs.NewStore(app)
	rec := createJob(t, app, "*/10 * * * * *")

	if err := store.Reschedule(rec, time.Now()); err != nil {
		t.Fatalf("Reschedule: %v", err)
	}

	next := rec.GetDateTime("nextRunAt").Time()
	if next.IsZero() {
		t.Fatal("a seconds schedule produced no next run")
	}
	if !next.After(time.Now()) {
		t.Error("the next run is in the past")
	}
	if delay := time.Until(next); delay > 10*time.Second {
		t.Errorf("the next run is %v away, which is not a ten-second schedule", delay)
	}
}
