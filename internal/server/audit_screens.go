package server

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/audit"
	"pocketadmin/internal/permission"
)

// The audit logs are read-only screens over four collections.
//
// Each is append-only through the application: no code path updates or deletes a
// row, so the resource layer is given list and export and nothing else. Retention
// is the only deletion path and it belongs to a scheduled task (see
// docs/decisions.md D-012).
func auditResources() []resource {
	return []resource{
		{
			Collection: "operation_logs",
			BasePath:   "/audit/operations",
			Title:      "Operation log",
			Singular:   "entry",
			Active:     "audit.operations",
			ListPerm:   permission.AuditOperationList,
			Order:      "-created",
		},
		{
			Collection: "security_audit_logs",
			BasePath:   "/audit/security",
			Title:      "Security events",
			Singular:   "event",
			Active:     "audit.security",
			ListPerm:   permission.AuditSecurityList,
			Order:      "-created",
		},
		{
			Collection: "login_logs",
			BasePath:   "/audit/logins",
			Title:      "Sign-in history",
			Singular:   "entry",
			Active:     "audit.logins",
			ListPerm:   permission.AuditLoginList,
			Order:      "-created",
		},
		{
			Collection: "exception_logs",
			BasePath:   "/audit/exceptions",
			Title:      "Exception log",
			Singular:   "exception",
			Active:     "audit.exceptions",
			ListPerm:   permission.AuditExceptionList,
			Order:      "-created",
		},
	}
}

// exportableCollections is the allow-list of collections a CSV export may read.
//
// The export route takes the log's name from the URL, so without a list it would
// be a way to dump any collection through a route whose permission only mentions
// logs.
var exportableCollections = map[string]string{
	"operation_logs":      permission.AuditOperationList,
	"security_audit_logs": permission.AuditSecurityList,
	"login_logs":          permission.AuditLoginList,
	"exception_logs":      permission.AuditExceptionList,
}

// registerAuditRoutes mounts the logs.
func (s *Server) registerAuditRoutes(panel *gin.RouterGroup) {
	for _, r := range auditResources() {
		s.resourceRoutes(panel, r)
	}

	// The permission is resolved per collection inside the handler, so the guard
	// here is the export permission plus the per-collection check below. Without
	// both, holding export on one log would export all of them.
	s.readGuarded(panel, http.MethodGet, "/audit/export/:collection",
		permission.AuditExport, s.handleAuditExport)
}

// maxExportRows bounds an export so one request cannot read an unbounded log.
const maxExportRows = 50000

// handleAuditExport streams a filtered log as CSV.
func (s *Server) handleAuditExport(c *gin.Context) {
	name := c.Param("collection")

	required, ok := exportableCollections[name]
	if !ok {
		s.renderErrorPage(c, http.StatusNotFound, "Unknown log",
			"That is not an exportable log.")
		return
	}

	// The export permission says "may export", the collection's own says "may see
	// this log". Both are needed: export is not a way around list.
	if !s.actor(c).Can(required) {
		s.renderErrorPage(c, http.StatusForbidden, "Forbidden",
			"You do not hold the permission to read this log.")
		return
	}

	col, err := s.app.FindCollectionByNameOrId(name)
	if err != nil || col == nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Schema missing", "The log does not exist.")
		return
	}

	filter, params := searchFilter(col, strings.TrimSpace(c.Query("q")))

	records, err := s.app.FindRecordsByFilter(col, filter, "-created", maxExportRows, 0, params)
	if err != nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Could not read the log", err.Error())
		return
	}

	// A stable column set, taken from the collection, so the CSV matches the
	// screen the operator was looking at.
	cols := listColumns(col)

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s-%s.csv"`, name, nowStamp()))
	// A CSV opened in a spreadsheet is a place where a formula in a cell becomes
	// something that runs. The escaping below neutralises that.
	c.Status(http.StatusOK)

	// A byte-order mark, because a spreadsheet otherwise reads UTF-8 as the local
	// codepage and mangles any non-ASCII text in the log.
	_, _ = c.Writer.WriteString("\ufeff")

	w := csv.NewWriter(c.Writer)
	defer w.Flush()

	header := make([]string, 0, len(cols))
	for _, col := range cols {
		header = append(header, col.Label)
	}
	_ = w.Write(header)

	for _, rec := range records {
		row := make([]string, 0, len(cols))
		for _, col := range cols {
			row = append(row, csvSafe(rec.GetString(col.Name)))
		}
		if err := w.Write(row); err != nil {
			return
		}
	}
}

// csvSafe neutralises a value that a spreadsheet would treat as a formula.
//
// Export is the one place where stored text is handed to another program to
// interpret. A cell beginning with =, +, - or @ is executed by Excel and Sheets,
// so the value is prefixed with a single quote, which both programs treat as
// literal text.
func csvSafe(value string) string {
	if value == "" {
		return ""
	}
	switch value[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + value
	}
	return value
}

// nowStamp returns a compact timestamp for a filename.
func nowStamp() string {
	return strings.ReplaceAll(
		strings.ReplaceAll(
			strings.TrimSpace(fmt.Sprint(time.Now().Format("20060102-150405"))), " ", ""), ":", "")
}

// severityFor maps an audit severity onto a Bootstrap badge class.
func severityFor(sev string) string {
	switch audit.Severity(sev) {
	case audit.SeverityCritical:
		return "danger"
	case audit.SeverityWarning:
		return "warning"
	default:
		return "secondary"
	}
}

// formatDuration renders a duration in milliseconds for display.
func formatDuration(ms int) string {
	if ms < 1000 {
		return strconv.Itoa(ms) + " ms"
	}
	return fmt.Sprintf("%.1f s", float64(ms)/1000)
}

// mustRecord is a small helper for templates that need a record by id.
func mustRecord(app core.App, collection, id string) *core.Record {
	rec, err := app.FindRecordById(collection, id)
	if err != nil {
		return nil
	}
	return rec
}
