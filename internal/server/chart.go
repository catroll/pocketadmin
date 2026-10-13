package server

import (
	"encoding/json"
	"fmt"
	"time"

	"pocketadmin/internal/monitoring"
)

// chartPayload is what the browser's renderer reads.
//
// It is built here rather than in the template so the shape is one thing a test can
// assert on, and so the labels are formatted once. Chart.js accepts far more than this;
// the fields below are the ones a line chart actually needs.
type chartPayload struct {
	Labels []string   `json:"labels"`
	Values []*float64 `json:"values"`
	Unit   string     `json:"unit"`
	Kind   string     `json:"kind"`

	// Max is the top of the axis when the series has a natural ceiling, such as a
	// percentage. Absent means Chart.js chooses, which is right for a load average.
	Max *float64 `json:"max,omitempty"`
}

// chartJSON renders a series for the renderer.
//
// A bucket with no reading becomes a null rather than a zero, so the line breaks across
// an outage. Drawing a machine that was down for an hour as a flat line at zero would
// say it was idle, which is a different thing from absent.
func chartJSON(points []monitoring.Point, window monitoring.Window, unit, kind string) string {
	payload := chartPayload{
		Labels: make([]string, 0, len(points)),
		Values: make([]*float64, 0, len(points)),
		Unit:   unit,
		Kind:   kind,
	}

	for _, point := range points {
		payload.Labels = append(payload.Labels, point.Label(window))
		if point.Count == 0 {
			payload.Values = append(payload.Values, nil)
			continue
		}

		value := point.Value
		payload.Values = append(payload.Values, &value)
	}

	// A percentage has a ceiling, and an axis that stops at the largest reading makes a
	// quiet machine look busy. A load average has none, and forcing one would clip the
	// spike that matters.
	if unit == "%" {
		max := 100.0
		payload.Max = &max
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

// sparklineJSON renders a compact series for a list, with no labels.
//
// It exists so a table row can show a shape without a full chart: a node list with six
// charts per row would be a page nobody can read.
func sparklineJSON(points []monitoring.Point) string {
	values := make([]*float64, 0, len(points))
	for _, point := range points {
		if point.Count == 0 {
			values = append(values, nil)
			continue
		}
		value := point.Value
		values = append(values, &value)
	}

	encoded, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

// sinceLabel renders how long ago something was, for a table cell.
func sinceLabel(at time.Time, now time.Time) string {
	if at.IsZero() {
		return "never"
	}

	age := now.Sub(at)
	switch {
	case age < time.Minute:
		return "just now"
	case age < time.Hour:
		return fmt.Sprintf("%d min ago", int(age.Minutes()))
	case age < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(age.Hours()))
	default:
		return fmt.Sprintf("%d d ago", int(age.Hours()/24))
	}
}
