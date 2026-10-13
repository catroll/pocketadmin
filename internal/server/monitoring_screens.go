package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	"pocketadmin/internal/audit"
	"pocketadmin/internal/monitoring"
	"pocketadmin/internal/permission"
	versionpkg "pocketadmin/internal/version"
)

// registerMonitoringRoutes mounts the monitoring screens.
func (s *Server) registerMonitoringRoutes(panel *gin.RouterGroup) {
	s.readGuarded(panel, http.MethodGet, "/monitoring", permission.MonitoringDashboard, s.handleMonitoring)
	s.readGuarded(panel, http.MethodGet, "/monitoring/system", permission.MonitoringSystemInfo, s.handleSystemInfo)

	s.guarded(panel, http.MethodPost, "/monitoring/snapshot", permission.MonitoringSnapshotRun, s.handleSnapshotNow)
}

// registerHealthRoutes mounts the health endpoints.
//
// There are two, and the split is deliberate.
//
// `/healthz` is unauthenticated, because a load balancer or an orchestrator has to
// reach it and has no session. It answers with one word and nothing else. A health
// endpoint that names the failing component to an anonymous caller is a free map of
// the system for anyone who asks.
//
// The detailed view lives behind a permission, and it is the one an operator uses.
func (s *Server) registerHealthRoutes(r *gin.Engine) {
	r.GET("/healthz", s.handlePublicHealth)
	r.GET("/readyz", s.handleReadiness)
}

// handlePublicHealth answers whether the process is serving.
func (s *Server) handlePublicHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// handleReadiness answers whether the process can serve requests, which is a
// different question from whether it is alive.
//
// A process whose database is unreachable is alive and should be taken out of a
// load balancer's rotation, not killed. Reporting the difference is what lets an
// orchestrator do the right one.
func (s *Server) handleReadiness(c *gin.Context) {
	health := s.health(c.Request.Context())

	status := http.StatusOK
	if health.Status == monitoring.StatusDown {
		status = http.StatusServiceUnavailable
	}
	// No detail. A caller that needs it holds the permission for the panel.
	c.JSON(status, gin.H{"status": health.Status})
}

// health gathers the component checks.
func (s *Server) health(ctx context.Context) monitoring.Health {
	started := time.Now()

	checks := []monitoring.Check{
		monitoring.CheckApplication(s.versionString()),
		monitoring.CheckDatabase(ctx, s.pingDatabase),
		monitoring.CheckScheduler(s.scheduler != nil, s.schedulerLastTick()),
	}

	detail := ""
	available := s.files != nil
	if !available {
		detail = "no storage driver was initialised, so the file library is unavailable"
	}
	checks = append(checks, monitoring.CheckStorage(available, detail))

	return monitoring.Health{
		Status:    monitoring.Overall(checks),
		Checks:    checks,
		CheckedAt: time.Now(),
		Duration:  time.Since(started),
	}
}

// handleMonitoring renders the dashboard.
func (s *Server) handleMonitoring(c *gin.Context) {
	now := time.Now()
	runtimeInfo := monitoring.ReadRuntime(s.metrics.Started())

	database := monitoring.ReadDatabase(s.app.DataDir())
	if count, err := s.tableCount(); err == nil {
		database.SetTableCount(count)
	}

	storage := s.storageInfo(now)
	traffic := s.metrics.Snapshot()

	actor := s.actor(c)
	s.render(c, http.StatusOK, "monitoring.html", monitoringPage{
		base:         s.newBase(c, "Monitoring", "monitoring"),
		Health:       s.health(c.Request.Context()),
		Runtime:      runtimeInfo,
		Database:     database,
		Storage:      storage,
		Traffic:      traffic,
		Recent:       s.metrics.RecentRequests(),
		RecentError:  s.metrics.RecentErrors(),
		CanSeeHealth: actor.Can(permission.MonitoringHealth),
		CanSnapshot:  actor.Can(permission.MonitoringSnapshotRun),
		Snapshots:    s.recentSnapshots(),
		Host:         s.hostMonitor(now),
		HostSettings: hostSettingsSite(),
	})
}

