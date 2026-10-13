package server

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/audit"
	"pocketadmin/internal/monitoring"
	"pocketadmin/internal/permission"
)

// registerNodeRoutes mounts the machine screens.
func (s *Server) registerNodeRoutes(panel *gin.RouterGroup) {
	s.readGuarded(panel, http.MethodGet, "/monitoring/nodes", permission.NodeList, s.handleNodeList)
	s.readGuarded(panel, http.MethodGet, "/monitoring/nodes/:id", permission.NodeList, s.handleNodeDetail)

	s.guarded(panel, http.MethodPost, "/monitoring/nodes/save", permission.NodeManage, s.handleNodeSave)
	s.guarded(panel, http.MethodPost, "/monitoring/nodes/:id/delete", permission.NodeManage, s.handleNodeDelete)
}

// nodeRow is one machine on the list.
type nodeRow struct {
	Record   *core.Record
	Code     string
	Name     string
	Hostname string
	OS       string
	Agent    string
	Status   string
	IsSelf   bool
	Health   monitoring.NodeHealth
	Samples  int
	Seen     string
	Summary  monitoring.Summary
}

// nodesPage is the machine list.
type nodesPage struct {
	base
	Rows         []nodeRow
	Interval     string
	Retention    string
	Enabled      bool
	SettingsSite string
	CanManage    bool
}

// handleNodeList renders the machines.
func (s *Server) handleNodeList(c *gin.Context) {
	nodes, err := monitoring.AllNodes(s.app)
	if err != nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Could not load the machines", err.Error())
		return
	}

	policy := s.hostSamplePolicy()
	now := time.Now()

	rows := make([]nodeRow, 0, len(nodes))
	for _, node := range nodes {
		row := nodeRow{
			Record:   node,
			Code:     node.GetString("code"),
			Name:     node.GetString("name"),
			Hostname: node.GetString("hostname"),
			OS:       node.GetString("os"),
			Agent:    node.GetString("agentVersion"),
			Status:   node.GetString("status"),
			IsSelf:   node.GetBool("isSelf"),
			Samples:  node.GetInt("sampleCount"),
			Health:   monitoring.NodeStatusFor(node, policy.Interval, now),
			Summary:  monitoring.Describe(s.app, node.Id),
		}
		if seen := node.GetDateTime("lastSeenAt"); !seen.IsZero() {
			row.Seen = seen.Time().Local().Format("2 Jan 15:04")
		}
		rows = append(rows, row)
	}

	s.render(c, http.StatusOK, "monitoring_nodes.html", nodesPage{
		base:         s.newBase(c, "Machines", "monitoring.nodes"),
		Rows:         rows,
		Interval:     humanDuration(policy.Interval),
		Retention:    retentionLabel(policy.RetentionDays),
		Enabled:      policy.Enabled,
		SettingsSite: hostSettingsSite(),
		CanManage:    s.actor(c).Can(permission.NodeManage),
	})
}

// nodeDetailPage is one machine with its history.
type nodeDetailPage struct {
	base
	Record      *core.Record
	Code        string
	Name        string
	Hostname    string
	OS          string
	Agent       string
	IsSelf      bool
	Status      string
	Health      monitoring.NodeHealth
	Samples     int
	Seen        string
	Window      string
	Windows     []string
	Charts      []nodeChart
	HasData     bool
	CanManage   bool
	SampleCount int
}

// nodeChart is one chart with its data pre-encoded for the renderer.
type nodeChart struct {
	Title string
	Unit  string
	JSON  string
}

// handleNodeDetail renders one machine.
func (s *Server) handleNodeDetail(c *gin.Context) {
	node, err := s.app.FindRecordById(monitoring.CollectionNodes, c.Param("id"))
	if err != nil || node == nil {
		s.renderErrorPage(c, http.StatusNotFound, "Not found", "That machine does not exist.")
		return
	}

	now := time.Now()
	window, label := monitoring.WindowFor(now, c.Query("window"))

	series, err := monitoring.BuildSeries(s.app, node.Id, window, now)
	if err != nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Could not read the history", err.Error())
		return
	}

	policy := s.hostSamplePolicy()

	view := nodeDetailPage{
		base:      s.newBase(c, node.GetString("name"), "monitoring.nodes"),
		Record:    node,
		Code:      node.GetString("code"),
		Name:      node.GetString("name"),
		Hostname:  node.GetString("hostname"),
		OS:        node.GetString("os"),
		Agent:     node.GetString("agentVersion"),
		IsSelf:    node.GetBool("isSelf"),
		Status:    node.GetString("status"),
		Health:    monitoring.NodeStatusFor(node, policy.Interval, now),
		Samples:   node.GetInt("sampleCount"),
		Window:    label,
		CanManage: s.actor(c).Can(permission.NodeManage),
	}
	if seen := node.GetDateTime("lastSeenAt"); !seen.IsZero() {
		view.Seen = seen.Time().Local().Format("2 Jan 15:04:05")
	}
	for _, preset := range monitoring.PresetWindows(now) {
		view.Windows = append(view.Windows, preset.Label)
	}

	// Each series becomes its own chart, so a reader can compare a machine's memory
	// against another's or against its own past without six lines on one axis fighting
	// for the same scale.
	for _, line := range series {
		points := line.Points
		if !hasPoints(points) {
			continue
		}
		view.HasData = true
		view.SampleCount += countPoints(points)

		view.Charts = append(view.Charts, nodeChart{
			Title: line.Name,
			Unit:  line.Unit,
			JSON:  chartJSON(points, window, line.Unit, line.Kind),
		})
	}

	s.render(c, http.StatusOK, "monitoring_node.html", view)
}

