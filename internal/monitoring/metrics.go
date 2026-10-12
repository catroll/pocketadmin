// Package monitoring holds the runtime picture: what the process is doing, what
// the database and the file library cost, and whether each part is alive.
//
// The metrics are kept in memory and are deliberately lossy. They answer "what is
// happening now" and "has anything changed lately", which is what an operator
// looking at a panel needs; they are not an accounting system, and pretending
// otherwise would mean persisting every request.
package monitoring

import (
	"sort"
	"sync"
	"time"
)

// latencyBuckets is the resolution of the latency histogram, in milliseconds.
//
// A histogram rather than a list of samples: keeping every duration would grow
// without bound on a busy process, and the percentile of a bounded histogram is
// accurate enough to answer "is this slow" while the memory is fixed.
var latencyBuckets = []float64{
	1, 2, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000,
}

// windowMinutes is how many one-minute buckets are retained.
//
// Sixty is enough for an hour of trend and for computing a rate over the last
// minute, five minutes and hour without keeping a second copy of the data.
const windowMinutes = 60

// minuteBucket is one minute of traffic.
type minuteBucket struct {
	// minute is the start of the window, in Unix minutes.
	minute int64

	requests int
	errors   int

	// latency counts how many requests fell into each bucket.
	latency []int

	// totalMs sums the durations, for the mean.
	totalMs float64
}

// Collector accumulates request metrics.
//
// All of it is bounded: one minute of buckets, rotated, so a process that runs for
// a year uses the same memory as one that started a minute ago.
type Collector struct {
	mu      sync.Mutex
	buckets []*minuteBucket
	started time.Time
}

// NewCollector returns an empty collector.
func NewCollector() *Collector {
	return &Collector{started: time.Now()}
}

// Started returns when the process began serving.
func (c *Collector) Started() time.Time { return c.started }

// Observe records one request.
//
// It is called on the request path, so it takes a single lock and does no
// allocation beyond the bucket it may need. A metrics collector that costs
// measurable time is a metrics collector that gets switched off.
func (c *Collector) Observe(status int, duration time.Duration) {
	now := time.Now()
	minute := now.Unix() / 60

	c.mu.Lock()
	defer c.mu.Unlock()

	bucket := c.bucketLocked(minute)
	bucket.requests++
	if status >= 500 || status == 0 {
		// A 4xx is the caller being wrong; a 5xx is this process being wrong. The
		// error rate is about the second one, because that is the one an operator
		// can act on.
		bucket.errors++
	}

	ms := float64(duration.Microseconds()) / 1000
	bucket.totalMs += ms

	// The last bucket in the slice is the one that is not full yet, so a value
	// above every bound lands there rather than being dropped.
	for i, bound := range latencyBuckets {
		if ms <= bound {
			bucket.latency[i]++
			return
		}
	}
	bucket.latency[len(bucket.latency)-1]++
}

// bucketLocked returns the bucket for a minute, creating or rotating as needed.
func (c *Collector) bucketLocked(minute int64) *minuteBucket {
	// The common case: the newest bucket is the current minute.
	if n := len(c.buckets); n > 0 && c.buckets[n-1].minute == minute {
		return c.buckets[n-1]
	}

	fresh := &minuteBucket{minute: minute, latency: make([]int, len(latencyBuckets))}

	// Drop anything older than the window, then append. Trimming from the front
	// keeps the slice ordered without a reallocation every minute.
	cutoff := minute - windowMinutes
	kept := c.buckets[:0]
	for _, b := range c.buckets {
		if b.minute > cutoff {
			kept = append(kept, b)
		}
	}
	c.buckets = append(kept, fresh)
	return fresh
}

// Snapshot is the traffic picture at one moment.
type Snapshot struct {
	RequestsPerMinute float64
	ErrorRate         float64
	P50Ms             float64
	P95Ms             float64
	P99Ms             float64
	MeanMs            float64
	TotalRequests     int
	TotalErrors       int

	// Uptime is how long the process has been serving.
	Uptime time.Duration
}

// Snapshot summarises the last minute.
func (c *Collector) Snapshot() Snapshot {
	return c.SnapshotOver(1)
}

// SnapshotOver summarises the last n minutes.
func (c *Collector) SnapshotOver(minutes int) Snapshot {
	if minutes < 1 {
		minutes = 1
	}

	now := time.Now()
	current := now.Unix() / 60
	cutoff := current - int64(minutes) + 1

	c.mu.Lock()
	defer c.mu.Unlock()

	included := make([]*minuteBucket, 0, len(c.buckets))
	for _, b := range c.buckets {
		if b.minute >= cutoff {
			included = append(included, b)
		}
	}

	out := Snapshot{Uptime: time.Since(c.started)}

	totalLatency := make([]int, len(latencyBuckets))

	for _, b := range included {
		out.TotalRequests += b.requests
		out.TotalErrors += b.errors
		out.MeanMs += b.totalMs
		for i, n := range b.latency {
			totalLatency[i] += n
		}
	}

	if out.TotalRequests == 0 {
		return out
	}

	// The rate is the count divided by the elapsed part of the window, so a
	// process that started ten seconds ago does not report a sixtieth of its
	// actual rate.
	elapsed := now.Sub(c.started)
	window := time.Duration(minutes) * time.Minute
	if elapsed < window {
		window = elapsed
	}
	if window > 0 {
		out.RequestsPerMinute = float64(out.TotalRequests) / window.Minutes()
	}

	out.ErrorRate = float64(out.TotalErrors) / float64(out.TotalRequests)
	out.MeanMs /= float64(out.TotalRequests)

	out.P50Ms = percentile(totalLatency, int64(out.TotalRequests), 0.50)
	out.P95Ms = percentile(totalLatency, int64(out.TotalRequests), 0.95)
	out.P99Ms = percentile(totalLatency, int64(out.TotalRequests), 0.99)

	return out
}

// percentile reads a value out of the histogram.
//
// The answer is the upper bound of the bucket the percentile falls in, so it is
// never an understatement: reporting 25 ms for something that took 24 is a smaller
// error than reporting 10.
func percentile(counts []int, total int64, fraction float64) float64 {
	if total == 0 {
		return 0
	}

	target := int64(float64(total) * fraction)
	if target < 1 {
		target = 1
	}

	var seen int64
	for i, count := range counts {
		seen += int64(count)
		if seen >= target {
			return latencyBuckets[i]
		}
	}
	return latencyBuckets[len(latencyBuckets)-1]
}

// RecentRequests returns the per-minute request counts, oldest first, for a chart.
func (c *Collector) RecentRequests() []int {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make([]int, 0, len(c.buckets))
	for _, b := range c.buckets {
		out = append(out, b.requests)
	}
	return out
}

// RecentErrors returns the per-minute error counts, oldest first.
func (c *Collector) RecentErrors() []int {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make([]int, 0, len(c.buckets))
	for _, b := range c.buckets {
		out = append(out, b.errors)
	}
	return out
}

// SlowestBuckets returns the number of requests in each latency bucket for the
// last minute, for a distribution display.
func (c *Collector) SlowestBuckets() ([]float64, []int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	counts := make([]int, len(latencyBuckets))
	if n := len(c.buckets); n > 0 {
		copy(counts, c.buckets[n-1].latency)
	}
	return latencyBuckets, counts
}

// sortedInts returns a copy of a slice in ascending order, for a caller that
// needs a stable display.
func sortedInts(values []int) []int {
	out := append([]int(nil), values...)
	sort.Ints(out)
	return out
}

var _ = sortedInts
