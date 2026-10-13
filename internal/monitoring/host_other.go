//go:build !linux

package monitoring

import "time"

// otherReader reports that the machine cannot be read.
//
// It is not an error and not a crash: the panel runs on a platform without /proc, and
// a screen that says "not available on this platform" is more use than one that shows
// zeroes as though they were measurements.
type otherReader struct{}

// NewHostReader returns a reader that reports nothing on this platform.
func NewHostReader(string) HostReader { return otherReader{} }

func (otherReader) Read(_ *HostSample, now time.Time) HostSample {
	return HostSample{
		At: now,
		Unsupported: []string{
			"cpu", "load", "memory", "disk", "network",
		},
	}
}
