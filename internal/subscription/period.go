// Package subscription holds recurring billing: plans, the subscriptions themselves
// and their cycles.
//
// The arithmetic in this package is date arithmetic rather than money arithmetic,
// and it has its own way of being wrong. Adding a month to the thirty-first of
// January is not the third of March, and a billing system that says it is charges
// people on the wrong day for the rest of their subscription.
package subscription

import (
	"time"
)

// Interval is how often a plan bills.
type Interval string

// Intervals.
const (
	IntervalDay   Interval = "day"
	IntervalWeek  Interval = "week"
	IntervalMonth Interval = "month"
	IntervalYear  Interval = "year"
)

// Intervals returns the vocabulary, matched by the migration's stored select.
func Intervals() []string {
	return []string{string(IntervalDay), string(IntervalWeek), string(IntervalMonth), string(IntervalYear)}
}

// Statuses.
const (
	StatusTrialing  = "trialing"
	StatusActive    = "active"
	StatusPastDue   = "past_due"
	StatusPaused    = "paused"
	StatusCancelled = "cancelled"
	StatusExpired   = "expired"
)

// Cycle statuses.
const (
	CyclePending  = "pending"
	CyclePaid     = "paid"
	CycleFailed   = "failed"
	CycleSkipped  = "skipped"
	CycleRefunded = "refunded"
)

// AddInterval advances a time by a number of intervals.
//
// Months and years clamp to the last day of the target month. Go's AddDate does not:
// it normalises, so the thirty-first of January plus one month is the third of March,
// and a subscriber who signed up on the thirty-first would be billed three days late
// for every February afterwards. Clamping is what every billing system does and what
// a customer expects -- the same day of the month, or the last one that exists.
func AddInterval(from time.Time, interval Interval, count int) time.Time {
	if count == 0 {
		return from
	}

	switch interval {
	case IntervalDay:
		return from.AddDate(0, 0, count)

	case IntervalWeek:
		return from.AddDate(0, 0, 7*count)

	case IntervalMonth:
		return addMonths(from, count)

	case IntervalYear:
		return addMonths(from, 12*count)

	default:
		// An unknown interval does not advance rather than advancing by something
		// arbitrary. A caller that reaches here has a plan whose interval the code
		// does not understand, and billing it on a guessed schedule is worse than
		// not billing it.
		return from
	}
}

// addMonths moves a date by whole months, clamping the day.
func addMonths(from time.Time, months int) time.Time {
	// The target month, computed on the first of the month so the day cannot
	// overflow while the month is being worked out.
	firstOfTarget := time.Date(from.Year(), from.Month(), 1, from.Hour(), from.Minute(),
		from.Second(), from.Nanosecond(), from.Location()).AddDate(0, months, 0)

	day := from.Day()
	if last := daysInMonth(firstOfTarget.Year(), firstOfTarget.Month(), from.Location()); day > last {
		day = last
	}

	return time.Date(firstOfTarget.Year(), firstOfTarget.Month(), day,
		from.Hour(), from.Minute(), from.Second(), from.Nanosecond(), from.Location())
}

// daysInMonth returns how many days a month has.
//
// The day before the first of the following month is the last of this one, which
// handles leap years without a table.
func daysInMonth(year int, month time.Month, loc *time.Location) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, loc).Day()
}

// Period is one billing period.
type Period struct {
	Start time.Time
	End   time.Time
}

// NextPeriod returns the period that follows one.
func NextPeriod(current Period, interval Interval, count int) Period {
	return Period{Start: current.End, End: AddInterval(current.End, interval, count)}
}

// Days returns how long a period is, rounded to whole days.
func (p Period) Days() int {
	return int(p.End.Sub(p.Start).Hours() / 24)
}

// TrialPeriod returns the initial period for a subscription with a trial.
//
// The trial is the first period and it ends after the given number of days; the
// first charge is at its end. A trial of zero days means the subscription starts on
// its billing cycle.
func TrialPeriod(now time.Time, trialDays int, interval Interval, count int) Period {
	if trialDays > 0 {
		end := now.AddDate(0, 0, trialDays)
		return Period{Start: now, End: end}
	}
	return Period{Start: now, End: AddInterval(now, interval, count)}
}

// ---------------------------------------------------------------------------
// Proration
// ---------------------------------------------------------------------------

// Proration is what a mid-cycle change costs.
type Proration struct {
	// Credit is what the unused part of the current period is worth.
	Credit int64

	// Charge is what the remaining part of the new plan costs.
	Charge int64

	// Due is the difference, which is negative when the change is a downgrade and
	// the account is owed time rather than money.
	Due int64

	// RemainingDays is how much of the current period is left.
	RemainingDays int

	// TotalDays is how long the current period was.
	TotalDays int
}

// Prorate works out the cost of changing plan in the middle of a period.
//
// The credit and the charge are computed as whole amounts from the daily rate, rather
// than as a percentage of the period, so the two figures shown to an operator add up
// to the amount actually charged. A percentage of a period and a percentage of a
// price do not necessarily agree to the unit, and the difference has to go somewhere.
//
// A downgrade produces a negative Due: the account is owed time, and the caller
// schedules the change for the end of the period rather than refunding. Refunding a
// fraction of a period is a decision about money, and the specification puts the
// downgrade at the next cycle.
func Prorate(currentPrice, newPrice int64, period Period, now time.Time) Proration {
	out := Proration{TotalDays: period.Days()}

	if period.End.Before(now) || out.TotalDays <= 0 {
		// The period is over, so there is nothing left to credit or to charge.
		return out
	}

	remaining := int(period.End.Sub(now).Hours() / 24)
	// Round up a part-day, so a change made in the last hours of a period does not
	// produce a free day.
	if period.End.Sub(now).Hours()-float64(remaining)*24 > 0 {
		remaining++
	}
	if remaining > out.TotalDays {
		remaining = out.TotalDays
	}
	out.RemainingDays = remaining

	if remaining <= 0 {
		return out
	}

	// Integer arithmetic throughout, rounded once per figure.
	out.Credit = mulDiv(currentPrice, int64(remaining), int64(out.TotalDays))
	out.Charge = mulDiv(newPrice, int64(remaining), int64(out.TotalDays))
	out.Due = out.Charge - out.Credit

	return out
}

// mulDiv returns a*b/c with the product widened so it cannot overflow.
func mulDiv(a, b, c int64) int64 {
	if c == 0 {
		return 0
	}
	whole := a / c
	remainder := a % c

	result := whole*b + remainder*b/c
	if (remainder*b)%c*2 >= c {
		result++
	}
	return result
}

// PauseCredit is how many whole days a pause has lasted.
//
// Only whole days are credited. A pause of an hour is not a day of service the
// account did not receive, and crediting it would let a subscriber extend their
// subscription indefinitely by pausing repeatedly.
func PauseCredit(pausedAt, resumedAt time.Time) int {
	if pausedAt.IsZero() || resumedAt.IsZero() || resumedAt.Before(pausedAt) {
		return 0
	}
	return int(resumedAt.Sub(pausedAt).Hours() / 24)
}
