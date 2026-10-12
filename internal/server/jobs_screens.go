package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/audit"
	"pocketadmin/internal/jobs"
	"pocketadmin/internal/permission"
)

// registerJobRoutes mounts the scheduled-task screens.
//
// The list is not a generic resource: every row carries enable, disable, pause
// and run actions, and the state shown is partly the stored row and partly what
// the scheduler is doing right now.
func (s *Server) registerJobRoutes(panel *gin.RouterGroup) {
	s.readGuarded(panel, http.MethodGet, "/jobs", permission.JobsJobList, s.handleJobList)
	s.readGuarded(panel, http.MethodGet, "/jobs/:id/runs", permission.JobsRunList, s.handleJobRuns)

	s.guarded(panel, http.MethodPost, "/jobs/:id/state", permission.JobsJobUpdate, s.handleJobState)
	s.guarded(panel, http.MethodPost, "/jobs/:id/schedule", permission.JobsJobUpdate, s.handleJobSchedule)
	s.guarded(panel, http.MethodPost, "/jobs/:id/run", permission.JobsJobExecute, s.handleJobRunNow)
}

// handleJobList renders every scheduled task.
func (s *Server) handleJobList(c *gin.Context) {
	col, err := s.app.FindCollectionByNameOrId(jobs.CollectionJobs)
	if err != nil || col == nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Schema missing", "The jobs collection does not exist.")
		return
	}

	records, err := s.app.FindRecordsByFilter(jobs.CollectionJobs, "", "name", 0, 0)
	if err != nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Could not load the tasks", err.Error())
		return
	}

	rows := make([]jobRow, 0, len(records))
	for _, rec := range records {
		code := rec.GetString("code")
		_, hasHandler := jobs.Lookup(code)

		row := jobRow{
			Record:      rec,
			Code:        code,
			Name:        rec.GetString("name"),
			Description: rec.GetString("description"),
			Cron:        rec.GetString("cron"),
			Status:      rec.GetString("status"),
			LastStatus:  rec.GetString("lastStatus"),
			Running:     jobs.Running(code),
			// A row whose handler is absent is inert; saying so is better than
			// showing a task that silently never runs.
			Orphaned: !hasHandler,
		}
		if at := rec.GetDateTime("nextRunAt"); !at.IsZero() {
			row.NextRun = at.Time().Local().Format("2 Jan 15:04")
		}
		if at := rec.GetDateTime("lastRunAt"); !at.IsZero() {
			row.LastRun = at.Time().Local().Format("2 Jan 15:04")
		}
		rows = append(rows, row)
	}

	actor := s.actor(c)

	s.render(c, http.StatusOK, "jobs.html", jobsPage{
		base:      s.newBase(c, "Scheduled tasks", "jobs"),
		Rows:      rows,
		Running:   jobs.RunningCodes(),
		CanUpdate: actor.Can(permission.JobsJobUpdate),
		CanRun:    actor.Can(permission.JobsJobExecute),
	})
}

// handleJobRuns renders one task's execution history.
func (s *Server) handleJobRuns(c *gin.Context) {
	rec, ok := s.findRecord(c, mustCollection(s.app, jobs.CollectionJobs))
	if !ok {
		return
	}

	runs, err := s.app.FindRecordsByFilter(jobs.CollectionRuns,
		"job = {:id}", "-startedAt", 100, 0, map[string]any{"id": rec.Id})
	if err != nil {
		runs = nil
	}

	s.render(c, http.StatusOK, "job_runs.html", jobRunsPage{
		base:   s.newBase(c, "Run history", "jobs"),
		Job:    rec,
		Runs:   runs,
		CanRun: s.actor(c).Can(permission.JobsJobExecute),
	})
}

// handleJobState enables, disables or pauses a task.
func (s *Server) handleJobState(c *gin.Context) {
	rec, ok := s.findRecord(c, mustCollection(s.app, jobs.CollectionJobs))
	if !ok {
		return
	}

	status := strings.TrimSpace(c.PostForm("status"))
	switch status {
	case "enabled", "disabled", "paused":
	default:
		s.setFlash(c, "danger", "Unrecognised state.")
		c.Redirect(http.StatusSeeOther, "/jobs")
		return
	}

	// A schedule that cannot be parsed would leave the task with no next run, so
	// the change is refused rather than applied and silently inert.
	if status == "enabled" {
		if err := jobs.ValidExpression(rec.GetString("cron")); err != nil {
			s.setFlash(c, "danger", "This task's schedule is invalid, so it cannot be enabled: "+err.Error())
			c.Redirect(http.StatusSeeOther, "/jobs")
			return
		}
	}

	rec.Set("status", status)
	if status != "enabled" {
		rec.Set("nextRunAt", nil)
	}

	if err := s.app.Save(rec); err != nil {
		s.setFlash(c, "danger", "Could not change the state: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/jobs")
		return
	}

	if status == "enabled" {
		store := jobs.NewStore(s.app)
		if err := store.Reschedule(rec, time.Now()); err != nil {
			s.setFlash(c, "warning", "Enabled, but the next run could not be computed: "+err.Error())
			c.Redirect(http.StatusSeeOther, "/jobs")
			return
		}
	}

	s.audit(c, auditEntry{
		Event:      audit.EventJobStateChanged,
		Module:     "jobs",
		TargetType: "job",
		TargetID:   rec.Id,
		Severity:   severityWarning,
		Summary:    "Task " + rec.GetString("code") + " set to " + status,
	})
	s.setFlash(c, "success", "Task "+status+".")
	c.Redirect(http.StatusSeeOther, "/jobs")
}

