package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"

	"pocketadmin/internal/settings"
)

// settingsMigration creates the shared settings table and seeds both surfaces.
//
// One table and one validation engine, discriminated by `surface`, because the
// back office and the storefront are separate concerns that happen to store the
// same shape of thing -- exactly as orders and refunds are separate concerns that
// share a database. Separate tables would mean two engines, two migrations and two
// places for the type rules to drift.
//
// The key registry lives in code, so a row is only meaningful if this binary
// declares it. Seeding writes a row per declared key so the description and the
// type are visible in the database, but the defaults are compiled in: deleting
// every row would leave the platform working with its defaults rather than
// unconfigured.
func init() {
	migrations.Register(func(app core.App) error {
		col := findCollection(app, "settings")
		if col == nil {
			col = newBase("settings")
		}
		ensureTimestamps(col)

		addField(col, selectOf("surface", true,
			string(settings.SurfaceAdmin), string(settings.SurfaceProduct)))
		addField(col, selectOf("category", true, settingsCategories()...))
		addField(col, text("key", true))
		addField(col, jsonField("value", 20000))
		addField(col, selectOf("valueType", false,
			string(settings.TypeString), string(settings.TypeNumber), string(settings.TypeBool),
			string(settings.TypeJSON), string(settings.TypeFile), string(settings.TypeSelect)))
		addField(col, text("description", false))
		addField(col, &core.BoolField{Name: "isPublic"})
		addField(col, &core.BoolField{Name: "isSystem"})
		addField(col, rel(app, "updatedBy", "users", false))

		col.AddIndex("idx_settings_unique", true, "surface, category, key", "")
		col.AddIndex("idx_settings_public", false, "surface, isPublic", "")

		disableCascade(col, "updatedBy")
		lockDown(col)

		if err := app.Save(col); err != nil {
			return err
		}

		if err := seedSettings(app); err != nil {
			return err
		}
		if err := seedPermissionPoints(app); err != nil {
			return err
		}
		return seedMenus(app)
	}, func(app core.App) error {
		if col := findCollection(app, "settings"); col != nil {
			return app.Delete(col)
		}
		return nil
	}, "1700000040_settings.go")
}

// settingsCategories lists every category either surface declares.
//
// The column is a closed select and the two surfaces do not share category names,
// so the list is the union rather than something per surface. A category that is
// not here cannot be stored at all, which is why it is built from the registries
// rather than written out by hand.
func settingsCategories() []string {
	seen := map[string]bool{}
	out := []string{}

	for _, surface := range settings.Surfaces() {
		reg, ok := settings.For(surface)
		if !ok {
			continue
		}
		for _, category := range reg.Categories() {
			if seen[category] {
				continue
			}
			seen[category] = true
			out = append(out, category)
		}
	}
	return out
}

// seedSettings writes one row per declared key, carrying its default.
//
// It is an upsert keyed on surface, category and key, so re-running it after a
// release that adds a key fills in the new one and leaves existing values alone.
func seedSettings(app core.App) error {
	col := findCollection(app, "settings")
	if col == nil {
		return nil
	}

	for _, surface := range settings.Surfaces() {
		reg, ok := settings.For(surface)
		if !ok {
			continue
		}

		for _, key := range reg.Keys() {
			rec, err := app.FindFirstRecordByFilter("settings",
				"surface = {:s} && category = {:c} && key = {:k}",
				map[string]any{"s": string(surface), "c": key.Category, "k": key.Key})
			if err != nil || rec == nil {
				rec = core.NewRecord(col)
				rec.Set("surface", string(surface))
				rec.Set("category", key.Category)
				rec.Set("key", key.Key)
				// Only a new row gets the default. Overwriting an existing value
				// on every boot would make the settings screen pointless.
				rec.Set("value", key.Default)
			}

			rec.Set("valueType", string(key.Type))
			rec.Set("description", key.Description)
			rec.Set("isPublic", key.Public)
			rec.Set("isSystem", true)

			if err := app.Save(rec); err != nil {
				return err
			}
		}
	}
	return nil
}
