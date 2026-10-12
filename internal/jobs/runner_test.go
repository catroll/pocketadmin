package jobs_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"pocketadmin/internal/jobs"
)

// handler builds a handler with a unique code so tests do not collide in the
// package-level registry.
func handler(code string, run func(ctx context.Context) (string, error)) jobs.Handler {
	return jobs.Handler{Code: code, Name: code, Run: func(ctx context.Context, _ map[string]any) (string, error) {
		return run(ctx)
	}}
}

// TestRunSucceeds covers the ordinary case.
func TestRunSucceeds(t *testing.T) {
	result := jobs.Run(context.Background(),
		handler("test-ok", func(context.Context) (string, error) { return "did the thing", nil }),
		nil, jobs.RunOptions{})

	if result.Status != jobs.StatusSuccess {
		t.Fatalf("status = %q, want success", result.Status)
	}
	if result.Output != "did the thing" {
		t.Errorf("output = %q", result.Output)
	}
	if result.Attempts != 1 {
		t.Errorf("attempts = %d, want 1", result.Attempts)
	}
}

// TestRunRetriesWithBackoff: a transient failure should be retried, and the
// attempt count should say how many it took.
func TestRunRetriesWithBackoff(t *testing.T) {
	var mu sync.Mutex
	attempts := 0

	result := jobs.Run(context.Background(),
		handler("test-retry", func(context.Context) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			attempts++
			if attempts < 3 {
				return "", errors.New("not yet")
			}
			return "succeeded on the third attempt", nil
		}),
		nil, jobs.RunOptions{MaxRetries: 3, Backoff: time.Millisecond})

	if result.Status != jobs.StatusSuccess {
		t.Fatalf("status = %q, want success: %s", result.Status, result.Error)
	}
	if result.Attempts != 3 {
		t.Errorf("attempts = %d, want 3", result.Attempts)
	}
}

// TestRunGivesUpAfterMaxRetries: a permanent failure must not retry forever.
func TestRunGivesUpAfterMaxRetries(t *testing.T) {
	var mu sync.Mutex
	attempts := 0

	result := jobs.Run(context.Background(),
		handler("test-giveup", func(context.Context) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			attempts++
			return "", errors.New("permanent")
		}),
		nil, jobs.RunOptions{MaxRetries: 2, Backoff: time.Millisecond})

	if result.Status != jobs.StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if attempts != 3 {
		t.Errorf("the handler ran %d times, want 3 (one attempt plus two retries)", attempts)
	}
	if !strings.Contains(result.Error, "permanent") {
		t.Errorf("the error was not carried through: %q", result.Error)
	}
}

// TestRunHonoursTimeout: a handler that ignores its context cannot be bounded, so
// the scheduler has to bound it anyway.
func TestRunHonoursTimeout(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	result := jobs.Run(context.Background(),
		handler("test-timeout", func(ctx context.Context) (string, error) {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-release:
				return "finished", nil
			}
		}),
		nil, jobs.RunOptions{Timeout: 20 * time.Millisecond})

	if result.Status != jobs.StatusTimeout {
		t.Fatalf("status = %q, want timeout", result.Status)
	}
}

// TestRunDoesNotRetryATimeout: the reason the attempt failed is the reason every
// later one would, so retrying a timeout only wastes the window.
func TestRunDoesNotRetryATimeout(t *testing.T) {
	var mu sync.Mutex
	attempts := 0

	result := jobs.Run(context.Background(),
		handler("test-timeout-noretry", func(ctx context.Context) (string, error) {
			mu.Lock()
			attempts++
			mu.Unlock()
			<-ctx.Done()
			return "", ctx.Err()
		}),
		nil, jobs.RunOptions{Timeout: 10 * time.Millisecond, MaxRetries: 5, Backoff: time.Millisecond})

	if result.Status != jobs.StatusTimeout {
		t.Fatalf("status = %q, want timeout", result.Status)
	}
	if attempts != 1 {
		t.Errorf("the handler ran %d times; a timeout should not be retried", attempts)
	}
}

