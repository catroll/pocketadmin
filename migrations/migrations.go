// Package migrations holds the platform's schema as Go migrations.
//
// Every migration registered here is idempotent: it checks for the collection or
// field before creating it, so a partially applied deployment can be re-run
// safely. PocketBase records applied migrations, but an interrupted run or a
// hand-edited database should not leave the schema unrecoverable.
//
// The package must be imported for its side effects; main.go does that with a
// blank import.
package migrations

import (
	"github.com/pocketbase/pocketbase/core"
)

// findCollection returns a collection by name, or nil when it does not exist.
func findCollection(app core.App, name string) *core.Collection {
	col, err := app.FindCollectionByNameOrId(name)
	if err != nil || col == nil {
		return nil
	}
	return col
}

// collectionID returns the id of a collection, or "" when it does not exist.
//
// Relation fields store the target collection's id, so this is how a migration
// wires two collections together without hard-coding generated ids.
func collectionID(app core.App, name string) string {
	if col := findCollection(app, name); col != nil {
		return col.Id
	}
	return ""
}

// newBase creates a base collection with its API rules locked to superusers.
//
// The panel reaches data through core.App, which bypasses collection rules
// entirely, so these rules exist only to keep the public /api surface from
// exposing anything by accident.
func newBase(name string) *core.Collection {
	col := core.NewBaseCollection(name)
	lockDown(col)
	return col
}

// newAuth creates an auth collection with its API rules locked to superusers.
func newAuth(name string) *core.Collection {
	col := core.NewAuthCollection(name)
	lockDown(col)
	return col
}

// lockDown clears every API rule so only superusers can reach the collection
// through PocketBase's REST API.
func lockDown(col *core.Collection) {
	col.ListRule = nil
	col.ViewRule = nil
	col.CreateRule = nil
	col.UpdateRule = nil
	col.DeleteRule = nil
}

// hasField reports whether a collection already declares a field.
func hasField(col *core.Collection, name string) bool {
	return col.Fields.GetByName(name) != nil
}

// addField appends a field when it is not already present and reports whether
// the collection was modified.
//
// This is what makes the migrations re-runnable: adding a field that exists is a
// no-op rather than an error.
func addField(col *core.Collection, field core.Field) bool {
	if hasField(col, field.GetName()) {
		return false
	}
	col.Fields.Add(field)
	return true
}

// ensureTimestamps adds the created/updated autodate pair that every base
// collection in this platform carries.
func ensureTimestamps(col *core.Collection) {
	addField(col, &core.AutodateField{Name: "created", OnCreate: true})
	addField(col, &core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true})
}

// text is a shorthand for a plain text field.
func text(name string, required bool) *core.TextField {
	return &core.TextField{Name: name, Required: required}
}

// selectOf is a shorthand for a single-value select field.
func selectOf(name string, required bool, values ...string) *core.SelectField {
	return &core.SelectField{Name: name, Required: required, Values: values}
}

// multiSelectOf is a shorthand for a multi-value select field.
func multiSelectOf(name string, maxSelect int, values ...string) *core.SelectField {
	return &core.SelectField{Name: name, Values: values, MaxSelect: maxSelect}
}

// rel is a shorthand for a single-record relation field.
func rel(app core.App, name, target string, required bool) *core.RelationField {
	return &core.RelationField{
		Name:         name,
		Required:     required,
		CollectionId: collectionID(app, target),
		MaxSelect:    1,
	}
}

// multiRel is a shorthand for a multi-record relation field.
func multiRel(app core.App, name, target string, maxSelect int) *core.RelationField {
	return &core.RelationField{
		Name:         name,
		CollectionId: collectionID(app, target),
		MaxSelect:    maxSelect,
	}
}
