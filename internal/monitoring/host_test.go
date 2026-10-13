package monitoring_test

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/monitoring"
	"pocketadmin/internal/testsupport"
)

// stubReader returns whatever it is given, and counts how often it was asked.
type stubReader struct {
	sample monitoring.HostSample
	calls  atomic.Int64
}

func (r *stubReader) Read(previous *monitoring.HostSample, now time.Time) monitoring.HostSample {
	r.calls.Add(1)
	out := r.sample
	out.At = now
	return out
}

// count reads how many metric rows exist.
func count(t *testing.T, app core.App) int {
	t.Helper()

	n, err := app.CountRecords(monitoring.CollectionHostMetrics)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	return int(n)
}

// TestThePercentagesAreDerivedFromTheCounters.
//
// The stored figures are totals since boot, so a percentage is a division rather than a
// column. Storing the percentage would freeze it against an interval that later reads
// differently.
func TestThePercentagesAreDerivedFromTheCounters(t *testing.T) {
	sample := monitoring.HostSample{
		CPUUsedSeconds:  25,
		CPUTotalSeconds: 100,
		MemUsedBytes:    3 << 30,
		MemTotalBytes:   4 << 30,
		DiskUsedBytes:   900,
		DiskTotalBytes:  1000,
	}

	if percent, ok := sample.CPUPercent(); !ok || percent != 25 {
		t.Errorf("cpu is %v (%v), want 25", percent, ok)
	}
	if percent, ok := sample.MemPercent(); !ok || percent != 75 {
		t.Errorf("memory is %v (%v), want 75", percent, ok)
	}
	if percent, ok := sample.DiskPercent(); !ok || percent != 90 {
		t.Errorf("disk is %v (%v), want 90", percent, ok)
	}

	// A platform that reported nothing yields no percentage rather than a confident
	// zero, because zero and unknown are different claims.
	empty := monitoring.HostSample{}
	if _, ok := empty.CPUPercent(); ok {
		t.Error("an empty sample produced a cpu percentage")
	}
	if _, ok := empty.MemPercent(); ok {
		t.Error("an empty sample produced a memory percentage")
	}
	if _, ok := empty.DiskPercent(); ok {
		t.Error("an empty sample produced a disk percentage")
	}

	// A counter that went backwards, which happens when the kernel's figures are read
	// at a strange moment, does not produce a nonsense figure.
	odd := monitoring.HostSample{CPUUsedSeconds: 200, CPUTotalSeconds: 100}
	if percent, ok := odd.CPUPercent(); !ok || percent != 100 {
		t.Errorf("an over-unity ratio gave %v (%v), want it clamped to 100", percent, ok)
	}
}

// TestAnUnsupportedPlatformReportsWhatItCannotRead.
//
// The build tag picks one of two readers. This test documents the contract both honour:
// a reader that cannot read something says so rather than returning zero, because a
// screen has to be able to tell "nothing" from "unknown".
func TestAnUnsupportedPlatformReportsWhatItCannotRead(t *testing.T) {
	reader := monitoring.NewHostReader(t.TempDir())
	sample := reader.Read(nil, time.Now())

	if !sample.At.Equal(sample.At) || sample.At.IsZero() {
		t.Error("a sample was returned with no time")
	}

	// On Linux the readings are present and nothing is unsupported. On anything else
	// every field is listed as unsupported. Either way the two agree.
	if len(sample.Unsupported) == 0 && sample.MemTotalBytes == 0 && sample.CPUTotalSeconds == 0 {
		t.Error("the reader reported neither readings nor what it could not read")
	}
}

