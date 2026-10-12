package subscription_test

import (
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/subscription"
	"pocketadmin/internal/testsupport"
)

// plan creates a subscription plan backed by a real product.
func plan(t *testing.T, app core.App, code string, interval string, count, trialDays, price int) *core.Record {
	t.Helper()

	products, _ := app.FindCollectionByNameOrId("products")
	product := core.NewRecord(products)
	product.Set("kind", "subscription_plan")
	product.Set("name", "Product "+code)
	product.Set("slug", "product-"+code)
	product.Set("status", "on_sale")
	product.Set("stockMode", "unlimited")
	product.Set("currency", "USD")
	product.Set("listPrice", price)
	if err := app.Save(product); err != nil {
		t.Fatalf("save product: %v", err)
	}

	col, _ := app.FindCollectionByNameOrId(subscription.CollectionPlans)
	rec := core.NewRecord(col)
	rec.Set("product", product.Id)
	rec.Set("name", code)
	rec.Set("code", code)
	rec.Set("interval", interval)
	rec.Set("intervalCount", count)
	rec.Set("trialDays", trialDays)
	rec.Set("status", "active")
	if err := app.Save(rec); err != nil {
		t.Fatalf("save plan: %v", err)
	}
	return rec
}

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

// cycles returns a subscription's cycle records.
func cycles(t *testing.T, app core.App, subscriptionID string) []*core.Record {
	t.Helper()

	records, err := app.FindRecordsByFilter(subscription.CollectionCycles,
		"subscription = {:s}", "cycleNumber", 0, 0, map[string]any{"s": subscriptionID})
	if err != nil {
		t.Fatalf("read cycles: %v", err)
	}
	return records
}

// TestStartWithoutATrialOpensTheFirstCycle.
func TestStartWithoutATrialOpensTheFirstCycle(t *testing.T) {
	app := testsupport.NewApp(t)
	now := day(2026, time.March, 1)

	p := plan(t, app, "monthly", string(subscription.IntervalMonth), 1, 0, 1999)
	user := account(t, app, "sub@example.com")

	rec, err := subscription.Start(app, user.Id, p.Id, now)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if rec.GetString("status") != subscription.StatusActive {
		t.Errorf("the status is %q, want active", rec.GetString("status"))
	}
	if !rec.GetDateTime("currentPeriodEnd").Time().Equal(day(2026, time.April, 1)) {
		t.Errorf("the period ends %s", rec.GetDateTime("currentPeriodEnd").Time().Format("2 Jan 2006"))
	}

	// The first cycle is recorded immediately, because a subscription with no billing
	// history is a dispute with no evidence.
	opened := cycles(t, app, rec.Id)
	if len(opened) != 1 {
		t.Fatalf("%d cycles were opened, want 1", len(opened))
	}
	if opened[0].GetInt("cycleNumber") != 1 {
		t.Errorf("the first cycle is numbered %d", opened[0].GetInt("cycleNumber"))
	}
	if opened[0].GetInt("amount") != 1999 {
		t.Errorf("the cycle is for %d, want the product price 1999", opened[0].GetInt("amount"))
	}
	if opened[0].GetString("status") != subscription.CyclePending {
		t.Errorf("the first cycle is %q, want pending", opened[0].GetString("status"))
	}
}

// TestStartWithATrialDoesNotBillUntilItEnds.
func TestStartWithATrialDoesNotBillUntilItEnds(t *testing.T) {
	app := testsupport.NewApp(t)
	now := day(2026, time.March, 1)

	p := plan(t, app, "trial", string(subscription.IntervalMonth), 1, 14, 1999)
	user := account(t, app, "trial@example.com")

	rec, err := subscription.Start(app, user.Id, p.Id, now)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if rec.GetString("status") != subscription.StatusTrialing {
		t.Errorf("the status is %q, want trialing", rec.GetString("status"))
	}
	if !rec.GetDateTime("trialEndAt").Time().Equal(day(2026, time.March, 15)) {
		t.Errorf("the trial ends %s", rec.GetDateTime("trialEndAt").Time().Format("2 Jan 2006"))
	}
	if !rec.GetDateTime("nextBillingAt").Time().Equal(day(2026, time.March, 15)) {
		t.Errorf("the first charge is at %s, want the end of the trial",
			rec.GetDateTime("nextBillingAt").Time().Format("2 Jan 2006"))
	}
}

