// Package i18n holds the interface translations.
//
// The messages are Go values rather than files loaded at run time. A catalogue in the
// binary cannot go missing from a deployment, cannot fail to parse at startup, and is
// checked by the compiler when it is referenced through a constant -- which is the same
// reasoning that put the permission codes in Go.
//
// A missing translation falls back to English and then to the key itself. Showing the key
// is deliberate: a screen with `monitoring.nodes.title` on it is obviously unfinished,
// and a screen with a blank where a label should be is a bug report nobody can act on.
package i18n

import (
	"fmt"
	"sort"
	"strings"
)

// Locale is an interface language.
type Locale string

// The shipped locales.
const (
	// English is the fallback and the language the messages are written in first.
	English Locale = "en"

	// SimplifiedChinese is the mainland convention.
	SimplifiedChinese Locale = "zh-CN"

	// TraditionalChinese is the Taiwan and Hong Kong convention. It is a separate
	// catalogue rather than a conversion: the two differ in vocabulary as well as in
	// characters -- 软件 and 軟體 are the same thing and no transliteration produces
	// one from the other -- so a mechanical conversion would be wrong in the places a
	// reader would notice most.
	TraditionalChinese Locale = "zh-TW"
)

// Locales lists the shipped languages in the order a picker should offer them.
func Locales() []Locale {
	return []Locale{English, SimplifiedChinese, TraditionalChinese}
}

// Default is the language used when nothing is chosen.
const Default = English

// Normalize maps what a browser or a form sends onto a shipped locale.
//
// It accepts the tags a browser actually sends -- `zh-Hans`, `zh_CN`, `zh` -- because a
// language that is offered and then not recognised is worse than one not offered at all.
func Normalize(raw string) Locale {
	tag := strings.ToLower(strings.TrimSpace(raw))
	if tag == "" {
		return Default
	}

	// A quality value, as Accept-Language carries: "zh-CN;q=0.9".
	if semicolon := strings.IndexByte(tag, ';'); semicolon > 0 {
		tag = strings.TrimSpace(tag[:semicolon])
	}
	tag = strings.ReplaceAll(tag, "_", "-")

	switch {
	case tag == "en" || strings.HasPrefix(tag, "en-"):
		return English

	case tag == "zh-tw" || tag == "zh-hk" || tag == "zh-mo" ||
		strings.HasPrefix(tag, "zh-hant"):
		return TraditionalChinese

	case tag == "zh" || strings.HasPrefix(tag, "zh-"):
		// A bare `zh` defaults to Simplified, which is what most of the people who
		// send it mean. A reader who wanted the other one can choose it.
		return SimplifiedChinese

	default:
		return Default
	}
}

// Name returns a locale's name in its own language, for a picker.
func (l Locale) Name() string {
	switch l {
	case SimplifiedChinese:
		return "简体中文"
	case TraditionalChinese:
		return "繁體中文"
	default:
		return "English"
	}
}

// HTMLTag is the value for a document's lang attribute.
func (l Locale) HTMLTag() string { return string(l) }

// catalogue is the message table, locale by locale.
//
// Keys are dotted and grouped by where they appear: `nav.` for navigation, `common.` for
// words that appear on many screens, and a module name for the rest. A key is a name, not
// a sentence -- it says where the message lives so that moving it is a rename rather than
// a hunt.
var catalogue = map[Locale]map[string]string{
	English:            english,
	SimplifiedChinese:  simplifiedChinese,
	TraditionalChinese: traditionalChinese,
}

// T renders a message in a locale.
//
// The lookup order is the locale, then English, then the key. Arguments are interpolated
// into `%s`-style verbs, so a message can carry a count without the caller assembling a
// sentence from fragments -- which is the thing that makes translation impossible.
func T(locale Locale, key string, args ...any) string {
	message := lookup(locale, key)
	if message == "" {
		message = lookup(English, key)
	}
	if message == "" {
		// The key itself. See the package comment: a visible key is a bug somebody can
		// report, a blank is not.
		message = key
	}

	if len(args) == 0 {
		return message
	}
	return fmt.Sprintf(message, args...)
}

// lookup finds a message, preferring the exact locale and then its base language.
func lookup(locale Locale, key string) string {
	if table, ok := catalogue[locale]; ok {
		if message, ok := table[key]; ok {
			return message
		}
	}

	// A regional tag falls back to its base: `zh-CN` to `zh`, and so on. Without this a
	// locale added later would show keys until every message was copied into it.
	if dash := strings.IndexByte(string(locale), '-'); dash > 0 {
		if table, ok := catalogue[Locale(string(locale)[:dash])]; ok {
			if message, ok := table[key]; ok {
				return message
			}
		}
	}
	return ""
}

// Missing returns the keys a locale does not have.
//
// It exists for the coverage test: a locale that is offered and half-translated is worse
// than one that is not offered, and the way to know is to ask.
func Missing(locale Locale) []string {
	table := catalogue[locale]

	var missing []string
	for key := range english {
		if _, ok := table[key]; !ok {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	return missing
}

// Keys returns every declared key, for a coverage test in the other direction.
func Keys() []string {
	out := make([]string, 0, len(english))
	for key := range english {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// Extra returns the keys a locale has that English does not, which are usually typos.
func Extra(locale Locale) []string {
	table := catalogue[locale]

	var extra []string
	for key := range table {
		if _, ok := english[key]; !ok {
			extra = append(extra, key)
		}
	}
	sort.Strings(extra)
	return extra
}
