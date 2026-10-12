package subscription_test

import (
	"testing"
	"time"

	"pocketadmin/internal/subscription"
)

// day builds a date for a readable expectation.
func day(year int, month time.Month, d int) time.Time {
	return time.Date(year, month, d, 9, 30, 0, 0, time.UTC)
}

// TestAddingAMonthClampsToTheLastDay is the defect this arithmetic exists to avoid.
//
// Go's AddDate normalises: the thirty-first of January plus one month is the third of
// March, because the thirty-first of February does not exist and the overflow rolls
// forward. A subscriber who signed up on the thirty-first would be billed three days
// late every February, and the drift would compound.
func TestAddingAMonthClampsToTheLastDay(t *testing.T) {
	cases := []struct {
		from time.Time
		want time.Time
		why  string
	}{
		{day(2026, time.January, 31), day(2026, time.February, 28), "a short month"},
		{day(2026, time.March, 31), day(2026, time.April, 30), "thirty days hath April"},
		{day(2026, time.May, 31), day(2026, time.June, 30), "June"},
		{day(2026, time.August, 31), day(2026, time.September, 30), "September"},
		{day(2026, time.October, 31), day(2026, time.November, 30), "November"},
		{day(2026, time.January, 15), day(2026, time.February, 15), "an ordinary day is kept"},
		{day(2026, time.December, 31), day(2027, time.January, 31), "across a year boundary"},
	}

	for _, tc := range cases {
		got := subscription.AddInterval(tc.from, subscription.IntervalMonth, 1)
		if !got.Equal(tc.want) {
			t.Errorf("%s: %s + 1 month = %s, want %s",
				tc.why, tc.from.Format("2 Jan 2006"), got.Format("2 Jan 2006"), tc.want.Format("2 Jan 2006"))
		}
	}
}

// TestTheLeapYearIsHandled: February has twenty-nine days in 2028 and the clamp has
// to find it.
func TestTheLeapYearIsHandled(t *testing.T) {
	got := subscription.AddInterval(day(2028, time.January, 31), subscription.IntervalMonth, 1)
	if !got.Equal(day(2028, time.February, 29)) {
		t.Errorf("31 Jan 2028 + 1 month = %s, want the 29th", got.Format("2 Jan 2006"))
	}

	// And the thirty-first of a thirty-day month lands on the thirtieth.
	got = subscription.AddInterval(day(2028, time.January, 31), subscription.IntervalMonth, 2)
	if !got.Equal(day(2028, time.March, 31)) {
		t.Errorf("31 Jan 2028 + 2 months = %s, want the 31st of March", got.Format("2 Jan 2006"))
	}
}

// TestTheTimeOfDayIsKept: a subscription bills at the moment it was taken out, not at
// midnight.
func TestTheTimeOfDayIsKept(t *testing.T) {
	from := time.Date(2026, time.March, 15, 14, 45, 30, 0, time.UTC)
	got := subscription.AddInterval(from, subscription.IntervalMonth, 1)

	if got.Hour() != 14 || got.Minute() != 45 || got.Second() != 30 {
		t.Errorf("the time of day became %s", got.Format("15:04:05"))
	}
}

// TestQuarterlyAndAnnualIntervals: the count multiplies the interval, so "every three
// months" is one plan rather than a special case.
func TestQuarterlyAndAnnualIntervals(t *testing.T) {
	from := day(2026, time.January, 31)

	if got := subscription.AddInterval(from, subscription.IntervalMonth, 3); !got.Equal(day(2026, time.April, 30)) {
		t.Errorf("quarterly from 31 Jan = %s, want 30 Apr", got.Format("2 Jan 2006"))
	}
	if got := subscription.AddInterval(from, subscription.IntervalYear, 1); !got.Equal(day(2027, time.January, 31)) {
		t.Errorf("annually from 31 Jan = %s, want 31 Jan next year", got.Format("2 Jan 2006"))
	}
	// A leap day anniversary clamps too.
	if got := subscription.AddInterval(day(2028, time.February, 29), subscription.IntervalYear, 1); !got.Equal(day(2029, time.February, 28)) {
		t.Errorf("29 Feb 2028 + 1 year = %s, want 28 Feb", got.Format("2 Jan 2006"))
	}
}

// TestDayAndWeekIntervals.
func TestDayAndWeekIntervals(t *testing.T) {
	from := day(2026, time.March, 15)

	if got := subscription.AddInterval(from, subscription.IntervalDay, 10); !got.Equal(day(2026, time.March, 25)) {
		t.Errorf("ten days = %s", got.Format("2 Jan 2006"))
	}
	if got := subscription.AddInterval(from, subscription.IntervalWeek, 2); !got.Equal(day(2026, time.March, 29)) {
		t.Errorf("two weeks = %s", got.Format("2 Jan 2006"))
	}
	// A week crosses a month boundary without any clamping.
	if got := subscription.AddInterval(day(2026, time.January, 31), subscription.IntervalWeek, 1); !got.Equal(day(2026, time.February, 7)) {
		t.Errorf("a week from 31 Jan = %s", got.Format("2 Jan 2006"))
	}
}

