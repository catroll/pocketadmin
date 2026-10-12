package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
)

// developerMigration creates the API keys.
//
// The key itself is never stored. Only a SHA-256 hash and a short clear prefix
// are kept, which is what makes a database dump not a set of working credentials.
func init() {
	migrations.Register(func(app core.App) error {
		col := findCollection(app, "api_keys")
		if col == nil {
			col = newBase("api_keys")
		}
		ensureTimestamps(col)

		addField(col, text("name", true))
		// The clear prefix is for display and lookup. It is not a secret and is
		// not treated as one.
		addField(col, text("keyPrefix", true))
		addField(col, text("keyHash", true))
		addField(col, rel(app, "owner", "users", true))
		// The scopes are permission points, so the vocabulary is the same one the
		// panel uses. A key cannot be given an authority that does not exist.
		addField(col, multiRel(app, "scopes", "permissions", 200))
		addField(col, &core.NumberField{Name: "rateLimitPerMinute"})
		addField(col, text("ipAllowList", false))
		addField(col, selectOf("status", true, "active", "revoked", "expired"))
		addField(col, &core.DateField{Name: "expiresAt"})
		addField(col, &core.DateField{Name: "lastUsedAt"})
		addField(col, &core.DateField{Name: "revokedAt"})
		addField(col, text("lastUsedIp", false))
		addField(col, &core.NumberField{Name: "usageCount"})
		addField(col, rel(app, "revokedBy", "users", false))

		col.AddIndex("idx_apikeys_prefix", true, "keyPrefix", "")
		// The hash is indexed because verification looks a row up by prefix and
		// then compares; the index also makes a duplicate-key mistake impossible.
		col.AddIndex("idx_apikeys_hash", false, "keyHash", "")
		col.AddIndex("idx_apikeys_owner", false, "owner", "")
		col.AddIndex("idx_apikeys_status", false, "status", "")

		disableCascade(col, "owner", "scopes", "revokedBy")
		lockDown(col)

		if err := app.Save(col); err != nil {
			return err
		}

		if err := seedPermissionPoints(app); err != nil {
			return err
		}
		return seedMenus(app)
	}, func(app core.App) error {
		if col := findCollection(app, "api_keys"); col != nil {
			return app.Delete(col)
		}
		return nil
	}, "1700000080_developer.go")
}
