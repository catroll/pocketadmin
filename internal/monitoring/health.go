package monitoring

import (
	"context"
	"runtime"
	"time"
)

// Component statuses.
const (
	StatusOK       = "ok"
	StatusDegraded = "degraded"
	StatusDown     = "down"
)

// Check is one component's liveness.
type Check struct {
	Name string

	// Status is the component's own verdict.
	Status string

	// Detail explains a non-ok status. It is shown on the panel and deliberately
	// kept out of the public endpoint.
	Detail string

	// Duration is how long the check itself took.
	Duration time.Duration
}

// Health is the whole picture.
type Health struct {
	Status    string
	Checks    []Check
	CheckedAt time.Time
	Duration  time.Duration
}

// Overall reduces the component checks to one verdict.
//
// The worst component decides. A process that is running while its database is
// unreachable is not healthy, and reporting "ok" because the web server answered
// is how a health check becomes worse than none.
func Overall(checks []Check) string {
	status := StatusOK
	for _, check := range checks {
		switch check.Status {
		case StatusDown:
			return StatusDown
		case StatusDegraded:
			status = StatusDegraded
		}
	}
	return status
}

// CheckDatabase pings the database.
func CheckDatabase(ctx context.Context, ping func(context.Context) error) Check {
	started := time.Now()
	check := Check{Name: "database"}

	done := make(chan error, 1)
	go func() { done <- ping(ctx) }()

	select {
	case err := <-done:
		if err != nil {
			check.Status = StatusDown
			check.Detail = err.Error()
		} else {
			check.Status = StatusOK
		}
	case <-time.After(3 * time.Second):
		// A ping that never returns is a database that is not answering, and
		// waiting for it would make the health endpoint itself unhealthy.
		check.Status = StatusDown
		check.Detail = "the database did not answer within 3 seconds"
	}

	check.Duration = time.Since(started)
	return check
}

// CheckScheduler reports whether the job scheduler is running.
func CheckScheduler(running bool, lastTick time.Time) Check {
	check := Check{Name: "scheduler"}

	if !running {
		check.Status = StatusDown
		check.Detail = "the scheduler is not running, so no scheduled task will fire"
		return check
	}

	// A scheduler that is "running" but has not ticked in several minutes is stuck,
	// which is the failure a liveness check exists to catch. The tick interval is
	// twenty seconds, so three minutes is comfortably beyond a missed tick and well
	// inside the shortest schedule an operator can write.
	if !lastTick.IsZero() && time.Since(lastTick) > 3*time.Minute {
		check.Status = StatusDegraded
		check.Detail = "the scheduler has not ticked recently"
		return check
	}

	check.Status = StatusOK
	return check
}

// CheckStorage reports whether the file library can reach its backing store.
func CheckStorage(available bool, detail string) Check {
	check := Check{Name: "storage"}
	if available {
		check.Status = StatusOK
		return check
	}

	// Degraded rather than down: the panel works without the file library, and a
	// single unhealthy subsystem should not take the whole service out of a load
	// balancer.
	check.Status = StatusDegraded
	check.Detail = detail
	return check
}

// CheckApplication is the process itself, which is trivially alive if this runs.
func CheckApplication(version string) Check {
	return Check{Name: "application", Status: StatusOK, Detail: version}
}

// RuntimeInfo is what the process is doing with memory and goroutines.
type RuntimeInfo struct {
	GoVersion    string
	GOOS         string
	GOARCH       string
	NumCPU       int
	Goroutines   int
	HeapBytes    uint64
	HeapInUse    uint64
	StackBytes   uint64
	TotalAlloc   uint64
	SysBytes     uint64
	GCCount      uint32
	LastGC       time.Time
	GCPauseMaxMs float64
	Uptime       time.Duration
}

// ReadRuntime collects the runtime picture.
//
// runtime.ReadMemStats stops the world briefly, so it is called on demand rather
// than on the request path. That is also why the traffic metrics are collected
// separately and cheaply.
func ReadRuntime(started time.Time) RuntimeInfo {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)

	info := RuntimeInfo{
		GoVersion:  runtime.Version(),
		GOOS:       runtime.GOOS,
		GOARCH:     runtime.GOARCH,
		NumCPU:     runtime.NumCPU(),
		Goroutines: runtime.NumGoroutine(),
		HeapBytes:  stats.HeapAlloc,
		HeapInUse:  stats.HeapInuse,
		StackBytes: stats.StackInuse,
		TotalAlloc: stats.TotalAlloc,
		SysBytes:   stats.Sys,
		GCCount:    stats.NumGC,
		Uptime:     time.Since(started),
	}

	if stats.NumGC > 0 {
		info.LastGC = time.Unix(0, int64(stats.LastGC))
		// The largest pause across the whole run, not the most recent one: a pause
		// of 400 ms an hour ago is the thing an operator needs to know about, and
		// the pause since the last collection is usually zero.
		info.GCPauseMaxMs = float64(stats.PauseNs[(stats.NumGC+255)%256]) / 1e6
		for _, pause := range stats.PauseNs {
			if ms := float64(pause) / 1e6; ms > info.GCPauseMaxMs {
				info.GCPauseMaxMs = ms
			}
		}
	}

	return info
}