// TestForbidSkipsWhileRunning: the point of the policy is that the second run
// does not happen at all, and that it is recorded as skipped rather than lost.
func TestForbidSkipsWhileRunning(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		jobs.Run(context.Background(),
			handler("test-forbid", func(ctx context.Context) (string, error) {
				close(started)
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-release:
					return "done", nil
				}
			}),
			nil, jobs.RunOptions{Concurrency: jobs.ConcurrencyForbid})
	}()

	<-started
	result := jobs.Run(context.Background(),
		handler("test-forbid", func(context.Context) (string, error) { return "should not run", nil }),
		nil, jobs.RunOptions{Concurrency: jobs.ConcurrencyForbid})

	if result.Status != jobs.StatusSkipped {
		t.Errorf("status = %q, want skipped", result.Status)
	}
	if result.Output == "should not run" {
		t.Error("the second run executed despite the forbid policy")
	}

	close(release)
	wg.Wait()
}

// TestReplaceCancelsTheRunningRun: the policy has to actually cancel, or it is
// just a slower way of overlapping.
func TestReplaceCancelsTheRunningRun(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		result := jobs.Run(context.Background(),
			handler("test-replace", func(ctx context.Context) (string, error) {
				close(started)
				<-ctx.Done()
				close(cancelled)
				return "", ctx.Err()
			}),
			nil, jobs.RunOptions{Concurrency: jobs.ConcurrencyReplace})
		if result.Status != jobs.StatusCancelled {
			t.Errorf("the first run ended as %q, want cancelled", result.Status)
		}
	}()

	<-started

	// The replacing run uses a different handler code for its body so it does not
	// re-enter the same cancellation, but the same code for the policy.
	replacement := jobs.Handler{
		Code: "test-replace",
		Name: "test-replace",
		Run:  func(context.Context, map[string]any) (string, error) { return "replaced", nil },
	}
	result := jobs.Run(context.Background(), replacement, nil,
		jobs.RunOptions{Concurrency: jobs.ConcurrencyReplace})

	<-cancelled
	wg.Wait()

	if result.Status != jobs.StatusSuccess {
		t.Errorf("the replacing run ended as %q, want success", result.Status)
	}
}

// TestRunningReportsInFlight: the scheduler asks this to decide whether to start
// a run, so it has to be accurate while a handler is executing.
func TestRunningReportsInFlight(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		jobs.Run(context.Background(),
			handler("test-running", func(ctx context.Context) (string, error) {
				close(started)
				<-release
				return "", nil
			}), nil, jobs.RunOptions{})
	}()

	<-started
	if !jobs.Running("test-running") {
		t.Error("a job that is executing was not reported as running")
	}

	close(release)
	wg.Wait()

	if jobs.Running("test-running") {
		t.Error("a job that finished is still reported as running")
	}
}

// TestRegistryRejectsProblems: a duplicate code or an invalid default schedule is
// a programming error, and both would otherwise show up as a task that silently
// never runs.
func TestRegistryRejectsProblems(t *testing.T) {
	jobs.Register(handler("test-registry", func(context.Context) (string, error) { return "", nil }))

	assertPanics(t, "a duplicate code", func() {
		jobs.Register(handler("test-registry", func(context.Context) (string, error) { return "", nil }))
	})
	assertPanics(t, "a handler with no Run", func() {
		jobs.Register(jobs.Handler{Code: "test-no-run"})
	})
	assertPanics(t, "an invalid default schedule", func() {
		jobs.Register(jobs.Handler{
			Code:        "test-bad-cron",
			DefaultCron: "not a cron",
			Run:         func(context.Context, map[string]any) (string, error) { return "", nil },
		})
	})

	if _, ok := jobs.Lookup("test-registry"); !ok {
		t.Error("a registered handler was not found")
	}
	if _, ok := jobs.Lookup("never-registered"); ok {
		t.Error("an unregistered code was found")
	}
	if len(jobs.Codes()) == 0 {
		t.Error("the registry is empty")
	}
}

// assertPanics runs a function that is expected to panic.
func assertPanics(t *testing.T, what string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s did not panic", what)
		}
	}()
	fn()
}
