package monitoring_test

import (
	"context"
	"testing"
	"time"

	"pocketadmin/internal/monitoring"
)

// TestObserveCountsRequestsAndErrors: the error rate counts 5xx and nothing else,
// because a 4xx is the caller being wrong and a 5xx is this process being wrong.
func TestObserveCountsRequestsAndErrors(t *testing.T) {
	collector := monitoring.NewCollector()

	collector.Observe(200, 5*time.Millisecond)
	collector.Observe(201, 5*time.Millisecond)
	collector.Observe(404, 5*time.Millisecond) // the caller's fault
	collector.Observe(500, 5*time.Millisecond)
	collector.Observe(503, 5*time.Millisecond)

	snapshot := collector.Snapshot()

	if snapshot.TotalRequests != 5 {
		t.Errorf("counted %d requests, want 5", snapshot.TotalRequests)
	}
	if snapshot.TotalErrors != 2 {
		t.Errorf("counted %d errors, want 2 (the 5xx responses)", snapshot.TotalErrors)
	}

	want := 2.0 / 5.0
	if diff := snapshot.ErrorRate - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("error rate %v, want %v", snapshot.ErrorRate, want)
	}
}

// TestPercentilesNeverUnderstate: a figure is the upper bound of the bucket the
// percentile falls in, so it is never smaller than the truth.
func TestPercentilesNeverUnderstate(t *testing.T) {
	collector := monitoring.NewCollector()

	// Ninety values at 5 ms and ten at 500 ms: p95 must land in the slow group.
	for i := 0; i < 90; i++ {
		collector.Observe(200, 5*time.Millisecond)
	}
	for i := 0; i < 10; i++ {
		collector.Observe(200, 500*time.Millisecond)
	}

	snapshot := collector.Snapshot()

	if snapshot.P50Ms < 5 || snapshot.P50Ms > 10 {
		t.Errorf("p50 = %v, want the bucket holding 5 ms", snapshot.P50Ms)
	}
	if snapshot.P95Ms < 500 {
		t.Errorf("p95 = %v, want at least 500 ms: the slowest tenth is 10%% of the sample",
			snapshot.P95Ms)
	}
	if snapshot.P99Ms < snapshot.P95Ms {
		t.Errorf("p99 (%v) is below p95 (%v)", snapshot.P99Ms, snapshot.P95Ms)
	}
	if snapshot.MeanMs <= 0 {
		t.Error("the mean was not computed")
	}
}

// TestASlowValueLandsInTheLastBucket: a request slower than every bound must be
// counted, not dropped.
func TestASlowValueLandsInTheLastBucket(t *testing.T) {
	collector := monitoring.NewCollector()
	collector.Observe(200, time.Hour)

	snapshot := collector.Snapshot()

	if snapshot.TotalRequests != 1 {
		t.Fatalf("counted %d requests, want 1", snapshot.TotalRequests)
	}
	if snapshot.P95Ms < 10000 {
		t.Errorf("an hour-long request reported p95 = %v", snapshot.P95Ms)
	}
}

// TestNothingObservedIsNotAnError: an idle process reports zero rather than
// dividing by it.
func TestNothingObservedIsNotAnError(t *testing.T) {
	collector := monitoring.NewCollector()
	snapshot := collector.Snapshot()

	if snapshot.TotalRequests != 0 || snapshot.ErrorRate != 0 ||
		snapshot.P50Ms != 0 || snapshot.P95Ms != 0 || snapshot.P99Ms != 0 {
		t.Errorf("an idle collector reported %+v", snapshot)
	}
	if snapshot.Uptime <= 0 {
		t.Error("the uptime was not reported")
	}
}

// TestTheWindowIsBounded: the collector must not grow with the number of minutes
// the process has been running.
func TestTheWindowIsBounded(t *testing.T) {
	collector := monitoring.NewCollector()

	// A value dated in the past is still recorded into the current bucket, so the
	// bound is asserted on the exposed series rather than by faking clocks.
	for i := 0; i < 200; i++ {
		collector.Observe(200, time.Millisecond)
	}

	if got := len(collector.RecentRequests()); got > 61 {
		t.Errorf("the window holds %d minutes, want at most 61", got)
	}
	if got := len(collector.RecentErrors()); got > 61 {
		t.Errorf("the error window holds %d minutes", got)
	}
}

