package demo

import (
	"fmt"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/membership"
	"pocketadmin/internal/order"
	"pocketadmin/internal/payment"
	"pocketadmin/internal/promotion"
	"pocketadmin/internal/refund"
	"pocketadmin/internal/subscription"
)

// This file is the business story the demonstration tells: customers hold memberships,
// some of them have coupons, some subscribe, and the orders that follow are priced with
// all of it. It is written through the modules' own functions rather than by inserting
// rows, so the example data is priced and scheduled by the same code the panel uses --
// which means the demonstration exercises the real arithmetic and cannot show a state the
// panel could not have produced.

// membership writes the tiers, their benefits and the purchasable plans.
func (s *Seeder) membership() error {
	tiers := []struct {
		key, code, name string
		level           int
		isDefault       bool
		rate            float64
		benefits        []struct {
			kind, label string
			value       map[string]any
		}
	}{
		{
			key: "tier.basic", code: "basic", name: "Basic", level: 10, isDefault: true,
		},
		{
			key: "tier.silver", code: "silver", name: "Silver", level: 20,
			rate: 0.95,
			benefits: []struct {
				kind, label string
				value       map[string]any
			}{
				{"discount_rate", "5% off", map[string]any{"rate": 0.95}},
				{"points_multiplier", "1.5x points", map[string]any{"multiplier": 1.5}},
			},
		},
		{
			key: "tier.gold", code: "gold", name: "Gold", level: 30,
			rate: 0.9,
			benefits: []struct {
				kind, label string
				value       map[string]any
			}{
				{"discount_rate", "10% off", map[string]any{"rate": 0.90}},
				{"free_shipping", "Free shipping", nil},
				{"points_multiplier", "2x points", map[string]any{"multiplier": 2.0}},
				{"priority_support", "Priority support", nil},
			},
		},
	}

	for i, tier := range tiers {
		rec, err := s.create("member_tiers", map[string]any{
			"name":        tier.name,
			"code":        tier.code,
			"level":       tier.level,
			"isDefault":   tier.isDefault,
			"description": "<p>The " + tier.name + " tier, from the demo dataset.</p>",
			"status":      "active",
			"sortOrder":   (i + 1) * 10,
		})
		if err != nil {
			return err
		}
		s.remember(tier.key, rec)

		for j, benefit := range tier.benefits {
			value := benefit.value
			if value == nil {
				value = map[string]any{}
			}
			if _, err := s.create("member_tier_benefits", map[string]any{
				"tier":      rec.Id,
				"kind":      benefit.kind,
				"value":     value,
				"label":     benefit.label,
				"sortOrder": (j + 1) * 10,
			}); err != nil {
				return err
			}
		}
	}

	// A plan is bought through an ordinary catalogue product, which is what lets a
	// membership go through the same order pipeline as anything else.
	plans := []struct {
		key, name, code, tier, slug string
		months, price               int
	}{
		{"plan.silver-year", "Silver, yearly", "silver-year", "tier.silver", "silver-year", 12, 9900},
		{"plan.gold-year", "Gold, yearly", "gold-year", "tier.gold", "gold-year", 12, 19900},
	}

	for _, plan := range plans {
		product, err := s.create("products", map[string]any{
			"kind":        "membership_plan",
			"name":        plan.name,
			"slug":        plan.slug,
			"subtitle":    "Example data",
			"description": "<p>A membership, from the demo dataset.</p>",
			"status":      "on_sale",
			"listPrice":   plan.price,
			"currency":    "USD",
			"taxable":     false,
			"stockMode":   "unlimited",
		})
		if err != nil {
			return err
		}

		if _, err := s.create("member_plans", map[string]any{
			"product":      product.Id,
			"name":         plan.name,
			"code":         plan.code,
			"tier":         s.id(plan.tier),
			"durationDays": plan.months * 30,
			"status":       "active",
		}); err != nil {
			return err
		}
	}

	// Memberships are granted through the module, so the expiry, the reason and the
	// status follow the same rules a hand-made grant would.
	grants := []struct {
		user, tier, reason string
		daysAgo, duration  int
	}{
		{"user.emma", "tier.gold", "subscribed to the yearly plan", 200, 365},
		{"user.frank", "tier.silver", "subscribed to the yearly plan", 120, 365},
		{"user.grace", "tier.gold", "support goodwill", 30, 90},
		{"user.iris", "tier.silver", "welcome offer", 10, 30},
	}

	for _, grant := range grants {
		at := s.daysAgo(grant.daysAgo)
		if _, err := membership.GrantMembership(s.app, membership.Grant{
			UserID:   s.id(grant.user),
			TierID:   s.id(grant.tier),
			Source:   membership.SourcePurchase,
			Reason:   grant.reason,
			Operator: s.id("user.alice"),
			// The duration is expressed as an expiry counted from the moment of the
			// grant, so the demonstration's dates line up with its narrative rather
			// than with the moment somebody ran the command.
			ExpireAt: at.AddDate(0, 0, grant.duration),
		}); err != nil {
			return err
		}
		s.result.Created["user_memberships"]++
	}

	return nil
}

