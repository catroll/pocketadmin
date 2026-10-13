package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
)

// preferencesSuperuserMigration lets a break-glass superuser keep preferences.
//
// The `user` relation points at `users`, and a superuser signing in through the
// break-glass path has no `users` record -- the identity model gives every *admin* a
// profile, and a superuser is deliberately not one. The relation was required, so
// saving preferences for a superuser tried to link a `_superusers` id to a `users`
// relation and failed with "Failed to find all relation records".
//
// The two principals are kept in separate columns rather than sharing one. The audit
// trail already draws that distinction -- a superuser is recorded as a system actor
// with an empty id and the email preserved -- and a single column holding ids from two
// tables would be a column nothing could join on or validate.
func init() {
	migrations.Register(func(app core.App) error {
		col := findCollection(app, "user_preferences")
		if col == nil {
			return nil
		}

		// The relation becomes optional: a row belongs to either a user or a
		// superuser, never both.
		if field, ok := col.Fields.GetByName("user").(*core.RelationField); ok {
			field.Required = false
		}

		if !hasField(col, "superuserId") {
			// A plain text field rather than a relation: PocketBase does not expose
			// `_superusers` as a relation target, and it participates in no cascade.
			addField(col, text("superuserId", false))
		}

		col.AddIndex("idx_prefs_superuser", false, "superuserId, context", "")

		return app.Save(col)
	}, func(app core.App) error {
		col := findCollection(app, "user_preferences")
		if col == nil {
			return nil
		}

		if field, ok := col.Fields.GetByName("user").(*core.RelationField); ok {
			field.Required = true
		}
		if hasField(col, "superuserId") {
			col.Fields.RemoveByName("superuserId")
		}
		return app.Save(col)
	}, "1700000190_preferences_superuser.go")
}