// storageInfo measures the file library.
func (s *Server) storageInfo(now time.Time) monitoring.StorageInfo {
	info := monitoring.StorageInfo{}

	files, err := s.app.FindAllRecords("files")
	if err == nil {
		for _, rec := range files {
			info.FileCount++
			info.TotalBytes += int64(rec.GetFloat("size"))

			// The growth figures are computed from the rows rather than from the
			// directory, because a deduplicated or orphaned object is not a file
			// anybody uploaded.
			if rec.GetDateTime("created").Time().After(monitoring.Since24Hours(now)) {
				info.Growth24hBytes += int64(rec.GetFloat("size"))
				info.Growth24hCount++
			}
		}
	}

	if dir := s.cfg.StorageDir; dir != "" {
		info.DirectoryBytes = monitoring.ReadStorageDirectory(dir)
	}
	return info
}

// recentSnapshots returns the most recent periodic measurements.
func (s *Server) recentSnapshots() []monitoringSnapshotRow {
	records, err := s.app.FindRecordsByFilter("metric_snapshots",
		"", "-capturedAt", 96, 0)
	if err != nil {
		return nil
	}

	out := make([]monitoringSnapshotRow, 0, len(records))
	for _, rec := range records {
		row := monitoringSnapshotRow{
			When:         rec.GetDateTime("capturedAt"),
			Goroutines:   rec.GetInt("goroutines"),
			HeapBytes:    int64(rec.GetFloat("heapBytes")),
			FileCount:    rec.GetInt("fileCount"),
			RequestsRate: rec.GetFloat("requestsPerMinute"),
			ErrorRate:    rec.GetFloat("errorRate"),
			LatencyP95:   rec.GetFloat("latencyP95Ms"),
		}
		out = append(out, row)
	}
	return out
}

// handleSnapshotNow records a measurement immediately.
func (s *Server) handleSnapshotNow(c *gin.Context) {
	if err := s.recordSnapshot(); err != nil {
		s.setFlash(c, "danger", "Could not record a snapshot: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/monitoring")
		return
	}
	s.setFlash(c, "success", "Snapshot recorded.")
	c.Redirect(http.StatusSeeOther, "/monitoring")
}

// recordSnapshot writes one measurement.
func (s *Server) recordSnapshot() error {
	col, err := s.app.FindCollectionByNameOrId("metric_snapshots")
	if err != nil || col == nil {
		return err
	}

	now := time.Now()
	runtimeInfo := monitoring.ReadRuntime(s.metrics.Started())
	database := monitoring.ReadDatabase(s.app.DataDir())
	storage := s.storageInfo(now)
	traffic := s.metrics.Snapshot()

	rec := core.NewRecord(col)
	rec.Set("capturedAt", now.UTC())
	rec.Set("memoryBytes", runtimeInfo.HeapBytes)
	rec.Set("goroutines", runtimeInfo.Goroutines)
	rec.Set("heapBytes", runtimeInfo.HeapBytes)
	rec.Set("dbBytes", database.MainBytes)
	rec.Set("auxDbBytes", database.AuxBytes)
	rec.Set("fileBytes", storage.TotalBytes)
	rec.Set("fileCount", storage.FileCount)
	rec.Set("requestsPerMinute", traffic.RequestsPerMinute)
	rec.Set("errorRate", traffic.ErrorRate)
	rec.Set("latencyP95Ms", traffic.P95Ms)

	return s.app.Save(rec)
}

// handleSystemInfo renders versions, build details and the update record.
func (s *Server) handleSystemInfo(c *gin.Context) {
	updates, _ := s.app.FindRecordsByFilter("system_updates",
		"", "-created", 50, 0)

	s.render(c, http.StatusOK, "monitoring_system.html", systemPage{
		base:         s.newBase(c, "System information", "monitoring.system"),
		Version:      s.versionString(),
		PocketBase:   s.pocketBaseVersion(),
		GoVersion:    runtime.Version(),
		GOOS:         runtime.GOOS,
		GOARCH:       runtime.GOARCH,
		NumCPU:       runtime.NumCPU(),
		DataDir:      s.app.DataDir(),
		ConfigFile:   s.cfg.ConfigFile,
		StorageDir:   s.cfg.StorageDir,
		BuildTime:    versionpkg.BuildTime(),
		Commit:       versionpkg.Commit(),
		Updates:      updates,
		UpdateChecks: versionpkg.UpdateChecksEnabled(),
	})
}