// handleJobSchedule changes a task's cron expression and policy.
func (s *Server) handleJobSchedule(c *gin.Context) {
	rec, ok := s.findRecord(c, mustCollection(s.app, jobs.CollectionJobs))
	if !ok {
		return
	}

	expression := strings.TrimSpace(c.PostForm("cron"))
	// Validation is the point of this handler: an expression that does not parse
	// leaves a task that never runs, which is indistinguishable from a task with
	// nothing to do.
	if err := jobs.ValidExpression(expression); err != nil {
		s.setFlash(c, "danger", "That schedule is not valid: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/jobs")
		return
	}

	rec.Set("cron", expression)

	for _, field := range []string{"timeoutSeconds", "maxRetries", "retryBackoffSeconds"} {
		if raw := strings.TrimSpace(c.PostForm(field)); raw != "" {
			rec.Set(field, raw)
		}
	}
	if concurrency := c.PostForm("concurrency"); concurrency != "" {
		rec.Set("concurrency", concurrency)
	}
	rec.Set("notifyOnFailure", c.PostForm("notifyOnFailure") != "")

	if err := s.app.Save(rec); err != nil {
		s.setFlash(c, "danger", "Could not save the schedule: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/jobs")
		return
	}

	store := jobs.NewStore(s.app)
	if err := store.Reschedule(rec, time.Now()); err != nil {
		s.setFlash(c, "warning", "Saved, but the next run could not be computed: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/jobs")
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventJobScheduleChanged,
		Module:     "jobs",
		TargetType: "job",
		TargetID:   rec.Id,
		Severity:   severityWarning,
		Summary:    "Task " + rec.GetString("code") + " schedule set to " + expression,
		Detail:     map[string]any{"cron": expression},
	})
	s.setFlash(c, "success", "Schedule saved.")
	c.Redirect(http.StatusSeeOther, "/jobs")
}

// handleJobRunNow runs a task immediately.
func (s *Server) handleJobRunNow(c *gin.Context) {
	rec, ok := s.findRecord(c, mustCollection(s.app, jobs.CollectionJobs))
	if !ok {
		return
	}

	if s.scheduler == nil {
		s.setFlash(c, "danger", "The scheduler is not running.")
		c.Redirect(http.StatusSeeOther, "/jobs")
		return
	}

	var userID string
	if actor := s.actor(c); actor != nil && !actor.IsSuperuser {
		userID = actor.UserID
	}

	// A manual run is an extra run, not an early one: the scheduled time is left
	// where it was, so pressing the button does not shift the schedule.
	result, err := s.scheduler.RunNow(rec, userID)

	s.audit(c, auditEntry{
		Event:      audit.EventJobRunManually,
		Module:     "jobs",
		TargetType: "job",
		TargetID:   rec.Id,
		Severity:   severityInfo,
		Summary:    "Task " + rec.GetString("code") + " run manually: " + result.Status,
		Detail:     map[string]any{"status": result.Status, "error": result.Error},
	})

	switch {
	case err != nil:
		s.setFlash(c, "danger", "The run could not be started: "+err.Error())
	case result.Status == jobs.StatusSuccess:
		s.setFlash(c, "success", "The task ran: "+result.Output)
	default:
		s.setFlash(c, "warning", "The task ended as "+result.Status+": "+result.Error)
	}

	c.Redirect(http.StatusSeeOther, "/jobs")
}

// jobRow is one task on the list.
type jobRow struct {
	Record      *core.Record
	Code        string
	Name        string
	Description string
	Cron        string
	Status      string
	LastStatus  string
	NextRun     string
	LastRun     string
	Running     bool
	Orphaned    bool
}

// jobsPage is the task list.
type jobsPage struct {
	base
	Rows      []jobRow
	Running   []string
	CanUpdate bool
	CanRun    bool
}

// jobRunsPage is one task's history.
type jobRunsPage struct {
	base
	Job    *core.Record
	Runs   []*core.Record
	CanRun bool
}
