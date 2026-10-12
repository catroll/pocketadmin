//go:build !unix

package monitoring

import "errors"

// diskUsage is unavailable on this platform, and saying so is better than
// reporting zero, which would read as a full disk.
func diskUsage(string) (total, available uint64, err error) {
	return 0, 0, errors.New("monitoring: disk usage is not implemented on this platform")
}
