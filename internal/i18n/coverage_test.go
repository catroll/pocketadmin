package i18n_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"pocketadmin/internal/i18n"
)

// This file guards the templates against untranslated English.
//
// The first pass at translation covered the navigation and the frame, and left the module
// screens in English -- which is worse than not offering the languages at all, because a
// reader who chose Chinese gets a screen in two languages and cannot tell whether the gap
// is their own mistake. A test is the only thing that keeps a new screen from being
// written in English and shipped that way.

// allowedEnglish is text that is deliberately not translated.
//
// Every entry is a decision. Adding one to silence a failure without that being true is
// how a half-translated interface gets through, so each says why it is here.
var allowedEnglish = map[string]string{
	// Product and protocol names.
	"PocketBase":             "the product's name",
	"API":                    "an acronym",
	"CSV":                    "a file format",
	"HTML":                   "a file format",
	"ISO-4217":               "a standard",
	"SKU":                    "a term of art",
	"JSON":                   "a file format",
	"SVG":                    "a file format",
	"Ctrl/Cmd":               "key names",
	"UTC":                    "a time zone",
	"ID":                     "an abbreviation",
	"IP":                     "an abbreviation",
	"GC":                     "an abbreviation",
	"CPU":                    "an abbreviation",
	"URL":                    "an abbreviation",
	"GET":                    "an HTTP method",
	"POST":                   "an HTTP method",
	"PUT":                    "an HTTP method",
	"PATCH":                  "an HTTP method",
	"DELETE":                 "an HTTP method",
	"Content-Type":           "an HTTP header",
	"Authorization":          "an HTTP header",
	"Bearer":                 "an HTTP scheme",
	"prov-txn-1":             "a sample value in a placeholder",
	"prov-txn-2":             "a sample value in a placeholder",
	"ORD-202610-00001":       "a sample order number in a placeholder",
	"WELCOME-HUGO":           "a sample coupon code",
	"EMP":                    "a prefix in an example value",
	"REF":                    "a prefix in an example value",
	"admin@example.com":      "an example address",
	"root@example.com":       "an example address",
	"demo.alice@example.com": "an example address",
	"tag":                    "an example value",
	"e.g. -10":               "an example value",
	"vip, archive":           "an example value in a placeholder",

	// Values an operator types into a field and the code compares against. They appear
	// inside <code> so a reader can copy them, and translating one would make the form
	// reject exactly what the label told them to enter.
	"variant":   "a stock-mode value the operator selects",
	"single":    "a stock-mode value the operator selects",
	"unlimited": "a stock-mode value the operator selects",
	"absolute":  "a validity-mode value the operator selects",
	"relative":  "a validity-mode value the operator selects",

	// Names and abbreviations that are the same in every language.
	"PocketAdmin": "the product's name",
	// Stable identifiers an API client matches on. A translated error code is not an
	// error code, and a translated header name is not a header.
	"Retry-After":         "an HTTP header name",
	"missing_credentials": "a stable API error code",
	"invalid_key":         "a stable API error code",
	"key_unusable":        "a stable API error code",
	"address_not_allowed": "a stable API error code",
	"owner_unavailable":   "a stable API error code",
	"insufficient_scope":  "a stable API error code",
	"rate_limited":        "a stable API error code",
	"admin":               "the surface name a preference record is filed under",
}

// textNode finds English a reader sees between two tags.
var textNode = regexp.MustCompile(`>([^<>{}\n]+)<`)

// attributeText finds English inside the three attributes a reader reads.
var attributeText = regexp.MustCompile(`(?:placeholder|title|aria-label)="([^"]*)"`)

// looksEnglish reports whether a fragment is likely to be a sentence somebody has to
// read, rather than a symbol, a number, or a fragment of markup.
func looksEnglish(fragment string) bool {
	text := strings.TrimSpace(fragment)
	if text == "" {
		return false
	}

	// Anything with a Go template action left in it is not a plain string; the parts
	// around the action are checked separately.
	if strings.ContainsAny(text, "{}") {
		return false
	}

	// Strip entities such as &mdash; and &nbsp;, which are not words.
	text = regexp.MustCompile(`&[a-zA-Z]+;`).ReplaceAllString(text, " ")
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}

	// A run of Latin letters that forms a word of three or more characters, with at
	// least one lowercase letter -- so "GET" and "ID" are not sentences, but "Status" is.
	if !regexp.MustCompile(`[A-Za-z]*[a-z]{2}[A-Za-z]*`).MatchString(text) {
		return false
	}

	// Trailing punctuation-only fragments are not sentences.
	if regexp.MustCompile(`^[[:punct:]\s]+$`).MatchString(text) {
		return false
	}

	return true
}