// TestTheSamplerWritesAtItsIntervalAndNotMoreOften is the lightweight property.
//
// The tick is a second and the interval is configurable, so most wake-ups must do
// nothing. Without that, a one-minute interval would write sixty rows a minute.
func TestTheSamplerWritesAtItsIntervalAndNotMoreOften(t *testing.T) {
	app := testsupport.NewApp(t)

	reader := &stubReader{sample: monitoring.HostSample{
		CPUUsedSeconds: 10, CPUTotalSeconds: 100, MemUsedBytes: 1 << 30, MemTotalBytes: 2 << 30,
	}}

	policy := monitoring.SamplePolicy{Enabled: true, Interval: time.Hour, RetentionDays: 7}
	sampler := monitoring.NewSampler(app, reader, func() monitoring.SamplePolicy { return policy })
	sampler.Start()
	defer sampler.Stop()

	// Long enough for several ticks, far short of the interval.
	time.Sleep(2500 * time.Millisecond)

	if got := count(t, app); got != 1 {
		t.Errorf("%d samples were written in two and a half seconds at an hourly interval, want 1", got)
	}
	if got := reader.calls.Load(); got != 1 {
		t.Errorf("the reader was asked %d times, want once: a skipped tick must not even read", got)
	}
}

// TestAShorterIntervalIsObeyed: which is what makes the setting real rather than a
// number on a page.
func TestAShorterIntervalIsObeyed(t *testing.T) {
	app := testsupport.NewApp(t)

	reader := &stubReader{sample: monitoring.HostSample{CPUUsedSeconds: 1, CPUTotalSeconds: 100}}
	policy := monitoring.SamplePolicy{Enabled: true, Interval: time.Second, RetentionDays: 7}

	sampler := monitoring.NewSampler(app, reader, func() monitoring.SamplePolicy { return policy })
	sampler.Start()
	defer sampler.Stop()

	time.Sleep(3500 * time.Millisecond)

	if got := count(t, app); got < 2 {
		t.Errorf("%d samples were written in three and a half seconds at a one-second interval, want several", got)
	}
}

// TestTurningItOffStopsTheWriting: the switch is a switch, not a filter on display.
func TestTurningItOffStopsTheWriting(t *testing.T) {
	app := testsupport.NewApp(t)

	reader := &stubReader{sample: monitoring.HostSample{CPUUsedSeconds: 1, CPUTotalSeconds: 100}}
	enabled := false
	policy := monitoring.SamplePolicy{Enabled: enabled, Interval: time.Second, RetentionDays: 7}

	sampler := monitoring.NewSampler(app, reader, func() monitoring.SamplePolicy { return policy })
	sampler.Start()
	defer sampler.Stop()

	time.Sleep(2500 * time.Millisecond)

	if got := count(t, app); got != 0 {
		t.Errorf("%d samples were written while sampling was off", got)
	}
	if got := reader.calls.Load(); got != 0 {
		t.Errorf("the reader was asked %d times while sampling was off", got)
	}
}

// TestPruningDeletesWhatIsOldAndKeepsWhatIsNot.
func TestPruningDeletesWhatIsOldAndKeepsWhatIsNot(t *testing.T) {
	app := testsupport.NewApp(t)
	now := time.Now()

	col, _ := app.FindCollectionByNameOrId(monitoring.CollectionHostMetrics)
	for _, at := range []time.Time{
		now.AddDate(0, 0, -30),
		now.AddDate(0, 0, -10),
		now.AddDate(0, 0, -8),
		now.AddDate(0, 0, -1),
		now,
	} {
		rec := core.NewRecord(col)
		rec.Set("at", at.UTC())
		rec.Set("cpuTotalSeconds", 100)
		if err := app.Save(rec); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	deleted, err := monitoring.PruneHostMetrics(app, 7, now)
	if err != nil {
		t.Fatalf("PruneHostMetrics: %v", err)
	}
	if deleted != 3 {
		t.Errorf("%d samples were deleted, want the three older than seven days", deleted)
	}
	if got := count(t, app); got != 2 {
		t.Errorf("%d samples remain, want the two inside the window", got)
	}

	// Zero means keep for ever, which is a legitimate choice for a small installation
	// that wants the history.
	if deleted, err := monitoring.PruneHostMetrics(app, 0, now); err != nil || deleted != 0 {
		t.Errorf("a retention of zero deleted %d samples (%v)", deleted, err)
	}
	if got := count(t, app); got != 2 {
		t.Errorf("%d samples remain after a no-op prune", got)
	}
}
