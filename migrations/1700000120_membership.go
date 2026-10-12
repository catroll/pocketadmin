package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
)

// membershipMigration creates the tier catalogue, its typed benefits, the
// purchasable plans and the memberships themselves.
//
// Benefits are typed rows rather than prose for the reason the specification gives:
// pricing and checkout have to read a discount rate, and a sentence cannot be
// parsed reliably enough to charge somebody correctly.
func init() {
	migrations.Register(func(app core.App) error {
		if err := createMemberTiers(app); err != nil {
			return err
		}
		if err := createMemberTierBenefits(app); err != nil {
			return err
		}
		if err := createMemberPlans(app); err != nil {
			return err
		}
		if err := createUserMemberships(app); err != nil {
			return err
		}
		if err := seedPermissionPoints(app); err != nil {
			return err
		}
		return seedMenus(app)
	}, func(app core.App) error {
		for _, name := range []string{"user_memberships", "member_plans",
			"member_tier_benefits", "member_tiers"} {
			if col := findCollection(app, name); col != nil {
				if err := app.Delete(col); err != nil {
					return err
				}
			}
		}
		return nil
	}, "1700000120_membership.go")
}

// createMemberTiers creates the tier catalogue.
func createMemberTiers(app core.App) error {
	col := findCollection(app, "member_tiers")
	if col == nil {
		col = newBase("member_tiers")
	}
	ensureTimestamps(col)

	addField(col, text("name", true))
	addField(col, text("code", true))
	// Higher is better. Ordering by a number rather than by the order rows happened
	// to be created is what makes "upgrade" and "downgrade" meaningful.
	addField(col, &core.NumberField{Name: "level"})
	addField(col, text("icon", false))
	addField(col, &core.EditorField{Name: "description"})
	addField(col, selectOf("status", true, "active", "disabled"))
	addField(col, &core.NumberField{Name: "sortOrder"})
	// Exactly one tier is granted on registration. There is no unique index that can
	// express "at most one true", so the rule is enforced in one place in code.
	addField(col, &core.BoolField{Name: "isDefault"})

	col.AddIndex("idx_tiers_code", true, "code", "")
	col.AddIndex("idx_tiers_level", false, "level", "")
	col.AddIndex("idx_tiers_status", false, "status", "")

	lockDown(col)

	return app.Save(col)
}

// createMemberTierBenefits creates the typed benefits.
func createMemberTierBenefits(app core.App) error {
	col := findCollection(app, "member_tier_benefits")
	if col == nil {
		col = newBase("member_tier_benefits")
	}
	ensureTimestamps(col)

	addField(col, rel(app, "tier", "member_tiers", true))
	addField(col, selectOf("kind", true,
		"discount_rate", "free_shipping", "points_multiplier", "content_access",
		"priority_support", "custom"))
	// The payload is json and its shape depends on the kind: {"rate": 0.9} for a
	// discount, {"multiplier": 2} for points. Typing it as a column per kind would
	// mean a sparse table and a migration per new kind.
	addField(col, jsonField("value", 4000))
	addField(col, text("label", false))
	addField(col, &core.NumberField{Name: "sortOrder"})

	col.AddIndex("idx_benefits_tier_kind", false, "tier, kind", "")

	disableCascade(col, "tier")
	lockDown(col)

	return app.Save(col)
}

// createMemberPlans creates the purchasable plans.
//
// A plan is backed by an ordinary catalogue product. That is what lets a membership
// be bought, discounted and refunded through the same order pipeline as anything
// else, rather than needing a payment path of its own.
func createMemberPlans(app core.App) error {
	col := findCollection(app, "member_plans")
	if col == nil {
		col = newBase("member_plans")
	}
	ensureTimestamps(col)

	addField(col, rel(app, "product", "products", true))
	addField(col, text("name", true))
	addField(col, text("code", true))
	addField(col, rel(app, "tier", "member_tiers", true))
	// Zero means the membership does not expire.
	addField(col, &core.NumberField{Name: "durationDays"})
	addField(col, selectOf("status", true, "active", "disabled"))
	addField(col, &core.NumberField{Name: "sortOrder"})

	col.AddIndex("idx_mplans_product", true, "product", "")
	col.AddIndex("idx_mplans_code", true, "code", "")
	col.AddIndex("idx_mplans_tier", false, "tier", "")
	col.AddIndex("idx_mplans_status", false, "status", "")

	disableCascade(col, "product", "tier")
	lockDown(col)

	return app.Save(col)
}

// createUserMemberships creates the memberships themselves.
func createUserMemberships(app core.App) error {
	col := findCollection(app, "user_memberships")
	if col == nil {
		col = newBase("user_memberships")
	}
	ensureTimestamps(col)

	addField(col, rel(app, "user", "users", true))
	addField(col, rel(app, "tier", "member_tiers", true))
	// Empty for a membership granted by hand rather than bought.
	addField(col, rel(app, "plan", "member_plans", false))
	// `order` — the purchase that created this membership — is deliberately absent.
	// It points at a collection the orders wave creates, and a relation to a missing
	// collection fails the migration rather than being inert, so the field is added
	// when its target exists, in the same way `files.provider` was.
	addField(col, &core.DateField{Name: "startAt"})
	// Empty expireAt means perpetual, which is a different thing from an expiry far
	// in the future and has to stay distinguishable.
	addField(col, &core.DateField{Name: "expireAt"})
	addField(col, selectOf("status", true,
		"pending", "active", "expired", "cancelled", "refunded"))
	addField(col, selectOf("source", true, "purchase", "gift", "trial", "admin", "promotion"))
	addField(col, rel(app, "grantedBy", "users", false))
	// The reason is required for a manual change: a membership that appeared or
	// disappeared without one is a support ticket nobody can answer.
	addField(col, text("reason", false))
	addField(col, text("remarks", false))

	col.AddIndex("idx_memberships_user_status", false, "user, status", "")
	col.AddIndex("idx_memberships_expire", false, "expireAt", "")
	col.AddIndex("idx_memberships_tier", false, "tier", "")

	disableCascade(col, "user", "tier", "plan", "grantedBy")
	lockDown(col)

	return app.Save(col)
}
