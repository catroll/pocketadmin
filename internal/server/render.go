package server

import (
	"encoding/json"
	"fmt"
	"html/template"
	"strings"
	"time"

	"github.com/gin-gonic/gin/render"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	webassets "pocketadmin/web"
)

// pageDef describes one server-rendered page together with the layout that
// wraps it. Templates are parsed per page so that each page can own its own
// "content" block without colliding with the other pages.
type pageDef struct {
	name       string // template name, also the base file name
	pageFile   string
	layoutFile string
	layoutName string
}

var pageDefs = []pageDef{
	{name: "login.html", pageFile: "pages/login.html", layoutFile: "layout_auth.html", layoutName: "layout_auth"},
	{name: "dashboard.html", pageFile: "pages/dashboard.html", layoutFile: "layout.html", layoutName: "layout"},
	{name: "collections.html", pageFile: "pages/collections.html", layoutFile: "layout.html", layoutName: "layout"},
	{name: "records.html", pageFile: "pages/records.html", layoutFile: "layout.html", layoutName: "layout"},
	{name: "record_form.html", pageFile: "pages/record_form.html", layoutFile: "layout.html", layoutName: "layout"},
	{name: "resource_list.html", pageFile: "pages/resource_list.html", layoutFile: "layout.html", layoutName: "layout"},
	{name: "resource_form.html", pageFile: "pages/resource_form.html", layoutFile: "layout.html", layoutName: "layout"},
	{name: "account_detail.html", pageFile: "pages/account_detail.html", layoutFile: "layout.html", layoutName: "layout"},
	{name: "preferences.html", pageFile: "pages/preferences.html", layoutFile: "layout.html", layoutName: "layout"},
	{name: "mfa.html", pageFile: "pages/mfa.html", layoutFile: "layout_auth.html", layoutName: "layout_auth"},
	{name: "org_chart.html", pageFile: "pages/org_chart.html", layoutFile: "layout.html", layoutName: "layout"},
	{name: "storage_upload.html", pageFile: "pages/storage_upload.html", layoutFile: "layout.html", layoutName: "layout"},
	{name: "monitoring.html", pageFile: "pages/monitoring.html", layoutFile: "layout.html", layoutName: "layout"},
	{name: "monitoring_system.html", pageFile: "pages/monitoring_system.html", layoutFile: "layout.html", layoutName: "layout"},
	{name: "integrations.html", pageFile: "pages/integrations.html", layoutFile: "layout.html", layoutName: "layout"},
	{name: "integration_form.html", pageFile: "pages/integration_form.html", layoutFile: "layout.html", layoutName: "layout"},
	{name: "integration_logs.html", pageFile: "pages/integration_logs.html", layoutFile: "layout.html", layoutName: "layout"},
	{name: "developer_keys.html", pageFile: "pages/developer_keys.html", layoutFile: "layout.html", layoutName: "layout"},
	{name: "developer_api.html", pageFile: "pages/developer_api.html", layoutFile: "layout.html", layoutName: "layout"},
	{name: "workbench.html", pageFile: "pages/workbench.html", layoutFile: "layout.html", layoutName: "layout"},
	{name: "workbench_inbox.html", pageFile: "pages/workbench_inbox.html", layoutFile: "layout.html", layoutName: "layout"},
	{name: "announcements.html", pageFile: "pages/announcements.html", layoutFile: "layout.html", layoutName: "layout"},
	{name: "announcement_detail.html", pageFile: "pages/announcement_detail.html", layoutFile: "layout.html", layoutName: "layout"},
	{name: "announcement_form.html", pageFile: "pages/announcement_form.html", layoutFile: "layout.html", layoutName: "layout"},
	{name: "jobs.html", pageFile: "pages/jobs.html", layoutFile: "layout.html", layoutName: "layout"},
	{name: "job_runs.html", pageFile: "pages/job_runs.html", layoutFile: "layout.html", layoutName: "layout"},
	{name: "settings.html", pageFile: "pages/settings.html", layoutFile: "layout.html", layoutName: "layout"},
	{name: "storage_references.html", pageFile: "pages/storage_references.html", layoutFile: "layout.html", layoutName: "layout"},
	{name: "dashboard.html", pageFile: "pages/dashboard.html", layoutFile: "layout.html", layoutName: "layout"},
	{name: "error.html", pageFile: "pages/error.html", layoutFile: "layout.html", layoutName: "layout"},
}

// htmlRenderer is a gin HTML renderer that keeps a fully independent template
// set per page.
type htmlRenderer struct {
	templates map[string]*template.Template
}

// Instance implements [render.HTMLRender].
func (r htmlRenderer) Instance(name string, data any) render.Render {
	tmpl, ok := r.templates[name]
	if !ok {
		return render.String{Format: "template %q is not registered", Data: []any{name}}
	}
	return render.HTML{Template: tmpl, Name: name, Data: data}
}

// buildTemplates parses every page together with the shared partials and its
// layout, then rebinds the page name to the layout tree so that rendering the
// page name renders the layout with that page's "content" block.
func buildTemplates() (map[string]*template.Template, error) {
	out := make(map[string]*template.Template, len(pageDefs))

	for _, def := range pageDefs {
		set, err := template.New(def.name).
			Funcs(templateFuncs()).
			ParseFS(webassets.TemplatesFS, "partials/*.html", def.layoutFile, def.pageFile)
		if err != nil {
			return nil, fmt.Errorf("parse page %s: %w", def.name, err)
		}

		clone, err := set.Clone()
		if err != nil {
			return nil, fmt.Errorf("clone page %s: %w", def.name, err)
		}

		layout := clone.Lookup(def.layoutName)
		if layout == nil {
			return nil, fmt.Errorf("page %s: layout %q not defined in %s", def.name, def.layoutName, def.layoutFile)
		}

		if set, err = set.AddParseTree(def.name, layout.Tree); err != nil {
			return nil, fmt.Errorf("bind layout for page %s: %w", def.name, err)
		}

		out[def.name] = set
	}

	return out, nil
}

