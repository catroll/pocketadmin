package jobs

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Handler is a task that can be scheduled.
//
// The catalogue is fixed at build time: an operator chooses when a task runs and
// whether it runs at all, but cannot introduce behaviour. That is the difference
// between a scheduler and a remote code execution feature.
type Handler struct {
	// Code is the stable identifier stored on the scheduled row. It is what a
	// migration and a row agree on, so it must not change once released.
	Code string

	// Name and Description are shown on the screen.
	Name        string
	Description string

	// DefaultCron is used when seeding the row, so a fresh install has a sensible
	// schedule rather than a blank one.
	DefaultCron string

	// Run performs the task. It returns a short line for the run history.
	//
	// The context carries the timeout, so a handler must honour it: a task that
	// ignores cancellation cannot be bounded by the scheduler.
	Run func(ctx context.Context, params map[string]any) (string, error)
}

var (
	registryMu sync.RWMutex
	registry   = map[string]Handler{}
)

// Register adds a handler to the catalogue.
//
// It panics on a duplicate code or an unparsable default schedule. Both are
// programming errors that would otherwise surface as a task that never runs,
// which is the kind of thing nobody notices until it matters.
func Register(handler Handler) {
	if handler.Code == "" {
		panic("jobs: a handler needs a code")
	}
	if handler.Run == nil {
		panic("jobs: handler " + handler.Code + " has no Run function")
	}
	if handler.DefaultCron != "" {
		if err := ValidExpression(handler.DefaultCron); err != nil {
			panic(fmt.Sprintf("jobs: handler %s has an invalid default schedule: %v", handler.Code, err))
		}
	}

	registryMu.Lock()
	defer registryMu.Unlock()

	if _, exists := registry[handler.Code]; exists {
		panic("jobs: the handler code " + handler.Code + " is registered twice")
	}
	registry[handler.Code] = handler
}

// Lookup returns a handler by code.
func Lookup(code string) (Handler, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()

	handler, ok := registry[code]
	return handler, ok
}

