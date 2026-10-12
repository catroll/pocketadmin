package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
)

// menuSuperuserMigration adds the superuserOnly flag to menu nodes.
//
// A permission code cannot express this: the raw data browser is deliberately
// not delegated to roles, because it writes directly to arbitrary collections
// and bypasses every module's business rules. Without a flag the node either
// carries no permission code, in which case every signed-in account sees a link
// that fails with 403, or it carries one, in which case granting that permission
// also hands over the data browser.
//
// This is a separate migration rather than an edit to 1700000001 because a
// migration that has already been applied is never re-run.
func init() {
	migrations.Register(func(app core.App) error {
		col := findCollection(app, "menus")
		if col == nil {
			return nil
		}

		if addField(col, &core.BoolField{Name: "superuserOnly"}) {
			return app.Save(col)
		}
		return nil
	}, func(app core.App) error {
		if col := findCollection(app, "menus"); col != nil {
			col.Fields.RemoveByName("superuserOnly")
			return app.Save(col)
		}
		return nil
	}, "1700000005_menu_superuser.go")
}