// TestRenewAdvancesFromThePeriodEndRatherThanFromNow: a renewal that ran a day late
// must not shift the billing day permanently.
func TestRenewAdvancesFromThePeriodEndRatherThanFromNow(t *testing.T) {
	app := testsupport.NewApp(t)
	now := day(2026, time.March, 1)

	p := plan(t, app, "renew", string(subscription.IntervalMonth), 1, 0, 1999)
	user := account(t, app, "renew@example.com")

	rec, err := subscription.Start(app, user.Id, p.Id, now)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// A task that runs two days late.
	late := day(2026, time.April, 3)
	if err := subscription.Renew(app, rec, p, late); err != nil {
		t.Fatalf("Renew: %v", err)
	}

	if !rec.GetDateTime("currentPeriodStart").Time().Equal(day(2026, time.April, 1)) {
		t.Errorf("the new period starts %s, want the old period's end",
			rec.GetDateTime("currentPeriodStart").Time().Format("2 Jan 2006"))
	}
	if !rec.GetDateTime("currentPeriodEnd").Time().Equal(day(2026, time.May, 1)) {
		t.Errorf("the new period ends %s, want 1 May",
			rec.GetDateTime("currentPeriodEnd").Time().Format("2 Jan 2006"))
	}

	if got := len(cycles(t, app, rec.Id)); got != 2 {
		t.Errorf("%d cycles after a renewal, want 2", got)
	}
}

// TestTheMonthEndClampSurvivesARenewal: the case that motivated the arithmetic.
func TestTheMonthEndClampSurvivesARenewal(t *testing.T) {
	app := testsupport.NewApp(t)
	jan31 := day(2026, time.January, 31)

	p := plan(t, app, "clamp", string(subscription.IntervalMonth), 1, 0, 1999)
	user := account(t, app, "clamp@example.com")

	rec, err := subscription.Start(app, user.Id, p.Id, jan31)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !rec.GetDateTime("currentPeriodEnd").Time().Equal(day(2026, time.February, 28)) {
		t.Fatalf("the first period ends %s, want 28 February",
			rec.GetDateTime("currentPeriodEnd").Time().Format("2 Jan 2006"))
	}

	if err := subscription.Renew(app, rec, p, day(2026, time.February, 28)); err != nil {
		t.Fatalf("Renew: %v", err)
	}
	// The second month starts on the twenty-eighth and so ends on the twenty-eighth,
	// rather than the clamp having moved the billing day for good.
	if !rec.GetDateTime("currentPeriodEnd").Time().Equal(day(2026, time.March, 28)) {
		t.Errorf("the second period ends %s, want 28 March",
			rec.GetDateTime("currentPeriodEnd").Time().Format("2 Jan 2006"))
	}
}

// TestCancellingAtThePeriodEndKeepsTheSubscriptionInForce: the account keeps what it
// paid for.
func TestCancellingAtThePeriodEndKeepsTheSubscriptionInForce(t *testing.T) {
	app := testsupport.NewApp(t)
	now := day(2026, time.March, 1)

	p := plan(t, app, "endcancel", string(subscription.IntervalMonth), 1, 0, 1999)
	user := account(t, app, "endcancel@example.com")

	rec, _ := subscription.Start(app, user.Id, p.Id, now)

	if err := subscription.Cancel(app, rec, true, "", "moving on", now); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	if rec.GetString("status") != subscription.StatusActive {
		t.Errorf("the status is %q, want active until the period ends", rec.GetString("status"))
	}
	if !rec.GetBool("cancelAtPeriodEnd") {
		t.Error("the pending cancellation was not recorded")
	}
	if rec.GetString("cancelReason") != "moving on" {
		t.Error("the reason was not recorded")
	}
	if rec.GetDateTime("nextBillingAt").IsZero() {
		t.Error("the next billing time was cleared, so the subscription would not renew before it ends")
	}
}

// TestCancellingImmediatelyEndsIt.
func TestCancellingImmediatelyEndsIt(t *testing.T) {
	app := testsupport.NewApp(t)
	now := day(2026, time.March, 1)

	p := plan(t, app, "nowcancel", string(subscription.IntervalMonth), 1, 0, 1999)
	user := account(t, app, "nowcancel@example.com")

	rec, _ := subscription.Start(app, user.Id, p.Id, now)

	if err := subscription.Cancel(app, rec, false, "", "abuse", now); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if rec.GetString("status") != subscription.StatusCancelled {
		t.Errorf("the status is %q, want cancelled", rec.GetString("status"))
	}
	if !rec.GetDateTime("nextBillingAt").IsZero() {
		t.Error("a cancelled subscription still has a billing time")
	}
}