// promotions writes a campaign and the coupons issued under it.
func (s *Seeder) promotions() error {
	campaign, err := s.create("campaigns", map[string]any{
		"name":        "Spring launch",
		"code":        "spring-launch",
		"description": "<p>A season of discounts, from the demo dataset.</p>",
		"startAt":     s.daysAgo(60).UTC(),
		"endAt":       s.daysAgo(-30).UTC(),
		"budgetCap":   500000,
		"status":      "active",
		"createdBy":   s.id("user.carol"),
	})
	if err != nil {
		return err
	}
	s.remember("campaign.spring", campaign)

	coupons := []struct {
		key, name, code, kind string
		value                 int
		maxDiscount           int
		minSpend              int
	}{
		{"coupon.ten", "Ten percent off", "SPRING10", promotion.TypePercentage, 1000, 5000, 0},
		{"coupon.five", "Five dollars off", "SPRING5", promotion.TypeFixedAmount, 500, 0, 3000},
		{"coupon.ship", "Free shipping", "FREESHIP", promotion.TypeFreeShipping, 0, 0, 0},
	}

	for _, coupon := range coupons {
		rec, err := s.create("coupons", map[string]any{
			"name":          coupon.name,
			"code":          coupon.code,
			"type":          coupon.kind,
			"value":         coupon.value,
			"maxDiscount":   coupon.maxDiscount,
			"minSpend":      coupon.minSpend,
			"currency":      "USD",
			"validityMode":  promotion.ValidityAbsolute,
			"validFrom":     s.daysAgo(60).UTC(),
			"validTo":       s.daysAgo(-30).UTC(),
			"totalQuantity": 500,
			"perUserLimit":  1,
			"applicability": promotion.ApplyAll,
			"stackable":     false,
			"campaign":      campaign.Id,
			"status":        "active",
			"createdBy":     s.id("user.carol"),
		})
		if err != nil {
			return err
		}
		s.remember(coupon.key, rec)
	}

	// A relative coupon counts from the moment it is issued, which is what "thirty days
	// from now" means and is the case worth demonstrating.
	welcome, err := s.create("coupons", map[string]any{
		"name":          "Welcome, thirty days",
		"type":          promotion.TypeFixedAmount,
		"value":         1000,
		"currency":      "USD",
		"validityMode":  promotion.ValidityRelative,
		"validDays":     30,
		"perUserLimit":  1,
		"applicability": promotion.ApplyAll,
		"status":        "active",
		"createdBy":     s.id("user.carol"),
	})
	if err != nil {
		return err
	}
	s.remember("coupon.welcome", welcome)

	// One issued coupon, held by a customer and not yet used, so the "issued" screen has
	// something in it that is not already spent.
	expiry := promotion.GrantExpiry(welcome, s.daysAgo(5))
	if _, err := s.create("coupon_grants", map[string]any{
		"coupon":   welcome.Id,
		"user":     s.id("user.hugo"),
		"code":     "WELCOME-HUGO",
		"status":   promotion.GrantIssued,
		"issuedAt": s.daysAgo(5).UTC(),
		"expireAt": expiry.UTC(),
		"issuedBy": s.id("user.carol"),
	}); err != nil {
		return err
	}

	return nil
}