// pingDatabase asks the database to answer something.
func (s *Server) pingDatabase(ctx context.Context) error {
	// A real query against the real tables, in the shape the driver accepts. The
	// obvious "SELECT 1" does not work here: the query builder returns more
	// destinations than the statement names, and the ping reported the database as
	// down while it was answering everything else perfectly -- including this
	// panel, which read 42 tables through the same connection.
	var probe []struct {
		Name string `db:"name"`
	}
	return s.app.DB().NewQuery(
		"SELECT name FROM sqlite_master WHERE type = 'table' LIMIT 1").All(&probe)
}

// tableCount asks the database how many tables it holds.
func (s *Server) tableCount() (int, error) {
	var rows []struct {
		Name string `db:"name"`
	}
	err := s.app.DB().NewQuery(
		"SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'").
		All(&rows)
	return len(rows), err
}

// schedulerLastTick returns when the scheduler last ran.
func (s *Server) schedulerLastTick() time.Time {
	if s.scheduler == nil {
		return time.Time{}
	}
	return s.scheduler.LastTick()
}

// pocketBaseVersion reports the embedded PocketBase version.
func (s *Server) pocketBaseVersion() string { return versionpkg.PocketBase() }

// recordStartupSnapshot writes a measurement at boot, so a trend chart has a
// starting point even on a process that has only just started.
func (s *Server) recordStartupSnapshot() {
	// Best effort: a failure here means no trend point, which is not a reason to
	// refuse to start.
	if err := s.recordSnapshot(); err != nil {
		slog.Debug("no startup snapshot was recorded", "error", err)
	}
}

// versionString is the panel version, for the health check and the system page.
func (s *Server) versionString() string { return versionpkg.Panel }

// monitoringPage is the dashboard.
type monitoringPage struct {
	base
	Health       monitoring.Health
	Runtime      monitoring.RuntimeInfo
	Database     monitoring.DatabaseInfo
	Storage      monitoring.StorageInfo
	Traffic      monitoring.Snapshot
	Recent       []int
	RecentError  []int
	Snapshots    []monitoringSnapshotRow
	CanSeeHealth bool
	CanSnapshot  bool

	// LastGCAgo is rendered here rather than in the template: the template helpers
	// take PocketBase's date type and runtime.MemStats reports a time.Time, and
	// converting at the edge is clearer than teaching the helpers a second type.
	LastGCAgo    string
	DatabaseLast time.Time

	// Host is the lightweight machine monitor: the figures the process can read about
	// the machine it is running on.
	Host         hostMonitorView
	HostSettings string
}

// gcAgo renders when the last collection ran.
//
// It formats the time directly rather than going through the template helpers,
// which take PocketBase's date type: runtime.MemStats reports a time.Time and the
// two are not convertible, so the conversion belongs here at the edge.
func gcAgo(at time.Time) string {
	if at.IsZero() {
		return "never"
	}

	elapsed := time.Since(at)
	switch {
	case elapsed < time.Minute:
		return "just now"
	case elapsed < time.Hour:
		return fmt.Sprintf("%d minutes ago", int(elapsed.Minutes()))
	case elapsed < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(elapsed.Hours()))
	default:
		return fmt.Sprintf("%d days ago", int(elapsed.Hours()/24))
	}
}

// monitoringSnapshotRow is one recorded measurement.
type monitoringSnapshotRow struct {
	When         types.DateTime
	Goroutines   int
	HeapBytes    int64
	FileCount    int
	RequestsRate float64
	ErrorRate    float64
	LatencyP95   float64
}

// systemPage is the system information screen.
type systemPage struct {
	base
	Version      string
	PocketBase   string
	GoVersion    string
	GOOS         string
	GOARCH       string
	NumCPU       int
	DataDir      string
	ConfigFile   string
	StorageDir   string
	BuildTime    string
	Commit       string
	Updates      []*core.Record
	UpdateChecks bool
}

var _ = audit.EventSettingsChanged
