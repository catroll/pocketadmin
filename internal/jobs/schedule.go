// Package jobs holds the scheduled-task engine.
//
// Task handlers are registered in code and the catalogue is fixed at build time:
// an operator chooses when a task runs and whether it runs at all, but cannot
// introduce behaviour. A scheduled row whose code has no registered handler is
// inert rather than an error, so a migration can add a task without an older
// binary refusing to start.
package jobs

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// fieldBounds are the inclusive ranges of the five cron fields.
type fieldBounds struct{ min, max int }

var cronFields = []fieldBounds{
	{0, 59}, // minute
	{0, 23}, // hour
	{1, 31}, // day of month
	{1, 12}, // month
	{0, 6},  // day of week, 0 is Sunday
}

// monthNames and dayNames are accepted in place of numbers, which is what every
// cron implementation does and what operators write.
var monthNames = map[string]int{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
}

var dayNames = map[string]int{
	"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
}

// Schedule is a parsed five-field cron expression.
type Schedule struct {
	// expression is kept for display and for storing back into a row.
	expression string

	minute, hour, dayOfMonth, month, dayOfWeek map[int]bool

	// domRestricted and dowRestricted record whether the caller narrowed the day
	// fields, which decides the matching rule below.
	domRestricted bool
	dowRestricted bool
}

// Expression returns the original text.
func (s Schedule) Expression() string { return s.expression }

// ParseSchedule parses a five-field cron expression.
//
// The fields are minute, hour, day of month, month and day of week. Each accepts
// a value, a range, a step, a list of any of those, and a name where one exists.
// Six-field expressions with a seconds field are rejected rather than
// misinterpreted: the leading field would silently be read as minutes and the
// task would run on the wrong schedule.
func ParseSchedule(expression string) (Schedule, error) {
	s := Schedule{expression: strings.TrimSpace(expression)}

	fields := strings.Fields(s.expression)
	if len(fields) != 5 {
		return Schedule{}, fmt.Errorf(
			"a cron expression needs five fields (minute hour day-of-month month day-of-week), got %d",
			len(fields))
	}

	parsed := make([]map[int]bool, len(fields))
	for i, field := range fields {
		values, err := parseCronField(field, i)
		if err != nil {
			return Schedule{}, err
		}
		parsed[i] = values
	}

	s.minute, s.hour, s.dayOfMonth, s.month, s.dayOfWeek =
		parsed[0], parsed[1], parsed[2], parsed[3], parsed[4]

	s.domRestricted = strings.TrimSpace(fields[2]) != "*"
	s.dowRestricted = strings.TrimSpace(fields[4]) != "*"

	return s, nil
}

// parseCronField parses one field into the set of values it matches.
func parseCronField(field string, index int) (map[int]bool, error) {
	bounds := cronFields[index]
	values := map[int]bool{}

	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("empty item in the %s field", cronFieldName(index))
		}

		step := 1
		if slash := strings.IndexByte(part, '/'); slash >= 0 {
			rawStep := part[slash+1:]
			part = part[:slash]

			parsed, err := strconv.Atoi(rawStep)
			if err != nil || parsed < 1 {
				return nil, fmt.Errorf("the step %q in the %s field is not a positive number",
					rawStep, cronFieldName(index))
			}
			step = parsed
		}

		start, end, err := cronRange(part, index, bounds)
		if err != nil {
			return nil, err
		}

		for v := start; v <= end; v += step {
			values[v] = true
		}
	}

	if len(values) == 0 {
		return nil, fmt.Errorf("the %s field matches nothing", cronFieldName(index))
	}
	return values, nil
}

// cronRange resolves the "a", "a-b" or "*" part of a field into bounds.
func cronRange(part string, index int, bounds fieldBounds) (int, int, error) {
	if part == "*" || part == "" {
		return bounds.min, bounds.max, nil
	}

	if dash := strings.IndexByte(part, '-'); dash > 0 {
		start, err := cronValue(part[:dash], index, bounds)
		if err != nil {
			return 0, 0, err
		}
		end, err := cronValue(part[dash+1:], index, bounds)
		if err != nil {
			return 0, 0, err
		}
		if start > end {
			return 0, 0, fmt.Errorf("the range %q in the %s field starts after it ends",
				part, cronFieldName(index))
		}
		return start, end, nil
	}

	value, err := cronValue(part, index, bounds)
	if err != nil {
		return 0, 0, err
	}
	return value, value, nil
}