// TestAnUnknownIntervalDoesNotAdvance: billing a plan on a guessed schedule is worse
// than not billing it.
func TestAnUnknownIntervalDoesNotAdvance(t *testing.T) {
	from := day(2026, time.March, 15)
	if got := subscription.AddInterval(from, subscription.Interval("fortnight"), 1); !got.Equal(from) {
		t.Errorf("an unknown interval advanced to %s", got.Format("2 Jan 2006"))
	}
	if got := subscription.AddInterval(from, subscription.IntervalMonth, 0); !got.Equal(from) {
		t.Errorf("a zero count advanced to %s", got.Format("2 Jan 2006"))
	}
}

// TestPeriodLengths.
func TestPeriodLengths(t *testing.T) {
	period := subscription.Period{
		Start: day(2026, time.January, 15),
		End:   day(2026, time.February, 15),
	}
	if got := period.Days(); got != 31 {
		t.Errorf("the period is %d days, want 31", got)
	}
}

// TestTrialPeriod: the trial is the first period and the first charge is at its end.
func TestTrialPeriod(t *testing.T) {
	now := day(2026, time.March, 1)

	withTrial := subscription.TrialPeriod(now, 14, subscription.IntervalMonth, 1)
	if !withTrial.End.Equal(day(2026, time.March, 15)) {
		t.Errorf("a fourteen-day trial ends %s", withTrial.End.Format("2 Jan 2006"))
	}
	if !withTrial.Start.Equal(now) {
		t.Error("the trial does not start now")
	}

	without := subscription.TrialPeriod(now, 0, subscription.IntervalMonth, 1)
	if !without.End.Equal(day(2026, time.April, 1)) {
		t.Errorf("no trial ends %s, want one month", without.End.Format("2 Jan 2006"))
	}
}

// ---------------------------------------------------------------------------
// Proration
// ---------------------------------------------------------------------------

// TestProrationCreditsAndChargesTheRemainingDays.
func TestProrationCreditsAndChargesTheRemainingDays(t *testing.T) {
	period := subscription.Period{
		Start: day(2026, time.March, 1),
		End:   day(2026, time.April, 1), // 31 days
	}

	// Half way through, upgrading from 1000 to 2000.
	now := period.Start.AddDate(0, 0, 15)
	result := subscription.Prorate(1000, 2000, period, now)

	if result.TotalDays != 31 {
		t.Fatalf("the period is %d days, want 31", result.TotalDays)
	}
	// Sixteen days remaining out of thirty-one.
	if result.RemainingDays != 16 {
		t.Errorf("%d days remain, want 16", result.RemainingDays)
	}

	wantCredit := int64(1000 * 16 / 31) // 516
	wantCharge := int64(2000 * 16 / 31) // 1032
	if result.Credit != wantCredit {
		t.Errorf("the credit is %d, want %d", result.Credit, wantCredit)
	}
	if result.Charge != wantCharge {
		t.Errorf("the charge is %d, want %d", result.Charge, wantCharge)
	}
	if result.Due != wantCharge-wantCredit {
		t.Errorf("the amount due is %d, want %d", result.Due, wantCharge-wantCredit)
	}
}

// TestADowngradeIsOwedTimeRatherThanMoney: a negative amount due means the account is
// owed, and the caller schedules the change for the next cycle instead of refunding.
func TestADowngradeIsOwedTimeRatherThanMoney(t *testing.T) {
	period := subscription.Period{
		Start: day(2026, time.March, 1),
		End:   day(2026, time.April, 1),
	}
	now := period.Start.AddDate(0, 0, 15)

	result := subscription.Prorate(2000, 1000, period, now)
	if result.Due >= 0 {
		t.Errorf("a downgrade is due %d, want a negative amount", result.Due)
	}
}

