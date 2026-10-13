package monitoring_test

import (
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/monitoring"
	"pocketadmin/internal/testsupport"
)

// sample writes a reading for a node.
func sample(t *testing.T, app core.App, nodeID string, at time.Time, cpuUsed, cpuTotal float64, memUsed, memTotal uint64, netRx uint64) {
	t.Helper()

	col, _ := app.FindCollectionByNameOrId(monitoring.CollectionHostMetrics)
	rec := core.NewRecord(col)
	rec.Set("node", nodeID)
	rec.Set("at", at.UTC())
	rec.Set("cpuUsedSeconds", cpuUsed)
	rec.Set("cpuTotalSeconds", cpuTotal)
	rec.Set("memUsedBytes", memUsed)
	rec.Set("memTotalBytes", memTotal)
	rec.Set("diskUsedBytes", 50)
	rec.Set("diskTotalBytes", 100)
	rec.Set("netRxBytes", netRx)
	if err := app.Save(rec); err != nil {
		t.Fatalf("save sample: %v", err)
	}
}

// TestTheSeriesCoverTheWindowEvenWhereThereIsNoData is what makes an outage visible.
//
// A bucket that received nothing is emitted with no reading rather than being skipped, so
// the browser draws a break. A machine that was down for an hour was absent, not idle,
// and a chart that joined the readings either side would say the opposite.
func TestTheSeriesCoverTheWindowEvenWhereThereIsNoData(t *testing.T) {
	app := testsupport.NewApp(t)

	node, err := monitoring.EnsureLocalNode(app)
	if err != nil {
		t.Fatalf("EnsureLocalNode: %v", err)
	}

	now := time.Now()
	// Two readings an hour apart, with a gap between them.
	sample(t, app, node.Id, now.Add(-50*time.Minute), 10, 100, 1, 10, 1000)
	sample(t, app, node.Id, now.Add(-10*time.Minute), 20, 100, 2, 10, 2000)

	window := monitoring.Window{From: now.Add(-time.Hour), To: now, Buckets: 60}
	series, err := monitoring.BuildSeries(app, node.Id, window, now)
	if err != nil {
		t.Fatalf("BuildSeries: %v", err)
	}
	if len(series) == 0 {
		t.Fatal("no series were built")
	}

	for _, line := range series {
		if len(line.Points) != 60 {
			t.Errorf("%s has %d points, want one per bucket", line.Name, len(line.Points))
		}

		withReadings := 0
		for _, point := range line.Points {
			if point.Count > 0 {
				withReadings++
			}
		}

		// A rate needs a reading before it, so two readings an hour apart give the
		// network series nothing to difference. Everything else carries both.
		if line.Kind == monitoring.KindRate {
			if withReadings > 2 {
				t.Errorf("%s has %d buckets with readings, want at most two", line.Name, withReadings)
			}
			continue
		}
		if withReadings == 0 || withReadings > 2 {
			t.Errorf("%s has %d buckets with readings, want one or two", line.Name, withReadings)
		}
	}
}

// TestTheProcessorFigureIsTheBucketMean: the samples in a bucket are averaged so a week
// of minute readings becomes a readable line.
func TestTheProcessorFigureIsTheBucketMean(t *testing.T) {
	app := testsupport.NewApp(t)

	node, _ := monitoring.EnsureLocalNode(app)
	now := time.Now()

	// Three readings a few seconds apart, at 10%, 20% and 30%. They share a bucket now
	// that the boundaries are aligned to the clock rather than to the request.
	base := now.Add(-30 * time.Minute).Truncate(time.Minute).Add(30 * time.Second)
	sample(t, app, node.Id, base, 10, 100, 1, 10, 0)
	sample(t, app, node.Id, base.Add(10*time.Second), 20, 100, 1, 10, 0)
	sample(t, app, node.Id, base.Add(20*time.Second), 30, 100, 1, 10, 0)

	series, err := monitoring.BuildSeries(app, node.Id,
		monitoring.Window{From: now.Add(-time.Hour), To: now, Buckets: 60}, now)
	if err != nil {
		t.Fatalf("BuildSeries: %v", err)
	}

	var cpu *monitoring.Series
	for i := range series {
		if series[i].Name == "Processor" {
			cpu = &series[i]
		}
	}
	if cpu == nil {
		t.Fatal("no processor series")
	}

	found := false
	for _, point := range cpu.Points {
		if point.Count == 3 {
			found = true
			if point.Value < 19 || point.Value > 21 {
				t.Errorf("the bucket average is %.1f, want about 20", point.Value)
			}
		}
	}
	if !found {
		t.Error("no bucket collected the three readings")
	}
}

