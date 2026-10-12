package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
)

// jobsMigration creates the scheduled-task metadata and its run history.
//
// The two collections are created here but no job rows are seeded: the catalogue
// of handlers is registered in code, and registration happens after migrations
// have run, so a migration cannot see it. The rows are ensured at startup instead,
// which is also the more honest arrangement -- the rows follow the code rather
// than a snapshot of it taken at migration time.
func init() {
	migrations.Register(func(app core.App) error {
		if err := createScheduledJobs(app); err != nil {
			return err
		}
		if err := createJobRuns(app); err != nil {
			return err
		}
		if err := seedPermissionPoints(app); err != nil {
			return err
		}
		return seedMenus(app)
	}, func(app core.App) error {
		for _, name := range []string{"job_runs", "scheduled_jobs"} {
			if col := findCollection(app, name); col != nil {
				if err := app.Delete(col); err != nil {
					return err
				}
			}
		}
		return nil
	}, "1700000050_jobs.go")
}

// createScheduledJobs creates the scheduling metadata.
func createScheduledJobs(app core.App) error {
	col := findCollection(app, "scheduled_jobs")
	if col == nil {
		col = newBase("scheduled_jobs")
	}
	ensureTimestamps(col)

	addField(col, text("name", true))
	// code must match a handler registered in this binary. A row whose code has no
	// handler is inert rather than an error, so a migration or a downgrade cannot
	// stop the process starting.
	addField(col, text("code", true))
	addField(col, text("description", false))
	addField(col, text("cron", true))
	addField(col, jsonField("params", 20000))
	addField(col, selectOf("status", true, "enabled", "disabled", "paused"))
	addField(col, selectOf("concurrency", false, "allow", "forbid", "replace"))
	addField(col, &core.NumberField{Name: "timeoutSeconds"})
	addField(col, &core.NumberField{Name: "maxRetries"})
	addField(col, &core.NumberField{Name: "retryBackoffSeconds"})
	addField(col, &core.BoolField{Name: "notifyOnFailure"})
	addField(col, multiRel(app, "notifyTargets", "users", 50))
	addField(col, &core.DateField{Name: "lastRunAt"})
	addField(col, &core.DateField{Name: "nextRunAt"})
	addField(col, selectOf("lastStatus", false,
		"running", "success", "failed", "timeout", "cancelled", "skipped"))
	addField(col, rel(app, "createdBy", "users", false))

	col.AddIndex("idx_jobs_code", true, "code", "")
	col.AddIndex("idx_jobs_status", false, "status", "")
	col.AddIndex("idx_jobs_next", false, "nextRunAt", "")

	disableCascade(col, "notifyTargets", "createdBy")
	lockDown(col)

	return app.Save(col)
}

// createJobRuns creates the execution history.
func createJobRuns(app core.App) error {
	col := findCollection(app, "job_runs")
	if col == nil {
		col = newBase("job_runs")
	}
	ensureTimestamps(col)

	addField(col, rel(app, "job", "scheduled_jobs", true))
	addField(col, &core.DateField{Name: "startedAt"})
	addField(col, &core.DateField{Name: "finishedAt"})
	addField(col, &core.NumberField{Name: "durationMs"})
	addField(col, selectOf("status", true,
		"running", "success", "failed", "timeout", "cancelled", "skipped"))
	addField(col, selectOf("triggeredBy", true, "cron", "manual", "retry", "api"))
	addField(col, rel(app, "triggeredByUser", "users", false))
	addField(col, &core.NumberField{Name: "attempt"})
	// Output and error are bounded but generous: a task that reports what it did
	// should not have that report truncated to nothing.
	addField(col, &core.TextField{Name: "output", Max: 20000})
	addField(col, &core.TextField{Name: "error", Max: 20000})

	col.AddIndex("idx_jobruns_job", false, "job, startedAt", "")
	col.AddIndex("idx_jobruns_status", false, "status", "")
	col.AddIndex("idx_jobruns_started", false, "startedAt", "")

	disableCascade(col, "job", "triggeredByUser")
	lockDown(col)

	return app.Save(col)
}
