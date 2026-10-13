package server

import (
	"fmt"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/monitoring"
	"pocketadmin/internal/settings"
)

// hostMonitorView is the host section of the monitoring page.
//
// Every figure is pre-rendered, including the ones that are absent. A template that
// decided between a number and "not available" would have to know which readings a
// platform can produce, and that knowledge belongs with the reader.
type hostMonitorView struct {
	Enabled bool

	Interval  string
	Retention string

	// Sampled is when the newest reading was taken.
	Sampled string

	// Fresh is false when the newest reading is older than a few intervals, which
	// means the sampler is not running rather than that the machine is idle.
	Fresh bool

	CPU    string
	Load   string
	Memory string
	Disk   string

	// Network rates, derived from the two newest readings. A rate needs two samples:
	// the counters are cumulative, so one reading cannot say how fast anything moved.
	NetRx string
	NetTx string

	Uptime string

	// Unsupported names what this platform could not report.
	Unsupported []string

	// Recent is the history, newest first.
	Recent []hostSampleRow
}

// hostSampleRow is one reading, rendered.
type hostSampleRow struct {
	At     string
	CPU    string
	Load   string
	Memory string
	Disk   string
	NetRx  string
	NetTx  string
}

// hostMonitor builds the host section.
func (s *Server) hostMonitor(now time.Time) hostMonitorView {
	policy := s.hostSamplePolicy()

	view := hostMonitorView{
		Enabled:   policy.Enabled,
		Interval:  humanDuration(policy.Interval),
		Retention: retentionLabel(policy.RetentionDays),
	}

	// Newest first, so the rate below is a difference between row zero and row one.
	records, err := s.app.FindRecordsByFilter(monitoring.CollectionHostMetrics,
		"", "-at", 60, 0)
	if err != nil || len(records) == 0 {
		return view
	}

	view.Fresh = now.Sub(records[0].GetDateTime("at").Time()) < freshWindow(policy.Interval)

	for i, rec := range records {
		row := hostSampleRow{
			At:     rec.GetDateTime("at").Time().Local().Format("15:04:05"),
			CPU:    cpuLabel(sampleAt(rec)),
			Load:   loadLabel(sampleAt(rec)),
			Memory: percentLabel(sampleAt(rec).MemPercent()),
			Disk:   percentLabel(sampleAt(rec).DiskPercent()),
			NetRx:  "—",
			NetTx:  "—",
		}

		// The rate is against the previous row, which is the next one in this list.
		if i+1 < len(records) {
			older := sampleAt(records[i+1])
			newer := sampleAt(rec)
			if seconds := newer.At.Sub(older.At).Seconds(); seconds > 0 {
				row.NetRx = byteRate(newer.NetRxBytes, older.NetRxBytes, seconds)
				row.NetTx = byteRate(newer.NetTxBytes, older.NetTxBytes, seconds)
			}
		}

		view.Recent = append(view.Recent, row)
	}

	newest := sampleAt(records[0])
	view.Sampled = records[0].GetDateTime("at").Time().Local().Format("2 Jan 15:04:05")
	view.CPU = cpuLabel(newest)
	view.Load = loadLabel(newest)
	view.Memory = percentLabel(newest.MemPercent())
	view.Disk = percentLabel(newest.DiskPercent())
	view.Uptime = uptimeLabel(newest.UptimeSeconds)
	view.Unsupported = newest.Unsupported

	if len(view.Recent) > 0 {
		view.NetRx = view.Recent[0].NetRx
		view.NetTx = view.Recent[0].NetTx
	}

	return view
}

// sampleAt reconstructs a sample from a stored row.
func sampleAt(rec *core.Record) monitoring.HostSample {
	return monitoring.HostSample{
		At:              rec.GetDateTime("at").Time(),
		CPUUsedSeconds:  rec.GetFloat("cpuUsedSeconds"),
		CPUTotalSeconds: rec.GetFloat("cpuTotalSeconds"),
		Load1:           rec.GetFloat("load1"),
		Load5:           rec.GetFloat("load5"),
		Load15:          rec.GetFloat("load15"),
		MemUsedBytes:    uint64(rec.GetFloat("memUsedBytes")),
		MemTotalBytes:   uint64(rec.GetFloat("memTotalBytes")),
		DiskUsedBytes:   uint64(rec.GetFloat("diskUsedBytes")),
		DiskTotalBytes:  uint64(rec.GetFloat("diskTotalBytes")),
		NetRxBytes:      uint64(rec.GetFloat("netRxBytes")),
		NetTxBytes:      uint64(rec.GetFloat("netTxBytes")),
		UptimeSeconds:   uint64(rec.GetFloat("uptimeSeconds")),
		Unsupported:     rec.GetStringSlice("unsupported"),
	}
}

// cpuLabel renders processor usage, which is a difference between two counters.
//
// The stored figures are totals since boot, so a single row cannot yield a percentage.
// The sampler computes it against the sample before, and what is stored here is that
// figure's numerator and denominator -- which is why the label needs both.
func cpuLabel(sample monitoring.HostSample) string {
	percent, ok := sample.CPUPercent()
	if !ok {
		return "—"
	}
	return fmt.Sprintf("%.1f%%", percent)
}

// loadLabel renders the one-minute load average.
func loadLabel(sample monitoring.HostSample) string {
	// Zero load is indistinguishable from a platform that does not report it, and the
	// unsupported list is what tells them apart.
	if sample.Load1 == 0 && sample.Load5 == 0 && sample.Load15 == 0 {
		if containsStringValue(sample.Unsupported, "load") {
			return "—"
		}
	}
	return fmt.Sprintf("%.2f", sample.Load1)
}

// percentLabel renders an optional percentage.
func percentLabel(percent float64, ok bool) string {
	if !ok {
		return "—"
	}
	return fmt.Sprintf("%.1f%%", percent)
}

// byteRate renders a counter difference over an interval.
func byteRate(newer, older uint64, seconds float64) string {
	if seconds <= 0 || newer < older {
		// A counter that went backwards means the interface was reset, which happens
		// and would otherwise render as an enormous negative rate.
		return "—"
	}
	return formatBytes(float64(newer-older)/seconds) + "/s"
}

// humanDuration renders an interval for a reader.
func humanDuration(d time.Duration) string {
	switch {
	case d <= 0:
		return "off"
	case d < time.Minute:
		return fmt.Sprintf("%d seconds", int(d.Seconds()))
	case d == time.Minute:
		return "1 minute"
	case d < time.Hour:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	default:
		return fmt.Sprintf("%.1f hours", d.Hours())
	}
}

// retentionLabel renders the retention window.
func retentionLabel(days int) string {
	if days <= 0 {
		return "kept for ever"
	}
	if days == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", days)
}

// uptimeLabel renders how long the machine has been up.
func uptimeLabel(seconds uint64) string {
	if seconds == 0 {
		return "—"
	}

	d := time.Duration(seconds) * time.Second
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	}
}

// freshWindow is how stale a reading may be before the sampler is presumed stopped.
//
// Three intervals plus a little, so a single missed tick is not reported as a failure
// while a genuinely stopped sampler is.
func freshWindow(interval time.Duration) time.Duration {
	if interval <= 0 {
		interval = time.Minute
	}
	return 3*interval + 10*time.Second
}

// containsStringValue reports whether a slice holds a value.
func containsStringValue(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// hostSettingsSite is where an operator changes the sampling configuration.
func hostSettingsSite() string { return "/settings/admin" }

// ensure the settings package is referenced even when the policy is the default.
var _ = settings.Admin