// TestBucketSeriesAgree: the two series must be the same length, or a chart
// plotting them together would be misaligned.
func TestBucketSeriesAgree(t *testing.T) {
	collector := monitoring.NewCollector()
	for i := 0; i < 5; i++ {
		collector.Observe(200, time.Millisecond)
		collector.Observe(500, time.Millisecond)
	}

	requests := collector.RecentRequests()
	errors := collector.RecentErrors()

	if len(requests) != len(errors) {
		t.Fatalf("the series have different lengths: %d and %d", len(requests), len(errors))
	}

	var totalRequests, totalErrors int
	for i := range requests {
		totalRequests += requests[i]
		totalErrors += errors[i]
	}
	if totalRequests != 10 {
		t.Errorf("the series total %d requests, want 10", totalRequests)
	}
	if totalErrors != 5 {
		t.Errorf("the series total %d errors, want 5", totalErrors)
	}
}

// TestOverallTakesTheWorstComponent: a process that is running while its database
// is unreachable is not healthy, and reporting "ok" because the web server
// answered is how a health check becomes worse than none.
func TestOverallTakesTheWorstComponent(t *testing.T) {
	cases := []struct {
		name   string
		checks []monitoring.Check
		want   string
	}{
		{"all well", []monitoring.Check{
			{Status: monitoring.StatusOK}, {Status: monitoring.StatusOK},
		}, monitoring.StatusOK},
		{"one degraded", []monitoring.Check{
			{Status: monitoring.StatusOK}, {Status: monitoring.StatusDegraded},
		}, monitoring.StatusDegraded},
		{"one down beats a degraded", []monitoring.Check{
			{Status: monitoring.StatusOK}, {Status: monitoring.StatusDegraded},
			{Status: monitoring.StatusDown},
		}, monitoring.StatusDown},
		{"nothing to judge", nil, monitoring.StatusOK},
	}

	for _, tc := range cases {
		if got := monitoring.Overall(tc.checks); got != tc.want {
			t.Errorf("%s: Overall = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestCheckDatabaseReportsFailureAndTimeouts.
func TestCheckDatabaseReportsFailureAndTimeouts(t *testing.T) {
	ok := monitoring.CheckDatabase(t.Context(), func(context.Context) error { return nil })
	if ok.Status != monitoring.StatusOK {
		t.Errorf("a working database reported %q", ok.Status)
	}

	broken := monitoring.CheckDatabase(t.Context(), func(context.Context) error {
		return errFailed
	})
	if broken.Status != monitoring.StatusDown {
		t.Errorf("a failing database reported %q", broken.Status)
	}
	if broken.Detail == "" {
		t.Error("a failure was reported without a reason")
	}

	// A ping that never returns is a database that is not answering, and waiting
	// for it would make the health endpoint itself unhealthy.
	blocked := monitoring.CheckDatabase(t.Context(), func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	if blocked.Status != monitoring.StatusDown {
		t.Errorf("a hanging database reported %q", blocked.Status)
	}
}

// TestCheckSchedulerCatchesAStuckLoop: "running" but not ticking is the failure a
// liveness check exists to catch.
func TestCheckSchedulerCatchesAStuckLoop(t *testing.T) {
	if got := monitoring.CheckScheduler(true, time.Now()); got.Status != monitoring.StatusOK {
		t.Errorf("a ticking scheduler reported %q", got.Status)
	}
	if got := monitoring.CheckScheduler(false, time.Now()); got.Status != monitoring.StatusDown {
		t.Errorf("a stopped scheduler reported %q", got.Status)
	}
	if got := monitoring.CheckScheduler(true, time.Now().Add(-time.Hour)); got.Status != monitoring.StatusDegraded {
		t.Errorf("a scheduler that has not ticked in an hour reported %q", got.Status)
	}
}

// TestCheckStorageIsDegradedNotDown: the panel works without the file library, and
// one unhealthy subsystem should not take the whole service out of a load
// balancer's rotation.
func TestCheckStorageIsDegradedNotDown(t *testing.T) {
	if got := monitoring.CheckStorage(true, ""); got.Status != monitoring.StatusOK {
		t.Errorf("available storage reported %q", got.Status)
	}
	if got := monitoring.CheckStorage(false, "no driver"); got.Status != monitoring.StatusDegraded {
		t.Errorf("unavailable storage reported %q", got.Status)
	}
}

// TestReadRuntimeIsPopulated: the panel reads real numbers, not zeroes.
func TestReadRuntimeIsPopulated(t *testing.T) {
	info := monitoring.ReadRuntime(time.Now().Add(-time.Minute))

	if info.GoVersion == "" {
		t.Error("the Go version was not reported")
	}
	if info.NumCPU < 1 {
		t.Errorf("reported %d CPUs", info.NumCPU)
	}
	if info.Goroutines < 1 {
		t.Errorf("reported %d goroutines", info.Goroutines)
	}
	if info.HeapBytes == 0 {
		t.Error("the heap size is zero")
	}
	if info.Uptime < time.Minute {
		t.Errorf("the uptime is %v", info.Uptime)
	}
}

var errFailed = errorString("the database is not answering")

type errorString string

func (e errorString) Error() string { return string(e) }