// hasPoints reports whether a series carries any reading at all.
func hasPoints(points []monitoring.Point) bool {
	for _, point := range points {
		if point.Count > 0 {
			return true
		}
	}
	return false
}

// countPoints totals the readings behind a series, for a "built from N readings" note.
func countPoints(points []monitoring.Point) int {
	total := 0
	for _, point := range points {
		total += point.Count
	}
	return total
}

// handleNodeSave registers or updates a machine.
func (s *Server) handleNodeSave(c *gin.Context) {
	id := strings.TrimSpace(c.PostForm("id"))

	col, err := s.app.FindCollectionByNameOrId(monitoring.CollectionNodes)
	if err != nil || col == nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Schema missing", "The collection does not exist.")
		return
	}

	var rec *core.Record
	if id == "" {
		rec = core.NewRecord(col)
	} else {
		rec, err = s.app.FindRecordById(monitoring.CollectionNodes, id)
		if err != nil || rec == nil {
			s.renderErrorPage(c, http.StatusNotFound, "Not found", "That machine does not exist.")
			return
		}
	}

	code := strings.TrimSpace(c.PostForm("code"))
	name := strings.TrimSpace(c.PostForm("name"))
	if code == "" || name == "" {
		s.setFlash(c, "danger", "A machine needs a code and a name.")
		c.Redirect(http.StatusSeeOther, "/monitoring/nodes")
		return
	}

	// The code is what an agent reports under, so it may not be changed once a machine
	// has reported: renaming it would orphan every reading already stored.
	if !rec.IsNew() && rec.GetString("code") != code {
		if rec.GetInt("sampleCount") > 0 {
			s.setFlash(c, "danger",
				"That machine has already reported, so its code cannot change. "+
					"Renaming it would orphan every reading it has sent.")
			c.Redirect(http.StatusSeeOther, "/monitoring/nodes/"+rec.Id)
			return
		}
	}

	rec.Set("code", code)
	rec.Set("name", name)
	rec.Set("status", nonEmptyString(c.PostForm("status"), monitoring.NodeActive))
	rec.Set("note", c.PostForm("note"))
	if rec.IsNew() {
		rec.Set("isSelf", false)
		rec.Set("sampleCount", 0)
	}

	if err := s.app.Save(rec); err != nil {
		s.setFlash(c, "danger", "Could not save: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/monitoring/nodes")
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "nodes",
		TargetType: "monitor_node",
		TargetID:   rec.Id,
		Severity:   severityWarning,
		Summary:    "Monitored machine \"" + name + "\" saved as " + code,
	})
	s.setFlash(c, "success", "Saved.")
	c.Redirect(http.StatusSeeOther, "/monitoring/nodes/"+rec.Id)
}

// handleNodeDelete retires a machine.
func (s *Server) handleNodeDelete(c *gin.Context) {
	rec, err := s.app.FindRecordById(monitoring.CollectionNodes, c.Param("id"))
	if err != nil || rec == nil {
		s.renderErrorPage(c, http.StatusNotFound, "Not found", "That machine does not exist.")
		return
	}

	// This machine cannot be removed: the sampler would recreate it on its next tick,
	// and an operator would watch it come back and wonder what they had done wrong.
	if rec.GetBool("isSelf") {
		s.setFlash(c, "danger",
			"This is the machine the panel runs on, so it cannot be removed. Disable it instead.")
		c.Redirect(http.StatusSeeOther, "/monitoring/nodes")
		return
	}

	// The readings outlive the machine unless somebody says otherwise. Deleting them
	// as a side effect of tidying the list would be a surprising amount of history to
	// lose to one click, so the count is stated and the operator chooses.
	if c.PostForm("withHistory") != "" {
		deleted := 0
		for batch := 0; batch < 200; batch++ {
			samples, err := s.app.FindRecordsByFilter(monitoring.CollectionHostMetrics,
				"node = {:n}", "at", 500, 0, map[string]any{"n": rec.Id})
			if err != nil || len(samples) == 0 {
				break
			}
			for _, sample := range samples {
				if err := s.app.Delete(sample); err != nil {
					s.setFlash(c, "danger", "Could not delete the history: "+err.Error())
					c.Redirect(http.StatusSeeOther, "/monitoring/nodes")
					return
				}
				deleted++
			}
			if len(samples) < 500 {
				break
			}
		}

		s.audit(c, auditEntry{
			Event:      audit.EventDataExported,
			Module:     "nodes",
			TargetType: "monitor_node",
			TargetID:   rec.Id,
			Severity:   severityCritical,
			Summary: "Deleted machine \"" + rec.GetString("name") + "\" and " +
				fmt.Sprintf("%d of its readings", deleted),
		})
	} else {
		s.audit(c, auditEntry{
			Event:      audit.EventSettingsChanged,
			Module:     "nodes",
			TargetType: "monitor_node",
			TargetID:   rec.Id,
			Severity:   severityWarning,
			Summary:    "Removed machine \"" + rec.GetString("name") + "\", keeping its readings",
		})
	}

	if err := s.app.Delete(rec); err != nil {
		s.setFlash(c, "danger", "Could not delete: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/monitoring/nodes")
		return
	}

	s.setFlash(c, "success", "Removed.")
	c.Redirect(http.StatusSeeOther, "/monitoring/nodes")
}
