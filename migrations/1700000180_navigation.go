package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
)

// navigationMigration restructures the sidebar into grouped surfaces.
//
// It is a migration rather than a change to the original seed because the original
// has already run on every existing installation, and a seed that is only read once
// cannot reorganise anything. The work is a single call, because seedMenus is
// authoritative and idempotent: it upserts every entry by name, resolves the parents
// and prunes the system menus that are no longer listed.
func init() {
	migrations.Register(func(app core.App) error {
		return seedMenus(app)
	}, func(app core.App) error {
		// The previous flat structure is restored by re-running the earlier seeds,
		// which every module migration does on boot. Nothing to undo here: deleting
		// the menu rows would take an operator's own menus with them.
		return nil
	}, "1700000180_navigation.go")
}
