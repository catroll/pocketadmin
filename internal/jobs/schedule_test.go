package jobs_test

import (
	"testing"
	"time"

	"pocketadmin/internal/jobs"
)

// at builds a time in UTC for a readable expectation.
func at(year int, month time.Month, day, hour, minute int) time.Time {
	return time.Date(year, month, day, hour, minute, 0, 0, time.UTC)
}

// TestNextMatchesKnownExpressions covers the schedules an operator actually
// writes, against times worked out by hand from the calendar.
func TestNextMatchesKnownExpressions(t *testing.T) {
	cases := []struct {
		expression string
		from       time.Time
		want       time.Time
	}{
		// Every minute.
		{"* * * * *", at(2026, time.March, 10, 8, 30), at(2026, time.March, 10, 8, 31)},
		// Every five minutes: the next multiple of five.
		{"*/5 * * * *", at(2026, time.March, 10, 8, 32), at(2026, time.March, 10, 8, 35)},
		// Hourly on the hour.
		{"0 * * * *", at(2026, time.March, 10, 8, 30), at(2026, time.March, 10, 9, 0)},
		// Daily at 03:00.
		{"0 3 * * *", at(2026, time.March, 10, 8, 30), at(2026, time.March, 11, 3, 0)},
		// Daily at 03:00, asked before it happens.
		{"0 3 * * *", at(2026, time.March, 10, 1, 0), at(2026, time.March, 10, 3, 0)},
		// Weekly on Monday at 09:00. 2026-03-10 is a Tuesday.
		{"0 9 * * mon", at(2026, time.March, 10, 8, 0), at(2026, time.March, 16, 9, 0)},
		// The first of the month at midnight.
		{"0 0 1 * *", at(2026, time.March, 10, 8, 0), at(2026, time.April, 1, 0, 0)},
		// Quarterly: January, April, July and October.
		{"0 0 1 */3 *", at(2026, time.March, 10, 8, 0), at(2026, time.April, 1, 0, 0)},
		// A list of hours.
		{"0 6,18 * * *", at(2026, time.March, 10, 8, 0), at(2026, time.March, 10, 18, 0)},
		// A range of hours.
		{"30 9-17 * * *", at(2026, time.March, 10, 8, 0), at(2026, time.March, 10, 9, 30)},
	}

	for _, tc := range cases {
		schedule, err := jobs.ParseSchedule(tc.expression)
		if err != nil {
			t.Errorf("%q did not parse: %v", tc.expression, err)
			continue
		}

		got, ok := schedule.Next(tc.from)
		if !ok {
			t.Errorf("%q found no next time after %s", tc.expression, tc.from)
			continue
		}
		if !got.Equal(tc.want) {
			t.Errorf("%q from %s = %s, want %s", tc.expression, tc.from, got, tc.want)
		}
	}
}

// TestNextIsStrictlyAfter is the property that stops a task running twice.
//
// An occurrence exactly at the given minute is in the past; returning it would
// make a scheduler that ticks every minute run the same job repeatedly.
func TestNextIsStrictlyAfter(t *testing.T) {
	schedule, err := jobs.ParseSchedule("*/5 * * * *")
	if err != nil {
		t.Fatalf("ParseSchedule: %v", err)
	}

	from := at(2026, time.March, 10, 8, 30)
	got, ok := schedule.Next(from)
	if !ok {
		t.Fatal("no next time")
	}
	if !got.After(from) {
		t.Errorf("Next returned %s, which is not after %s", got, from)
	}
	if !got.Equal(at(2026, time.March, 10, 8, 35)) {
		t.Errorf("Next = %s, want 08:35", got)
	}
}

// TestDayOfMonthAndDayOfWeekAreAUnion is the rule every hand-written evaluator
// gets wrong.
//
// "0 0 1 * 1" means the first of the month *or* any Monday, not the first of the
// month when it happens to be a Monday.
func TestDayOfMonthAndDayOfWeekAreAUnion(t *testing.T) {
	schedule, err := jobs.ParseSchedule("0 0 1 * 1")
	if err != nil {
		t.Fatalf("ParseSchedule: %v", err)
	}

	// 2026-03-10 is a Tuesday; the next Monday is the 16th, which is before the
	// first of April. Under an intersection the answer would be a 1st that is a
	// Monday, which does not occur until June 2026.
	got, ok := schedule.Next(at(2026, time.March, 10, 0, 0))
	if !ok {
		t.Fatal("no next time")
	}
	if !got.Equal(at(2026, time.March, 16, 0, 0)) {
		t.Errorf("Next = %s, want the 16th (the next Monday)", got)
	}
}

// TestRestrictingOneDayFieldUsesOnlyThatField: when only one is narrowed the rule
// is that field alone, which is the other half of the same convention.
func TestRestrictingOneDayFieldUsesOnlyThatField(t *testing.T) {
	// Every day, but only in March.
	schedule, err := jobs.ParseSchedule("0 0 * 3 *")
	if err != nil {
		t.Fatalf("ParseSchedule: %v", err)
	}

	got, ok := schedule.Next(at(2026, time.March, 10, 1, 0))
	if !ok {
		t.Fatal("no next time")
	}
	if !got.Equal(at(2026, time.March, 11, 0, 0)) {
		t.Errorf("Next = %s, want the next day", got)
	}

	// And from April it waits until March.
	got, ok = schedule.Next(at(2026, time.April, 1, 0, 0))
	if !ok {
		t.Fatal("no next time")
	}
	if !got.Equal(at(2027, time.March, 1, 0, 0)) {
		t.Errorf("Next = %s, want March next year", got)
	}
}

