package monitoring

import (
	"time"
)

// HostSample is one reading of the machine the process is running on.
//
// Everything is in the units the kernel reports rather than in units that are nice to
// display. Percentages would be a lie about CPU, whose figure is a ratio over an
// interval rather than an instant, and network totals are **cumulative byte counters**
// rather than rates -- a rate needs two samples and pretending otherwise would mean
// storing a figure whose meaning depends on when the previous row was written.
//
// Every field is optional. A platform that cannot report one leaves it zero, and zero
// is rendered as "unknown" rather than as a measurement of nothing.
type HostSample struct {
	At time.Time

	// CPUSeconds is the processor time consumed since the previous sample, in user
	// plus system plus idle seconds. The usage percentage is derived from it. It is
	// reported this way rather than as a percentage because the percentage needs the
	// interval, and the interval is known to the caller that scheduled the samples.
	CPUUsedSeconds  float64
	CPUTotalSeconds float64

	Load1  float64
	Load5  float64
	Load15 float64

	MemUsedBytes  uint64
	MemTotalBytes uint64

	DiskUsedBytes  uint64
	DiskTotalBytes uint64

	// NetRxBytes and NetTxBytes are cumulative totals across the non-loopback
	// interfaces. A rate is the difference between two samples divided by the time
	// between them.
	NetRxBytes uint64
	NetTxBytes uint64

	// UptimeSeconds is how long the machine has been up, when it is known.
	UptimeSeconds uint64

	// Unsupported names what this platform could not report, so a screen can say so
	// rather than showing a confident zero.
	Unsupported []string
}

// CPUPercent returns the processor usage over the interval the sample covers.
//
// It is derived here rather than stored, so the stored row keeps the raw counters and
// the figure can be recomputed if the interval is read differently later.
func (s HostSample) CPUPercent() (float64, bool) {
	if s.CPUTotalSeconds <= 0 {
		return 0, false
	}
	percent := s.CPUUsedSeconds / s.CPUTotalSeconds * 100
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	return percent, true
}

// MemPercent returns memory in use as a percentage.
func (s HostSample) MemPercent() (float64, bool) {
	if s.MemTotalBytes == 0 {
		return 0, false
	}
	return float64(s.MemUsedBytes) / float64(s.MemTotalBytes) * 100, true
}

// DiskPercent returns the space in use as a percentage.
func (s HostSample) DiskPercent() (float64, bool) {
	if s.DiskTotalBytes == 0 {
		return 0, false
	}
	return float64(s.DiskUsedBytes) / float64(s.DiskTotalBytes) * 100, true
}

// HostReader reads the machine.
//
// It is an interface so the sampler can be tested without a /proc, and so a platform
// that reports nothing can be represented by one that returns an empty sample rather
// than by a build tag at every call site.
type HostReader interface {
	// Read returns the current reading. The previous reading is supplied because CPU
	// usage and network rates are differences: a reader that has no previous sample
	// reports the counters it can and leaves the differences zero.
	Read(previous *HostSample, now time.Time) HostSample
}
