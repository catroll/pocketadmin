package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
)

// hostMetricsMigration creates the table the lightweight host monitor writes to.
//
// It is one table and one screen. The feature exists so that a small installation does
// not have to run Prometheus and Grafana to notice its disk filling up, and every
// column below is the kernel's own figure rather than a derived one -- a percentage
// computed at write time cannot be recomputed if the interval it was computed over is
// read differently later.
func init() {
	migrations.Register(func(app core.App) error {
		col := findCollection(app, "host_metrics")
		if col == nil {
			col = newBase("host_metrics")
		}
		ensureTimestamps(col)

		addField(col, &core.DateField{Name: "at"})

		// Processor time in seconds, used and total, so the percentage is a division
		// by the caller rather than a number frozen at write time.
		addField(col, &core.NumberField{Name: "cpuUsedSeconds"})
		addField(col, &core.NumberField{Name: "cpuTotalSeconds"})

		addField(col, &core.NumberField{Name: "load1"})
		addField(col, &core.NumberField{Name: "load5"})
		addField(col, &core.NumberField{Name: "load15"})

		addField(col, &core.NumberField{Name: "memUsedBytes"})
		addField(col, &core.NumberField{Name: "memTotalBytes"})
		addField(col, &core.NumberField{Name: "diskUsedBytes"})
		addField(col, &core.NumberField{Name: "diskTotalBytes"})

		// Cumulative counters, not rates. A rate needs two rows and the time between
		// them, and storing one would make a row's meaning depend on its neighbour.
		addField(col, &core.NumberField{Name: "netRxBytes"})
		addField(col, &core.NumberField{Name: "netTxBytes"})
		addField(col, &core.NumberField{Name: "uptimeSeconds"})

		// What the platform could not report, so a screen says "not available" rather
		// than showing a confident zero.
		addField(col, multiSelectOf("unsupported", 0,
			"cpu", "load", "memory", "disk", "network"))

		col.AddIndex("idx_host_metrics_at", false, "at", "")

		disableCascade(col)
		lockDown(col)

		return app.Save(col)
	}, func(app core.App) error {
		if col := findCollection(app, "host_metrics"); col != nil {
			return app.Delete(col)
		}
		return nil
	}, "1700000200_host_metrics.go")
}
