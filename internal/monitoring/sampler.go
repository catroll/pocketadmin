package monitoring

import (
	"log/slog"
	"sync"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// CollectionHostMetrics holds the samples.
const CollectionHostMetrics = "host_metrics"

// SamplePolicy is how the sampler is configured.
type SamplePolicy struct {
	// Enabled turns sampling off. Nothing is written while it is off, and nothing is
	// deleted either: switching it back on resumes rather than starting over.
	Enabled bool

	// Interval is how often a sample is taken.
	Interval time.Duration

	// RetentionDays is how long a sample is kept.
	RetentionDays int
}

// DefaultSamplePolicy is the policy when nothing is configured.
//
// A minute is often enough to see a machine fill up and rare enough to be free. Seven
// days is roughly a hundred thousand rows a year at that rate -- small enough that the
// retention job is housekeeping rather than a necessity, which is the right size for a
// feature that exists to avoid running a monitoring stack.
func DefaultSamplePolicy() SamplePolicy {
	return SamplePolicy{Enabled: true, Interval: time.Minute, RetentionDays: 7}
}

// Sampler writes a host sample on a schedule.
//
// It is a goroutine rather than a scheduled task on purpose. A task would write a run
// row every time it fired -- a thousand a day at the default interval, all of them
// saying "took a measurement" -- and the interval would be limited to what a cron
// expression can say. A ticker comparing two times is cheaper than either.
type Sampler struct {
	app    core.App
	reader HostReader

	// policy is read on every tick rather than captured, so an operator changing the
	// interval or switching sampling off takes effect without a restart.
	policy func() SamplePolicy

	// tick is how often the clock is consulted. It bounds how late a sample can be,
	// not how often one is taken.
	tick time.Duration

	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once

	// node is the row for the machine running this process, resolved on first write.
	node *core.Record

	mu         sync.Mutex
	previous   *HostSample
	lastSample time.Time
	lastError  string
	written    int64
}

// NewSampler returns a sampler.
func NewSampler(app core.App, reader HostReader, policy func() SamplePolicy) *Sampler {
	if reader == nil {
		reader = NewHostReader("")
	}
	if policy == nil {
		policy = DefaultSamplePolicy
	}

	return &Sampler{
		app:    app,
		reader: reader,
		policy: policy,
		tick:   time.Second,
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
}

// Start begins sampling in the background.
func (s *Sampler) Start() {
	go s.loop()
}

// Stop ends the loop.
func (s *Sampler) Stop() {
	s.stopOnce.Do(func() { close(s.stop) })
	<-s.done
}

// loop is the sampler's body.
func (s *Sampler) loop() {
	defer close(s.done)

	ticker := time.NewTicker(s.tick)
	defer ticker.Stop()

	slog.Info("host sampler started", "tick", s.tick.String())

	for {
		select {
		case <-s.stop:
			slog.Info("host sampler stopped")
			return
		case now := <-ticker.C:
			s.maybeSample(now)
		}
	}
}

// maybeSample writes a sample when the interval has passed.
//
// The decision is two time comparisons. When the interval is a minute and the tick is a
// second, fifty-nine of every sixty wake-ups do nothing at all, which is what makes a
// one-second tick affordable.
func (s *Sampler) maybeSample(now time.Time) {
	policy := s.policy()
	if !policy.Enabled {
		return
	}

	interval := policy.Interval
	if interval <= 0 {
		interval = time.Minute
	}

	s.mu.Lock()
	due := s.lastSample.IsZero() || now.Sub(s.lastSample) >= interval
	previous := s.previous
	s.mu.Unlock()

	if !due {
		return
	}

	sample := s.reader.Read(previous, now)

	if err := s.write(sample); err != nil {
		// A failed write is recorded and the loop continues. A disk that is full or a
		// database that is locked is exactly the situation this feature exists to make
		// visible, and stopping would hide it.
		s.mu.Lock()
		s.lastError = err.Error()
		s.lastSample = now
		s.mu.Unlock()
		slog.Warn("could not record a host sample", "error", err)
		return
	}

	s.mu.Lock()
	s.previous = &sample
	s.lastSample = now
	s.lastError = ""
	s.written++
	s.mu.Unlock()
}

// write records a sample.
func (s *Sampler) write(sample HostSample) error {
	col, err := s.app.FindCollectionByNameOrId(CollectionHostMetrics)
	if err != nil || col == nil {
		return err
	}

	// The machine is resolved once and cached. A missing row is recreated rather than
	// reported as a failure: a sample that belongs to no machine is worse than a
	// registry row appearing on its own.
	node, err := s.localNode()
	if err != nil {
		return err
	}

	rec := core.NewRecord(col)
	rec.Set("node", node.Id)
	rec.Set("at", sample.At.UTC())
	rec.Set("cpuUsedSeconds", sample.CPUUsedSeconds)
	rec.Set("cpuTotalSeconds", sample.CPUTotalSeconds)
	rec.Set("load1", sample.Load1)
	rec.Set("load5", sample.Load5)
	rec.Set("load15", sample.Load15)
	rec.Set("memUsedBytes", sample.MemUsedBytes)
	rec.Set("memTotalBytes", sample.MemTotalBytes)
	rec.Set("diskUsedBytes", sample.DiskUsedBytes)
	rec.Set("diskTotalBytes", sample.DiskTotalBytes)
	rec.Set("netRxBytes", sample.NetRxBytes)
	rec.Set("netTxBytes", sample.NetTxBytes)
	rec.Set("uptimeSeconds", sample.UptimeSeconds)
	if len(sample.Unsupported) > 0 {
		rec.Set("unsupported", sample.Unsupported)
	}

	if err := s.app.Save(rec); err != nil {
		return err
	}

	return RecordSample(s.app, node, sample.At)
}

// localNode returns this machine's registry row, creating it when it is missing.
func (s *Sampler) localNode() (*core.Record, error) {
	s.mu.Lock()
	cached := s.node
	s.mu.Unlock()

	if cached != nil {
		return cached, nil
	}

	node, err := EnsureLocalNode(s.app)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.node = node
	s.mu.Unlock()

	return node, nil
}

// SampleNow takes a reading immediately, for a screen that has just been opened.
//
// It does not disturb the schedule: the sample is written and the interval continues
// from where it was, so opening a page does not shift every later reading.
func (s *Sampler) SampleNow() (HostSample, error) {
	s.mu.Lock()
	previous := s.previous
	s.mu.Unlock()

	sample := s.reader.Read(previous, time.Now())
	if err := s.write(sample); err != nil {
		return sample, err
	}

	s.mu.Lock()
	s.previous = &sample
	s.lastSample = time.Now()
	s.written++
	s.mu.Unlock()

	return sample, nil
}

// Status is what the sampler is doing, for the screen.
type SamplerStatus struct {
	// Written is how many samples this process has recorded.
	Written int64

	// LastSample is when it last did.
	LastSample time.Time

	// LastError is why the last attempt failed, empty when it did not.
	LastError string

	// Previous is the last reading, for a rate that needs two.
	Previous *HostSample
}

// Status returns the sampler's state.
func (s *Sampler) Status() SamplerStatus {
	s.mu.Lock()
	defer s.mu.Unlock()

	return SamplerStatus{
		Written:    s.written,
		LastSample: s.lastSample,
		LastError:  s.lastError,
		Previous:   s.previous,
	}
}

// PruneHostMetrics deletes samples older than the retention window.
//
// It deletes in batches and returns how many went, so a first run against a database
// that has been collecting for a year does not hold a write lock for the whole of it.
func PruneHostMetrics(app core.App, retentionDays int, now time.Time) (int, error) {
	if retentionDays <= 0 {
		// Zero means keep for ever, which is a legitimate choice for a small
		// installation that wants a long history.
		return 0, nil
	}

	cutoff := now.AddDate(0, 0, -retentionDays)

	total := 0
	for batch := 0; batch < 100; batch++ {
		records, err := app.FindRecordsByFilter(CollectionHostMetrics,
			"at < {:cutoff}", "at", 500, 0, map[string]any{"cutoff": cutoff.UTC()})
		if err != nil {
			return total, err
		}
		if len(records) == 0 {
			return total, nil
		}

		for _, rec := range records {
			if err := app.Delete(rec); err != nil {
				return total, err
			}
			total++
		}

		if len(records) < 500 {
			return total, nil
		}
	}

	return total, nil
}
