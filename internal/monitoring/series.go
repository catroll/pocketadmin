package monitoring

import (
	"fmt"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// Point is one bucket of a chart.
type Point struct {
	// At is the start of the bucket.
	At time.Time

	// Value is the bucket's figure, in whatever unit the series carries.
	Value float64

	// Count is how many samples went into it. A bucket built from one reading is a
	// measurement; one built from sixty is an average, and a chart that does not know
	// the difference cannot say why a spike looks flat.
	Count int
}

// Series is one line on a chart.
type Series struct {
	Name   string
	Unit   string
	Kind   string
	Points []Point
}

// Series kinds, which decide how a reader should read the line.
const (
	// KindAverage is a percentage or a load: the mean over the bucket.
	KindAverage = "average"

	// KindRate is a counter difference over time, in bytes per second.
	KindRate = "rate"

	// KindGauge is an instantaneous total, such as bytes in use.
	KindGauge = "gauge"
)

// Window is a time range and a resolution.
type Window struct {
	From time.Time
	To   time.Time

	// Buckets is how many points the chart gets. The samples inside a bucket are
	// averaged, so a week of minute readings becomes a readable line rather than ten
	// thousand dots the browser has to draw and the eye cannot.
	Buckets int
}

// PresetWindows returns the ranges a screen offers.
//
// The bucket counts are chosen so every range produces a similar number of points: a
// chart with ten thousand points is slow to draw and unreadable, and one with five says
// nothing.
func PresetWindows(now time.Time) []struct {
	Label  string
	Window Window
} {
	return []struct {
		Label  string
		Window Window
	}{
		{"Last hour", Window{From: now.Add(-time.Hour), To: now, Buckets: 60}},
		{"Last 6 hours", Window{From: now.Add(-6 * time.Hour), To: now, Buckets: 72}},
		{"Last 24 hours", Window{From: now.Add(-24 * time.Hour), To: now, Buckets: 96}},
		{"Last 7 days", Window{From: now.AddDate(0, 0, -7), To: now, Buckets: 84}},
	}
}

// WindowFor picks a preset by its label, falling back to the first.
func WindowFor(now time.Time, label string) (Window, string) {
	presets := PresetWindows(now)
	for _, preset := range presets {
		if preset.Label == label {
			return preset.Window, preset.Label
		}
	}
	return presets[0].Window, presets[0].Label
}

// BuildSeries turns a node's samples into chart lines over a window.
//
// The samples are read once and every series is derived from the same read, so the lines
// on a chart are guaranteed to describe the same instants. Reading once per series would
// be several queries and would let a sample arriving between them put the lines out of
// step by one bucket, which is exactly the kind of difference somebody would spend an
// afternoon explaining.
func BuildSeries(app core.App, nodeID string, window Window, now time.Time) ([]Series, error) {
	if window.Buckets <= 0 {
		window.Buckets = 60
	}
	if !window.To.After(window.From) {
		window.To = now
		window.From = now.Add(-time.Hour)
	}

	// The bucket boundaries are aligned to the clock, not to the moment the request
	// arrived. Without this a window beginning at 04:08:37 puts its boundaries on the
	// :37 second and the next request, a second later, puts them on :38 -- so the same
	// reading falls in a different bucket each time the page is reloaded, and a chart
	// that shifts under a reader is a chart nobody can compare with anything.
	window.From = alignDown(window.From, window.To.Sub(window.From)/time.Duration(window.Buckets))

	records, err := app.FindRecordsByFilter(CollectionHostMetrics,
		"node = {:n} && at >= {:from} && at <= {:to}", "at", 5000, 0,
		map[string]any{"n": nodeID, "from": window.From.UTC(), "to": window.To.UTC()})
	if err != nil {
		return nil, err
	}

	buckets := bucketise(records, window)

	// Nothing to draw is not an empty chart. A window with no readings produces no
	// series at all, so the screen can say so rather than rendering six flat lines at
	// zero, which is a claim about the machine rather than about the data.
	if !anyReadings(buckets) {
		return nil, nil
	}

	return []Series{
		{
			Name: "Processor", Unit: "%", Kind: KindAverage,
			Points: gaugePoints(buckets, func(b bucket) float64 {
				if b.cpuTotal <= 0 {
					return 0
				}
				return clampPercent(b.cpuUsed / b.cpuTotal * 100)
			}, func(b bucket) bool { return b.cpuTotal > 0 }),
		},
		{
			Name: "Memory", Unit: "%", Kind: KindAverage,
			Points: gaugePoints(buckets, func(b bucket) float64 {
				if b.memTotal <= 0 {
					return 0
				}
				return clampPercent(float64(b.memUsed) / float64(b.memTotal) * 100)
			}, func(b bucket) bool { return b.memTotal > 0 }),
		},
		{
			Name: "Disk", Unit: "%", Kind: KindAverage,
			Points: gaugePoints(buckets, func(b bucket) float64 {
				if b.diskTotal <= 0 {
					return 0
				}
				return clampPercent(float64(b.diskUsed) / float64(b.diskTotal) * 100)
			}, func(b bucket) bool { return b.diskTotal > 0 }),
		},
		{
			Name: "Load (1m)", Unit: "", Kind: KindAverage,
			// A load average is a sum of readings, so it is divided by the count to
			// become a mean. The percentages above are already ratios of the bucket's
			// own sums and must not be.
			Points: gaugePoints(buckets, func(b bucket) float64 {
				return b.load1 / float64(b.count)
			}, always),
		},
		{
			Name: "Network in", Unit: "B/s", Kind: KindRate,
			Points: ratePoints(buckets, func(b bucket) uint64 { return b.netRx }),
		},
		{
			Name: "Network out", Unit: "B/s", Kind: KindRate,
			Points: ratePoints(buckets, func(b bucket) uint64 { return b.netTx }),
		},
	}, nil
}

// alignDown rounds a time down to a multiple of a width.
//
// It is applied to the window's start so two requests a second apart produce the same
// buckets. A width that does not divide evenly into a day still aligns consistently,
// which is all the chart needs.
func alignDown(at time.Time, width time.Duration) time.Time {
	if width <= 0 {
		return at
	}
	return at.Truncate(width)
}

// anyReadings reports whether any bucket collected a sample.
func anyReadings(buckets []bucket) bool {
	for _, b := range buckets {
		if b.count > 0 {
			return true
		}
	}
	return false
}

// bucket accumulates the samples that fall into one chart point.
type bucket struct {
	at    time.Time
	count int

	cpuUsed, cpuTotal   float64
	load1               float64
	memUsed, memTotal   uint64
	diskUsed, diskTotal uint64
	netRx, netTx        uint64
}

// bucketise groups the samples into the window's buckets.
//
// A bucket that received nothing is still emitted, with a zero count, so the browser
// draws a gap rather than joining the readings either side of an outage into a
// continuous line. A machine that was down for an hour should look like it.
func bucketise(records []*core.Record, window Window) []bucket {
	span := window.To.Sub(window.From)
	width := span / time.Duration(window.Buckets)
	if width <= 0 {
		width = time.Minute
	}

	buckets := make([]bucket, window.Buckets)
	for i := range buckets {
		buckets[i].at = window.From.Add(time.Duration(i) * width)
	}

	for _, rec := range records {
		at := rec.GetDateTime("at").Time()
		index := int(at.Sub(window.From) / width)
		if index < 0 || index >= len(buckets) {
			continue
		}

		b := &buckets[index]
		b.count++
		b.cpuUsed += rec.GetFloat("cpuUsedSeconds")
		b.cpuTotal += rec.GetFloat("cpuTotalSeconds")
		b.load1 += rec.GetFloat("load1")
		b.memUsed += uint64(rec.GetFloat("memUsedBytes"))
		b.memTotal += uint64(rec.GetFloat("memTotalBytes"))
		b.diskUsed += uint64(rec.GetFloat("diskUsedBytes"))
		b.diskTotal += uint64(rec.GetFloat("diskTotalBytes"))

		// The counters are cumulative, so the bucket keeps the newest reading rather
		// than a sum: adding them would be meaningless and the rate below is a
		// difference between adjacent buckets.
		b.netRx = uint64(rec.GetFloat("netRxBytes"))
		b.netTx = uint64(rec.GetFloat("netTxBytes"))
	}

	return buckets
}

// guagePoints renders one figure per bucket.
//
// The caller produces the **mean** itself rather than a total to be divided. A percentage
// is a ratio of the bucket's sums -- `sum(used)/sum(total)` -- and dividing that by the
// sample count divides it twice: a machine at twenty percent would be drawn at six and a
// half, which is a chart that sends somebody looking for a problem that is not there. A
// load average is genuinely additive and the caller divides it by the count instead.
//
// A bucket with no samples yields no point, so the line breaks rather than dropping to
// zero. A machine that stopped reporting did not start using no processor.
func gaugePoints(buckets []bucket, value func(bucket) float64, valid func(bucket) bool) []Point {
	out := make([]Point, 0, len(buckets))

	for _, b := range buckets {
		if b.count == 0 || !valid(b) {
			out = append(out, Point{At: b.at})
			continue
		}
		out = append(out, Point{At: b.at, Value: value(b), Count: b.count})
	}
	return out
}

// ratePoints renders a counter difference per second between adjacent buckets.
//
// The first bucket has nothing before it, so it yields no point. A counter that went
// backwards means the interface was reset, and that bucket yields no point too rather
// than an enormous negative spike.
func ratePoints(buckets []bucket, counter func(bucket) uint64) []Point {
	out := make([]Point, 0, len(buckets))

	for i, b := range buckets {
		if i == 0 || b.count == 0 {
			out = append(out, Point{At: b.at})
			continue
		}

		previous := buckets[i-1]
		if previous.count == 0 {
			out = append(out, Point{At: b.at})
			continue
		}

		seconds := b.at.Sub(previous.at).Seconds()
		newer, older := counter(b), counter(previous)
		if seconds <= 0 || newer < older {
			out = append(out, Point{At: b.at})
			continue
		}

		out = append(out, Point{
			At: b.at, Value: float64(newer-older) / seconds, Count: b.count,
		})
	}
	return out
}

// clampPercent keeps a ratio inside nought to a hundred.
func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

// always accepts every bucket.
func always(bucket) bool { return true }

// Label renders a point's time for a chart axis.
func (p Point) Label(window Window) string {
	if window.To.Sub(window.From) > 48*time.Hour {
		return p.At.Local().Format("Mon 15:04")
	}
	return p.At.Local().Format("15:04")
}

// Summary is a node's headline figures for a list.
type Summary struct {
	CPU     string
	Memory  string
	Disk    string
	Samples int
}

// Describe builds a summary from a node's newest sample.
func Describe(app core.App, nodeID string) Summary {
	records, err := app.FindRecordsByFilter(CollectionHostMetrics,
		"node = {:n}", "-at", 1, 0, map[string]any{"n": nodeID})
	if err != nil || len(records) == 0 {
		return Summary{}
	}

	sample := sampleFrom(records[0])
	summary := Summary{Samples: 1}

	if percent, ok := sample.CPUPercent(); ok {
		summary.CPU = fmt.Sprintf("%.1f%%", percent)
	}
	if percent, ok := sample.MemPercent(); ok {
		summary.Memory = fmt.Sprintf("%.1f%%", percent)
	}
	if percent, ok := sample.DiskPercent(); ok {
		summary.Disk = fmt.Sprintf("%.1f%%", percent)
	}
	return summary
}

// sampleFrom reconstructs a sample from a stored row.
func sampleFrom(rec *core.Record) HostSample {
	return HostSample{
		At:              rec.GetDateTime("at").Time(),
		CPUUsedSeconds:  rec.GetFloat("cpuUsedSeconds"),
		CPUTotalSeconds: rec.GetFloat("cpuTotalSeconds"),
		Load1:           rec.GetFloat("load1"),
		MemUsedBytes:    uint64(rec.GetFloat("memUsedBytes")),
		MemTotalBytes:   uint64(rec.GetFloat("memTotalBytes")),
		DiskUsedBytes:   uint64(rec.GetFloat("diskUsedBytes")),
		DiskTotalBytes:  uint64(rec.GetFloat("diskTotalBytes")),
	}
}
