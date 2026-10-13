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

// fieldBounds are the inclusive ranges of a cron field.
type fieldBounds struct{ min, max int }

// cronFields are the five fields of the standard expression, in order.
var cronFields = []fieldBounds{
	{0, 59}, // minute
	{0, 23}, // hour
	{1, 31}, // day of month
	{1, 12}, // month
	{0, 6},  // day of week, 0 is Sunday
}

// secondsField is the extra leading field of a six-field expression.
var secondsField = fieldBounds{0, 59}

// monthNames and dayNames are accepted in place of numbers, which is what every
// cron implementation does and what operators write.
var monthNames = map[string]int{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
}

var dayNames = map[string]int{
	"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
}

// Schedule is a parsed cron expression of five or six fields.
type Schedule struct {
	// expression is kept for display and for storing back into a row.
	expression string

	// hasSeconds records whether the expression carried a seconds field. It decides
	// the granularity of the search below, and whether a second set exists at all.
	hasSeconds bool

	second, minute, hour, dayOfMonth, month, dayOfWeek map[int]bool

	// domRestricted and dowRestricted record whether the caller narrowed the day
	// fields, which decides the matching rule below.
	domRestricted bool
	dowRestricted bool
}

// Expression returns the original text.
func (s Schedule) Expression() string { return s.expression }

// ParseSchedule parses a cron expression of five or six fields.
//
// Five fields are the familiar minute, hour, day of month, month, day of week. Six
// add a **leading** seconds field, which is the convention Quartz and Spring use.
//
// The two shapes are distinguished by the count, and that is the only safe way to do
// it: a six-field expression read as five would take the seconds field for minutes
// and run a task on a schedule nobody asked for. There is no ambiguity because a valid
// expression has exactly five fields or exactly six, and anything else is refused.
//
// Each field accepts a value, a range, a step, a list of any of those, and a name
// where one exists.
func ParseSchedule(expression string) (Schedule, error) {
	s := Schedule{expression: strings.TrimSpace(expression)}

	fields := strings.Fields(s.expression)

	var bounds []fieldBounds
	switch len(fields) {
	case 5:
		bounds = cronFields
	case 6:
		s.hasSeconds = true
		bounds = append([]fieldBounds{secondsField}, cronFields...)
	default:
		return Schedule{}, fmt.Errorf(
			"a cron expression needs five fields (minute hour day-of-month month day-of-week) "+
				"or six with a leading seconds field, got %d", len(fields))
	}

	parsed := make([]map[int]bool, len(fields))
	for i, field := range fields {
		values, err := parseCronField(field, i, bounds)
		if err != nil {
			return Schedule{}, err
		}
		parsed[i] = values
	}

	if s.hasSeconds {
		s.second = parsed[0]
		parsed = parsed[1:]
	}

	s.minute, s.hour, s.dayOfMonth, s.month, s.dayOfWeek =
		parsed[0], parsed[1], parsed[2], parsed[3], parsed[4]

	// The day fields are the third and fifth of the standard shape, which the offset
	// above has already restored.
	s.domRestricted = strings.TrimSpace(fields[len(fields)-3]) != "*"
	s.dowRestricted = strings.TrimSpace(fields[len(fields)-1]) != "*"

	return s, nil
}

// HasSeconds reports whether the expression carries a seconds field.
func (s Schedule) HasSeconds() bool { return s.hasSeconds }

// parseCronField parses one field into the set of values it matches.
func parseCronField(field string, index int, bounds []fieldBounds) (map[int]bool, error) {
	limits := bounds[index]
	// The name is resolved once, from the field's position in the whole expression,
	// and passed down. Every error below then names the field the operator actually
	// typed into, which an index alone cannot do once a seconds field shifts the rest.
	name := cronFieldName(index, len(bounds))
	values := map[int]bool{}

	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("empty item in the %s field", name)
		}

		step := 1
		if slash := strings.IndexByte(part, '/'); slash >= 0 {
			rawStep := part[slash+1:]
			part = part[:slash]

			parsed, err := strconv.Atoi(rawStep)
			if err != nil || parsed < 1 {
				return nil, fmt.Errorf("the step %q in the %s field is not a positive number",
					rawStep, name)
			}
			step = parsed
		}

		start, end, err := cronRange(part, name, limits)
		if err != nil {
			return nil, err
		}

		for v := start; v <= end; v += step {
			values[v] = true
		}
	}

	if len(values) == 0 {
		return nil, fmt.Errorf("the %s field matches nothing", name)
	}
	return values, nil
}