// TestCancellingRequiresAReason.
func TestCancellingRequiresAReason(t *testing.T) {
	app := testsupport.NewApp(t)
	now := day(2026, time.March, 1)

	p := plan(t, app, "noreason", string(subscription.IntervalMonth), 1, 0, 1999)
	user := account(t, app, "noreason@example.com")
	rec, _ := subscription.Start(app, user.Id, p.Id, now)

	if err := subscription.Cancel(app, rec, true, "", "", now); err == nil {
		t.Error("a cancellation without a reason was accepted")
	}
}

// TestPausingAndResumingPreservesPaidTime is the thing pausing exists to avoid: a
// subscriber paying for a period they could not use.
func TestPausingAndResumingPreservesPaidTime(t *testing.T) {
	app := testsupport.NewApp(t)
	now := day(2026, time.March, 1)

	p := plan(t, app, "pause", string(subscription.IntervalMonth), 1, 0, 1999)
	user := account(t, app, "pause@example.com")

	rec, _ := subscription.Start(app, user.Id, p.Id, now)
	originalEnd := rec.GetDateTime("currentPeriodEnd").Time()

	// Paused for ten days.
	pausedAt := rec.GetDateTime("currentPeriodStart").Time().AddDate(0, 0, 5)
	if err := subscription.Pause(app, rec, pausedAt); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if rec.GetString("status") != subscription.StatusPaused {
		t.Errorf("the status is %q, want paused", rec.GetString("status"))
	}

	resumedAt := pausedAt.AddDate(0, 0, 10)
	if err := subscription.Unpause(app, rec, resumedAt); err != nil {
		t.Fatalf("Unpause: %v", err)
	}

	if rec.GetString("status") != subscription.StatusActive {
		t.Errorf("the status is %q, want active", rec.GetString("status"))
	}
	if got := rec.GetInt("pausedDaysAccrued"); got != 10 {
		t.Errorf("%d paused days accrued, want 10", got)
	}

	want := originalEnd.AddDate(0, 0, 10)
	if !rec.GetDateTime("currentPeriodEnd").Time().Equal(want) {
		t.Errorf("the period now ends %s, want %s (the original plus the pause)",
			rec.GetDateTime("currentPeriodEnd").Time().Format("2 Jan 2006"),
			want.Format("2 Jan 2006"))
	}
	// And the next charge follows the extended period rather than the old one.
	if !rec.GetDateTime("nextBillingAt").Time().Equal(want) {
		t.Errorf("the next charge is at %s, want %s",
			rec.GetDateTime("nextBillingAt").Time().Format("2 Jan 2006"), want.Format("2 Jan 2006"))
	}
}

// TestACancelledSubscriptionCannotBePaused.
func TestACancelledSubscriptionCannotBePaused(t *testing.T) {
	app := testsupport.NewApp(t)
	now := day(2026, time.March, 1)

	p := plan(t, app, "nopause", string(subscription.IntervalMonth), 1, 0, 1999)
	user := account(t, app, "nopause@example.com")
	rec, _ := subscription.Start(app, user.Id, p.Id, now)

	rec.Set("status", subscription.StatusCancelled)
	if err := app.Save(rec); err != nil {
		t.Fatalf("save: %v", err)
	}

	if err := subscription.Pause(app, rec, now); err == nil {
		t.Error("a cancelled subscription was paused")
	}
}

