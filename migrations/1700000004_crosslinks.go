package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
)

// crossLinksMigration wires up the relations that could not be declared while
// the collections were being created, because they point the other way.
//
//   - departments.leader     -> employees   (organization -> identity)
//   - roles.customDepartments -> departments (rbac -> organization)
//
// Splitting these out keeps the three schema migrations free of ordering
// compromises, at the cost of one small extra migration.
func init() {
	migrations.Register(func(app core.App) error {
		departments := findCollection(app, "departments")
		if departments != nil {
			if addField(departments, rel(app, "leader", "employees", false)) {
				departments.AddIndex("idx_departments_leader", false, "leader", "")
				if err := app.Save(departments); err != nil {
					return err
				}
			}
		}

		roles := findCollection(app, "roles")
		if roles != nil {
			if addField(roles, multiRel(app, "customDepartments", "departments", 200)) {
				if err := app.Save(roles); err != nil {
					return err
				}
			}
		}

		return nil
	}, func(app core.App) error {
		if departments := findCollection(app, "departments"); departments != nil {
			departments.Fields.RemoveByName("leader")
			if err := app.Save(departments); err != nil {
				return err
			}
		}
		if roles := findCollection(app, "roles"); roles != nil {
			roles.Fields.RemoveByName("customDepartments")
			if err := app.Save(roles); err != nil {
				return err
			}
		}
		return nil
	}, "1700000004_crosslinks.go")
}

// seedPermissionPoints inserts one row per declared permission point.
//
// The permission package is the source of truth in code, so the seed walks it
// rather than duplicating the list. A drift test asserts the database and the
// package agree in both directions.
func seedPermissionPoints(app core.App) error {
	col := findCollection(app, "permissions")
	if col == nil {
		return nil
	}

	// The action column is a closed select, so a permission point whose action is
	// not in it cannot be written -- the row is rejected and the point silently
	// cannot be granted. Widening it here rather than in each module's migration
	// keeps the seeder independent of migration order: a module adds a constant
	// and an action, and this makes room for it on whatever database runs next.
	if err := ensurePermissionActions(app, col); err != nil {
		return err
	}

	for _, p := range permissionPoints() {
		rec, err := app.FindFirstRecordByFilter("permissions", "code = {:code}", map[string]any{"code": p.Code})
		if err != nil || rec == nil {
			rec = core.NewRecord(col)
			rec.Set("code", p.Code)
			rec.Set("isSystem", true)
		}

		rec.Set("name", p.Name)
		rec.Set("module", p.Module)
		rec.Set("resource", p.Resource)
		rec.Set("action", p.Action)
		rec.Set("type", p.Type)
		rec.Set("status", "active")

		if err := app.Save(rec); err != nil {
			return err
		}
	}

	return nil
}

// seedDictionary inserts a dictionary type and its items when absent.
func seedDictionary(app core.App, code, name string, items []struct{ Value, Label string }) error {
	col := findCollection(app, "dictionaries")
	itemsCol := findCollection(app, "dictionary_items")
	if col == nil || itemsCol == nil {
		return nil
	}

	dict, err := app.FindFirstRecordByFilter("dictionaries", "code = {:code}", map[string]any{"code": code})
	if err != nil || dict == nil {
		dict = core.NewRecord(col)
		dict.Set("code", code)
		dict.Set("isSystem", true)
	}
	dict.Set("name", name)
	dict.Set("status", "active")
	if err := app.Save(dict); err != nil {
		return err
	}

	for i, item := range items {
		rec, err := app.FindFirstRecordByFilter("dictionary_items",
			"dictionary = {:d} && value = {:v}",
			map[string]any{"d": dict.Id, "v": item.Value})
		if err != nil || rec == nil {
			rec = core.NewRecord(itemsCol)
			rec.Set("dictionary", dict.Id)
			rec.Set("value", item.Value)
		}
		rec.Set("label", item.Label)
		rec.Set("sortOrder", i*10)
		rec.Set("status", "active")
		if err := app.Save(rec); err != nil {
			return err
		}
	}

	return nil
}