// TestNoTemplateShowsUntranslatedEnglish walks every template and reports English that is
// not inside a message call.
func TestNoTemplateShowsUntranslatedEnglish(t *testing.T) {
	root := filepath.Join("..", "..", "web", "templates")

	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".html") {
			return err
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walking the templates: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no templates were found, so this test proves nothing")
	}

	// The message call is removed before the scan, so its key and its arguments are not
	// mistaken for English a reader sees. What remains is everything else.
	messageCall := regexp.MustCompile(`\{\{[^}]*\.T\s+"[^"]*"[^}]*\}\}`)

	found := map[string][]string{}

	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}

		body := messageCall.ReplaceAllString(string(raw), ">")
		// HTML comments are for whoever edits the template.
		body = regexp.MustCompile(`(?s)<!--.*?-->`).ReplaceAllString(body, "")

		rel, _ := filepath.Rel(root, path)
		var hits []string

		collect := func(candidate string) {
			// The whole fragment is checked against the allow-list before it is split.
			// Splitting first turns `vip, archive` -- one example value -- into two
			// candidates that match nothing, and a rule that only works when the text
			// happens to have no comma is not a rule.
			if _, ok := allowedEnglish[strings.TrimSpace(candidate)]; ok {
				return
			}

			for _, part := range regexp.MustCompile(`[.!?]\s+|,\s+`).Split(candidate, -1) {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				if _, ok := allowedEnglish[part]; ok {
					continue
				}
				if !looksEnglish(part) {
					continue
				}
				hits = append(hits, part)
			}
		}

		for _, m := range textNode.FindAllStringSubmatch(body, -1) {
			collect(m[1])
		}
		for _, m := range attributeText.FindAllStringSubmatch(body, -1) {
			collect(m[1])
		}

		if len(hits) > 0 {
			sort.Strings(hits)
			found[rel] = hits
		}
	}

	if len(found) == 0 {
		return
	}

	var names []string
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)

	total := 0
	for _, name := range names {
		total += len(found[name])
		t.Errorf("%s has %d untranslated string(s):", name, len(found[name]))
		for _, hit := range found[name] {
			t.Errorf("    %s", hit)
		}
	}
	t.Errorf("in total, %d English string(s) reach a reader without being translated. "+
		"Wrap each in {{.T \"key\"}} and add the key to the three catalogues; if it genuinely "+
		"must stay in English, add it to allowedEnglish with the reason.", total)
}

// TestEveryKeyUsedInATemplateExists.
//
// A key that is not in the catalogue falls back to itself, so a missing one is not an
// error -- it is the key name shown to a reader in place of a sentence. It survives every
// other check: the coverage test sees no English, the locale tests see a complete
// catalogue, and the page renders. Only looking at what the templates actually ask for
// finds it.
//
// Reusing a key that exists is the point of the shared vocabulary; inventing one that does
// not is the mistake this catches.
func TestEveryKeyUsedInATemplateExists(t *testing.T) {
	known := map[string]bool{}
	for _, key := range i18n.Keys() {
		known[key] = true
	}

	root := filepath.Join("..", "..", "web", "templates")

	// {{.T "key"}}, {{$.T "key"}}, {{$page.T "key"}} -- the receiver varies.
	call := regexp.MustCompile(`\.T\s+"([^"]+)"`)

	missing := map[string][]string{}

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".html") {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		rel, _ := filepath.Rel(root, path)
		for _, m := range call.FindAllStringSubmatch(string(raw), -1) {
			if !known[m[1]] {
				missing[rel] = append(missing[rel], m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the templates: %v", err)
	}

	var names []string
	for name := range missing {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		keys := missing[name]
		sort.Strings(keys)
		t.Errorf("%s uses %d key(s) that are not in the catalogue: %s",
			name, len(keys), strings.Join(keys, ", "))
	}
}