// subscriptions writes a plan and the subscribers on it.
func (s *Seeder) subscriptions() error {
	product, err := s.create("products", map[string]any{
		"kind":        "subscription_plan",
		"name":        "Pro, monthly",
		"slug":        "pro-monthly",
		"subtitle":    "Example data",
		"description": "<p>A subscription, from the demo dataset.</p>",
		"status":      "on_sale",
		"listPrice":   2999,
		"currency":    "USD",
		"taxable":     true,
		"stockMode":   "unlimited",
	})
	if err != nil {
		return err
	}

	plan, err := s.create("subscription_plans", map[string]any{
		"product":       product.Id,
		"name":          "Pro, monthly",
		"code":          "pro-monthly",
		"interval":      string(subscription.IntervalMonth),
		"intervalCount": 1,
		"trialDays":     14,
		"status":        "active",
	})
	if err != nil {
		return err
	}
	s.remember("subplan.pro", plan)

	// One subscription in a trial and one well into its life, so both states are visible
	// and the difference between them is on the screen.
	subscribers := []struct {
		user     string
		daysAgo  int
		trialing bool
	}{
		{"user.grace", 3, true},
		{"user.frank", 90, false},
	}

	for _, subscriber := range subscribers {
		at := s.daysAgo(subscriber.daysAgo)
		result, err := subscription.Start(s.app, s.id(subscriber.user), plan.Id, at)
		if err != nil {
			return err
		}
		s.result.Created["subscriptions"]++

		// A subscription that has been running for a while has been billed, so its
		// cycles are advanced to the present rather than left at the first one.
		if subscriber.trialing {
			continue
		}
		for i := 0; i < 3; i++ {
			if err := subscription.Renew(s.app, result, plan, at.AddDate(0, 0, (i+1)*30)); err != nil {
				return err
			}
			s.result.Created["subscription_cycles"]++
		}
	}

	return nil
}

// commerce writes the orders, their payments and a refund.
//
// The orders go through order.Create, so the amount breakdown, the item snapshots and the
// stock reservation are the ones the panel would produce. Inserting rows directly would
// produce a demonstration whose totals do not add up, which is the one thing a
// demonstration of a money system must not do.
func (s *Seeder) commerce() error {
	type line struct {
		productKey, variantLabel string
		quantity                 int
	}
	orders := []struct {
		key, user, source, status, payment string
		daysAgo                            int
		paid                               bool
		refunded                           bool
		lines                              []line
	}{
		{
			key: "order.emma", user: "user.emma", source: order.SourceWeb,
			status: order.StatusCompleted, payment: order.PaymentPaid,
			daysAgo: 40, paid: true,
			lines: []line{{"prod.headphones", "Black", 1}, {"prod.notebook", "A5", 2}},
		},
		{
			key: "order.frank", user: "user.frank", source: order.SourceWeb,
			status: order.StatusCompleted, payment: order.PaymentPaid,
			daysAgo: 25, paid: true,
			lines: []line{{"prod.lamp", "Warm white", 1}},
		},
		{
			key: "order.grace", user: "user.grace", source: order.SourceAdmin,
			status: order.StatusPaid, payment: order.PaymentPaid,
			daysAgo: 12, paid: true,
			lines: []line{{"prod.daypack", "18 litre", 1}, {"prod.bottle", "500 ml", 3}},
		},
		{
			key: "order.hugo", user: "user.hugo", source: order.SourceWeb,
			status: order.StatusPendingPayment, payment: order.PaymentUnpaid,
			daysAgo: 2,
			lines:   []line{{"prod.speaker", "Single", 1}},
		},
		{
			key: "order.iris", user: "user.iris", source: order.SourceWeb,
			status: order.StatusRefunded, payment: order.PaymentRefunded,
			daysAgo: 50, paid: true, refunded: true,
			lines: []line{{"prod.notebook", "A5", 5}},
		},
	}

	for _, spec := range orders {
		at := s.daysAgo(spec.daysAgo)

		lines := make([]order.Line, 0, len(spec.lines))
		for _, l := range spec.lines {
			variant, err := s.app.FindRecordById("product_variants", s.id(l.productKey+".variant."+l.variantLabel))
			if err != nil || variant == nil {
				return fmt.Errorf("the example variant %s %s is missing", l.productKey, l.variantLabel)
			}
			product, err := s.app.FindRecordById("products", variant.GetString("product"))
			if err != nil || product == nil {
				return fmt.Errorf("the example product %s is missing", l.productKey)
			}

			line := order.Line{
				ProductID:    product.Id,
				VariantID:    variant.Id,
				CategoryID:   product.GetString("category"),
				ProductName:  product.GetString("name"),
				VariantLabel: variant.GetString("label"),
				SKU:          variant.GetString("sku"),
				UnitPrice:    int64(variant.GetFloat("listPrice")),
				Quantity:     l.quantity,
				Currency:     "USD",
			}
			if sale := int64(variant.GetFloat("salePrice")); sale > 0 && sale < line.UnitPrice {
				line.UnitPrice = sale
			}
			lines = append(lines, line)
		}

		// The membership discount is read the way the panel reads it, so an order placed
		// by a Gold member is priced as one.
		rate := 1.0
		tierID := ""
		if active, err := membership.Active(s.app, s.id(spec.user), at); err == nil && active != nil {
			tierID = active.Tier.Id
			rate = membership.DiscountRate(s.app, s.id(spec.user), at)
		}

		record, err := order.Create(s.app, order.CreateRequest{
			UserID: s.id(spec.user),
			Source: spec.source,
			Lines:  lines,
			Pricing: order.Input{
				Currency:    "USD",
				MemberRate:  rate,
				ShippingFee: 500,
				TaxRate:     8.25,
			},
			MemberTierID:    tierID,
			ShippingAddress: address("Example recipient", "Shanghai", "1 Example Road", "200000"),
			Reason:          "created by the demo dataset",
			Now:             at,
		})
		if err != nil {
			return err
		}
		s.remember(spec.key, record)

		if spec.paid {
			paidAt := at.Add(2 * time.Hour)

			// The payment goes through the module, which opens an intent, writes the
			// transaction and credits the order in one place. Writing the transaction by
			// hand would produce a payment with no intent behind it -- a row the panel
			// could not have made and the "waiting for payment" screen would not show.
			//
			// The clock is not injectable through that call, so the order's own timestamps
			// remain the record of when it was placed; the payment is dated now, which is
			// what recording a settlement after the fact means.
			if _, err := payment.RecordManualPayment(s.app, payment.ManualRequest{
				OrderID:    record.Id,
				Amount:     int64(record.GetFloat("grandTotal")),
				Currency:   "USD",
				Method:     payment.MethodBankTransfer,
				Reason:     "settled outside the demo, recorded by hand",
				RecordedBy: s.id("user.dan"),
				Now:        paidAt,
			}); err != nil {
				return err
			}
			s.result.Created["payment_intents"]++
			s.result.Created["payment_transactions"]++

			// The record is re-read, and this is not tidiness.
			//
			// RecordManualPayment credits the order on a record it loaded itself. The
			// `record` held here is the one order.Create returned, whose amountPaid is
			// still zero, and saving it would write that zero back over the payment --
			// which is exactly what happened the first time this ran: three orders ended
			// up completed and unpaid, and the refund that followed had nothing to
			// refund.
			record, err = s.app.FindRecordById("orders", record.Id)
			if err != nil || record == nil {
				return fmt.Errorf("could not re-read the example order: %w", err)
			}

			// A record that has been paid and delivered is completed; one that is merely
			// paid is left for an operator to move, so both states exist.
			if spec.status == order.StatusCompleted || spec.status == order.StatusRefunded {
				for _, step := range []string{order.StatusFulfilling, order.StatusCompleted} {
					if err := order.Transition(s.app, record, step, s.id("user.alice"), "delivered", paidAt.Add(time.Hour)); err != nil {
						return err
					}
					s.result.Created["order_events"]++
				}
			}
		}

		if spec.refunded {
			if err := s.refundOrder(record, at.AddDate(0, 0, 5)); err != nil {
				return err
			}
		}
	}

	return nil
}

