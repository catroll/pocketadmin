package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"

	"pocketadmin/internal/identity"
)

// phoneUniquenessMigration makes an account's phone number unique.
//
// Email uniqueness is already enforced by PocketBase's auth collection. Phone
// numbers are a second identity used for sign-in, notification and support, so a
// duplicate is a real problem: two accounts cannot both be reached by, or
// recovered through, the same number.
//
// The index is partial. A blank phone is not a value and must not collide, and
// most accounts start without one, so the predicate restricts the constraint to
// rows that actually carry a number.
func init() {
	migrations.Register(func(app core.App) error {
		col := findCollection(app, "users")
		if col == nil {
			return nil
		}

		// Normalise before constraining, or an existing pair that differs only by
		// surrounding whitespace would collide on the index rather than being
		// reported as what it is.
		records, err := app.FindAllRecords("users")
		if err != nil {
			return err
		}
		for _, rec := range records {
			phone := identity.NormalisePhone(rec.GetString("phone"))
			if phone == rec.GetString("phone") {
				continue
			}
			rec.Set("phone", phone)
			if err := app.Save(rec); err != nil {
				return err
			}
		}

		col.RemoveIndex("idx_users_phone")
		col.AddIndex("idx_users_phone", true, "phone", "phone != ''")
		return app.Save(col)
	}, func(app core.App) error {
		if col := findCollection(app, "users"); col != nil {
			col.RemoveIndex("idx_users_phone")
			return app.Save(col)
		}
		return nil
	}, "1700000007_phone_unique.go")
}