// cronValue resolves one value, accepting a name for months and weekdays.
func cronValue(raw string, index int, bounds fieldBounds) (int, error) {
	raw = strings.TrimSpace(raw)

	if index == 3 {
		if v, ok := monthNames[strings.ToLower(raw)]; ok {
			return v, nil
		}
	}
	if index == 4 {
		if v, ok := dayNames[strings.ToLower(raw)]; ok {
			return v, nil
		}
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number in the %s field", raw, cronFieldName(index))
	}
	if value < bounds.min || value > bounds.max {
		return 0, fmt.Errorf("%d is outside %d-%d in the %s field",
			value, bounds.min, bounds.max, cronFieldName(index))
	}
	return value, nil
}

// cronFieldName names a field for an error message.
func cronFieldName(index int) string {
	switch index {
	case 0:
		return "minute"
	case 1:
		return "hour"
	case 2:
		return "day-of-month"
	case 3:
		return "month"
	default:
		return "day-of-week"
	}
}

// maxSearchDays bounds the search for the next occurrence.
//
// The longest legitimate gap is a leap day, which occurs at most every four years;
// five years of days is comfortably beyond that and keeps a malformed expression
// such as "0 0 30 2 *" from looping forever.
const maxSearchDays = 366 * 5

// Next returns the first occurrence strictly after the given time.
//
// The second result is false when the expression can never match again, which
// happens for a date that does not exist in any year, such as 30 February.
func (s Schedule) Next(after time.Time) (time.Time, bool) {
	loc := after.Location()

	// Start at the next whole minute: an occurrence at the current minute is in
	// the past, and returning it would run a task twice.
	candidate := after.Truncate(time.Minute).Add(time.Minute)

	for day := 0; day < maxSearchDays; day++ {
		if s.month[int(candidate.Month())] && s.dayMatches(candidate) {
			// On the first day only times at or after the candidate are eligible;
			// on later days the whole day is.
			startHour, startMinute := 0, 0
			if day == 0 {
				startHour, startMinute = candidate.Hour(), candidate.Minute()
			}

			if at, ok := s.firstTimeOn(candidate, startHour, startMinute); ok {
				return at, true
			}
		}

		// Advance to the first minute of the next day. Reconstructing the date
		// rather than adding 24 hours keeps this correct across a daylight-saving
		// change, where a day is not 24 hours long.
		candidate = time.Date(candidate.Year(), candidate.Month(), candidate.Day(),
			0, 0, 0, 0, loc).AddDate(0, 0, 1)
	}

	return time.Time{}, false
}

// dayMatches applies the day-of-month and day-of-week rule.
//
// When both fields are restricted the match is a union, not an intersection. That
// is the historical Vixie cron behaviour and the thing every hand-written cron
// evaluator gets wrong: "0 0 1 * 1" means the first of the month *or* any Monday,
// not the first of the month when it is a Monday.
func (s Schedule) dayMatches(t time.Time) bool {
	domMatch := s.dayOfMonth[t.Day()]
	dowMatch := s.dayOfWeek[int(t.Weekday())]

	switch {
	case s.domRestricted && s.dowRestricted:
		return domMatch || dowMatch
	case s.domRestricted:
		return domMatch
	case s.dowRestricted:
		return dowMatch
	default:
		return true
	}
}

// firstTimeOn returns the earliest matching time on a day, at or after the given
// hour and minute.
func (s Schedule) firstTimeOn(day time.Time, fromHour, fromMinute int) (time.Time, bool) {
	hours := sortedKeys(s.hour)

	for _, hour := range hours {
		if hour < fromHour {
			continue
		}

		minutes := sortedKeys(s.minute)
		for _, minute := range minutes {
			if hour == fromHour && minute < fromMinute {
				continue
			}
			return time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, day.Location()), true
		}
	}

	return time.Time{}, false
}

// sortedKeys returns a set's members in ascending order.
func sortedKeys(set map[int]bool) []int {
	out := make([]int, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Ints(out)
	return out
}

// ValidExpression reports whether a cron expression parses.
func ValidExpression(expression string) error {
	_, err := ParseSchedule(expression)
	return err
}