// TestTheNetworkSeriesIsARateNotACounter.
//
// The stored figures are cumulative since boot, so a chart drawn from them directly would
// rise for ever and say nothing. The rate is the difference between adjacent buckets over
// the time between them, and the first bucket has nothing before it.
func TestTheNetworkSeriesIsARateNotACounter(t *testing.T) {
	app := testsupport.NewApp(t)

	node, _ := monitoring.EnsureLocalNode(app)
	now := time.Now()

	// Two readings a minute apart, 6000 bytes further along.
	sample(t, app, node.Id, now.Add(-2*time.Minute), 1, 100, 1, 10, 100000)
	sample(t, app, node.Id, now.Add(-1*time.Minute), 1, 100, 1, 10, 106000)

	series, err := monitoring.BuildSeries(app, node.Id,
		monitoring.Window{From: now.Add(-time.Hour), To: now, Buckets: 60}, now)
	if err != nil {
		t.Fatalf("BuildSeries: %v", err)
	}

	var net *monitoring.Series
	for i := range series {
		if series[i].Name == "Network in" {
			net = &series[i]
		}
	}
	if net == nil {
		t.Fatal("no network series")
	}
	if net.Kind != monitoring.KindRate {
		t.Errorf("the network series is %q, want a rate", net.Kind)
	}

	// The first bucket has nothing to difference against.
	if net.Points[0].Count != 0 {
		t.Error("the first network bucket carries a reading, which it cannot")
	}

	// The second is 6000 bytes over 60 seconds: 100 bytes a second.
	found := false
	for _, point := range net.Points {
		if point.Count == 0 {
			continue
		}
		found = true
		if point.Value < 90 || point.Value > 110 {
			t.Errorf("the rate is %.1f B/s, want about 100", point.Value)
		}
	}
	if !found {
		t.Error("no network rate was produced")
	}
}

// TestACounterThatWentBackwardsProducesNoRate: an interface reset would otherwise be an
// enormous negative spike, which is worse than a gap.
func TestACounterThatWentBackwardsProducesNoRate(t *testing.T) {
	app := testsupport.NewApp(t)

	node, _ := monitoring.EnsureLocalNode(app)
	now := time.Now()

	sample(t, app, node.Id, now.Add(-2*time.Minute), 1, 100, 1, 10, 900000)
	sample(t, app, node.Id, now.Add(-1*time.Minute), 1, 100, 1, 10, 1000)

	series, err := monitoring.BuildSeries(app, node.Id,
		monitoring.Window{From: now.Add(-time.Hour), To: now, Buckets: 60}, now)
	if err != nil {
		t.Fatalf("BuildSeries: %v", err)
	}

	for _, line := range series {
		for _, point := range line.Points {
			if point.Count > 0 && point.Value < 0 {
				t.Errorf("%s produced a negative reading: %.1f", line.Name, point.Value)
			}
		}
	}
}

// TestAWindowWithNoReadingsProducesNothing: the screen says so rather than drawing an
// empty axis.
func TestAWindowWithNoReadingsProducesNothing(t *testing.T) {
	app := testsupport.NewApp(t)

	node, _ := monitoring.EnsureLocalNode(app)
	now := time.Now()

	series, err := monitoring.BuildSeries(app, node.Id,
		monitoring.Window{From: now.Add(-time.Hour), To: now, Buckets: 60}, now)
	if err != nil {
		t.Fatalf("BuildSeries: %v", err)
	}
	if len(series) != 0 {
		t.Errorf("%d series were built from no readings", len(series))
	}
}

// TestAWindowWithNoEndFallsBack: a request with a nonsense range gets the default rather
// than an empty chart or a division by zero.
func TestAWindowWithNoEndFallsBack(t *testing.T) {
	app := testsupport.NewApp(t)

	node, _ := monitoring.EnsureLocalNode(app)
	now := time.Now()
	sample(t, app, node.Id, now.Add(-time.Minute), 1, 100, 1, 10, 0)

	series, err := monitoring.BuildSeries(app, node.Id, monitoring.Window{}, now)
	if err != nil {
		t.Fatalf("BuildSeries: %v", err)
	}
	if len(series) == 0 {
		t.Error("an empty window produced nothing at all")
	}
}

// TestTheWindowPresetsAreAllOffered.
func TestTheWindowPresetsAreAllOffered(t *testing.T) {
	now := time.Now()
	presets := monitoring.PresetWindows(now)

	if len(presets) < 3 {
		t.Fatalf("%d presets, want at least three", len(presets))
	}
	for _, preset := range presets {
		if preset.Window.Buckets < 12 {
			t.Errorf("%s has %d buckets, too few to draw", preset.Label, preset.Window.Buckets)
		}
		if !preset.Window.To.After(preset.Window.From) {
			t.Errorf("%s is not a range", preset.Label)
		}
	}

	// An unknown label falls back rather than producing an empty window.
	window, label := monitoring.WindowFor(now, "not a window")
	if window.Buckets == 0 || label == "" {
		t.Error("an unknown window did not fall back to a preset")
	}
}
