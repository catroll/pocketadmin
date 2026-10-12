package membership_test

import (
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/membership"
	"pocketadmin/internal/testsupport"
)

// account creates a user.
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

// tier creates a tier.
func tier(t *testing.T, app core.App, code string, level int, isDefault bool) *core.Record {
	t.Helper()

	col, _ := app.FindCollectionByNameOrId(membership.CollectionTiers)
	rec := core.NewRecord(col)
	rec.Set("name", code)
	rec.Set("code", code)
	rec.Set("level", level)
	rec.Set("status", "active")
	rec.Set("isDefault", isDefault)
	if err := app.Save(rec); err != nil {
		t.Fatalf("save tier: %v", err)
	}
	return rec
}

// benefit adds a benefit to a tier.
func benefit(t *testing.T, app core.App, tierID, kind string, value map[string]any, label string) {
	t.Helper()

	col, _ := app.FindCollectionByNameOrId(membership.CollectionBenefits)
	rec := core.NewRecord(col)
	rec.Set("tier", tierID)
	rec.Set("kind", kind)
	rec.Set("value", value)
	rec.Set("label", label)
	if err := app.Save(rec); err != nil {
		t.Fatalf("save benefit: %v", err)
	}
}

// TestDiscountRateIsANumber is the reason the package exists.
//
// "10% off" and "10 percent off selected items" are the same string to a regular
// expression and different amounts of money to a customer, so the rate is stored
// and read as a number.
func TestDiscountRateIsANumber(t *testing.T) {
	app := testsupport.NewApp(t)
	now := time.Now()

	gold := tier(t, app, "gold", 20, false)
	benefit(t, app, gold.Id, membership.BenefitDiscountRate, map[string]any{"rate": 0.9}, "10% off")

	user := account(t, app, "member@example.com")

	// Without a membership there is no discount, which is a rate of one.
	if got := membership.DiscountRate(app, user.Id, now); got != 1.0 {
		t.Errorf("an account with no membership has a rate of %v, want 1", got)
	}

	if _, err := membership.GrantMembership(app, membership.Grant{
		UserID: user.Id, TierID: gold.Id, Source: membership.SourcePurchase,
	}); err != nil {
		t.Fatalf("GrantMembership: %v", err)
	}

	if got := membership.DiscountRate(app, user.Id, now); got != 0.9 {
		t.Errorf("the discount rate is %v, want 0.9", got)
	}
}

// TestBenefitsAreResolvedByKind: a screen reads the labels, a checkout reads the
// numbers, and neither parses a sentence.
func TestBenefitsAreResolvedByKind(t *testing.T) {
	app := testsupport.NewApp(t)

	platinum := tier(t, app, "platinum", 30, false)
	benefit(t, app, platinum.Id, membership.BenefitDiscountRate, map[string]any{"rate": 0.85}, "15% off")
	benefit(t, app, platinum.Id, membership.BenefitFreeShipping, nil, "Free shipping")
	benefit(t, app, platinum.Id, membership.BenefitPointsMultiplier, map[string]any{"multiplier": 2.0}, "2x points")
	benefit(t, app, platinum.Id, membership.BenefitContentAccess, map[string]any{"access": []any{"vip", "archive"}}, "VIP content")
	benefit(t, app, platinum.Id, membership.BenefitPrioritySupport, nil, "Priority support")
	benefit(t, app, platinum.Id, membership.BenefitCustom, map[string]any{"note": "x"}, "Something local")

	benefits := membership.ResolveBenefits(app, platinum.Id)

	if !benefits.HasDiscount || benefits.DiscountRate != 0.85 {
		t.Errorf("the discount rate is %v (has=%v)", benefits.DiscountRate, benefits.HasDiscount)
	}
	if !benefits.FreeShipping {
		t.Error("free shipping was not resolved")
	}
	if benefits.PointsMultiplier != 2.0 {
		t.Errorf("the points multiplier is %v", benefits.PointsMultiplier)
	}
	if len(benefits.ContentAccess) != 2 {
		t.Errorf("content access is %v", benefits.ContentAccess)
	}
	if !benefits.PrioritySupport {
		t.Error("priority support was not resolved")
	}
	if len(benefits.Custom) != 1 {
		t.Errorf("the custom benefit was lost: %v", benefits.Custom)
	}
	if len(benefits.Labels) != 6 {
		t.Errorf("%d labels were kept, want 6", len(benefits.Labels))
	}
}