// refundOrder requests, approves and sends a refund through the module, so the order's
// ledger is updated by the same code the screens use.
func (s *Seeder) refundOrder(record *core.Record, at time.Time) error {
	number, err := refund.NextRefundNo(s.app, "REF", at)
	if err != nil {
		return err
	}

	request, err := refund.Request(s.app, refund.RequestInput{
		RefundNo:     number,
		OrderID:      record.Id,
		Mode:         refund.ModeFull,
		Currency:     record.GetString("currency"),
		Reason:       "damaged",
		ReasonDetail: "arrived with a cracked screen",
		RequestedBy:  s.id("user.alice"),
	})
	if err != nil {
		return err
	}
	s.result.Created["refunds"]++

	// Requested by one person and approved by another, which is the rule the screen
	// enforces and therefore the only state the demonstration should show.
	if _, err := refund.Approve(s.app, request, s.id("user.dan"), "checked with the customer", refund.DefaultPolicy()); err != nil {
		return err
	}
	result, err := refund.Execute(s.app, request, offlineRefund{}, at)
	if err != nil {
		return err
	}
	if !result.Succeeded {
		return fmt.Errorf("the example refund was not sent: %s", result.Message)
	}

	s.result.Created["payment_transactions"]++
	return nil
}

// offlineRefund settles a refund without a provider, which is what a demonstration of a
// shop that takes cash needs.
type offlineRefund struct{}

func (offlineRefund) Refund(app core.App, rec *core.Record, amount int64) (refund.Execution, error) {
	return refund.Execution{
		ProviderRefundID: "demo-refund-" + rec.GetString("refundNo"),
		Response:         map[string]any{"settled": "offline"},
	}, nil
}