// TestAScheduledChangeTakesEffectAtTheNextCycle: which is how a downgrade avoids
// refunding part of a period already paid for.
func TestAScheduledChangeTakesEffectAtTheNextCycle(t *testing.T) {
	app := testsupport.NewApp(t)
	now := day(2026, time.March, 1)

	cheap := plan(t, app, "cheap", string(subscription.IntervalMonth), 1, 0, 999)
	dear := plan(t, app, "dear", string(subscription.IntervalMonth), 1, 0, 4999)
	user := account(t, app, "change@example.com")

	rec, _ := subscription.Start(app, user.Id, dear.Id, now)

	if err := subscription.ScheduleChange(app, rec, cheap.Id); err != nil {
		t.Fatalf("ScheduleChange: %v", err)
	}
	// The change has not happened yet.
	if rec.GetString("plan") != dear.Id {
		t.Error("the plan changed before the period ended")
	}
	if rec.GetString("pendingPlan") != cheap.Id {
		t.Error("the scheduled change was not recorded")
	}

	if err := subscription.Renew(app, rec, dear, day(2026, time.April, 1)); err != nil {
		t.Fatalf("Renew: %v", err)
	}

	if rec.GetString("plan") != cheap.Id {
		t.Error("the scheduled change did not take effect at the renewal")
	}
	if rec.GetString("pendingPlan") != "" {
		t.Error("the scheduled change was not cleared")
	}

	// The cycle that was opened is for the new plan's price.
	opened := cycles(t, app, rec.Id)
	last := opened[len(opened)-1]
	if last.GetInt("amount") != 999 {
		t.Errorf("the new cycle is for %d, want the new plan's price 999", last.GetInt("amount"))
	}
}

// TestSchedulingTheCurrentPlanIsANoOp: an operator who resubmits a form should not be
// told off for it.
func TestSchedulingTheCurrentPlanIsANoOp(t *testing.T) {
	app := testsupport.NewApp(t)
	now := day(2026, time.March, 1)

	p := plan(t, app, "same", string(subscription.IntervalMonth), 1, 0, 1999)
	user := account(t, app, "same@example.com")
	rec, _ := subscription.Start(app, user.Id, p.Id, now)

	if err := subscription.ScheduleChange(app, rec, p.Id); err != nil {
		t.Errorf("scheduling the plan already held was an error: %v", err)
	}
	if rec.GetString("pendingPlan") != "" {
		t.Error("a pending change was recorded for the plan already held")
	}
}

// TestDunningGivesUpAndLapsesRatherThanCancelling: a cancellation is an intent the
// customer expresses, not one the system infers from a decline.
func TestDunningGivesUpAndLapsesRatherThanCancelling(t *testing.T) {
	app := testsupport.NewApp(t)
	now := day(2026, time.March, 1)

	p := plan(t, app, "dunning", string(subscription.IntervalMonth), 1, 0, 1999)
	user := account(t, app, "dunning@example.com")
	rec, _ := subscription.Start(app, user.Id, p.Id, now)

	// Three failures, the default budget.
	for i := 1; i <= 3; i++ {
		if err := subscription.MarkPastDue(app, rec, now.AddDate(0, 0, i)); err != nil {
			t.Fatalf("MarkPastDue: %v", err)
		}
		if rec.GetInt("retryCount") != i {
			t.Errorf("the retry count is %d after %d failures", rec.GetInt("retryCount"), i)
		}
	}

	if rec.GetString("status") != subscription.StatusExpired {
		t.Errorf("the status is %q, want expired after the budget was spent", rec.GetString("status"))
	}
	if rec.GetString("status") == subscription.StatusCancelled {
		t.Error("a failed charge cancelled the subscription, which is an intent the customer did not express")
	}
	if !rec.GetDateTime("nextBillingAt").IsZero() {
		t.Error("an expired subscription still has a billing time")
	}
}

// TestActiveCountIgnoresFinishedSubscriptions.
func TestActiveCountIgnoresFinishedSubscriptions(t *testing.T) {
	app := testsupport.NewApp(t)
	now := day(2026, time.March, 1)

	p := plan(t, app, "count", string(subscription.IntervalMonth), 1, 0, 1999)

	count, err := subscription.ActiveCount(app, p.Id)
	if err != nil || count != 0 {
		t.Fatalf("count is %d (%v), want 0", count, err)
	}

	user := account(t, app, "count@example.com")
	rec, _ := subscription.Start(app, user.Id, p.Id, now)

	count, _ = subscription.ActiveCount(app, p.Id)
	if count != 1 {
		t.Errorf("count is %d, want 1", count)
	}

	// A cancelled one does not count against the plan.
	rec.Set("status", subscription.StatusCancelled)
	if err := app.Save(rec); err != nil {
		t.Fatalf("save: %v", err)
	}
	count, _ = subscription.ActiveCount(app, p.Id)
	if count != 0 {
		t.Errorf("count is %d after a cancellation, want 0", count)
	}
}
