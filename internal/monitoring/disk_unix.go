//go:build unix

package monitoring

import "golang.org/x/sys/unix"

// diskUsage returns the total and available bytes on the volume holding a path.
//
// It reports what the filesystem says rather than walking it: an operator needs to
// know how close the volume is to full, and a walk would both be slow and answer a
// different question.
func diskUsage(path string) (total, available uint64, err error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}

	// Bavail is what an unprivileged process may use, which is the number that
	// matters: reporting the total free space would overstate it by the reserved
	// blocks the process cannot touch.
	return stat.Blocks * uint64(stat.Bsize), stat.Bavail * uint64(stat.Bsize), nil
}