// TestLeapDayIsFound: the longest legitimate gap between occurrences.
func TestLeapDayIsFound(t *testing.T) {
	schedule, err := jobs.ParseSchedule("0 0 29 2 *")
	if err != nil {
		t.Fatalf("ParseSchedule: %v", err)
	}

	got, ok := schedule.Next(at(2026, time.March, 1, 0, 0))
	if !ok {
		t.Fatal("no next time")
	}
	if !got.Equal(at(2028, time.February, 29, 0, 0)) {
		t.Errorf("Next = %s, want 2028-02-29", got)
	}
}

// TestAnImpossibleDateTerminates: 30 February does not exist, and searching for
// it must stop rather than loop.
func TestAnImpossibleDateTerminates(t *testing.T) {
	schedule, err := jobs.ParseSchedule("0 0 30 2 *")
	if err != nil {
		t.Fatalf("ParseSchedule: %v", err)
	}

	if _, ok := schedule.Next(at(2026, time.January, 1, 0, 0)); ok {
		t.Error("an impossible date reported a next occurrence")
	}
}

// TestParseRejectsMalformedExpressions: validation on write is only useful if it
// rejects what an operator mistyped.
func TestParseRejectsMalformedExpressions(t *testing.T) {
	bad := map[string]string{
		"":                  "empty",
		"* * * *":           "four fields",
		"* * * * * *":       "six fields",
		"60 * * * *":        "minute out of range",
		"* 24 * * *":        "hour out of range",
		"* * 0 * *":         "day of month below one",
		"* * 32 * *":        "day of month above 31",
		"* * * 13 *":        "month above twelve",
		"* * * * 7":         "day of week above six",
		"*/0 * * * *":       "zero step",
		"*/x * * * *":       "non-numeric step",
		"5-1 * * * *":       "reversed range",
		"1,,2 * * * *":      "empty list item",
		"abc * * * *":       "a word in the minute field",
		"* * * * notaday":   "an unknown weekday name",
		"* * * notamonth *": "an unknown month name",
	}

	for expression, why := range bad {
		if _, err := jobs.ParseSchedule(expression); err == nil {
			t.Errorf("%q (%s) was accepted", expression, why)
		}
	}
}

// TestParseAcceptsNames: month and weekday names are what operators write.
func TestParseAcceptsNames(t *testing.T) {
	for _, expression := range []string{
		"0 0 * jan *",
		"0 0 * JAN *",
		"0 9 * * mon",
		"0 9 * * MON",
		"0 9 * * mon-fri",
		"0 0 1 jan,jul *",
	} {
		if _, err := jobs.ParseSchedule(expression); err != nil {
			t.Errorf("%q was rejected: %v", expression, err)
		}
	}
}

// TestZeroIsSundayAndSevenIsRejected: cron's numbering has two conventions and
// only one of them is accepted, deliberately.
//
// Both 0 and 7 mean Sunday in the wild. Accepting 7 would make an expression
// copied from a system where 7 is valid mean something different here, so it is
// refused rather than reinterpreted.
func TestZeroIsSundayAndSevenIsRejected(t *testing.T) {
	schedule, err := jobs.ParseSchedule("0 0 * * 0")
	if err != nil {
		t.Fatalf("zero should be accepted as Sunday: %v", err)
	}

	// 2026-03-15 is a Sunday.
	got, ok := schedule.Next(at(2026, time.March, 10, 0, 0))
	if !ok {
		t.Fatal("no next time")
	}
	if !got.Equal(at(2026, time.March, 15, 0, 0)) {
		t.Errorf("Next = %s, want Sunday the 15th", got)
	}

	if _, err := jobs.ParseSchedule("0 0 * * 7"); err == nil {
		t.Error("seven was accepted as a day of week")
	}
}

// TestScheduleKeepsItsExpression: the text is stored back into the row, so it has
// to survive a round trip unchanged.
func TestScheduleKeepsItsExpression(t *testing.T) {
	const expression = "*/15 2-6 * * mon-fri"

	schedule, err := jobs.ParseSchedule(expression)
	if err != nil {
		t.Fatalf("ParseSchedule: %v", err)
	}
	if schedule.Expression() != expression {
		t.Errorf("Expression = %q, want %q", schedule.Expression(), expression)
	}
}

// TestNextRespectsTheLocation: a daily schedule fires at the local hour, not the
// UTC one, or a task set for midnight would run at the wrong time for everyone
// outside UTC.
func TestNextRespectsTheLocation(t *testing.T) {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skipf("no timezone database: %v", err)
	}

	schedule, err := jobs.ParseSchedule("0 0 * * *")
	if err != nil {
		t.Fatalf("ParseSchedule: %v", err)
	}

	from := time.Date(2026, time.March, 10, 8, 0, 0, 0, shanghai)
	got, ok := schedule.Next(from)
	if !ok {
		t.Fatal("no next time")
	}
	if got.Location().String() != shanghai.String() {
		t.Errorf("the result is in %s, want %s", got.Location(), shanghai)
	}
	if got.Hour() != 0 {
		t.Errorf("the result is at hour %d local, want 0", got.Hour())
	}
}
