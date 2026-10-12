package richtext

import (
	"log/slog"

	"github.com/pocketbase/pocketbase/core"
)

// Fields maps a collection to the editor fields it holds.
//
// It is a list rather than a discovery because the hook has to be bound to a
// collection name before any record is written, and a schema query at that point
// would be a query on every boot for an answer that changes only when a migration
// does. A test enumerates the live schema and fails when a collection with an editor
// field is missing from here, so the list cannot quietly fall behind.
var Fields = map[string][]string{
	"announcements":      {"content"},
	"todos":              {"description"},
	"messages":           {"content"},
	"system_updates":     {"releaseNotes"},
	"product_categories": {"description"},
	"member_tiers":       {"description"},
	"campaigns":          {"description"},
	"products":           {"description"},
}

// Register installs the sanitiser on every collection that holds rich text.
//
// It runs as a model hook rather than in handlers because a hook is the one place
// every write passes through: a screen, a seed, an import and the public API all go
// through the same door, and a sanitiser installed in a handler protects that
// handler and nothing else. Five of the six editor fields in this platform were
// unprotected before this existed, which is the evidence for the claim.
func Register(app core.App) {
	for collection, fields := range Fields {
		fields := fields

		sanitise := func(e *core.RecordEvent) error {
			if e.Record != nil {
				SanitizeFields(e.Record, fields)
			}
			return e.Next()
		}

		app.OnRecordCreate(collection).BindFunc(sanitise)
		app.OnRecordUpdate(collection).BindFunc(sanitise)
	}
}

// SanitizeFields rewrites the named fields of a record in place.
func SanitizeFields(rec *core.Record, fields []string) {
	for _, name := range fields {
		raw := rec.GetString(name)
		if raw == "" {
			continue
		}

		clean := Sanitize(raw)
		if clean == raw {
			continue
		}

		rec.Set(name, clean)
		slog.Debug("sanitised rich text",
			"collection", rec.Collection().Name, "field", name,
			"removedBytes", len(raw)-len(clean))
	}
}
