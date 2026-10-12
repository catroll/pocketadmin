package jobs

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// Scheduler runs due jobs on a tick.
//
// It is a single goroutine that wakes on an interval and asks what is due, rather
// than a timer per job. One loop is easier to reason about, keeps the number of
// goroutines independent of the number of tasks, and makes the "what is running"
// question answerable in one place.
type Scheduler struct {
	store *Store

	// tick is how often the loop looks for due work. It bounds how late a task
	// can be, so it must be shorter than the finest schedule an operator can
	// write -- a per-minute expression needs a tick well under a minute.
	tick time.Duration

	// maxConcurrent bounds how many jobs run at once across all codes, so a
	// pathological schedule cannot start every task in the same instant.
	maxConcurrent int

	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}

	// running is a wait group so shutdown can wait for in-flight work rather
	// than killing it mid-write.
	wg sync.WaitGroup
}

// NewScheduler returns a scheduler over a store.
func NewScheduler(store *Store) *Scheduler {
	return &Scheduler{
		store:         store,
		tick:          20 * time.Second,
		maxConcurrent: 4,
		stop:          make(chan struct{}),
		done:          make(chan struct{}),
	}
}

// Start begins the loop in the background.
//
// It returns immediately; Stop waits for the loop and for any job it started.
func (s *Scheduler) Start(app core.App) {
	go s.loop(app)
}

// Stop ends the loop and waits for in-flight jobs.
func (s *Scheduler) Stop() {
	s.stopOnce.Do(func() { close(s.stop) })
	<-s.done
	s.wg.Wait()
}

// loop is the scheduler's body.
func (s *Scheduler) loop(app core.App) {
	defer close(s.done)

	ticker := time.NewTicker(s.tick)
	defer ticker.Stop()

	slog.Info("scheduler started", "tick", s.tick.String())

	for {
		select {
		case <-s.stop:
			slog.Info("scheduler stopped")
			return
		case <-ticker.C:
			s.runDue(app)
		}
	}
}

// runDue starts every job whose time has arrived.
func (s *Scheduler) runDue(app core.App) {
	defer func() {
		// A panic in the scheduler would take down the process, and a scheduling
		// loop is not worth that. The job runner already contains handler panics
		// by not recovering them here; this contains a fault in the loop itself.
		if r := recover(); r != nil {
			slog.Error("the scheduler recovered from a panic", "error", r)
		}
	}()

	now := time.Now()

	due, err := s.store.Due(now, s.maxConcurrent)
	if err != nil {
		slog.Error("could not read due jobs", "error", err)
		return
	}

	for _, rec := range due {
		select {
		case <-s.stop:
			return
		default:
		}

		s.wg.Add(1)
		go func(rec *core.Record) {
			defer s.wg.Done()
			s.execute(rec)
		}(rec)
	}
}

// execute runs one job and records the outcome.
func (s *Scheduler) execute(rec *core.Record) {
	code := rec.GetString("code")

	handler, ok := Lookup(code)
	if !ok {
		// The row survives a downgrade that removed the handler. Recording the
		// skip makes the situation visible without stopping the process.
		slog.Warn("a scheduled task has no registered handler; skipping", "code", code)
		run, err := s.store.StartRun(rec, "cron", "")
		if err == nil {
			_ = s.store.FinishRun(run, nil, Result{
				Status: StatusSkipped,
				Error:  "no handler is registered for " + code,
			})
		}
		return
	}

	run, err := s.store.StartRun(rec, "cron", "")
	if err != nil {
		slog.Error("could not record the start of a run", "code", code, "error", err)
		return
	}

	result := Run(context.Background(), handler, Params(rec), OptionsFor(rec))

	if err := s.store.FinishRun(run, rec, result); err != nil {
		slog.Error("could not record the outcome of a run", "code", code, "error", err)
	}

	switch result.Status {
	case StatusSuccess:
		slog.Info("task finished", "code", code, "duration", result.Duration.String())
	case StatusSkipped:
		slog.Info("task skipped", "code", code, "reason", result.Output)
	default:
		slog.Error("task failed", "code", code, "status", result.Status, "error", result.Error)
	}
}

// RunNow executes a job immediately, outside the schedule.
//
// The scheduled time is not consumed: a manual run is an extra run, not an early
// one, so the next scheduled occurrence is left where it was.
func (s *Scheduler) RunNow(rec *core.Record, userID string) (Result, error) {
	handler, ok := Lookup(rec.GetString("code"))
	if !ok {
		return Result{}, ErrNoHandler
	}

	run, err := s.store.StartRun(rec, "manual", userID)
	if err != nil {
		return Result{}, err
	}

	result := Run(context.Background(), handler, Params(rec), OptionsFor(rec))

	if err := s.store.FinishRun(run, rec, result); err != nil {
		return result, err
	}
	return result, nil
}

// ErrNoHandler is returned when a job names a handler this binary does not have.
var ErrNoHandler = errNoHandler{}

type errNoHandler struct{}

func (errNoHandler) Error() string {
	return "no handler is registered for that task"
}
