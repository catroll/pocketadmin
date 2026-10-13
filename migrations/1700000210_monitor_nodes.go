package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
)

// monitorNodesMigration turns the host monitor into a multi-machine one.
//
// The first version recorded one machine -- the one the process runs on. That is the
// case that needs the least, and an installation with two servers needs the feature
// most: a disk filling up on the machine nobody logs into is exactly what this is for.
//
// A node is a row rather than a name in a column, because a machine has a lifecycle:
// it is registered once, it reports for months, it is retired. The samples point at it.
func init() {
	migrations.Register(func(app core.App) error {
		if err := createMonitorNodes(app); err != nil {
			return err
		}
		if err := attachMetricsToNodes(app); err != nil {
			return err
		}
		if err := seedPermissionPoints(app); err != nil {
			return err
		}
		return seedMenus(app)
	}, func(app core.App) error {
		// The metrics column first: its rows point at the nodes removed below.
		if col := findCollection(app, "host_metrics"); col != nil && hasField(col, "node") {
			col.Fields.RemoveByName("node")
			if err := app.Save(col); err != nil {
				return err
			}
		}
		if col := findCollection(app, "developer_api_keys"); col != nil && hasField(col, "monitorNode") {
			col.Fields.RemoveByName("monitorNode")
			if err := app.Save(col); err != nil {
				return err
			}
		}
		if col := findCollection(app, "monitor_nodes"); col != nil {
			return app.Delete(col)
		}
		return nil
	}, "1700000210_monitor_nodes.go")
}

// createMonitorNodes creates the machine registry.
func createMonitorNodes(app core.App) error {
	col := findCollection(app, "monitor_nodes")
	if col == nil {
		col = newBase("monitor_nodes")
	}
	ensureTimestamps(col)

	// The code is what a node reports under, so it is stable and unique. It is chosen
	// by the operator rather than derived from the host name, because a host name
	// changes when a machine is rebuilt and the history would be orphaned by it.
	addField(col, text("code", true))
	addField(col, text("name", true))
	addField(col, text("hostname", false))
	addField(col, text("os", false))
	addField(col, text("agentVersion", false))
	addField(col, &core.EditorField{Name: "note"})

	// The machine running this process. Marked rather than inferred, because "which
	// one is this" is a question the screen asks and the answer must not depend on
	// host names agreeing.
	addField(col, &core.BoolField{Name: "isSelf"})

	addField(col, selectOf("status", true, "active", "disabled"))
	addField(col, &core.DateField{Name: "lastSeenAt"})
	addField(col, &core.DateField{Name: "lastSampleAt"})
	addField(col, &core.NumberField{Name: "sampleCount"})

	col.AddIndex("idx_monitor_nodes_code", true, "code", "")
	col.AddIndex("idx_monitor_nodes_status", false, "status", "")
	col.AddIndex("idx_monitor_nodes_last_seen", false, "lastSeenAt", "")

	disableCascade(col)
	lockDown(col)

	if err := app.Save(col); err != nil {
		return err
	}

	// The machine this process runs on is registered immediately, so a fresh install
	// has one node reporting without anybody configuring anything. Every existing
	// sample belongs to it.
	existing, _ := app.FindFirstRecordByFilter(col.Id, "code = 'local'")
	if existing == nil {
		rec := core.NewRecord(col)
		rec.Set("code", "local")
		rec.Set("name", "This machine")
		rec.Set("isSelf", true)
		rec.Set("status", "active")
		rec.Set("sampleCount", 0)
		if err := app.Save(rec); err != nil {
			return err
		}
		existing = rec
	}

	// Backfill: every sample taken before this migration belongs to the local machine.
	metrics := findCollection(app, "host_metrics")
	if metrics == nil {
		return nil
	}

	samples, err := app.FindRecordsByFilter("host_metrics", "", "at", 5000, 0)
	if err != nil {
		return nil
	}
	for _, sample := range samples {
		sample.Set("node", existing.Id)
		if err := app.Save(sample); err != nil {
			return err
		}
	}

	return nil
}

// attachMetricsToNodes points the samples at a machine.
//
// The relation is optional because the sampler writes the row and links it in one save,
// and a required relation would make the intermediate state invalid. The sampler always
// sets it.
func attachMetricsToNodes(app core.App) error {
	col := findCollection(app, "host_metrics")
	if col == nil {
		return nil
	}

	if !hasField(col, "node") {
		addField(col, rel(app, "node", "monitor_nodes", false))
	}
	col.AddIndex("idx_host_metrics_node_at", false, "node, at", "")

	// A bound key is how a remote agent authenticates. It is a relation on the key
	// rather than a token of its own, so a node inherits the key's expiry, revocation,
	// rate limit and audit trail instead of duplicating all four.
	keys := findCollection(app, "developer_api_keys")
	if keys != nil {
		if !hasField(keys, "monitorNode") {
			addField(keys, rel(app, "monitorNode", "monitor_nodes", false))
		}
		disableCascade(keys, "monitorNode")
		if err := app.Save(keys); err != nil {
			return err
		}
	}

	disableCascade(col, "node")
	return app.Save(col)
}