func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"add":        func(a, b int) int { return a + b },
		"sub":        func(a, b int) int { return a - b },
		"seq":        seq,
		"dict":       dict,
		"hasPrefix":  strings.HasPrefix,
		"upper":      strings.ToUpper,
		"lower":      strings.ToLower,
		"join":       strings.Join,
		"formatTime": formatTime,
		"jsonPretty": jsonPretty,
		"truncate":   truncate,

		// divf divides for a chart height, guarding a zero denominator so a quiet
		// minute renders as an empty bar rather than a division by zero.
		"divf": func(a, b int) float64 {
			if b == 0 {
				return 0
			}
			return float64(a) / float64(b)
		},

		// safeHTML renders a stored body as markup rather than as text.
		//
		// It is only safe because the value has been through the sanitiser at the
		// storage boundary, which is a model hook and therefore cannot be bypassed
		// by a screen or an API call. Every other kind of user-supplied text in
		// this panel goes through the default escaping, and that is deliberate.
		"safeHTML":   func(value string) template.HTML { return template.HTML(value) },
		"treeIndent": treeIndent,
		"fileURL":    fileURL,
		"isImage":    isImage,
		"json":       jsonString,
		"percent":    percent,

		// percentf renders a fraction that is already a ratio, for a value computed
		// in Go rather than from two counts in the template.
		"percentf": func(fraction float64) string {
			return fmt.Sprintf("%.2f%%", fraction*100)
		},
		"ago":   relativeTime,
		"bytes": formatBytes,
	}
}

// formatBytes renders a byte count with a binary unit suffix.
// toInt64 converts any numeric value to int64.
//
// It exists because the values rendered as byte counts come from three places that
// do not agree: runtime.MemStats reports uint64, database file sizes are int64 and a
// JSON number arrives as float64. A template that failed on the difference is
// exactly what happened when the monitoring panel first rendered.
func toInt64(value any) int64 {
	switch v := value.(type) {
	case int:
		return int64(v)
	case int8:
		return int64(v)
	case int16:
		return int64(v)
	case int32:
		return int64(v)
	case int64:
		return v
	case uint:
		return int64(v)
	case uint8:
		return int64(v)
	case uint16:
		return int64(v)
	case uint32:
		return int64(v)
	case uint64:
		// Clamped rather than wrapped: a negative size on a screen is worse than a
		// very large one.
		if v > uint64(1<<63-1) {
			return 1<<63 - 1
		}
		return int64(v)
	case float32:
		return int64(v)
	case float64:
		return int64(v)
	default:
		return 0
	}
}

// formatBytes renders a byte count.
//
// It takes any numeric type because the values it formats come from three places
// that do not agree: runtime.MemStats reports uint64, the database file sizes are
// int64 and a JSON number arrives as float64. A template that failed on the
// difference would be a template that broke when a source changed its mind, which
// is exactly what happened.
func formatBytes(value any) string {
	n := toInt64(value)
	if n < 0 {
		n = 0
	}

	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}

	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// seq returns the inclusive integer range [from, to], used to build pagination.
func seq(from, to int) []int {
	if to < from {
		return nil
	}
	out := make([]int, 0, to-from+1)
	for i := from; i <= to; i++ {
		out = append(out, i)
	}
	return out
}

// dict builds a map from alternating key/value pairs, for passing several
// values into a sub-template.
func dict(values ...any) (map[string]any, error) {
	if len(values)%2 != 0 {
		return nil, fmt.Errorf("dict expects an even number of arguments")
	}
	out := make(map[string]any, len(values)/2)
	for i := 0; i < len(values); i += 2 {
		key, ok := values[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict keys must be strings")
		}
		out[key] = values[i+1]
	}
	return out, nil
}

func formatTime(t types.DateTime) string {
	if t.IsZero() {
		return "—"
	}
	return t.Time().Format("2006-01-02 15:04:05")
}

func jsonPretty(v any) string {
	if v == nil {
		return ""
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(raw)
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 1 {
		return string(runes[:max])
	}
	return string(runes[:max-1]) + "…"
}

// fileURL builds the public download URL for a file stored on a record.
func fileURL(collection *core.Collection, recordID, filename string) string {
	return fmt.Sprintf("/api/files/%s/%s/%s", collection.Id, recordID, filename)
}

func isImage(filename string) bool {
	lower := strings.ToLower(filename)
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".svg", ".avif"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// jsonString marshals a value for embedding in an HTML attribute. The
// attribute escaping performed by html/template is reversed by the browser when
// the attribute is read back through element.dataset.
func jsonString(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(raw)
}

func percent(part, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(part) / float64(total) * 100
}

// relativeTime renders a coarse "x ago" string for dashboards.
// formatDate renders a date without the time, for fields where only the day
// matters -- an expiry or a revocation.
func formatDate(ts types.DateTime) string {
	if ts.IsZero() {
		return ""
	}
	return ts.Time().Local().Format("2 Jan 2006")
}

func relativeTime(ts types.DateTime) string {
	if ts.IsZero() {
		return "—"
	}
	d := time.Since(ts.Time())
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
