package promotion_test

import (
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/promotion"
	"pocketadmin/internal/testsupport"
)

// seq makes each fixture name unique, so a partial unique index on the code and a
// unique grant code are not tripped by a test that creates several.
var seq int

// product creates a real product, because a relation field refuses an id that names
// nothing.
func product(t *testing.T, app core.App, name, categoryID string) *core.Record {
	t.Helper()

	col, _ := app.FindCollectionByNameOrId("products")
	rec := core.NewRecord(col)
	rec.Set("kind", "physical")
	rec.Set("name", name)
	rec.Set("slug", "slug-"+name)
	rec.Set("status", "on_sale")
	rec.Set("stockMode", "unlimited")
	rec.Set("currency", "USD")
	if categoryID != "" {
		rec.Set("category", categoryID)
	}
	if err := app.Save(rec); err != nil {
		t.Fatalf("save product: %v", err)
	}
	return rec
}

// category creates a real product category.
func category(t *testing.T, app core.App, name string) *core.Record {
	t.Helper()

	col, _ := app.FindCollectionByNameOrId("product_categories")
	rec := core.NewRecord(col)
	rec.Set("name", name)
	rec.Set("slug", "cat-"+name)
	rec.Set("status", "active")
	if err := app.Save(rec); err != nil {
		t.Fatalf("save category: %v", err)
	}
	return rec
}

// account creates a real user, because a relation field refuses an id that names
// nothing.
func account(t *testing.T, app core.App, email string) *core.Record {
	t.Helper()

	col, _ := app.FindCollectionByNameOrId("users")
	rec := core.NewRecord(col)
	rec.Set("email", email)
	rec.Set("password", "change-me-please")
	rec.Set("passwordConfirm", "change-me-please")
	rec.Set("status", "active")
	if err := app.Save(rec); err != nil {
		t.Fatalf("save user: %v", err)
	}
	return rec
}

// coupon creates a coupon template.
func coupon(t *testing.T, app core.App, fields map[string]any) *core.Record {
	t.Helper()

	col, _ := app.FindCollectionByNameOrId(promotion.CollectionCoupons)
	rec := core.NewRecord(col)

	seq++
	// Defaults, overridden by the caller.
	rec.Set("name", "Test coupon")
	rec.Set("type", promotion.TypePercentage)
	rec.Set("status", "active")
	rec.Set("validityMode", promotion.ValidityAbsolute)
	rec.Set("applicability", promotion.ApplyAll)

	for name, value := range fields {
		rec.Set(name, value)
	}

	if rec.GetString("code") == "" {
		// Left empty on purpose by the tests that exercise auto-issue, so the
		// partial index is what keeps them apart rather than a code the tests set.
		if _, asked := fields["code"]; !asked {
			rec.Set("code", "")
		}
	}

	if err := app.Save(rec); err != nil {
		t.Fatalf("save coupon: %v", err)
	}
	return rec
}

// grant creates an issued instance.
func grant(t *testing.T, app core.App, couponID, userID, code string) *core.Record {
	t.Helper()

	col, _ := app.FindCollectionByNameOrId(promotion.CollectionGrants)
	rec := core.NewRecord(col)
	rec.Set("coupon", couponID)
	seq++
	rec.Set("code", code)
	rec.Set("status", promotion.GrantIssued)
	rec.Set("issuedAt", time.Now().UTC())
	if userID != "" {
		rec.Set("user", userID)
	}
	if err := app.Save(rec); err != nil {
		t.Fatalf("save grant: %v", err)
	}
	return rec
}