// TestADiscountOutsideNoughtToOneIsIgnored: a value outside the range is far more
// likely to be a percentage typed into the wrong field than an intended 9000%
// discount, and applying it would charge a negative amount.
func TestADiscountOutsideNoughtToOneIsIgnored(t *testing.T) {
	app := testsupport.NewApp(t)

	// Distinct codes, because the collection has a unique index on them and a loop
	// reusing one is a test bug rather than a finding.
	for i, rate := range []float64{-0.5, 1.5, 90.0} {
		rec := tier(t, app, "odd-"+string(rune('a'+i)), 10, false)
		benefit(t, app, rec.Id, membership.BenefitDiscountRate, map[string]any{"rate": rate}, "odd")

		benefits := membership.ResolveBenefits(app, rec.Id)
		if benefits.HasDiscount {
			t.Errorf("a rate of %v was accepted as a discount", rate)
		}
	}
}

// TestTheBetterDiscountWinsWhenATierDeclaresTwo: the order of rows is not something
// an editor controls, so the result must not depend on it.
func TestTheBetterDiscountWinsWhenATierDeclaresTwo(t *testing.T) {
	app := testsupport.NewApp(t)
	gold := tier(t, app, "gold", 20, false)

	benefit(t, app, gold.Id, membership.BenefitDiscountRate, map[string]any{"rate": 0.95}, "5% off")
	benefit(t, app, gold.Id, membership.BenefitDiscountRate, map[string]any{"rate": 0.8}, "20% off")

	if got := membership.ResolveBenefits(app, gold.Id).DiscountRate; got != 0.8 {
		t.Errorf("the rate is %v, want the better 0.8", got)
	}
}

// TestMalformedBenefitDoesNotRemoveTheOthers: one bad row must not strip every
// benefit from a paying member.
func TestMalformedBenefitDoesNotRemoveTheOthers(t *testing.T) {
	app := testsupport.NewApp(t)
	gold := tier(t, app, "gold", 20, false)

	benefit(t, app, gold.Id, membership.BenefitDiscountRate, map[string]any{"wrong": "shape"}, "broken")
	benefit(t, app, gold.Id, membership.BenefitPointsMultiplier, map[string]any{"multiplier": 3.0}, "3x")

	benefits := membership.ResolveBenefits(app, gold.Id)
	if benefits.HasDiscount {
		t.Error("a benefit with no rate was treated as a discount")
	}
	if benefits.PointsMultiplier != 3.0 {
		t.Errorf("the good benefit was lost: %v", benefits.PointsMultiplier)
	}
}

// TestTheHighestTierWins: a gift must not downgrade a paying member.
func TestTheHighestTierWins(t *testing.T) {
	app := testsupport.NewApp(t)
	now := time.Now()

	silver := tier(t, app, "silver", 10, false)
	gold := tier(t, app, "gold", 20, false)
	benefit(t, app, gold.Id, membership.BenefitDiscountRate, map[string]any{"rate": 0.9}, "10% off")

	user := account(t, app, "both@example.com")

	// A gift of silver, then a purchase of gold.
	if _, err := membership.GrantMembership(app, membership.Grant{
		UserID: user.Id, TierID: silver.Id, Source: membership.SourceGift, Reason: "thank you",
	}); err != nil {
		t.Fatalf("GrantMembership: %v", err)
	}
	if _, err := membership.GrantMembership(app, membership.Grant{
		UserID: user.Id, TierID: gold.Id, Source: membership.SourcePurchase,
	}); err != nil {
		t.Fatalf("GrantMembership: %v", err)
	}

	active, err := membership.Active(app, user.Id, now)
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	if active == nil || active.Tier.GetString("code") != "gold" {
		t.Errorf("the active tier is not the higher one: %+v", active)
	}

	// And the reverse order gives the same answer.
	silverHolder := account(t, app, "reverse@example.com")
	if _, err := membership.GrantMembership(app, membership.Grant{
		UserID: silverHolder.Id, TierID: gold.Id, Source: membership.SourcePurchase,
	}); err != nil {
		t.Fatalf("GrantMembership: %v", err)
	}
	if _, err := membership.GrantMembership(app, membership.Grant{
		UserID: silverHolder.Id, TierID: silver.Id, Source: membership.SourceGift, Reason: "gift",
	}); err != nil {
		t.Fatalf("GrantMembership: %v", err)
	}

	active, _ = membership.Active(app, silverHolder.Id, now)
	if active == nil || active.Tier.GetString("code") != "gold" {
		t.Errorf("the order of grants changed the answer: %+v", active)
	}
}

