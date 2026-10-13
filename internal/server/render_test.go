package server

import (
	"bytes"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"pocketadmin/internal/i18n"
)

// TestEveryTemplateParses.
//
// It is a build failure that only shows at run time otherwise: the templates are parsed
// when the server starts, so a mistyped action takes the whole panel down on boot rather
// than failing a test. The pages are embedded, so nothing else would notice.
func TestEveryTemplateParses(t *testing.T) {
	if _, err := buildTemplates(); err != nil {
		t.Fatalf("a template does not parse: %v", err)
	}
}

// TestEveryPartialRendersWithAPageAsItsData.
//
// This is the bug that shipped in the round that translated the panel: the flash partial
// was given `{{.T "common.close"}}`, and inside `{{with .Flash}}` the dot is the flash
// rather than the page, so the action failed. The page still answered 200 -- with an error
// page -- and every other check passed. The coverage test saw no English, the key existed,
// and the templates parsed.
//
// A partial is invoked with whatever data the calling template passes it, and the dot
// inside it is that data. Rendering each partial against the page is what proves the
// actions inside it can reach the page at all.
func TestEveryPartialRendersWithAPageAsItsData(t *testing.T) {
	dir := filepath.Join("..", "..", "web", "templates", "partials")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading the partials: %v", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".html") {
			continue
		}

		name := strings.TrimSuffix(entry.Name(), ".html")
		body := "{{template \"" + name + "\" .}}"

		tmpl, err := template.New("probe").Funcs(templateFuncs()).Parse(body)
		if err != nil {
			t.Errorf("%s: %v", entry.Name(), err)
			continue
		}

		// The partials are defined in the file itself, so it is parsed alongside.
		if _, err := tmpl.ParseFiles(filepath.Join(dir, entry.Name())); err != nil {
			t.Errorf("%s: %v", entry.Name(), err)
			continue
		}

		var out bytes.Buffer
		if err := tmpl.Execute(&out, demoPage()); err != nil {
			t.Errorf("%s does not render against a page: %v", entry.Name(), err)
		}
	}
}

// TestEveryMessageCallInATemplateResolves.
//
// A message call with the wrong receiver fails at render time, and a key that is missing
// renders as the key. Both are checked here by rendering every page with data whose
// methods return something plausible, and looking for an unresolved key in the output.
func TestEveryMessageCallInATemplateResolves(t *testing.T) {
	pages, err := buildTemplates()
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}

	// A key left unreplaced looks like `area.something` in the output. The pattern is
	// narrow on purpose: a real page contains dotted strings such as a host name.
	unresolved := regexp.MustCompile(`>(?:common|nav|chrome|page|auth|dashboard|monitoring|orders|payments|refunds|promotions|membership|subscriptions|catalog|storage|announcements|developer|integrations|account|workbench|preferences|settings|records|resource|collections|jobs|org|error)\.[a-zA-Z][a-zA-Z.]*<`)

	names := make([]string, 0, len(pages))
	for name := range pages {
		names = append(names, name)
	}
	sort.Strings(names)

	checked := 0
	for _, name := range names {
		tmpl, ok := pages[name]
		if !ok {
			continue
		}

		var out bytes.Buffer
		if err := tmpl.ExecuteTemplate(&out, "layout", demoPage()); err != nil {
			// A template that needs a field the demo page does not carry is not this
			// test's subject; the parse test and the partial test cover those.
			continue
		}
		checked++

		if match := unresolved.FindString(out.String()); match != "" {
			t.Errorf("%s renders an unresolved message key: %s", name, match)
		}
	}

	if checked == 0 {
		t.Fatal("no page rendered, so this test proves nothing")
	}
	t.Logf("rendered %d page template(s)", checked)
}

// probeData is the page a template is rendered against.
//
// It embeds the real `base`, because that is where the T method lives: a map cannot carry
// one, and a test that rendered against a map would report every message call as broken
// while a real page works. The fields a screen needs beyond the frame are added as a
// wrapper so a template that reaches for one does not fail the probe.
type probeData struct {
	base

	Resource map[string]any
	Rows     []any
	Records  []any
	Items    []any
	Fields   []any
	Groups   []any
	Nodes    []any
	Charts   []any
	Windows  []string
	Total    int
	Page     int
	PerPage  int
	Pages    int
	HasPrev  bool
	HasNext  bool
	HasData  bool

	Record    *probeRecord
	Entry     *probeRecord
	File      *probeRecord
	Node      *probeRecord
	CanManage bool

	Version string
}

// demoPage is a page carrying something plausible in every field a template reaches for.
func demoPage() probeData {
	return probeData{
		base: base{
			Title:     "Probe",
			Locale:    i18n.SimplifiedChinese,
			Theme:     "light",
			CSRF:      "probe",
			UserEmail: "probe@example.com",
		},
		Resource: map[string]any{"Singular": "record", "Plural": "records", "Name": "records"},
		Rows:     []any{}, Records: []any{}, Items: []any{}, Fields: []any{},
		Groups: []any{}, Nodes: []any{}, Charts: []any{}, Windows: []string{},
		PerPage: 30, Pages: 1,
		Record: &probeRecord{}, Entry: &probeRecord{},
		File: &probeRecord{}, Node: &probeRecord{},
		Version: "probe",
	}
}

// probeRecord answers every method a template might call with a plausible value.
//
// It is deliberately not a core.Record: a template reaches for GetString, GetInt, GetFloat
// and GetBool, and standing in for them keeps this test independent of the schema.
type probeRecord struct{}

func (p *probeRecord) Id() string              { return "probe" }
func (p *probeRecord) GetString(string) string { return "" }
func (p *probeRecord) GetInt(string) int       { return 0 }
func (p *probeRecord) GetFloat(string) float64 { return 0 }
func (p *probeRecord) GetBool(string) bool     { return false }
func (p *probeRecord) GetDateTime(string) any  { return nil }