// cronRange resolves the "a", "a-b" or "*" part of a field into bounds.
func cronRange(part, name string, bounds fieldBounds) (int, int, error) {
	if part == "*" || part == "" {
		return bounds.min, bounds.max, nil
	}

	if dash := strings.IndexByte(part, '-'); dash > 0 {
		start, err := cronValue(part[:dash], name, bounds)
		if err != nil {
			return 0, 0, err
		}
		end, err := cronValue(part[dash+1:], name, bounds)
		if err != nil {
			return 0, 0, err
		}
		if start > end {
			return 0, 0, fmt.Errorf("the range %q in the %s field starts after it ends", part, name)
		}
		return start, end, nil
	}

	value, err := cronValue(part, name, bounds)
	if err != nil {
		return 0, 0, err
	}
	return value, value, nil
}

// cronValue resolves one value, accepting a name for months and weekdays.
func cronValue(raw, name string, bounds fieldBounds) (int, error) {
	raw = strings.TrimSpace(raw)

	// The names are resolved by field name rather than by position. Position was
	// correct while every expression had five fields, and a seconds field moves the
	// month and the day of the week by one -- so an index-based lookup would have
	// started accepting "jan" in the hour field and rejecting it in the month.
	switch name {
	case "month":
		if v, ok := monthNames[strings.ToLower(raw)]; ok {
			return v, nil
		}
	case "day-of-week":
		if v, ok := dayNames[strings.ToLower(raw)]; ok {
			return v, nil
		}
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number in the %s field", raw, name)
	}
	if value < bounds.min || value > bounds.max {
		return 0, fmt.Errorf("%d is outside %d-%d in the %s field", value, bounds.min, bounds.max, name)
	}
	return value, nil
}

// cronFieldName names a field for an error message.
func cronFieldName(index, fieldCount int) string {
	// With six fields the seconds field shifts everything by one, so the name is
	// derived from the distance to the end rather than from the index. A name that
	// disagreed with the position would send an operator to the wrong field.
	if fieldCount == 6 {
		switch index {
		case 0:
			return "second"
		case 1:
			return "minute"
		case 2:
			return "hour"
		case 3:
			return "day-of-month"
		case 4:
			return "month"
		default:
			return "day-of-week"
		}
	}

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

	// Start at the next whole unit: an occurrence at the current minute -- or second,
	// when the expression has one -- is in the past, and returning it would run a task
	// twice.
	//
	// The granularity of the search follows the expression. A five-field expression
	// has no opinion about seconds, so its occurrences are on the minute and scanning
	// by second would be 60 times the work for the same answer.
	candidate := after.Truncate(time.Minute).Add(time.Minute)
	if s.hasSeconds {
		candidate = after.Truncate(time.Second).Add(time.Second)
	}

	for day := 0; day < maxSearchDays; day++ {
		if s.month[int(candidate.Month())] && s.dayMatches(candidate) {
			// On the first day only times at or after the candidate are eligible;
			// on later days the whole day is.
			startHour, startMinute, startSecond := 0, 0, 0
			if day == 0 {
				startHour, startMinute, startSecond = candidate.Hour(), candidate.Minute(), candidate.Second()
			}

			if at, ok := s.firstTimeOn(candidate, startHour, startMinute, startSecond); ok {
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
// firstTimeOn returns the first matching time on a day at or after the given position.
//
// A matching day always yields a time: the hour, minute and second sets are non-empty
// because the parser refuses a field that matches nothing. That is what bounds the
// work -- the caller never scans a whole day looking for something that is not there.
func (s Schedule) firstTimeOn(day time.Time, fromHour, fromMinute, fromSecond int) (time.Time, bool) {
	// An expression without a seconds field occurs at second zero, which is the only
	// value its (absent) set could contain.
	seconds := []int{0}
	if s.hasSeconds {
		seconds = sortedKeys(s.second)
	}

	for _, hour := range sortedKeys(s.hour) {
		if hour < fromHour {
			continue
		}

		for _, minute := range sortedKeys(s.minute) {
			if hour == fromHour && minute < fromMinute {
				continue
			}

			for _, second := range seconds {
				if hour == fromHour && minute == fromMinute && second < fromSecond {
					continue
				}
				return time.Date(day.Year(), day.Month(), day.Day(), hour, minute, second, 0,
					day.Location()), true
			}
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
