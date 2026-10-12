package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
)

// organizationMigration creates the department tree and the position catalogue.
//
// departments.leader points at employees, which is created by the identity
// migration. Adding it here would deadlock the two, so it is wired up in the
// cross-links migration instead.
func init() {
	migrations.Register(func(app core.App) error {
		if err := createDepartments(app); err != nil {
			return err
		}
		return createPositions(app)
	}, func(app core.App) error {
		for _, name := range []string{"positions", "departments"} {
			if col := findCollection(app, name); col != nil {
				if err := app.Delete(col); err != nil {
					return err
				}
			}
		}
		return nil
	}, "1700000002_organization.go")
}

// createDepartments creates the department tree.
//
// `path` is a materialised path such as "/dep1/dep3/dep7/" maintained by the
// service layer. It turns "this department and all of its descendants" into one
// indexed prefix match instead of a recursive walk.
func createDepartments(app core.App) error {
	col := findCollection(app, "departments")
	if col == nil {
		col = newBase("departments")
	}

	ensureTimestamps(col)
	addField(col, text("name", true))
	addField(col, &core.TextField{Name: "code"})
	addField(col, &core.TextField{Name: "path"})
	addField(col, &core.NumberField{Name: "level"})
	addField(col, &core.TextField{Name: "phone"})
	addField(col, &core.EmailField{Name: "email"})
	addField(col, &core.NumberField{Name: "sortOrder"})
	addField(col, selectOf("status", false, "active", "disabled"))
	addField(col, &core.TextField{Name: "description"})
	addField(col, &core.DateField{Name: "deletedAt"})

	col.AddIndex("idx_departments_code", true, "code", "code != ''")
	col.AddIndex("idx_departments_path", false, "path", "")
	col.AddIndex("idx_departments_status", false, "status", "")

	if err := app.Save(col); err != nil {
		return err
	}

	if addField(col, &core.RelationField{
		Name:         "parent",
		CollectionId: col.Id,
		MaxSelect:    1,
	}) {
		col.AddIndex("idx_departments_parent", false, "parent", "")
		return app.Save(col)
	}

	return nil
}

// createPositions creates the position catalogue.
func createPositions(app core.App) error {
	col := findCollection(app, "positions")
	if col == nil {
		col = newBase("positions")
	}

	ensureTimestamps(col)
	addField(col, text("name", true))
	addField(col, &core.TextField{Name: "code"})
	addField(col, rel(app, "department", "departments", false))
	addField(col, &core.NumberField{Name: "level"})
	addField(col, &core.TextField{Name: "category"})
	addField(col, &core.NumberField{Name: "headcount"})
	addField(col, &core.NumberField{Name: "sortOrder"})
	addField(col, selectOf("status", false, "active", "disabled"))
	addField(col, &core.TextField{Name: "description"})
	addField(col, &core.DateField{Name: "deletedAt"})

	col.AddIndex("idx_positions_code", true, "code", "code != ''")
	col.AddIndex("idx_positions_department", false, "department", "")
	col.AddIndex("idx_positions_status", false, "status", "")
	disableCascade(col, "department")

	return app.Save(col)
}
