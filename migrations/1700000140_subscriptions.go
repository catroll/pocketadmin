package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
)

// subscriptionsMigration creates the recurring-billing plans, the subscriptions and
// their billing cycles.
//
// A subscription plan is backed by an ordinary catalogue product, the same way a
// membership plan is, which is what lets a renewal be an order like any other and go
// through the same payment pipeline.
func init() {
	migrations.Register(func(app core.App) error {
		if err := createSubscriptionPlans(app); err != nil {
			return err
		}
		if err := createSubscriptions(app); err != nil {
			return err
		}
		if err := createSubscriptionCycles(app); err != nil {
			return err
		}
		if err := seedPermissionPoints(app); err != nil {
			return err
		}
		return seedMenus(app)
	}, func(app core.App) error {
		for _, name := range []string{"subscription_cycles", "subscriptions", "subscription_plans"} {
			if col := findCollection(app, name); col != nil {
				if err := app.Delete(col); err != nil {
					return err
				}
			}
		}
		return nil
	}, "1700000140_subscriptions.go")
}

// createSubscriptionPlans creates the billing plans.
func createSubscriptionPlans(app core.App) error {
	col := findCollection(app, "subscription_plans")
	if col == nil {
		col = newBase("subscription_plans")
	}
	ensureTimestamps(col)

	addField(col, rel(app, "product", "products", true))
	addField(col, text("name", true))
	addField(col, text("code", true))
	// The interval and its count together, so "every three months" is one plan
	// rather than a special case.
	addField(col, selectOf("interval", true, "day", "week", "month", "year"))
	addField(col, &core.NumberField{Name: "intervalCount"})
	addField(col, &core.NumberField{Name: "trialDays"})
	addField(col, rel(app, "tier", "member_tiers", false))
	// Zero means the subscription renews indefinitely.
	addField(col, &core.NumberField{Name: "maxCycles"})
	addField(col, selectOf("status", true, "active", "disabled"))
	addField(col, &core.NumberField{Name: "sortOrder"})

	col.AddIndex("idx_subplans_product", true, "product", "")
	col.AddIndex("idx_subplans_code", true, "code", "")
	col.AddIndex("idx_subplans_status", false, "status", "")

	disableCascade(col, "product", "tier")
	lockDown(col)

	return app.Save(col)
}

// createSubscriptions creates the subscriptions.
func createSubscriptions(app core.App) error {
	col := findCollection(app, "subscriptions")
	if col == nil {
		col = newBase("subscriptions")
	}
	ensureTimestamps(col)

	addField(col, rel(app, "user", "users", true))
	addField(col, rel(app, "plan", "subscription_plans", true))
	addField(col, selectOf("status", true,
		"trialing", "active", "past_due", "paused", "cancelled", "expired"))

	addField(col, &core.DateField{Name: "startedAt"})
	// The period is materialised rather than computed from the start date, because a
	// pause extends it: an account that paused for a month is not on the cycle it
	// would have been on.
	addField(col, &core.DateField{Name: "currentPeriodStart"})
	addField(col, &core.DateField{Name: "currentPeriodEnd"})
	addField(col, &core.DateField{Name: "trialEndAt"})

	// Cancelling at the period end is the courteous default: the account keeps what
	// it paid for. Cancelling immediately is a separate intent.
	addField(col, &core.BoolField{Name: "cancelAtPeriodEnd"})
	addField(col, &core.DateField{Name: "cancelledAt"})
	addField(col, text("cancelReason", false))
	addField(col, rel(app, "cancelledBy", "users", false))

	addField(col, &core.DateField{Name: "pausedAt"})
	addField(col, &core.DateField{Name: "resumedAt"})
	// Days already paused, added to the period on resume so the account does not pay
	// for time it could not use.
	addField(col, &core.NumberField{Name: "pausedDaysAccrued"})

	// A scheduled change takes effect at the next cycle, which is how a downgrade
	// avoids having to refund part of a period already paid for.
	addField(col, rel(app, "pendingPlan", "subscription_plans", false))
	addField(col, &core.NumberField{Name: "retryCount"})
	addField(col, &core.DateField{Name: "nextBillingAt"})
	addField(col, &core.NumberField{Name: "cycleCount"})

	col.AddIndex("idx_subs_user_status", false, "user, status", "")
	col.AddIndex("idx_subs_status_next", false, "status, nextBillingAt", "")
	col.AddIndex("idx_subs_plan", false, "plan", "")
	col.AddIndex("idx_subs_period_end", false, "currentPeriodEnd", "")

	disableCascade(col, "user", "plan", "cancelledBy", "pendingPlan")
	lockDown(col)

	return app.Save(col)
}

// createSubscriptionCycles creates the per-cycle billing records.
func createSubscriptionCycles(app core.App) error {
	col := findCollection(app, "subscription_cycles")
	if col == nil {
		col = newBase("subscription_cycles")
	}
	ensureTimestamps(col)

	addField(col, rel(app, "subscription", "subscriptions", true))
	// One-based, so cycle one is the first period the account paid for.
	addField(col, &core.NumberField{Name: "cycleNumber"})
	addField(col, &core.DateField{Name: "periodStart"})
	addField(col, &core.DateField{Name: "periodEnd"})
	// `order` — the order generated for this cycle — is deliberately absent: it names
	// a collection the orders wave creates.
	addField(col, &core.NumberField{Name: "amount"})
	addField(col, text("currency", false))
	addField(col, selectOf("status", true, "pending", "paid", "failed", "skipped", "refunded"))
	addField(col, &core.NumberField{Name: "attempts"})
	addField(col, &core.DateField{Name: "lastAttemptAt"})
	// Credited when a plan changes mid-cycle, so the account is not charged twice for
	// the same days.
	addField(col, &core.NumberField{Name: "prorationCredit"})
	addField(col, text("note", false))

	col.AddIndex("idx_cycles_sub_number", false, "subscription, cycleNumber", "")
	col.AddIndex("idx_cycles_status", false, "status", "")
	col.AddIndex("idx_cycles_end", false, "periodEnd", "")

	disableCascade(col, "subscription")
	lockDown(col)

	return app.Save(col)
}