// TestPercentageIsInBasisPoints is the reason the column is an integer.
//
// Twelve and a half percent of 1999 is 249.875. A whole-number percent column cannot
// hold 12.5, and a float cannot be trusted with the arithmetic: the answer is 250
// units, rounded once, and it has to be the same every time.
func TestPercentageIsInBasisPoints(t *testing.T) {
	app := testsupport.NewApp(t)

	rec := coupon(t, app, map[string]any{
		"type": promotion.TypePercentage, "value": 1250, // 12.5%
	})

	result := promotion.Evaluate(app, rec, nil,
		promotion.Cart{Subtotal: 1999, Currency: "USD"}, time.Now())

	if !result.Applicable {
		t.Fatalf("the coupon was refused: %s", result.Reason)
	}
	// 1999 * 1250 / 10000 = 249.875, which rounds to 250.
	if result.Discount != 250 {
		t.Errorf("the discount is %d, want 250", result.Discount)
	}
}

// TestWholePercentagesAreExact.
func TestWholePercentagesAreExact(t *testing.T) {
	app := testsupport.NewApp(t)

	cases := []struct {
		points   int
		subtotal int64
		want     int64
	}{
		{1000, 1999, 200},   // 10% of 19.99 is 2.00, rounded
		{1000, 2000, 200},   // exact
		{5000, 1999, 1000},  // half
		{10000, 1999, 1999}, // all of it
		{2500, 10000, 2500}, // a quarter of a hundred
		{3333, 30000, 9999}, // a third, near enough
	}

	for _, tc := range cases {
		rec := coupon(t, app, map[string]any{
			"name": "p", "type": promotion.TypePercentage, "value": tc.points,
		})

		result := promotion.Evaluate(app, rec, nil,
			promotion.Cart{Subtotal: tc.subtotal, Currency: "USD"}, time.Now())

		if !result.Applicable {
			t.Errorf("%d basis points on %d was refused: %s", tc.points, tc.subtotal, result.Reason)
			continue
		}
		if result.Discount != tc.want {
			t.Errorf("%d basis points on %d gives %d, want %d",
				tc.points, tc.subtotal, result.Discount, tc.want)
		}
	}
}

// TestADiscountNeverExceedsTheSubtotal: a fixed-amount coupon on a small basket
// would otherwise produce a negative total, which is a refund the shop did not
// agree to.
func TestADiscountNeverExceedsTheSubtotal(t *testing.T) {
	app := testsupport.NewApp(t)

	rec := coupon(t, app, map[string]any{
		"type": promotion.TypeFixedAmount, "value": 5000, "currency": "USD",
	})

	result := promotion.Evaluate(app, rec, nil,
		promotion.Cart{Subtotal: 1999, Currency: "USD"}, time.Now())

	if !result.Applicable {
		t.Fatalf("refused: %s", result.Reason)
	}
	if result.Discount != 1999 {
		t.Errorf("the discount is %d, want the whole 1999", result.Discount)
	}
}

// TestTheCapApplies.
func TestTheCapApplies(t *testing.T) {
	app := testsupport.NewApp(t)

	rec := coupon(t, app, map[string]any{
		"type": promotion.TypePercentage, "value": 5000, "maxDiscount": 1000,
	})

	result := promotion.Evaluate(app, rec, nil,
		promotion.Cart{Subtotal: 10000, Currency: "USD"}, time.Now())

	if result.Discount != 1000 {
		t.Errorf("the discount is %d, want the cap of 1000", result.Discount)
	}
}

// TestMinimumSpendIsCheckedBeforeTheAmount: reporting "below the minimum spend" for
// a coupon that expired last week would send somebody to fill a basket for nothing.
func TestMinimumSpendIsCheckedBeforeTheAmount(t *testing.T) {
	app := testsupport.NewApp(t)

	rec := coupon(t, app, map[string]any{
		"type": promotion.TypePercentage, "value": 1000, "minSpend": 5000,
	})

	result := promotion.Evaluate(app, rec, nil,
		promotion.Cart{Subtotal: 1999, Currency: "USD"}, time.Now())

	if result.Applicable {
		t.Error("a coupon below its minimum spend was applied")
	}
	if result.Reason == "" {
		t.Error("the refusal gave no reason")
	}

	// At the minimum it applies.
	result = promotion.Evaluate(app, rec, nil,
		promotion.Cart{Subtotal: 5000, Currency: "USD"}, time.Now())
	if !result.Applicable {
		t.Errorf("a coupon at its minimum spend was refused: %s", result.Reason)
	}
}

