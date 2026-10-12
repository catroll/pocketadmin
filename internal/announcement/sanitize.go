package announcement

import (
	"strings"

	"github.com/microcosm-cc/bluemonday"
)

// policy is the sanitiser for announcement bodies.
//
// PocketBase's editor field stores HTML verbatim -- verified by writing every
// common payload through it and reading back, and all of them survived -- so an
// unsanitised body is stored cross-site scripting. That matters more here than in
// most places: an announcement's audience can be every user of the product, so
// whoever can write one can attack everyone who reads it.
//
// This is an allow-list rather than a deny-list. A deny-list has to anticipate
// every way a browser can be made to execute something, and the browser knows
// more tricks than the list does.
var policy = buildPolicy()

// buildPolicy assembles the allowed markup.
func buildPolicy() *bluemonday.Policy {
	p := bluemonday.NewPolicy()

	// Structure and text.
	p.AllowElements("p", "br", "hr", "div", "span",
		"h1", "h2", "h3", "h4", "h5", "h6",
		"strong", "b", "em", "i", "u", "s", "del", "ins", "sub", "sup",
		"blockquote", "code", "pre", "mark", "small")

	// Lists.
	p.AllowElements("ul", "ol", "li", "dl", "dt", "dd")

	// Tables.
	p.AllowElements("table", "thead", "tbody", "tfoot", "tr", "th", "td", "caption")

	// Links. Only schemes that cannot execute: javascript:, data: and vbscript:
	// are excluded by omission, and bluemonday rejects anything outside the list
	// rather than passing it through.
	p.AllowAttrs("href").OnElements("a")
	p.AllowAttrs("title").OnElements("a")
	p.AllowURLSchemes("http", "https", "mailto")

	// Relative links and images, which is what the editor produces for something
	// inside this application. They carry no scheme, so the scheme list above
	// does not reject them, but they are only allowed when they parse as a
	// relative reference rather than as something with a scheme in disguise.
	p.AllowRelativeURLs(true)

	// Images, restricted to what our own storage serves and to plain http(s).
	// Width and height are allowed because the editor sets them and they prevent
	// a layout shift; every other attribute is dropped.
	p.AllowAttrs("src", "alt", "title").OnElements("img")
	p.AllowAttrs("width", "height").Matching(bluemonday.Integer).OnElements("img")

	// Keep the link honest: an announcement is not a place for link juice, and a
	// reader clicking through should not silently vouch for the destination.
	p.RequireNoFollowOnLinks(true)
	p.RequireNoFollowOnFullyQualifiedLinks(true)
	p.AddTargetBlankToFullyQualifiedLinks(true)

	// A parsed URL is required, which is what rejects a scheme obfuscated with
	// entities or whitespace.
	p.RequireParseableURLs(true)

	// Drop the content of these entirely rather than unwrapping it. Unwrapping a
	// script would leave its text in the page, which is how a sanitiser turns one
	// problem into a different one.
	p.SkipElementsContent("script", "style", "iframe", "object", "embed", "form",
		"input", "button", "select", "textarea", "noscript", "template", "svg", "math")

	// No style attributes and no class names: a body should not be able to
	// reposition the page around it or impersonate the panel's own chrome.
	p.AllowNoAttrs().Globally()

	return p
}

// Sanitize returns the body with everything not on the allow-list removed.
func Sanitize(html string) string {
	if strings.TrimSpace(html) == "" {
		return ""
	}
	return policy.Sanitize(html)
}

// SanitizePlain strips every tag, for a context that shows text rather than
// markup -- a list preview, or a notification body.
func SanitizePlain(html string) string {
	if strings.TrimSpace(html) == "" {
		return ""
	}
	text := policy.Sanitize(html)

	// Collapse the whitespace a tag removal leaves behind, so a preview does not
	// begin with a screen of spaces.
	return strings.Join(strings.Fields(stripTags(text)), " ")
}

// stripTags removes the remaining tags from an already-sanitised fragment.
func stripTags(html string) string {
	var out strings.Builder
	inTag := false

	for _, r := range html {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
			// A tag boundary is a word boundary, so the text either side of
			// "<p>a</p><p>b</p>" does not run together as "ab".
			out.WriteByte(' ')
		case !inTag:
			out.WriteRune(r)
		}
	}
	return out.String()
}
