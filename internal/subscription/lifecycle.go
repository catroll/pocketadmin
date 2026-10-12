package subscription

import (
	"fmt"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// Collection names.
const (
	CollectionPlans         = "subscription_plans"
	CollectionSubscriptions = "subscriptions"
	CollectionCycles        = "subscription_cycles"
)

// Dunning is the retry policy for a failed charge.
type Dunning struct {
	// MaxAttempts bounds how many times a cycle is retried before the subscription
	// is allowed to lapse.
	MaxAttempts int

	// Backoff is the wait before the first retry; each later retry doubles it.
	Backoff time.Duration
}

// DefaultDunning is the policy when none is configured.
//
// Three attempts over about a week, which is the shape most card failures recover
// within: an expired card is fixed by the customer, and a soft decline by the issuer
// often clears on its own.
func DefaultDunning() Dunning {
	return Dunning{MaxAttempts: 3, Backoff: 24 * time.Hour}
}

// maxBackoff caps the exponential wait.
const maxBackoff = 7 * 24 * time.Hour

// NextAttempt returns when a failed cycle should be retried, and whether it should
// be at all.
func (d Dunning) NextAttempt(attempts int, lastAttempt time.Time) (time.Time, bool) {
	if d.MaxAttempts <= 0 {
		d = DefaultDunning()
	}
	if attempts >= d.MaxAttempts {
		return time.Time{}, false
	}

	wait := d.Backoff
	for i := 0; i < attempts; i++ {
		wait *= 2
		if wait > maxBackoff {
			wait = maxBackoff
			break
		}
	}

	from := lastAttempt
	if from.IsZero() {
		from = time.Now()
	}
	return from.Add(wait), true
}

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

// Start creates a subscription for an account.
//
// A plan with trial days starts in the trial and does not bill until it ends; every
// other plan starts on its first billing period. The distinction is materialised
// rather than recomputed, because "is this account in a trial" is a question the
// billing task asks.
func Start(app core.App, userID, planID string, now time.Time) (*core.Record, error) {
	if userID == "" || planID == "" {
		return nil, fmt.Errorf("subscription: a subscription needs an account and a plan")
	}

	plan, err := app.FindRecordById(CollectionPlans, planID)
	if err != nil || plan == nil {
		return nil, fmt.Errorf("subscription: no such plan")
	}
	if plan.GetString("status") != "active" {
		return nil, fmt.Errorf("subscription: that plan is not available")
	}

	col, err := app.FindCollectionByNameOrId(CollectionSubscriptions)
	if err != nil || col == nil {
		return nil, fmt.Errorf("subscription: the subscriptions collection is missing")
	}

	interval := Interval(plan.GetString("interval"))
	count := plan.GetInt("intervalCount")
	if count <= 0 {
		count = 1
	}

	period := TrialPeriod(now, plan.GetInt("trialDays"), interval, count)

	rec := core.NewRecord(col)
	rec.Set("user", userID)
	rec.Set("plan", planID)
	rec.Set("startedAt", now.UTC())
	rec.Set("currentPeriodStart", period.Start.UTC())
	rec.Set("currentPeriodEnd", period.End.UTC())
	rec.Set("nextBillingAt", period.End.UTC())

	if plan.GetInt("trialDays") > 0 {
		rec.Set("status", StatusTrialing)
		rec.Set("trialEndAt", period.End.UTC())
	} else {
		rec.Set("status", StatusActive)
	}

	if err := app.Save(rec); err != nil {
		return nil, fmt.Errorf("subscription: save: %w", err)
	}

	// The first cycle is recorded straight away, in the pending state. A cycle that
	// only appears when it is charged leaves a new subscription with no billing
	// history, and the history is what a dispute is settled with.
	if err := OpenCycle(app, rec, plan, period, CyclePending, now); err != nil {
		return nil, err
	}

	return rec, nil
}

// OpenCycle writes the cycle record for a period.
func OpenCycle(app core.App, rec *core.Record, plan *core.Record, period Period, status string, now time.Time) error {
	col, err := app.FindCollectionByNameOrId(CollectionCycles)
	if err != nil || col == nil {
		return fmt.Errorf("subscription: the cycles collection is missing")
	}

	cycle := core.NewRecord(col)
	cycle.Set("subscription", rec.Id)
	cycle.Set("cycleNumber", rec.GetInt("cycleCount")+1)
	cycle.Set("periodStart", period.Start.UTC())
	cycle.Set("periodEnd", period.End.UTC())
	cycle.Set("status", status)
	cycle.Set("amount", cycleAmount(app, plan))
	cycle.Set("currency", planCurrency(app, plan))

	if err := app.Save(cycle); err != nil {
		return fmt.Errorf("subscription: open a cycle: %w", err)
	}

	rec.Set("cycleCount", rec.GetInt("cycleCount")+1)
	return app.Save(rec)
}

// cycleAmount returns what a cycle costs, read from the plan's product.
func cycleAmount(app core.App, plan *core.Record) int64 {
	return PlanPrice(app, plan)
}

// planCurrency reads the currency from a plan's product.
//
// The price lives on the product, which is what lets a renewal be an order like any
// other.
func planCurrency(app core.App, plan *core.Record) string {
	product, err := app.FindRecordById("products", plan.GetString("product"))
	if err != nil || product == nil {
		return ""
	}
	return product.GetString("currency")
}

// PlanPrice returns a plan's price from its product.
//
// The sale price wins when there is a genuine discount, because that is what a
// customer would be charged for the product on its own.
func PlanPrice(app core.App, plan *core.Record) int64 {
	product, err := app.FindRecordById("products", plan.GetString("product"))
	if err != nil || product == nil {
		return 0
	}

	list := int64(product.GetFloat("listPrice"))
	sale := int64(product.GetFloat("salePrice"))
	if sale > 0 && sale < list {
		return sale
	}
	return list
}

// Renew advances a subscription into its next period.
func Renew(app core.App, rec *core.Record, plan *core.Record, now time.Time) error {
	interval := Interval(plan.GetString("interval"))
	count := plan.GetInt("intervalCount")
	if count <= 0 {
		count = 1
	}

	// Advancing from the period end rather than from now is deliberate: a renewal
	// that ran a day late must not shift the billing day permanently, and a
	// subscription that was suspended must not silently gain a day.
	period := NextPeriod(Period{
		Start: rec.GetDateTime("currentPeriodStart").Time(),
		End:   rec.GetDateTime("currentPeriodEnd").Time(),
	}, interval, count)

	rec.Set("currentPeriodStart", period.Start.UTC())
	rec.Set("currentPeriodEnd", period.End.UTC())
	rec.Set("nextBillingAt", period.End.UTC())
	rec.Set("retryCount", 0)
	rec.Set("status", StatusActive)

	if err := app.Save(rec); err != nil {
		return err
	}

	// A scheduled change takes effect here rather than mid-period, which is how a
	// downgrade avoids refunding part of a period already paid for.
	if pending := rec.GetString("pendingPlan"); pending != "" {
		rec.Set("plan", pending)
		rec.Set("pendingPlan", nil)
		if err := app.Save(rec); err != nil {
			return err
		}
		if next, err := app.FindRecordById(CollectionPlans, pending); err == nil && next != nil {
			plan = next
		}
	}

	// The maximum number of cycles, when the plan has one.
	if max := plan.GetInt("maxCycles"); max > 0 && rec.GetInt("cycleCount") >= max {
		rec.Set("status", StatusExpired)
		return app.Save(rec)
	}

	return OpenCycle(app, rec, plan, period, CyclePending, now)
}

// ScheduleChange sets a plan change to take effect at the end of the period.
func ScheduleChange(app core.App, rec *core.Record, planID string) error {
	if planID == "" {
		return fmt.Errorf("subscription: no plan")
	}
	if rec.GetString("plan") == planID {
		// Changing to the plan already held is a no-op rather than an error: an
		// operator who resubmits a form should not be told off for it.
		return nil
	}

	rec.Set("pendingPlan", planID)
	return app.Save(rec)
}

// Cancel records an intent to stop.
//
// Cancelling at the period end is the default: the account keeps what it has paid
// for. Cancelling immediately ends it now, which is what a refund or an abuse case
// needs, and the two are different intents rather than one with a flag.
func Cancel(app core.App, rec *core.Record, atPeriodEnd bool, operator, reason string, now time.Time) error {
	if reason == "" {
		return fmt.Errorf("subscription: cancelling requires a reason")
	}

	rec.Set("cancelledAt", now.UTC())
	rec.Set("cancelReason", reason)
	if operator != "" {
		rec.Set("cancelledBy", operator)
	}

	if atPeriodEnd {
		rec.Set("cancelAtPeriodEnd", true)
		// The subscription stays in force until the period ends, which is the whole
		// point of choosing this option.
		return app.Save(rec)
	}

	rec.Set("cancelAtPeriodEnd", false)
	rec.Set("status", StatusCancelled)
	rec.Set("nextBillingAt", nil)
	return app.Save(rec)
}

// Resume cancels a pending cancellation, or restarts a cancelled subscription.
func Resume(app core.App, rec *core.Record, now time.Time) error {
	rec.Set("cancelAtPeriodEnd", false)
	rec.Set("cancelledAt", nil)
	rec.Set("cancelReason", "")

	if rec.GetString("status") == StatusCancelled {
		// Restarting a cancelled subscription begins a fresh period from now rather
		// than resuming the old one, which may have lapsed months ago.
		rec.Set("status", StatusActive)
		rec.Set("currentPeriodStart", now.UTC())
	}

	return app.Save(rec)
}

// Pause stops billing without ending the subscription.
func Pause(app core.App, rec *core.Record, now time.Time) error {
	if rec.GetString("status") == StatusPaused {
		return nil
	}
	if rec.GetString("status") == StatusCancelled || rec.GetString("status") == StatusExpired {
		return fmt.Errorf("subscription: a %s subscription cannot be paused", rec.GetString("status"))
	}

	rec.Set("status", StatusPaused)
	rec.Set("pausedAt", now.UTC())
	rec.Set("nextBillingAt", nil)
	return app.Save(rec)
}

// Unpause resumes a paused subscription and extends its period by the time lost.
//
// Without the extension a subscriber who paused for a fortnight would pay for a
// period they could not use, which is the thing pausing exists to avoid.
func Unpause(app core.App, rec *core.Record, now time.Time) error {
	if rec.GetString("status") != StatusPaused {
		return nil
	}

	pausedAt := rec.GetDateTime("pausedAt")
	credit := PauseCredit(pausedAt.Time(), now)

	rec.Set("status", StatusActive)
	rec.Set("resumedAt", now.UTC())
	rec.Set("pausedDaysAccrued", rec.GetInt("pausedDaysAccrued")+credit)

	if credit > 0 {
		if end := rec.GetDateTime("currentPeriodEnd"); !end.IsZero() {
			extended := end.Time().AddDate(0, 0, credit)
			rec.Set("currentPeriodEnd", extended.UTC())
			rec.Set("nextBillingAt", extended.UTC())
		}
	}

	// The next billing time follows the period end. It is set from the extended end
	// rather than from the original one, so the extension actually takes effect.
	if !rec.GetDateTime("currentPeriodEnd").IsZero() {
		rec.Set("nextBillingAt", rec.GetDateTime("currentPeriodEnd").Time().UTC())
	}

	return app.Save(rec)
}

// DueForRenewal returns the subscriptions whose period has ended.
func DueForRenewal(app core.App, now time.Time) ([]*core.Record, error) {
	return app.FindRecordsByFilter(CollectionSubscriptions,
		"status = {:trialing} || status = {:active} || status = {:past}",
		"nextBillingAt", 0, 0,
		map[string]any{"trialing": StatusTrialing, "active": StatusActive, "past": StatusPastDue})
}

// MarkPastDue moves a subscription whose charge failed into the retry state.
func MarkPastDue(app core.App, rec *core.Record, now time.Time) error {
	rec.Set("status", StatusPastDue)
	rec.Set("retryCount", rec.GetInt("retryCount")+1)

	next, ok := DefaultDunning().NextAttempt(rec.GetInt("retryCount"), now)
	if !ok {
		// The budget is spent. The subscription lapses rather than being cancelled:
		// the account may still fix its card, and a cancellation is an intent the
		// customer expresses rather than one the system infers from a decline.
		rec.Set("status", StatusExpired)
		rec.Set("nextBillingAt", nil)
		return app.Save(rec)
	}

	rec.Set("nextBillingAt", next.UTC())
	return app.Save(rec)
}

// ActiveCount counts a plan's live subscriptions.
func ActiveCount(app core.App, planID string) (int, error) {
	records, err := app.FindRecordsByFilter(CollectionSubscriptions,
		"plan = {:p} && status != {:c} && status != {:e}", "", 0, 0,
		map[string]any{"p": planID, "c": StatusCancelled, "e": StatusExpired})
	if err != nil {
		return 0, err
	}
	return len(records), nil
}
