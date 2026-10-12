package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"

	"pocketadmin/internal/integration"
)

// integrationsMigration creates the provider configurations and their log.
//
// This is also where the forward dependency reserved in the storage module is
// settled: `files.provider` has been a text column since wave 2 with nothing to
// point at, and `integration_providers.code` is what it names.
func init() {
	migrations.Register(func(app core.App) error {
		if err := createIntegrationProviders(app); err != nil {
			return err
		}
		if err := createIntegrationLogs(app); err != nil {
			return err
		}
		if err := seedLocalStorageProvider(app); err != nil {
			return err
		}
		if err := addFileProviderField(app); err != nil {
			return err
		}
		if err := seedPermissionPoints(app); err != nil {
			return err
		}
		return seedMenus(app)
	}, func(app core.App) error {
		for _, name := range []string{"integration_logs", "integration_providers"} {
			if col := findCollection(app, name); col != nil {
				if err := app.Delete(col); err != nil {
					return err
				}
			}
		}
		return nil
	}, "1700000090_integrations.go")
}

// createIntegrationProviders creates the provider configurations.
func createIntegrationProviders(app core.App) error {
	col := findCollection(app, "integration_providers")
	if col == nil {
		col = newBase("integration_providers")
	}
	ensureTimestamps(col)

	addField(col, text("name", true))
	// The code is what settings, files.provider and payment_transactions name, so
	// it is unique and is meant to be stable once anything refers to it.
	addField(col, text("code", true))
	addField(col, selectOf("type", true, integration.Types()...))
	// The driver list is built from the catalogue, so a driver that is not
	// registered in this build cannot be stored.
	addField(col, selectOf("driver", true, integration.DriverCodes()...))
	addField(col, jsonField("config", 20000))
	// Encrypted at rest with the application's encryption key. The column is text
	// rather than json on purpose: it holds ciphertext, and typing it as json
	// would invite something to read it as a value.
	addField(col, &core.TextField{Name: "secrets", Max: 20000})
	addField(col, &core.BoolField{Name: "sandbox"})
	addField(col, selectOf("status", true, "enabled", "disabled", "testing", "error"))
	addField(col, &core.BoolField{Name: "isDefault"})
	addField(col, &core.NumberField{Name: "priority"})
	addField(col, &core.DateField{Name: "lastTestAt"})
	addField(col, selectOf("lastTestResult", false, "success", "failure", "untested"))
	addField(col, text("lastTestMessage", false))
	addField(col, text("description", false))
	addField(col, rel(app, "createdBy", "users", false))
	addField(col, &core.DateField{Name: "deletedAt"})

	col.AddIndex("idx_intprov_code", true, "code", "")
	col.AddIndex("idx_intprov_type_default", false, "type, isDefault", "")
	col.AddIndex("idx_intprov_status", false, "status", "")
	col.AddIndex("idx_intprov_deleted", false, "deletedAt", "")

	disableCascade(col, "createdBy")
	lockDown(col)

	return app.Save(col)
}

// createIntegrationLogs creates the integration log.
func createIntegrationLogs(app core.App) error {
	col := findCollection(app, "integration_logs")
	if col == nil {
		col = newBase("integration_logs")
	}
	ensureTimestamps(col)

	addField(col, rel(app, "provider", "integration_providers", false))
	addField(col, selectOf("channel", true, "email", "sms", "storage", "auth", "payment", "webhook"))
	addField(col, text("action", false))
	addField(col, text("target", false))
	// Redacted before storage with the same redactor the audit log uses: an
	// integration payload is where credentials and signatures travel.
	addField(col, jsonField("request", 20000))
	addField(col, jsonField("response", 20000))
	addField(col, selectOf("status", true, "success", "failure"))
	addField(col, &core.TextField{Name: "error", Max: 4000})
	addField(col, &core.NumberField{Name: "durationMs"})

	col.AddIndex("idx_intlog_provider", false, "provider, created", "")
	col.AddIndex("idx_intlog_channel_status", false, "channel, status", "")
	col.AddIndex("idx_intlog_created", false, "created", "")

	disableCascade(col, "provider")
	lockDown(col)

	return app.Save(col)
}

// addFileProviderField settles the forward dependency reserved in the storage
// module.
//
// `files.provider` was deliberately left off the collection in wave 2 because it
// names a row in a collection that did not exist yet, and a relation to nothing is
// worse than no relation: it reads as complete and enforces nothing. The target
// exists now, so the field is added here rather than left as a permanent hole.
//
// It is a text column rather than a relation, because the documented shape names
// the provider by its code and because a file's provider must survive the provider
// row being soft-deleted -- a file that says where its bytes live is the one record
// that has to keep saying it.
func addFileProviderField(app core.App) error {
	files := findCollection(app, "files")
	if files == nil {
		return nil
	}

	if !hasField(files, "provider") {
		addField(files, text("provider", false))
		files.AddIndex("idx_files_provider", false, "provider", "")
	}

	return app.Save(files)
}

// seedLocalStorageProvider makes the local disk driver available out of the box.
//
// It is the only driver that needs no credentials, and it is what the platform
// already does: without a row, `files.provider` would name nothing and the file
// library would have no provider to resolve. Existing installations get it on
// upgrade, and an operator who moves to object storage adds a provider and points
// the setting at it.
func seedLocalStorageProvider(app core.App) error {
	col := findCollection(app, "integration_providers")
	if col == nil {
		return nil
	}

	existing, err := app.FindFirstRecordByFilter("integration_providers",
		"code = {:c}", map[string]any{"c": "local"})
	if err == nil && existing != nil {
		return nil
	}

	rec := core.NewRecord(col)
	rec.Set("name", "Local disk")
	rec.Set("code", "local")
	rec.Set("type", string(integration.TypeStorage))
	rec.Set("driver", "local")
	rec.Set("description", "Uploaded files are kept on this server. The default, and the only driver that needs no credentials.")
	rec.Set("status", string(integration.StatusEnabled))
	rec.Set("isDefault", true)
	rec.Set("priority", 100)
	rec.Set("lastTestResult", integration.TestUntested)
	rec.Set("config", map[string]any{})

	return app.Save(rec)
}
