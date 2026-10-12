package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
)

// promotionsMigration creates campaigns, coupon templates and the grants issued
// from them.
//
// A coupon is two things and they are separate collections. The template describes
// what the discount is; a grant is one instance of it, held by one account, with its
// own expiry and its own used-or-not. Folding them together would make a shared
// public code impossible and would mean editing a template rewrote the terms of
// every coupon already handed out.
func init() {
	migrations.Register(func(app core.App) error {
		if err := createCampaigns(app); err != nil {
			return err
		}
		if err := createCoupons(app); err != nil {
			return err
		}
		if err := createCouponGrants(app); err != nil {
			return err
		}
		if err := seedPermissionPoints(app); err != nil {
			return err
		}
		return seedMenus(app)
	}, func(app core.App) error {
		for _, name := range []string{"coupon_grants", "coupons", "campaigns"} {
			if col := findCollection(app, name); col != nil {
				if err := app.Delete(col); err != nil {
					return err
				}
			}
		}
		return nil
	}, "1700000130_promotions.go")
}

// createCampaigns creates the campaigns a coupon may belong to.
func createCampaigns(app core.App) error {
	col := findCollection(app, "campaigns")
	if col == nil {
		col = newBase("campaigns")
	}
	ensureTimestamps(col)

	addField(col, text("name", true))
	addField(col, text("code", true))
	addField(col, &core.EditorField{Name: "description"})
	addField(col, &core.DateField{Name: "startAt"})
	addField(col, &core.DateField{Name: "endAt"})
	// Minor units, like every other money column. Zero means uncapped.
	addField(col, &core.NumberField{Name: "budgetCap"})
	// Denormalised, and written only by the code that applies a coupon, so the
	// figure can always be recomputed from the grants that consumed it.
	addField(col, &core.NumberField{Name: "spentAmount"})
	addField(col, selectOf("status", true, "draft", "scheduled", "active", "ended", "paused"))
	addField(col, rel(app, "createdBy", "users", false))

	col.AddIndex("idx_campaigns_code", true, "code", "")
	col.AddIndex("idx_campaigns_status_start", false, "status, startAt", "")
	col.AddIndex("idx_campaigns_end", false, "endAt", "")

	disableCascade(col, "createdBy")
	lockDown(col)

	return app.Save(col)
}

// createCoupons creates the coupon templates.
func createCoupons(app core.App) error {
	col := findCollection(app, "coupons")
	if col == nil {
		col = newBase("coupons")
	}
	ensureTimestamps(col)

	addField(col, text("name", true))
	// An empty code means the coupon is only ever issued as a generated one, which
	// is how a private discount works.
	addField(col, text("code", false))
	addField(col, selectOf("type", true, "fixed_amount", "percentage", "free_shipping"))
	// Minor units for a fixed amount, **basis points** for a percentage. Basis
	// points because a whole-number column cannot hold 12.5%, and a floating-point
	// one is the mistake internal/money exists to avoid: 12.5% of 1999 is not a
	// number a float gets right.
	addField(col, &core.NumberField{Name: "value"})
	addField(col, &core.NumberField{Name: "maxDiscount"})
	addField(col, &core.NumberField{Name: "minSpend"})
	addField(col, text("currency", false))

	// Two ways to be valid, and they are not interchangeable. An absolute window is
	// a campaign; a relative period counts from the moment of the grant, which is
	// what a "30 days from now" welcome offer means.
	addField(col, selectOf("validityMode", true, "absolute", "relative"))
	addField(col, &core.DateField{Name: "validFrom"})
	addField(col, &core.DateField{Name: "validTo"})
	addField(col, &core.NumberField{Name: "validDays"})

	addField(col, &core.NumberField{Name: "totalQuantity"})
	addField(col, &core.NumberField{Name: "perUserLimit"})
	addField(col, &core.NumberField{Name: "claimedCount"})
	addField(col, &core.NumberField{Name: "usedCount"})

	addField(col, selectOf("applicability", true, "all", "categories", "products", "tiers"))
	addField(col, multiRel(app, "categories", "product_categories", 100))
	addField(col, multiRel(app, "products", "products", 200))
	addField(col, multiRel(app, "tiers", "member_tiers", 50))
	addField(col, multiRel(app, "excludeProducts", "products", 200))

	// Stacking. The defaults are the cautious ones: a coupon that stacks with
	// nothing and with no membership discount cannot be combined by accident into a
	// larger discount than anybody intended.
	addField(col, &core.BoolField{Name: "stackable"})
	addField(col, &core.BoolField{Name: "stacksWithMemberDiscount"})
	addField(col, &core.NumberField{Name: "maxStacked"})

	addField(col, rel(app, "campaign", "campaigns", false))
	addField(col, selectOf("status", true, "draft", "active", "paused", "expired"))
	addField(col, rel(app, "createdBy", "users", false))
	addField(col, &core.DateField{Name: "deletedAt"})

	// The unique index is partial. A coupon with no public code is auto-issued per
	// grant, which the specification calls for, and a plain unique index would allow
	// exactly one such coupon in the whole table -- so the second one an operator
	// created would be refused with a message about a field they left empty on
	// purpose.
	col.AddIndex("idx_coupons_code", true, "code", "code != ''")
	col.AddIndex("idx_coupons_status", false, "status", "")
	col.AddIndex("idx_coupons_validity", false, "validFrom, validTo", "")
	col.AddIndex("idx_coupons_campaign", false, "campaign", "")
	col.AddIndex("idx_coupons_deleted", false, "deletedAt", "")

	disableCascade(col, "categories", "products", "tiers", "excludeProducts", "campaign", "createdBy")
	lockDown(col)

	return app.Save(col)
}

// createCouponGrants creates the issued instances.
func createCouponGrants(app core.App) error {
	col := findCollection(app, "coupon_grants")
	if col == nil {
		col = newBase("coupon_grants")
	}
	ensureTimestamps(col)

	addField(col, rel(app, "coupon", "coupons", true))
	// Empty for a public code nobody has claimed yet. That is why the relation is
	// optional: the code exists before its holder does.
	addField(col, rel(app, "user", "users", false))
	addField(col, text("code", true))
	addField(col, selectOf("status", true, "issued", "used", "expired", "voided"))
	addField(col, &core.DateField{Name: "issuedAt"})
	addField(col, &core.DateField{Name: "expireAt"})
	addField(col, &core.DateField{Name: "usedAt"})
	// `order` — the order that consumed it — is deliberately absent. It names a
	// collection the orders wave creates, and a relation to a missing collection
	// fails the migration rather than being inert.
	addField(col, &core.NumberField{Name: "discountApplied"})
	addField(col, rel(app, "issuedBy", "users", false))
	addField(col, text("voidReason", false))

	// Every grant has a generated code, so this one is unconditional: it is the
	// lookup a customer's code goes through.
	col.AddIndex("idx_grants_code", true, "code", "")
	col.AddIndex("idx_grants_user_status", false, "user, status", "")
	col.AddIndex("idx_grants_coupon_status", false, "coupon, status", "")
	col.AddIndex("idx_grants_expire", false, "expireAt", "")

	disableCascade(col, "coupon", "user", "issuedBy")
	lockDown(col)

	return app.Save(col)
}