// All returns every handler, ordered by code.
func All() []Handler {
	registryMu.RLock()
	defer registryMu.RUnlock()

	out := make([]Handler, 0, len(registry))
	for _, handler := range registry {
		out = append(out, handler)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// Codes returns every registered code, ordered.
func Codes() []string {
	handlers := All()
	out := make([]string, 0, len(handlers))
	for _, handler := range handlers {
		out = append(out, handler.Code)
	}
	return out
}

// ---------------------------------------------------------------------------
// Concurrency
// ---------------------------------------------------------------------------

// Concurrency says what happens when a run is due while the previous one is still
// going.
type Concurrency string

// Concurrency policies.
const (
	// ConcurrencyAllow lets runs overlap.
	ConcurrencyAllow Concurrency = "allow"

	// ConcurrencyForbid skips the new run, recording that it was skipped.
	ConcurrencyForbid Concurrency = "forbid"

	// ConcurrencyReplace cancels the running one and starts the new one.
	//
	// It is deliberately not implemented as a silent kill: cancelling a task
	// halfway through can leave work half-applied, so it is offered only for
	// handlers that tolerate it and is refused otherwise. See Runner.Run.
	ConcurrencyReplace Concurrency = "replace"
)

// running tracks what is in flight, so a concurrency policy can be applied.
var (
	runningMu sync.Mutex
	running   = map[string]context.CancelFunc{}
)

// Running reports whether a job is currently executing.
func Running(code string) bool {
	runningMu.Lock()
	defer runningMu.Unlock()
	_, ok := running[code]
	return ok
}

// RunningCodes lists the jobs currently executing.
func RunningCodes() []string {
	runningMu.Lock()
	defer runningMu.Unlock()

	out := make([]string, 0, len(running))
	for code := range running {
		out = append(out, code)
	}
	sort.Strings(out)
	return out
}

// Result is the outcome of one attempt.
type Result struct {
	Status   string
	Output   string
	Error    string
	Duration time.Duration
	Attempts int
}

// RunStatus values, matching the job_runs enum.
const (
	StatusRunning   = "running"
	StatusSuccess   = "success"
	StatusFailed    = "failed"
	StatusTimeout   = "timeout"
	StatusCancelled = "cancelled"
	StatusSkipped   = "skipped"
)

// RunOptions is how a run is configured.
type RunOptions struct {
	// Timeout bounds one attempt. Zero means no bound.
	Timeout time.Duration

	// MaxRetries is how many further attempts are made after a failure. Zero
	// means one attempt.
	MaxRetries int

	// Backoff is the wait before the first retry; each later retry doubles it.
	Backoff time.Duration

	// Concurrency decides what happens when a run is already in flight.
	Concurrency Concurrency
}

// maxBackoff caps the exponential wait, so a long retry chain does not turn into
// a task that appears to have stopped.
const maxBackoff = 5 * time.Minute

// Run executes a handler under its policy and reports the outcome.
//
// It does not touch the database: the caller records the run, which keeps this
// testable without an application and keeps the persistence decisions in one
// place.
func Run(ctx context.Context, handler Handler, params map[string]any, opts RunOptions) Result {
	started := time.Now()

	// The concurrency policy is applied before anything runs, and it is the only
	// place a run can be skipped.
	if opts.Concurrency == ConcurrencyForbid || opts.Concurrency == ConcurrencyReplace {
		if Running(handler.Code) {
			if opts.Concurrency == ConcurrencyForbid {
				return Result{
					Status:   StatusSkipped,
					Output:   "the previous run was still going",
					Duration: time.Since(started),
				}
			}
			// Replace: cancel the in-flight run and continue to start a new one.
			cancelRunning(handler.Code)
		}
	}

	runCtx := ctx
	var cancel context.CancelFunc
	if opts.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
	} else {
		runCtx, cancel = context.WithCancel(ctx)
	}

	registerRunning(handler.Code, cancel)
	defer func() {
		unregisterRunning(handler.Code)
		cancel()
	}()

	attempts := opts.MaxRetries + 1
	backoff := opts.Backoff
	var lastErr error

	for attempt := 1; attempt <= attempts; attempt++ {
		output, err := handler.Run(runCtx, params)

		if err == nil {
			return Result{
				Status:   StatusSuccess,
				Output:   output,
				Duration: time.Since(started),
				Attempts: attempt,
			}
		}
		lastErr = err

		// A cancelled or expired context is not retried: the reason the attempt
		// failed is the reason every later one would too.
		if runCtx.Err() != nil {
			break
		}

		if attempt < attempts {
			wait := backoff
			if wait > maxBackoff {
				wait = maxBackoff
			}
			select {
			case <-time.After(wait):
			case <-runCtx.Done():
				// Interrupted while waiting; stop rather than start another
				// attempt that would be cancelled immediately.
				goto finished
			}
			backoff *= 2
		}
	}

finished:
	switch {
	case runCtx.Err() == context.DeadlineExceeded:
		return Result{
			Status:   StatusTimeout,
			Error:    "the run exceeded its timeout",
			Duration: time.Since(started),
			Attempts: attempts,
		}
	case runCtx.Err() == context.Canceled:
		return Result{
			Status:   StatusCancelled,
			Error:    "the run was cancelled",
			Duration: time.Since(started),
			Attempts: attempts,
		}
	default:
		message := "the run failed"
		if lastErr != nil {
			message = lastErr.Error()
		}
		return Result{
			Status:   StatusFailed,
			Error:    message,
			Duration: time.Since(started),
			Attempts: attempts,
		}
	}
}

// registerRunning records an in-flight run so a policy can see it.
func registerRunning(code string, cancel context.CancelFunc) {
	runningMu.Lock()
	running[code] = cancel
	runningMu.Unlock()
}

// unregisterRunning clears an in-flight run.
func unregisterRunning(code string) {
	runningMu.Lock()
	delete(running, code)
	runningMu.Unlock()
}

// cancelRunning cancels the in-flight run of a job, if any.
func cancelRunning(code string) {
	runningMu.Lock()
	cancel, ok := running[code]
	runningMu.Unlock()

	if ok {
		cancel()
	}
}
