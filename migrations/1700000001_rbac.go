package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
)

// rbacMigration creates the role, permission, menu and dictionary collections.
//
// Order matters here: permissions and menus are created before roles, because
// roles reference both. departments does not exist yet, so roles.customDepartments
// is wired up later in the cross-links migration.
func init() {
	migrations.Register(func(app core.App) error {
		if err := createPermissions(app); err != nil {
			return err
		}
		if err := createMenus(app); err != nil {
			return err
		}
		if err := createDictionaries(app); err != nil {
			return err
		}
		return createRoles(app)
	}, func(app core.App) error {
		for _, name := range []string{"roles", "dictionary_items", "dictionaries", "menus", "permissions"} {
			if col := findCollection(app, name); col != nil {
				if err := app.Delete(col); err != nil {
					return err
				}
			}
		}
		return nil
	}, "1700000001_rbac.go")
}

// createPermissions creates the catalogue of permission points.
func createPermissions(app core.App) error {
	col := findCollection(app, "permissions")
	if col == nil {
		col = newBase("permissions")
	}

	ensureTimestamps(col)
	addField(col, text("name", true))
	addField(col, &core.TextField{Name: "code", Required: true})
	addField(col, text("module", true))
	addField(col, text("resource", false))
	// The action list is the platform's operation vocabulary. "revoke" is
	// included because session revocation is a distinct capability that must be
	// grantable on its own.
	addField(col, selectOf("action", false,
		"list", "view", "create", "update", "delete", "export", "import",
		"execute", "approve", "manage", "revoke"))
	addField(col, selectOf("type", false, "menu", "button", "api", "data"))
	addField(col, &core.TextField{Name: "description"})
	addField(col, &core.BoolField{Name: "isSystem"})
	addField(col, selectOf("status", false, "active", "disabled"))

	col.AddIndex("idx_permissions_code", true, "code", "")
	col.AddIndex("idx_permissions_module", false, "module, resource", "")
	col.AddIndex("idx_permissions_type", false, "type", "")

	return app.Save(col)
}

// createMenus creates the navigation tree.
//
// The parent relation is self-referential, so the collection is saved once to
// obtain an id and then saved again with the relation in place.
func createMenus(app core.App) error {
	col := findCollection(app, "menus")
	if col == nil {
		col = newBase("menus")
	}

	ensureTimestamps(col)
	addField(col, text("title", true))
	addField(col, &core.TextField{Name: "name", Required: true})
	addField(col, selectOf("type", true, "directory", "menu", "button", "link"))
	addField(col, selectOf("platform", false, "admin", "web"))
	addField(col, &core.TextField{Name: "path"})
	addField(col, &core.TextField{Name: "component"})
	addField(col, &core.TextField{Name: "icon"})
	addField(col, &core.TextField{Name: "permissionCode"})
	addField(col, &core.NumberField{Name: "sortOrder"})
	addField(col, &core.BoolField{Name: "visible"})
	addField(col, &core.BoolField{Name: "keepAlive"})
	addField(col, &core.BoolField{Name: "external"})
	addField(col, &core.URLField{Name: "link"})
	addField(col, selectOf("status", false, "active", "disabled"))
	addField(col, &core.BoolField{Name: "isSystem"})

	col.AddIndex("idx_menus_name", true, "name", "")
	col.AddIndex("idx_menus_platform", false, "platform, status", "")
	col.AddIndex("idx_menus_permission", false, "permissionCode", "")

	if err := app.Save(col); err != nil {
		return err
	}

	// Second pass: the collection now has an id it can point at itself.
	if addField(col, &core.RelationField{
		Name:         "parent",
		CollectionId: col.Id,
		MaxSelect:    1,
	}) {
		col.AddIndex("idx_menus_parent", false, "parent", "")
		return app.Save(col)
	}

	return nil
}

// createDictionaries creates the dictionary type and item collections.
func createDictionaries(app core.App) error {
	types := findCollection(app, "dictionaries")
	if types == nil {
		types = newBase("dictionaries")
	}
	ensureTimestamps(types)
	addField(types, text("name", true))
	addField(types, &core.TextField{Name: "code", Required: true})
	addField(types, &core.TextField{Name: "description"})
	addField(types, selectOf("status", false, "active", "disabled"))
	addField(types, &core.BoolField{Name: "isSystem"})
	types.AddIndex("idx_dictionaries_code", true, "code", "")
	if err := app.Save(types); err != nil {
		return err
	}

	items := findCollection(app, "dictionary_items")
	if items == nil {
		items = newBase("dictionary_items")
	}
	ensureTimestamps(items)
	addField(items, rel(app, "dictionary", "dictionaries", true))
	addField(items, text("label", true))
	addField(items, text("value", true))
	addField(items, &core.NumberField{Name: "sortOrder"})
	addField(items, selectOf("status", false, "active", "disabled"))
	addField(items, &core.TextField{Name: "color"})
	addField(items, &core.TextField{Name: "cssClass"})
	addField(items, &core.BoolField{Name: "isDefault"})
	addField(items, &core.JSONField{Name: "extra", MaxSize: 20000})
	items.AddIndex("idx_dict_items_unique", true, "dictionary, value", "")
	items.AddIndex("idx_dict_items_order", false, "dictionary, sortOrder", "")

	if err := app.Save(items); err != nil {
		return err
	}

	if addField(items, &core.RelationField{
		Name:         "parent",
		CollectionId: items.Id,
		MaxSelect:    1,
	}) {
		return app.Save(items)
	}

	return nil
}

// createRoles creates the role collection with its permission and menu grants.
func createRoles(app core.App) error {
	col := findCollection(app, "roles")
	if col == nil {
		col = newBase("roles")
	}

	ensureTimestamps(col)
	addField(col, text("name", true))
	addField(col, &core.TextField{Name: "code", Required: true})
	addField(col, &core.TextField{Name: "description"})
	addField(col, selectOf("dataScope", false,
		"all", "department", "department_and_below", "self", "custom"))
	addField(col, multiRel(app, "permissions", "permissions", 500))
	addField(col, multiRel(app, "menus", "menus", 500))
	addField(col, selectOf("status", false, "active", "disabled"))
	addField(col, &core.NumberField{Name: "sortOrder"})
	addField(col, &core.BoolField{Name: "isSystem"})
	addField(col, &core.DateField{Name: "deletedAt"})

	col.AddIndex("idx_roles_code", true, "code", "")
	col.AddIndex("idx_roles_status", false, "status", "")

	// Deleting a role must not delete the permission points it referenced.
	disableCascade(col, "permissions", "menus")

	return app.Save(col)
}

// disableCascade clears cascadeDelete on the named relation fields.
//
// PocketBase defaults new relation fields to cascade, which would make deleting
// a role delete the shared permission and menu vocabularies. Business deletion
// rules belong in service code anyway, so cascades are turned off everywhere.
func disableCascade(col *core.Collection, names ...string) {
	for _, name := range names {
		f, ok := col.Fields.GetByName(name).(*core.RelationField)
		if !ok {
			continue
		}
		f.CascadeDelete = false
		f.MaxSelect = max(f.MaxSelect, 1)
	}
}

// jsonField is a small helper for optional JSON bags.
func jsonField(name string, maxSize int64) *core.JSONField {
	return &core.JSONField{Name: name, MaxSize: maxSize}
}