// TestFreeShippingDiscountsNothingAgainstTheSubtotal: it is a statement about the
// shipping, and reporting it as a subtotal discount would understate the total.
func TestFreeShippingDiscountsNothingAgainstTheSubtotal(t *testing.T) {
	app := testsupport.NewApp(t)

	rec := coupon(t, app, map[string]any{"type": promotion.TypeFreeShipping})

	result := promotion.Evaluate(app, rec, nil,
		promotion.Cart{Subtotal: 1999, Currency: "USD"}, time.Now())

	if !result.Applicable || !result.FreeShipping {
		t.Fatalf("free shipping was refused: %s", result.Reason)
	}
	if result.Discount != 0 {
		t.Errorf("free shipping discounted %d from the subtotal", result.Discount)
	}
}

// TestApplicabilityRestrictsWhatIsDiscounted: a coupon for one category must not
// discount the whole basket.
func TestApplicabilityRestrictsWhatIsDiscounted(t *testing.T) {
	app := testsupport.NewApp(t)

	clothingCat := category(t, app, "clothing")
	electronicsCat := category(t, app, "electronics")
	gardenCat := category(t, app, "garden")
	shirt := product(t, app, "shirt", clothingCat.Id)
	laptop := product(t, app, "laptop", electronicsCat.Id)

	cart := promotion.Cart{
		Subtotal: 10000,
		Currency: "USD",
		Lines: []promotion.Line{
			{ProductID: shirt.Id, CategoryID: clothingCat.Id, Amount: 3000},
			{ProductID: laptop.Id, CategoryID: electronicsCat.Id, Amount: 7000},
		},
	}

	// A whole-cart coupon.
	all := coupon(t, app, map[string]any{
		"name": "all", "type": promotion.TypePercentage, "value": 1000,
	})
	if got := promotion.Evaluate(app, all, nil, cart, time.Now()).Discount; got != 1000 {
		t.Errorf("a whole-cart 10%% coupon gave %d, want 1000", got)
	}

	// One restricted to clothing.
	clothing := coupon(t, app, map[string]any{
		"name": "clothing", "type": promotion.TypePercentage, "value": 1000,
		"applicability": promotion.ApplyCategories, "categories": []string{clothingCat.Id},
	})
	if got := promotion.Evaluate(app, clothing, nil, cart, time.Now()).Discount; got != 300 {
		t.Errorf("a clothing-only 10%% coupon gave %d, want 300 (10%% of the 3000 shirt)", got)
	}

	// One restricted to a product.
	product := coupon(t, app, map[string]any{
		"name": "laptop", "type": promotion.TypePercentage, "value": 1000,
		"applicability": promotion.ApplyProducts, "products": []string{laptop.Id},
	})
	if got := promotion.Evaluate(app, product, nil, cart, time.Now()).Discount; got != 700 {
		t.Errorf("a laptop-only 10%% coupon gave %d, want 700", got)
	}

	// One restricted to a category not in the cart.
	absent := coupon(t, app, map[string]any{
		"name": "absent", "type": promotion.TypePercentage, "value": 1000,
		"applicability": promotion.ApplyCategories, "categories": []string{gardenCat.Id},
	})
	result := promotion.Evaluate(app, absent, nil, cart, time.Now())
	if result.Applicable {
		t.Error("a coupon for a category not in the cart was applied")
	}

	// A tier-targeted coupon applies to the whole cart, because the tier is a
	// statement about the customer rather than about the goods.
	// A real tier: the relation is validated on save, so a made-up id is refused.
	tierCol, _ := app.FindCollectionByNameOrId("member_tiers")
	gold := core.NewRecord(tierCol)
	gold.Set("name", "Gold")
	gold.Set("code", "gold")
	gold.Set("level", 20)
	gold.Set("status", "active")
	if err := app.Save(gold); err != nil {
		t.Fatalf("save tier: %v", err)
	}

	tiered := coupon(t, app, map[string]any{
		"name": "gold-coupon", "type": promotion.TypePercentage, "value": 1000,
		"applicability": promotion.ApplyTiers, "tiers": []string{gold.Id},
	})
	if got := promotion.Evaluate(app, tiered, nil, cart, time.Now()).Applicable; got {
		t.Error("a tier coupon was applied to a cart holding none of its tiers")
	}

	cart.TierIDs = []string{gold.Id, "silver"}
	if got := promotion.Evaluate(app, tiered, nil, cart, time.Now()).Discount; got != 1000 {
		t.Errorf("a gold-tier coupon gave %d on the whole cart, want 1000", got)
	}
}