// TestAnExpiredMembershipGrantsNothing: the scheduled task keeps the list honest,
// and this keeps a request from granting a benefit that ended yesterday.
func TestAnExpiredMembershipGrantsNothing(t *testing.T) {
	app := testsupport.NewApp(t)
	now := time.Now()

	gold := tier(t, app, "gold", 20, false)
	benefit(t, app, gold.Id, membership.BenefitDiscountRate, map[string]any{"rate": 0.5}, "half off")

	user := account(t, app, "lapsed@example.com")

	// Granted already expired, which is what a task that has not run yet leaves
	// behind.
	if _, err := membership.GrantMembership(app, membership.Grant{
		UserID: user.Id, TierID: gold.Id, Source: membership.SourcePurchase,
		ExpireAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("GrantMembership: %v", err)
	}

	if got := membership.DiscountRate(app, user.Id, now); got != 1.0 {
		t.Errorf("an expired membership granted a rate of %v", got)
	}

	// The scheduled task then moves it.
	expired, err := membership.ExpireDue(app, now)
	if err != nil {
		t.Fatalf("ExpireDue: %v", err)
	}
	if expired != 1 {
		t.Errorf("expired %d memberships, want 1", expired)
	}
}

// TestAPerpetualMembershipIsNotExpired: an empty expiry is a different thing from
// one far in the future and has to stay distinguishable.
func TestAPerpetualMembershipIsNotExpired(t *testing.T) {
	app := testsupport.NewApp(t)
	now := time.Now()

	lifetime := tier(t, app, "lifetime", 99, false)
	user := account(t, app, "lifetime@example.com")

	rec, err := membership.GrantMembership(app, membership.Grant{
		UserID: user.Id, TierID: lifetime.Id, Source: membership.SourcePurchase,
	})
	if err != nil {
		t.Fatalf("GrantMembership: %v", err)
	}
	if !rec.GetDateTime("expireAt").IsZero() {
		t.Error("a perpetual membership was given an expiry")
	}

	active, _ := membership.Active(app, user.Id, now)
	if active == nil || !active.Perpetual {
		t.Errorf("the membership is not reported as perpetual: %+v", active)
	}

	// Far into the future it is still in force.
	future := now.AddDate(100, 0, 0)
	if active, _ := membership.Active(app, user.Id, future); active == nil {
		t.Error("a perpetual membership lapsed")
	}
}

// TestAManualGrantRequiresAReason: a membership that appears without one is a
// support question nobody can answer.
func TestAManualGrantRequiresAReason(t *testing.T) {
	app := testsupport.NewApp(t)
	gold := tier(t, app, "gold", 20, false)
	user := account(t, app, "manual@example.com")

	if _, err := membership.GrantMembership(app, membership.Grant{
		UserID: user.Id, TierID: gold.Id, Source: membership.SourceAdmin,
	}); err == nil {
		t.Error("a manual grant without a reason was accepted")
	}

	if _, err := membership.GrantMembership(app, membership.Grant{
		UserID: user.Id, TierID: gold.Id, Source: membership.SourceAdmin, Reason: "support goodwill",
	}); err != nil {
		t.Errorf("a manual grant with a reason was refused: %v", err)
	}

	// A purchase needs no reason: the order is the reason.
	if _, err := membership.GrantMembership(app, membership.Grant{
		UserID: user.Id, TierID: gold.Id, Source: membership.SourcePurchase,
	}); err != nil {
		t.Errorf("a purchase was refused for having no reason: %v", err)
	}
}

// TestExtendingAddsToWhatIsLeft: a renewal must not shorten a membership that still
// has time on it.
func TestExtendingAddsToWhatIsLeft(t *testing.T) {
	app := testsupport.NewApp(t)
	gold := tier(t, app, "gold", 20, false)
	user := account(t, app, "renew@example.com")

	rec, err := membership.GrantMembership(app, membership.Grant{
		UserID: user.Id, TierID: gold.Id, Source: membership.SourcePurchase,
		DurationDays: 30,
	})
	if err != nil {
		t.Fatalf("GrantMembership: %v", err)
	}

	original := rec.GetDateTime("expireAt").Time()

	if err := membership.Extend(app, rec, 30, "", "renewal"); err != nil {
		t.Fatalf("Extend: %v", err)
	}

	extended := rec.GetDateTime("expireAt").Time()
	want := original.AddDate(0, 0, 30)

	if !extended.Equal(want) {
		t.Errorf("extension ends at %v, want %v (the original plus thirty days)", extended, want)
	}

	// Extending requires a reason.
	if err := membership.Extend(app, rec, 30, "", ""); err == nil {
		t.Error("an extension without a reason was accepted")
	}
	// And a zero extension is a no-op rather than an error.
	if err := membership.Extend(app, rec, 0, "", ""); err != nil {
		t.Errorf("a zero extension was refused: %v", err)
	}
}

// TestAnExtensionRevivesAnExpiredMembership: that is what a renewal is for.
func TestAnExtensionRevivesAnExpiredMembership(t *testing.T) {
	app := testsupport.NewApp(t)
	gold := tier(t, app, "gold", 20, false)
	user := account(t, app, "revive@example.com")

	rec, err := membership.GrantMembership(app, membership.Grant{
		UserID: user.Id, TierID: gold.Id, Source: membership.SourcePurchase,
		ExpireAt: time.Now().Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("GrantMembership: %v", err)
	}

	rec.Set("status", membership.StatusExpired)
	if err := app.Save(rec); err != nil {
		t.Fatalf("save: %v", err)
	}

	if err := membership.Extend(app, rec, 30, "", "paid again"); err != nil {
		t.Fatalf("Extend: %v", err)
	}
	if rec.GetString("status") != membership.StatusActive {
		t.Errorf("the revived membership is %q, want active", rec.GetString("status"))
	}
}

// TestEndingAMembershipRequiresAReasonAndATerminalState.
func TestEndingAMembershipRequiresAReasonAndATerminalState(t *testing.T) {
	app := testsupport.NewApp(t)
	gold := tier(t, app, "gold", 20, false)
	user := account(t, app, "cancel@example.com")

	rec, err := membership.GrantMembership(app, membership.Grant{
		UserID: user.Id, TierID: gold.Id, Source: membership.SourcePurchase,
	})
	if err != nil {
		t.Fatalf("GrantMembership: %v", err)
	}

	if err := membership.SetStatus(app, rec, membership.StatusCancelled, "", ""); err == nil {
		t.Error("a cancellation without a reason was accepted")
	}
	if err := membership.SetStatus(app, rec, membership.StatusActive, "", "x"); err == nil {
		t.Error("moving to a non-terminal state was accepted here")
	}
	if err := membership.SetStatus(app, rec, membership.StatusCancelled, "", "asked to stop"); err != nil {
		t.Errorf("a cancellation was refused: %v", err)
	}
	if rec.GetString("status") != membership.StatusCancelled {
		t.Errorf("the status is %q", rec.GetString("status"))
	}
}

// TestExactlyOneDefaultTier: no unique index can express "at most one true", so the
// rule lives in one place in code and is asserted here.
func TestExactlyOneDefaultTier(t *testing.T) {
	app := testsupport.NewApp(t)

	basic := tier(t, app, "basic", 1, true)
	premium := tier(t, app, "premium", 50, false)

	def, err := membership.DefaultTier(app)
	if err != nil || def == nil || def.GetString("code") != "basic" {
		t.Fatalf("the default tier is not the one marked: %v %v", def, err)
	}

	if err := membership.MakeDefault(app, premium); err != nil {
		t.Fatalf("MakeDefault: %v", err)
	}

	def, _ = membership.DefaultTier(app)
	if def == nil || def.GetString("code") != "premium" {
		t.Fatalf("the default did not move: %v", def)
	}

	// Exactly one.
	all, _ := app.FindAllRecords(membership.CollectionTiers)
	defaults := 0
	for _, rec := range all {
		if rec.GetBool("isDefault") {
			defaults++
		}
	}
	if defaults != 1 {
		t.Errorf("%d tiers are default, want 1", defaults)
	}
	_ = basic
}

// TestADisabledTierGrantsNothing: whatever memberships point at it.
func TestADisabledTierGrantsNothing(t *testing.T) {
	app := testsupport.NewApp(t)
	now := time.Now()

	gold := tier(t, app, "gold", 20, false)
	benefit(t, app, gold.Id, membership.BenefitDiscountRate, map[string]any{"rate": 0.5}, "half off")

	user := account(t, app, "disabled@example.com")
	if _, err := membership.GrantMembership(app, membership.Grant{
		UserID: user.Id, TierID: gold.Id, Source: membership.SourcePurchase,
	}); err != nil {
		t.Fatalf("GrantMembership: %v", err)
	}

	if got := membership.DiscountRate(app, user.Id, now); got != 0.5 {
		t.Fatalf("the discount is %v before disabling, want 0.5", got)
	}

	gold.Set("status", "disabled")
	if err := app.Save(gold); err != nil {
		t.Fatalf("save: %v", err)
	}

	if got := membership.DiscountRate(app, user.Id, now); got != 1.0 {
		t.Errorf("a disabled tier granted a rate of %v", got)
	}
}

// TestActiveMemberCountAndPlanReference: the two questions asked before a tier may
// be deleted.
func TestActiveMemberCountAndPlanReference(t *testing.T) {
	app := testsupport.NewApp(t)
	now := time.Now()

	gold := tier(t, app, "gold", 20, false)
	user := account(t, app, "counted@example.com")

	count, err := membership.ActiveMemberCount(app, gold.Id, now)
	if err != nil || count != 0 {
		t.Fatalf("count is %d (%v), want 0", count, err)
	}

	if _, err := membership.GrantMembership(app, membership.Grant{
		UserID: user.Id, TierID: gold.Id, Source: membership.SourcePurchase,
	}); err != nil {
		t.Fatalf("GrantMembership: %v", err)
	}

	count, _ = membership.ActiveMemberCount(app, gold.Id, now)
	if count != 1 {
		t.Errorf("count is %d, want 1", count)
	}

	// A lapsed membership that the task has not moved yet is not counted.
	lapsed := account(t, app, "lapsed2@example.com")
	if _, err := membership.GrantMembership(app, membership.Grant{
		UserID: lapsed.Id, TierID: gold.Id, Source: membership.SourcePurchase,
		ExpireAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("GrantMembership: %v", err)
	}
	count, _ = membership.ActiveMemberCount(app, gold.Id, now)
	if count != 1 {
		t.Errorf("count is %d after a lapsed membership, want 1", count)
	}

	// No plan references it yet.
	if referenced, _ := membership.ReferencedByPlan(app, gold.Id); referenced {
		t.Error("a tier with no plan was reported as referenced")
	}

	// A plan does reference it.
	products, _ := app.FindCollectionByNameOrId("products")
	product := core.NewRecord(products)
	product.Set("kind", "membership_plan")
	product.Set("name", "Gold annual")
	product.Set("slug", "gold-annual")
	product.Set("status", "on_sale")
	product.Set("stockMode", "unlimited")
	product.Set("currency", "USD")
	product.Set("listPrice", 9900)
	if err := app.Save(product); err != nil {
		t.Fatalf("save product: %v", err)
	}

	plans, _ := app.FindCollectionByNameOrId(membership.CollectionPlans)
	plan := core.NewRecord(plans)
	plan.Set("product", product.Id)
	plan.Set("name", "Gold annual")
	plan.Set("code", "gold-annual")
	plan.Set("tier", gold.Id)
	plan.Set("durationDays", 365)
	plan.Set("status", "active")
	if err := app.Save(plan); err != nil {
		t.Fatalf("save plan: %v", err)
	}

	if referenced, _ := membership.ReferencedByPlan(app, gold.Id); !referenced {
		t.Error("a referenced tier was reported as unreferenced")
	}
}

// TestExpiringSoonFindsTheWindow.
func TestExpiringSoonFindsTheWindow(t *testing.T) {
	app := testsupport.NewApp(t)
	now := time.Now()

	gold := tier(t, app, "gold", 20, false)

	soon := account(t, app, "soon@example.com")
	if _, err := membership.GrantMembership(app, membership.Grant{
		UserID: soon.Id, TierID: gold.Id, Source: membership.SourcePurchase,
		ExpireAt: now.Add(3 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("GrantMembership: %v", err)
	}

	later := account(t, app, "later@example.com")
	if _, err := membership.GrantMembership(app, membership.Grant{
		UserID: later.Id, TierID: gold.Id, Source: membership.SourcePurchase,
		ExpireAt: now.Add(300 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("GrantMembership: %v", err)
	}

	expiring, err := membership.ExpiringSoon(app, 7*24*time.Hour, now)
	if err != nil {
		t.Fatalf("ExpiringSoon: %v", err)
	}
	if len(expiring) != 1 {
		t.Fatalf("%d memberships expire within a week, want 1", len(expiring))
	}
	if expiring[0].GetString("user") != soon.Id {
		t.Error("the wrong membership was reported")
	}
}