// TestProrationAtTheEdges: a change at the very start charges nearly the whole period,
// and one at the end charges nearly nothing -- but not nothing.
func TestProrationAtTheEdges(t *testing.T) {
	period := subscription.Period{
		Start: day(2026, time.March, 1),
		End:   day(2026, time.April, 1),
	}

	atStart := subscription.Prorate(1000, 2000, period, period.Start)
	if atStart.RemainingDays != 31 {
		t.Errorf("%d days remain at the start, want 31", atStart.RemainingDays)
	}
	if atStart.Due != 1000 {
		t.Errorf("the amount due at the start is %d, want the full difference", atStart.Due)
	}

	// An hour before the end still has a day remaining, because a part-day is not
	// free.
	almost := subscription.Prorate(1000, 2000, period, period.End.Add(-time.Hour))
	if almost.RemainingDays != 1 {
		t.Errorf("%d days remain an hour before the end, want 1", almost.RemainingDays)
	}
	if almost.Due <= 0 {
		t.Error("a change in the last hour was free")
	}

	// Past the end there is nothing to prorate.
	after := subscription.Prorate(1000, 2000, period, period.End.Add(time.Hour))
	if after.Due != 0 || after.RemainingDays != 0 {
		t.Errorf("a change after the period ended is due %d over %d days", after.Due, after.RemainingDays)
	}
}

// TestTheCreditAndTheChargeAddUp: they are computed from whole amounts so that the
// two figures shown to an operator agree with what is charged. A percentage of a
// period and a percentage of a price do not necessarily agree to the unit.
func TestTheCreditAndTheChargeAddUp(t *testing.T) {
	period := subscription.Period{
		Start: day(2026, time.March, 1),
		End:   day(2026, time.April, 1),
	}

	for offset := 1; offset < 31; offset++ {
		now := period.Start.AddDate(0, 0, offset)
		result := subscription.Prorate(1999, 4999, period, now)

		if result.Due != result.Charge-result.Credit {
			t.Errorf("%d days in: due %d but charge %d minus credit %d",
				offset, result.Due, result.Charge, result.Credit)
		}
		if result.Credit < 0 || result.Charge < 0 {
			t.Errorf("%d days in: a negative figure, credit %d charge %d",
				offset, result.Credit, result.Charge)
		}
	}
}

// TestPauseCreditCountsWholeDaysOnly: crediting an hour would let a subscriber extend
// indefinitely by pausing repeatedly.
func TestPauseCreditCountsWholeDaysOnly(t *testing.T) {
	base := day(2026, time.March, 1)

	cases := []struct {
		resumed time.Time
		want    int
	}{
		{base.Add(30 * time.Minute), 0},
		{base.Add(23 * time.Hour), 0},
		{base.Add(24 * time.Hour), 1},
		{base.Add(47 * time.Hour), 1},
		{base.Add(10 * 24 * time.Hour), 10},
	}

	for _, tc := range cases {
		if got := subscription.PauseCredit(base, tc.resumed); got != tc.want {
			t.Errorf("a pause of %v credits %d days, want %d", tc.resumed.Sub(base), got, tc.want)
		}
	}

	// Nonsense inputs credit nothing rather than a negative amount.
	if got := subscription.PauseCredit(base, base.Add(-time.Hour)); got != 0 {
		t.Errorf("a resume before the pause credited %d", got)
	}
	if got := subscription.PauseCredit(time.Time{}, base); got != 0 {
		t.Errorf("a missing pause credited %d", got)
	}
}

// TestDunningBacksOffAndGivesUp: the budget is bounded, and when it is spent the
// subscription lapses rather than being cancelled -- a cancellation is an intent the
// customer expresses, not one the system infers from a decline.
func TestDunningBacksOffAndGivesUp(t *testing.T) {
	policy := subscription.Dunning{MaxAttempts: 3, Backoff: 24 * time.Hour}
	last := day(2026, time.March, 1)

	// The first retry waits one day.
	next, ok := policy.NextAttempt(0, last)
	if !ok || !next.Equal(last.Add(24*time.Hour)) {
		t.Errorf("the first retry is at %v (%v)", next, ok)
	}
	// The second waits two.
	next, ok = policy.NextAttempt(1, last)
	if !ok || !next.Equal(last.Add(48*time.Hour)) {
		t.Errorf("the second retry is at %v (%v)", next, ok)
	}
	// The third waits four.
	next, ok = policy.NextAttempt(2, last)
	if !ok || !next.Equal(last.Add(96*time.Hour)) {
		t.Errorf("the third retry is at %v (%v)", next, ok)
	}
	// And then there are no more.
	if _, ok := policy.NextAttempt(3, last); ok {
		t.Error("a fourth attempt was offered past the budget")
	}

	// The backoff is capped, so a long chain does not become a task that looks stuck.
	long := subscription.Dunning{MaxAttempts: 40, Backoff: 24 * time.Hour}
	next, ok = long.NextAttempt(20, last)
	if !ok {
		t.Fatal("no attempt was offered")
	}
	if next.Sub(last) > 8*24*time.Hour {
		t.Errorf("the backoff grew to %v, which exceeds the cap", next.Sub(last))
	}

	// A policy with no attempts falls back to the default rather than giving up
	// immediately.
	if _, ok := (subscription.Dunning{}).NextAttempt(0, last); !ok {
		t.Error("an empty policy offered no attempt at all")
	}
}