// TestExclusionsApplyWhateverTheTargeting: an exclusion is a statement about a
// product, not about how the coupon was aimed.
func TestExclusionsApplyWhateverTheTargeting(t *testing.T) {
	app := testsupport.NewApp(t)

	shirtCat := category(t, app, "shirt-cat")
	laptopCat := category(t, app, "laptop-cat")
	shirt := product(t, app, "a-shirt", shirtCat.Id)
	laptop := product(t, app, "a-laptop", laptopCat.Id)

	cart := promotion.Cart{
		Subtotal: 10000,
		Currency: "USD",
		Lines: []promotion.Line{
			{ProductID: shirt.Id, CategoryID: shirtCat.Id, Amount: 3000},
			{ProductID: laptop.Id, CategoryID: laptopCat.Id, Amount: 7000},
		},
	}

	rec := coupon(t, app, map[string]any{
		"type": promotion.TypePercentage, "value": 1000,
		"applicability": promotion.ApplyAll, "excludeProducts": []string{laptop.Id},
	})

	result := promotion.Evaluate(app, rec, nil, cart, time.Now())
	if result.Discount != 300 {
		t.Errorf("the discount is %d, want 300 (the shirt alone)", result.Discount)
	}
}

// TestGrantStateIsChecked.
func TestGrantStateIsChecked(t *testing.T) {
	app := testsupport.NewApp(t)
	now := time.Now()

	rec := coupon(t, app, map[string]any{"type": promotion.TypePercentage, "value": 1000})
	cart := promotion.Cart{Subtotal: 1000, Currency: "USD"}

	for _, status := range []string{promotion.GrantUsed, promotion.GrantVoided, promotion.GrantExpired} {
		g := grant(t, app, rec.Id, "", "code-"+status)
		g.Set("status", status)
		if err := app.Save(g); err != nil {
			t.Fatalf("save: %v", err)
		}

		if result := promotion.Evaluate(app, rec, g, cart, now); result.Applicable {
			t.Errorf("a %s grant was applied: %s", status, result.Reason)
		}
	}

	// An issued one applies.
	g := grant(t, app, rec.Id, "", "code-issued")
	if result := promotion.Evaluate(app, rec, g, cart, now); !result.Applicable {
		t.Errorf("an issued grant was refused: %s", result.Reason)
	}

	// And one past its own expiry does not.
	expired := grant(t, app, rec.Id, "", "code-expired-grant")
	expired.Set("expireAt", now.Add(-time.Hour))
	if err := app.Save(expired); err != nil {
		t.Fatalf("save: %v", err)
	}
	if result := promotion.Evaluate(app, rec, expired, cart, now); result.Applicable {
		t.Error("a grant past its own expiry was applied")
	}
}

