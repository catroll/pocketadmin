package developer

import (
	"sync"
	"time"
)

// limiter is a fixed-window counter per key.
//
// A fixed window is the right shape here and a sliding one would be better in
// theory: the window boundary allows twice the limit across it, which for an API
// key is not worth the memory and the bookkeeping of tracking every request time.
// The limit exists to stop a runaway client, not to bill anybody.
type limiter struct {
	mu      sync.Mutex
	windows map[string]*window
}

type window struct {
	// started is when the current window opened.
	started time.Time

	// count is how many requests have been admitted in it.
	count int
}

// Limiter is the exported handle the server holds.
type Limiter = limiter

// NewLimiter returns an empty limiter.
func NewLimiter() *limiter {
	return &limiter{windows: map[string]*window{}}
}

// Allow reports whether a request may proceed, and how long to wait if not.
func (l *limiter) Allow(key string, limit int, now time.Time) (bool, time.Duration) {
	// A zero limit means no limit, which is what an installation that has not
	// configured one gets.
	if limit <= 0 {
		return true, 0
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	entry, ok := l.windows[key]
	if !ok || now.Sub(entry.started) >= time.Minute {
		l.windows[key] = &window{started: now, count: 1}
		l.pruneLocked(now)
		return true, 0
	}

	if entry.count >= limit {
		return false, time.Minute - now.Sub(entry.started)
	}

	entry.count++
	return true, 0
}

// pruneLocked drops windows that have expired.
//
// Without this the map grows with every key that has ever been used, which over a
// long-running process is a slow leak. It runs inside the lock and only on the
// path that opens a new window, so it costs nothing on the common path.
func (l *limiter) pruneLocked(now time.Time) {
	// Only sweep occasionally: a map of a few hundred entries does not need
	// visiting on every new window.
	if len(l.windows) < 64 {
		return
	}
	for key, entry := range l.windows {
		if now.Sub(entry.started) >= 2*time.Minute {
			delete(l.windows, key)
		}
	}
}

// RateLimitError describes a rejected request.
type RateLimitError struct {
	RetryAfter time.Duration
}

func (e RateLimitError) Error() string { return "developer: the rate limit for this key was exceeded" }
