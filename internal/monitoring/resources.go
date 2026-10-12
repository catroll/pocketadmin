package monitoring

import (
	"os"
	"path/filepath"
	"time"
)

// DatabaseInfo describes what the database costs on disk.
type DatabaseInfo struct {
	// MainBytes is the size of the main database file.
	MainBytes int64

	// AuxBytes is the size of the auxiliary database, which PocketBase uses for
	// logs and which grows separately from the data.
	AuxBytes int64

	// WALBytes is the write-ahead log. A log that stays large means checkpoints
	// are not completing, which is worth seeing before it fills the disk.
	WALBytes int64

	// TableCount is how many tables the database holds.
	TableCount int

	// DataDir is where the files live.
	DataDir string

	// AvailableBytes and TotalBytes are the free and total space on the volume.
	AvailableBytes uint64
	TotalBytes     uint64
}

// ReadDatabase measures the database files.
//
// It measures files rather than querying the database for its own size because a
// size query is one more thing that fails when the database is the problem, and
// the point of this panel is to work when something else does not.
func ReadDatabase(dataDir string) DatabaseInfo {
	info := DatabaseInfo{DataDir: dataDir}

	info.MainBytes = fileSize(filepath.Join(dataDir, "data.db"))
	info.AuxBytes = fileSize(filepath.Join(dataDir, "auxiliary.db"))
	info.WALBytes = fileSize(filepath.Join(dataDir, "data.db-wal"))

	if total, available, err := diskUsage(dataDir); err == nil {
		info.TotalBytes = total
		info.AvailableBytes = available
	}

	return info
}

// SetTableCount records how many tables the database holds.
func (info *DatabaseInfo) SetTableCount(count int) { info.TableCount = count }

// fileSize returns a file's size, or zero when it is absent.
//
// Absence is not an error: a database with no write-ahead log at the moment of
// reading is normal, and treating it as a failure would make the panel report a
// problem that is not one.
func fileSize(path string) int64 {
	stat, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return stat.Size()
}

// StorageInfo describes the file library.
type StorageInfo struct {
	// FileCount and TotalBytes are the live files in the library.
	FileCount  int
	TotalBytes int64

	// DirectoryBytes is what the storage directory actually occupies, which is
	// larger than the live total when objects have been deduplicated or orphaned.
	DirectoryBytes int64

	// Growth24hBytes is how much the live total grew in the last day.
	Growth24hBytes int64

	// Growth24hCount is how many files were added in the last day.
	Growth24hCount int
}

// ReadStorageDirectory measures the storage directory on disk.
func ReadStorageDirectory(dir string) int64 {
	var total int64

	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		total += info.Size()
		return nil
	})

	return total
}

// Since24Hours is the cutoff for the growth figures.
func Since24Hours(now time.Time) time.Time { return now.Add(-24 * time.Hour) }

// uptime renders a duration for display.
func Uptime(started time.Time) time.Duration { return time.Since(started) }