// TestAGrantBelongsToOneAccount: a named grant must not be usable by somebody else.
func TestAGrantBelongsToOneAccount(t *testing.T) {
	app := testsupport.NewApp(t)
	now := time.Now()

	rec := coupon(t, app, map[string]any{"type": promotion.TypePercentage, "value": 1000})
	alice := account(t, app, "alice@example.com")
	bob := account(t, app, "bob@example.com")

	g := grant(t, app, rec.Id, alice.Id, "code-alice")

	if result := promotion.Evaluate(app, rec, g,
		promotion.Cart{Subtotal: 1000, Currency: "USD", HolderID: bob.Id}, now); result.Applicable {
		t.Error("a grant belonging to one account was applied to another's cart")
	}
	if result := promotion.Evaluate(app, rec, g,
		promotion.Cart{Subtotal: 1000, Currency: "USD", HolderID: alice.Id}, now); !result.Applicable {
		t.Errorf("the holder was refused their own grant: %s", result.Reason)
	}
}

// TestTheCurrencyMustMatch: ten dollars off a euro basket is ten of something, and
// there is no sensible conversion here.
func TestTheCurrencyMustMatch(t *testing.T) {
	app := testsupport.NewApp(t)

	rec := coupon(t, app, map[string]any{
		"type": promotion.TypeFixedAmount, "value": 500, "currency": "USD",
	})

	if result := promotion.Evaluate(app, rec, nil,
		promotion.Cart{Subtotal: 1999, Currency: "EUR"}, time.Now()); result.Applicable {
		t.Error("a dollar coupon was applied to a euro order")
	}
	if result := promotion.Evaluate(app, rec, nil,
		promotion.Cart{Subtotal: 1999, Currency: "USD"}, time.Now()); !result.Applicable {
		t.Error("a dollar coupon was refused on a dollar order")
	}
}

// TestValidityWindows.
func TestValidityWindows(t *testing.T) {
	app := testsupport.NewApp(t)
	now := time.Now()

	cart := promotion.Cart{Subtotal: 1000, Currency: "USD"}

	// Not yet valid.
	future := coupon(t, app, map[string]any{
		"name": "future", "type": promotion.TypePercentage, "value": 1000,
		"validFrom": now.Add(time.Hour),
	})
	if result := promotion.Evaluate(app, future, nil, cart, now); result.Applicable {
		t.Error("a coupon that is not valid yet was applied")
	}

	// Expired.
	past := coupon(t, app, map[string]any{
		"name": "past", "type": promotion.TypePercentage, "value": 1000,
		"validTo": now.Add(-time.Hour),
	})
	if result := promotion.Evaluate(app, past, nil, cart, now); result.Applicable {
		t.Error("an expired coupon was applied")
	}

	// In force.
	current := coupon(t, app, map[string]any{
		"name": "current", "type": promotion.TypePercentage, "value": 1000,
		"validFrom": now.Add(-time.Hour), "validTo": now.Add(time.Hour),
	})
	if result := promotion.Evaluate(app, current, nil, cart, now); !result.Applicable {
		t.Errorf("a coupon in force was refused: %s", result.Reason)
	}
}

// TestGrantExpiryFollowsTheMode: a relative coupon counts from the grant, an
// absolute one inherits its window, and the two are not interchangeable.
func TestGrantExpiryFollowsTheMode(t *testing.T) {
	app := testsupport.NewApp(t)
	issued := time.Now()

	relative := coupon(t, app, map[string]any{
		"name": "rel", "type": promotion.TypePercentage, "value": 1000,
		"validityMode": promotion.ValidityRelative, "validDays": 30,
	})
	got := promotion.GrantExpiry(relative, issued)
	want := issued.AddDate(0, 0, 30)
	if !got.Equal(want) {
		t.Errorf("a relative coupon expires at %v, want %v (thirty days from the grant)", got, want)
	}

	window := issued.AddDate(0, 0, 365)
	absolute := coupon(t, app, map[string]any{
		"name": "abs", "type": promotion.TypePercentage, "value": 1000,
		"validityMode": promotion.ValidityAbsolute, "validTo": window,
	})
	if got := promotion.GrantExpiry(absolute, issued); !got.Equal(window) {
		t.Errorf("an absolute coupon expires at %v, want its own window %v", got, window)
	}

	// A relative coupon with no period and no window never expires.
	forever := coupon(t, app, map[string]any{
		"name": "forever", "type": promotion.TypePercentage, "value": 1000,
		"validityMode": promotion.ValidityRelative, "validDays": 0,
	})
	if got := promotion.GrantExpiry(forever, issued); !got.IsZero() {
		t.Errorf("a coupon with no period expires at %v", got)
	}
}

