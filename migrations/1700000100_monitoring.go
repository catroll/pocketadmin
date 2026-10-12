package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
)

// monitoringMigration creates the optional metric snapshots and the update record.
//
// Snapshots are optional in the sense the specification means: the live panel works
// without them, and they exist so a trend can be seen over a period longer than the
// in-memory window. Keeping every measurement forever is not the intent.
func init() {
	migrations.Register(func(app core.App) error {
		if err := createMetricSnapshots(app); err != nil {
			return err
		}
		if err := createSystemUpdates(app); err != nil {
			return err
		}
		if err := seedPermissionPoints(app); err != nil {
			return err
		}
		return seedMenus(app)
	}, func(app core.App) error {
		for _, name := range []string{"system_updates", "metric_snapshots"} {
			if col := findCollection(app, name); col != nil {
				if err := app.Delete(col); err != nil {
					return err
				}
			}
		}
		return nil
	}, "1700000100_monitoring.go")
}

// createMetricSnapshots creates the periodic measurements.
func createMetricSnapshots(app core.App) error {
	col := findCollection(app, "metric_snapshots")
	if col == nil {
		col = newBase("metric_snapshots")
	}
	ensureTimestamps(col)

	addField(col, &core.DateField{Name: "capturedAt"})
	addField(col, &core.NumberField{Name: "cpuPercent"})
	addField(col, &core.NumberField{Name: "memoryBytes"})
	addField(col, &core.NumberField{Name: "goroutines"})
	addField(col, &core.NumberField{Name: "heapBytes"})
	addField(col, &core.NumberField{Name: "dbBytes"})
	addField(col, &core.NumberField{Name: "auxDbBytes"})
	addField(col, &core.NumberField{Name: "fileBytes"})
	addField(col, &core.NumberField{Name: "fileCount"})
	addField(col, &core.NumberField{Name: "requestsPerMinute"})
	addField(col, &core.NumberField{Name: "errorRate"})
	addField(col, &core.NumberField{Name: "latencyP95Ms"})

	col.AddIndex("idx_snapshots_captured", false, "capturedAt", "")

	lockDown(col)

	return app.Save(col)
}

// createSystemUpdates creates the record of version checks and applied updates.
func createSystemUpdates(app core.App) error {
	col := findCollection(app, "system_updates")
	if col == nil {
		col = newBase("system_updates")
	}
	ensureTimestamps(col)

	addField(col, text("version", true))
	// The version that was running. It is recorded separately from the target so an
	// applied update has a before and an after, which is the whole reason the
	// record exists.
	addField(col, text("fromVersion", false))
	addField(col, selectOf("channel", true, "stable", "beta"))
	addField(col, selectOf("status", true, "available", "downloading", "ready", "applied", "failed", "rolled_back"))
	addField(col, &core.EditorField{Name: "releaseNotes"})
	addField(col, &core.DateField{Name: "publishedAt"})
	addField(col, &core.DateField{Name: "appliedAt"})
	addField(col, rel(app, "appliedBy", "users", false))
	// The checksum and the error are what make an update auditable: what was
	// expected, and what went wrong.
	addField(col, text("checksum", false))
	addField(col, &core.TextField{Name: "error", Max: 4000})

	col.AddIndex("idx_updates_status", false, "status", "")
	col.AddIndex("idx_updates_version", false, "version", "")

	disableCascade(col, "appliedBy")
	lockDown(col)

	return app.Save(col)
}
