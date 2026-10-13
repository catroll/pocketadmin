package demo

import (
	"time"

	"pocketadmin/internal/audit"
)

// telemetry writes the monitoring readings and a handful of log entries.
//
// The logs exist so the audit screens have something to show. They are written directly
// rather than by performing the actions they describe: a demonstration does not need to
// sign in twelve times to have a sign-in history, and doing so would be slower and no more
// truthful.

// telemetry writes host readings, a snapshot and the log entries.
func (s *Seeder) telemetry() error {
	// The readings go on the machine this process runs on, so the demonstration's charts
	// describe a real machine rather than an invented one.
	node, err := ensureLocalNode(s.app)
	if err != nil {
		return err
	}

	// A day of readings, one every fifteen minutes, so every window on the chart screen
	// has something in it. The values wander rather than sitting flat, because a flat
	// line demonstrates nothing about a chart.
	const readings = 96
	for i := readings; i > 0; i-- {
		at := s.now.Add(-time.Duration(i) * 15 * time.Minute)

		// A gentle daily curve plus a little variation, so the line has a shape.
		phase := float64(i) / float64(readings)
		cpuUsed := 12 + 8*phase + float64((i*7)%11)
		cpuTotal := 100.0
		memoryUsed := uint64((48 + 6*phase + float64((i*3)%9)) * 1024 * 1024 * 1024 / 100)
		diskUsed := uint64(72 * 1024 * 1024 * 1024 * 1024 / 100)

		if _, err := s.create("host_metrics", map[string]any{
			"node":            node.Id,
			"at":              at.UTC(),
			"cpuUsedSeconds":  cpuUsed,
			"cpuTotalSeconds": cpuTotal,
			"load1":           0.6 + 1.4*phase,
			"load5":           0.5 + 1.2*phase,
			"load15":          0.4 + 1.0*phase,
			"memUsedBytes":    memoryUsed,
			"memTotalBytes":   uint64(16) * 1024 * 1024 * 1024,
			"diskUsedBytes":   diskUsed,
			"diskTotalBytes":  uint64(100) * 1024 * 1024 * 1024 * 1024,
			"netRxBytes":      uint64(4_000_000_000 + i*3_000_000),
			"netTxBytes":      uint64(9_000_000_000 + i*7_000_000),
			"uptimeSeconds":   uint64(86400 * 12),
		}); err != nil {
			return err
		}
	}

	// The machine row is not written by `create`, so its count is added here; without it
	// the summary would say the readings were written and not that a machine now has
	// them.
	if err := recordNodeSample(s.app, node, s.now); err != nil {
		return err
	}

	if _, err := s.create("metric_snapshots", map[string]any{
		"at":           s.hoursAgo(1).UTC(),
		"goroutines":   48,
		"heapBytes":    12 * 1024 * 1024,
		"fileCount":    6,
		"requestsRate": 2.4,
		"errorRate":    0.0,
		"latencyP95":   18,
	}); err != nil {
		return err
	}

	// A few sign-ins, most of them fine and one that is not, so the security screen has
	// a story rather than a wall of successes.
	signIns := []struct {
		email, result, reason, risk string
		hoursAgo                    int
	}{
		{"demo.alice@example.com", "success", "", "low", 26},
		{"demo.bob@example.com", "success", "", "low", 25},
		{"demo.carol@example.com", "success", "", "low", 8},
		{"demo.dan@example.com", "failure", "wrong password", "medium", 6},
		{"demo.dan@example.com", "success", "", "low", 6},
	}
	for _, signIn := range signIns {
		if _, err := s.create("login_logs", map[string]any{
			"context":    "admin",
			"identifier": signIn.email,
			"result":     signIn.result,
			"reason":     signIn.reason,
			"riskLevel":  signIn.risk,
			"ip":         "203.0.113.10",
			"userAgent":  "Mozilla/5.0 (demo)",
		}); err != nil {
			return err
		}
	}

	// The event names come from the audit module's own catalogue, because the column is
	// a closed set: a name invented here would be refused, and rightly -- an event the
	// code can never emit is not an event.
	for _, event := range []struct {
		event, summary, severity string
	}{
		{string(audit.EventSettingsChanged), "Changed the back-office settings in console", "warning"},
		{string(audit.EventAPIKeyCreated), "Issued an API key for the warehouse integration", "warning"},
		{string(audit.EventLoginAnomaly), "Signed in from an unfamiliar address", "warning"},
	} {
		if _, err := s.create("security_audit_logs", map[string]any{
			"event":      event.event,
			"summary":    event.summary,
			"severity":   event.severity,
			"actorType":  "user",
			"actorEmail": "demo.alice@example.com",
			"module":     "demo",
		}); err != nil {
			return err
		}
	}

	for _, entry := range []struct {
		method, path string
		status       int
		ms           int
	}{
		{"GET", "/orders", 200, 34},
		{"POST", "/orders/create", 303, 128},
		{"GET", "/catalog/products", 200, 51},
		{"POST", "/payments/reconcile", 200, 240},
	} {
		if _, err := s.create("operation_logs", map[string]any{
			"actorType":  "user",
			"actorEmail": "demo.alice@example.com",
			"method":     entry.method,
			"path":       entry.path,
			"result":     "success",
			"statusCode": entry.status,
			"durationMs": entry.ms,
			"ip":         "203.0.113.10",
		}); err != nil {
			return err
		}
	}

	if _, err := s.create("exception_logs", map[string]any{
		"message":  "Example exception: the demo dataset records one so the screen is not empty",
		"module":   "demo",
		"level":    "error",
		"path":     "/orders/create",
		"method":   "POST",
		"resolved": false,
	}); err != nil {
		return err
	}

	if _, err := s.create("system_updates", map[string]any{
		"version":      "1.1.0",
		"fromVersion":  "1.0.0",
		"channel":      "stable",
		"status":       "applied",
		"releaseNotes": "<p>Example record from the demo dataset.</p>",
		"appliedAt":    s.daysAgo(3).UTC(),
	}); err != nil {
		return err
	}

	return nil
}