// TestIssuanceLimits: a limit enforced against a denormalised counter is not
// enforced at all, so the counts are read from the grants.
func TestIssuanceLimits(t *testing.T) {
	app := testsupport.NewApp(t)

	bounded := coupon(t, app, map[string]any{
		"name": "bounded", "type": promotion.TypePercentage, "value": 1000,
		"totalQuantity": 2, "perUserLimit": 1,
	})

	if ok, why := promotion.CanIssue(app, bounded); !ok {
		t.Errorf("a fresh coupon could not be issued: %s", why)
	}
	alice := account(t, app, "alice@example.com")
	bob := account(t, app, "bob@example.com")
	carol := account(t, app, "carol@example.com")

	if ok, why := promotion.CanIssueTo(app, bounded, alice.Id); !ok {
		t.Errorf("Alice could not be issued a fresh coupon: %s", why)
	}

	grant(t, app, bounded.Id, alice.Id, "g1")

	if ok, why := promotion.CanIssueTo(app, bounded, alice.Id); ok {
		t.Error("Alice was issued a coupon above her per-account limit")
	} else if why == "" {
		t.Error("the refusal gave no reason")
	}
	// And another account is unaffected.
	if ok, why := promotion.CanIssueTo(app, bounded, bob.Id); !ok {
		t.Errorf("Bob was refused because Alice had one: %s", why)
	}

	grant(t, app, bounded.Id, bob.Id, "g2")

	if ok, why := promotion.CanIssue(app, bounded); ok {
		t.Error("a coupon past its total quantity was still issuable")
	} else if why == "" {
		t.Error("the refusal gave no reason")
	}

	// A voided grant does not count against the total: it was never used.
	voided := grant(t, app, bounded.Id, carol.Id, "g3")
	voided.Set("status", promotion.GrantVoided)
	if err := app.Save(voided); err != nil {
		t.Fatalf("save: %v", err)
	}
	if ok, _ := promotion.CanIssue(app, bounded); ok {
		t.Error("a voided grant freed a slot it should not have")
	}
}

// TestAnInactiveCouponIsRefused.
func TestAnInactiveCouponIsRefused(t *testing.T) {
	app := testsupport.NewApp(t)
	cart := promotion.Cart{Subtotal: 1000, Currency: "USD"}

	for _, status := range []string{"draft", "paused", "expired"} {
		rec := coupon(t, app, map[string]any{
			"name": status, "type": promotion.TypePercentage, "value": 1000, "status": status,
		})
		if result := promotion.Evaluate(app, rec, nil, cart, time.Now()); result.Applicable {
			t.Errorf("a %s coupon was applied", status)
		}
	}
}

// TestAPercentageAboveAHundredIsClamped: a misconfigured "free" coupon should give
// the order away, not fail at the till.
func TestAPercentageAboveAHundredIsClamped(t *testing.T) {
	app := testsupport.NewApp(t)

	rec := coupon(t, app, map[string]any{
		"type": promotion.TypePercentage, "value": 25000, // 250%
	})

	result := promotion.Evaluate(app, rec, nil,
		promotion.Cart{Subtotal: 1999, Currency: "USD"}, time.Now())

	if !result.Applicable {
		t.Fatalf("refused: %s", result.Reason)
	}
	if result.Discount != 1999 {
		t.Errorf("the discount is %d, want the whole subtotal", result.Discount)
	}
}
