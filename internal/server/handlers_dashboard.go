package server

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"

	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"
)

// maxChartSeries caps how many collections appear in the dashboard chart.
const maxChartSeries = 12

// handleDashboard renders the overview page: headline counters, a per-collection
// record chart and the storage footprint.
//
// It deliberately does not list the collections. There is a page for that, reachable
// from the navigation, and a copy of its table on the dashboard is a second thing to
// keep in step that nobody reads twice.
func (s *Server) handleDashboard(c *gin.Context) {
	collections, err := s.app.FindAllCollections()
	if err != nil {
		s.renderErrorPage(c, http.StatusInternalServerError,
			"Could not load collections", err.Error())
		return
	}

	stats := make([]collectionStat, 0, len(collections))
	var totalRecords, authRecords, superusers int64

	for _, col := range collections {
		if hiddenCollections[col.Name] {
			continue
		}

		stat := collectionStat{
			Name:       col.Name,
			Type:       col.Type,
			System:     col.System,
			FieldCount: len(col.Fields),
			IsView:     col.IsView(),
		}

		if !col.IsView() {
			if n, err := s.app.CountRecords(col); err == nil {
				stat.RecordCount = n
				totalRecords += n
			}
		}

		if col.IsAuth() {
			authRecords += stat.RecordCount
			if col.Name == core.CollectionNameSuperusers {
				superusers = stat.RecordCount
			}
		}

		stats = append(stats, stat)
	}

	sortCollectionStats(stats)

	// Build the chart from the busiest collections so the bars stay readable.
	chart := chartData{}
	for _, stat := range stats {
		if stat.RecordCount <= 0 || stat.IsView {
			continue
		}
		if len(chart.Labels) >= maxChartSeries {
			break
		}
		chart.Labels = append(chart.Labels, stat.Name)
		chart.Values = append(chart.Values, stat.RecordCount)
	}

	databaseBytes, uploadBytes, fileCount := s.storageUsage()

	s.render(c, http.StatusOK, "dashboard.html", dashboardPage{
		base: s.newBase(c, "Dashboard", "dashboard"),
		Stats: dashboardStats{
			Collections:   len(stats),
			Records:       totalRecords,
			Superusers:    superusers,
			AuthRecords:   authRecords,
			Files:         fileCount,
			DataDir:       s.app.DataDir(),
			DatabaseBytes: databaseBytes,
			UploadBytes:   uploadBytes,
		},
		Chart: chart,
	})
}

// handleCollections renders the full collection index.
func (s *Server) handleCollections(c *gin.Context) {
	collections, err := s.app.FindAllCollections()
	if err != nil {
		s.renderErrorPage(c, http.StatusInternalServerError,
			"Could not load collections", err.Error())
		return
	}

	stats := make([]collectionStat, 0, len(collections))
	var totalRecords int64

	for _, col := range collections {
		stat := collectionStat{
			Name:       col.Name,
			Type:       col.Type,
			System:     col.System,
			FieldCount: len(col.Fields),
			IsView:     col.IsView(),
		}

		if !col.IsView() {
			if n, err := s.app.CountRecords(col); err == nil {
				stat.RecordCount = n
				totalRecords += n
			}
		}

		stats = append(stats, stat)
	}

	sortCollectionStats(stats)

	s.render(c, http.StatusOK, "collections.html", collectionsPage{
		base:        s.newBase(c, "Collections", "collections"),
		Collections: stats,
		TotalRecs:   totalRecords,
	})
}

// sortCollectionStats orders user collections first and the busiest first
// within each group, which keeps the internal/system tables at the bottom.
func sortCollectionStats(stats []collectionStat) {
	sort.SliceStable(stats, func(i, j int) bool {
		a, b := stats[i], stats[j]

		if a.System != b.System {
			return !a.System
		}
		if a.RecordCount != b.RecordCount {
			return a.RecordCount > b.RecordCount
		}
		return a.Name < b.Name
	})
}

// storageUsage reports the SQLite footprint and the size of uploaded files.
func (s *Server) storageUsage() (dbBytes, uploadBytes int64, files int) {
	dataDir := s.app.DataDir()

	for _, name := range []string{"data.db", "data.db-wal", "data.db-shm", "auxiliary.db"} {
		if info, err := os.Stat(filepath.Join(dataDir, name)); err == nil {
			dbBytes += info.Size()
		}
	}

	_ = filepath.WalkDir(filepath.Join(dataDir, core.LocalStorageDirName),
		func(_ string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return nil
			}
			if info, err := entry.Info(); err == nil {
				uploadBytes += info.Size()
				files++
			}
			return nil
		})

	return dbBytes, uploadBytes, files
}
