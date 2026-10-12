package richtext_test

import (
	"strings"
	"testing"

	"pocketadmin/internal/richtext"
)

// TestEveryCommonPayloadIsNeutralised is the test that justifies the dependency.
//
// Each input is something that executes in a browser if it reaches the page
// unaltered. They were all written through PocketBase's editor field first and
// all of them survived verbatim, which is why this exists.
func TestEveryCommonPayloadIsNeutralised(t *testing.T) {
	payloads := map[string]string{
		"script tag":         `<p>Hello</p><script>alert(1)</script>`,
		"image onerror":      `<img src=x onerror="alert(1)">`,
		"javascript href":    `<a href="javascript:alert(1)">click</a>`,
		"inline handler":     `<p onclick="alert(1)">text</p>`,
		"iframe":             `<iframe src="https://evil.example"></iframe>`,
		"svg onload":         `<svg/onload=alert(1)>`,
		"style block":        `<style>body{background:url("javascript:alert(1)")}</style>`,
		"body onload":        `<body onload="alert(1)">`,
		"form":               `<form action="https://evil.example"><input name="x"></form>`,
		"meta refresh":       `<meta http-equiv="refresh" content="0;url=https://evil.example">`,
		"object":             `<object data="evil.swf"></object>`,
		"embed":              `<embed src="evil.swf">`,
		"vbscript href":      `<a href="vbscript:msgbox(1)">x</a>`,
		"data uri href":      `<a href="data:text/html,<script>alert(1)</script>">x</a>`,
		"entity obfuscation": `<a href="java&#115;cript:alert(1)">x</a>`,
		"newline obfuscation": `<a href="java
script:alert(1)">x</a>`,
		"tab obfuscation": `<a href="java&#9;script:alert(1)">x</a>`,
		"style attribute": `<p style="position:fixed;top:0;left:0;width:100%">overlay</p>`,
		"class attribute": `<div class="main-sidebar">impersonate</div>`,
		"nested script":   `<div><p><script>alert(1)</script></p></div>`,
		"template":        `<template><script>alert(1)</script></template>`,
		"noscript":        `<noscript><p>fallback</p></noscript>`,
		"math":            `<math><mtext><script>alert(1)</script></mtext></math>`,
		"srcdoc":          `<iframe srcdoc="<script>alert(1)</script>"></iframe>`,
		"formaction":      `<button formaction="javascript:alert(1)">x</button>`,
		"xlink href":      `<svg><a xlink:href="javascript:alert(1)">x</a></svg>`,
	}

	for name, payload := range payloads {
		out := richtext.Sanitize(payload)

		lower := strings.ToLower(out)
		for _, forbidden := range []string{
			"<script", "</script", "onerror", "onload", "onclick", "javascript:",
			"vbscript:", "<iframe", "<object", "<embed", "<form", "<input",
			"<style", "<meta", "<template", "<noscript", "<svg", "<math",
			"style=", "class=", "formaction", "srcdoc", "xlink",
		} {
			if strings.Contains(lower, forbidden) {
				t.Errorf("%s: %q survived in %q", name, forbidden, out)
			}
		}
	}
}

// TestOrdinaryMarkupSurvives: a sanitiser that removes everything is safe and
// useless.
func TestOrdinaryMarkupSurvives(t *testing.T) {
	input := `<h2>Maintenance</h2>` +
		`<p>The system will be <strong>unavailable</strong> from <em>02:00</em>.</p>` +
		`<ul><li>Read-only mode</li><li>No new orders</li></ul>` +
		`<blockquote>Plan accordingly.</blockquote>` +
		`<p><a href="https://status.example.com">Status page</a></p>` +
		`<p><code>curl -s https://api.example.com/health</code></p>`

	out := richtext.Sanitize(input)

	for _, want := range []string{
		"<h2>Maintenance</h2>", "<strong>unavailable</strong>", "<em>02:00</em>",
		"<li>Read-only mode</li>", "<blockquote>", "https://status.example.com",
		"<code>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("%q was removed from %q", want, out)
		}
	}
}

// TestTablesSurvive: an announcement is a reasonable place for a schedule.
func TestTablesSurvive(t *testing.T) {
	input := `<table><thead><tr><th>Window</th></tr></thead>` +
		`<tbody><tr><td>02:00-04:00</td></tr></tbody></table>`

	out := richtext.Sanitize(input)
	for _, want := range []string{"<table>", "<th>Window</th>", "<td>02:00-04:00</td>"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q was removed from %q", want, out)
		}
	}
}

// TestLinksGetNoFollow: a reader clicking through must not silently vouch for the
// destination, and a fully qualified link should open in a new tab so a reader
// does not lose their place in the panel.
func TestLinksGetNoFollow(t *testing.T) {
	out := richtext.Sanitize(`<a href="https://example.com">x</a>`)

	if !strings.Contains(out, "nofollow") {
		t.Errorf("no nofollow on a fully qualified link: %q", out)
	}
	if !strings.Contains(out, "noopener") {
		t.Errorf("no noopener on a link that opens a new tab: %q", out)
	}
}

// TestRelativeLinksSurvive: the editor produces relative paths for things inside
// this application.
func TestRelativeLinksSurvive(t *testing.T) {
	out := richtext.Sanitize(`<a href="/orders/123">order</a>`)
	if !strings.Contains(out, `href="/orders/123"`) {
		t.Errorf("a relative link was removed: %q", out)
	}
}

// TestSanitizeIsIdempotent: sanitising an already-sanitised body must not change
// it again, or a re-save would slowly erode the content.
func TestSanitizeIsIdempotent(t *testing.T) {
	input := `<h2>Title</h2><p>Text with <strong>bold</strong> and <a href="https://x.example">a link</a>.</p>`

	once := richtext.Sanitize(input)
	twice := richtext.Sanitize(once)

	if once != twice {
		t.Errorf("sanitising twice changed the result:\n once: %q\ntwice: %q", once, twice)
	}
}

// TestSanitizePlainStripsEverything: a list preview and a notification body show
// text, not markup.
func TestSanitizePlainStripsEverything(t *testing.T) {
	out := richtext.SanitizePlain(`<h2>Heads up</h2><p>The system is <strong>down</strong>.</p>`)

	if strings.Contains(out, "<") {
		t.Errorf("markup survived: %q", out)
	}
	if !strings.Contains(out, "Heads up") || !strings.Contains(out, "down") {
		t.Errorf("text was lost: %q", out)
	}
	// The text either side of a tag boundary must not run together.
	if strings.Contains(out, "Heads upThe") {
		t.Errorf("tag boundaries were not treated as word breaks: %q", out)
	}
}

// TestEmptyInputIsEmpty: an announcement with no body is allowed, and must not
// produce a stray fragment.
func TestEmptyInputIsEmpty(t *testing.T) {
	for _, input := range []string{"", "   ", "\n\t"} {
		if out := richtext.Sanitize(input); out != "" {
			t.Errorf("Sanitize(%q) = %q, want empty", input, out)
		}
		if out := richtext.SanitizePlain(input); out != "" {
			t.Errorf("SanitizePlain(%q) = %q, want empty", input, out)
		}
	}
}
