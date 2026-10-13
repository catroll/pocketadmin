package i18n_test

import (
	"strings"
	"testing"

	"pocketadmin/internal/i18n"
)

// TestEveryLocaleIsComplete is the property that decides whether a language should be
// offered at all.
//
// A locale that is offered and half-translated is worse than one that is not offered: the
// reader gets a screen in two languages and cannot tell whether the gap is their own
// mistake. The catalogue is small enough that this is a hard requirement rather than an
// aspiration.
func TestEveryLocaleIsComplete(t *testing.T) {
	for _, locale := range i18n.Locales() {
		if missing := i18n.Missing(locale); len(missing) > 0 {
			t.Errorf("%s is missing %d message(s): %s", locale, len(missing),
				strings.Join(firstFew(missing, 6), ", "))
		}
		if extra := i18n.Extra(locale); len(extra) > 0 {
			// A key that exists only in a translation is usually a typo, and a typo
			// means the message it was meant to be is showing its key.
			t.Errorf("%s has %d key(s) English does not: %s", locale, len(extra),
				strings.Join(firstFew(extra, 6), ", "))
		}
	}
}

// TestNoMessageIsLeftInEnglish.
//
// A translation that is identical to the English is either a word that genuinely has no
// translation -- an acronym, a product name -- or a key somebody forgot. The short
// allow-list below is the first kind; anything else appearing here is the second, and the
// test names it so somebody can decide which.
func TestNoMessageIsLeftInEnglish(t *testing.T) {
	// Words that are the same in both languages, or are names.
	allowed := map[string]bool{
		"nav.developer.api":  true, // "API" is "API"
		"nav.developer.keys": true,
	}

	for _, locale := range []i18n.Locale{i18n.SimplifiedChinese, i18n.TraditionalChinese} {
		for _, key := range i18n.Keys() {
			if allowed[key] {
				continue
			}
			got := i18n.T(locale, key)
			want := i18n.T(i18n.English, key)
			if got == want {
				t.Errorf("%s: %q is identical to the English (%q)", locale, key, want)
			}
		}
	}
}

// TestTheTraditionalCatalogueIsNotAConversion is the reason there are two Chinese
// catalogues rather than one and a converter.
//
// The first version of this test counted messages that are identical in both, on the
// theory that a conversion would leave many the same. It found eighteen, and every one of
// them is a word that genuinely uses the same characters in both scripts -- 取消, 全部,
// 是, 否, 商品, 角色. Counting overlap measures the language, not the translation.
//
// The signal that does work is characters. A small set of glyphs exist in one script and
// not the other, so a Simplified character in the Traditional catalogue is a conversion
// that was not finished, and the other way round is a translation pasted into the wrong
// file.
func TestTheTraditionalCatalogueIsNotAConversion(t *testing.T) {
	// Characters that exist only in the Simplified set, and their Traditional partners.
	// Only characters that exist in one script and not the other. 限, 管, 理 and many
	// others are identical in both, and putting one here produces a failure that says
	// nothing.
	simplifiedOnly := "数单务员设种类别订会员规则权删标记时间线个问题为这样经过还没开关闭团队"
	traditionalOnly := "數單務員設種類別訂會員規則權刪標記時間線個問題為這樣經過還沒開關閉團隊"

	for _, key := range i18n.Keys() {
		simplified := i18n.T(i18n.SimplifiedChinese, key)
		traditional := i18n.T(i18n.TraditionalChinese, key)

		for _, r := range simplifiedOnly {
			if strings.ContainsRune(traditional, r) {
				t.Errorf("%s: the Traditional message %q contains the Simplified character %q",
					key, traditional, r)
				break
			}
		}
		for _, r := range traditionalOnly {
			if strings.ContainsRune(simplified, r) {
				t.Errorf("%s: the Simplified message %q contains the Traditional character %q",
					key, simplified, r)
				break
			}
		}
	}
}

// TestNormalizeAcceptsWhatBrowsersSend.
//
// A language that is offered and then not recognised is worse than one not offered: the
// picker says 简体中文, the reader chooses it, and the panel stays English.
func TestNormalizeAcceptsWhatBrowsersSend(t *testing.T) {
	cases := map[string]i18n.Locale{
		"":            i18n.English,
		"en":          i18n.English,
		"en-GB":       i18n.English,
		"en-US":       i18n.English,
		"zh":          i18n.SimplifiedChinese,
		"zh-CN":       i18n.SimplifiedChinese,
		"zh_CN":       i18n.SimplifiedChinese,
		"zh-Hans":     i18n.SimplifiedChinese,
		"zh-Hans-CN":  i18n.SimplifiedChinese,
		"zh-TW":       i18n.TraditionalChinese,
		"zh_HK":       i18n.TraditionalChinese,
		"zh-Hant":     i18n.TraditionalChinese,
		"zh-Hant-TW":  i18n.TraditionalChinese,
		" ZH-cn ":     i18n.SimplifiedChinese,
		"zh-CN;q=0.9": i18n.SimplifiedChinese,
		"fr":          i18n.English,
		"de-DE":       i18n.English,
	}

	for raw, want := range cases {
		if got := i18n.Normalize(raw); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", raw, got, want)
		}
	}
}

// TestAMissingMessageFallsBackVisibly.
//
// The fallback is English and then the key itself. A visible key is a bug somebody can
// report; a blank is not.
func TestAMissingMessageFallsBackVisibly(t *testing.T) {
	if got := i18n.T(i18n.SimplifiedChinese, "does.not.exist"); got != "does.not.exist" {
		t.Errorf("a missing key rendered as %q, want the key itself", got)
	}

	// A locale that is not shipped at all falls back to English rather than to keys.
	if got := i18n.T(i18n.Locale("fr"), "common.save"); got != "Save" {
		t.Errorf("an unshipped locale rendered %q, want the English", got)
	}
}

// TestMessagesCarryTheirArguments.
func TestMessagesCarryTheirArguments(t *testing.T) {
	if got := i18n.T(i18n.English, "common.rows", 3); got != "3 row(s)" {
		t.Errorf("got %q", got)
	}
	if got := i18n.T(i18n.SimplifiedChinese, "common.rows", 3); got != "3 条" {
		t.Errorf("got %q", got)
	}
	if got := i18n.T(i18n.TraditionalChinese, "common.rows", 3); got != "3 筆" {
		t.Errorf("got %q", got)
	}
}

// TestALocaleNamesItselfInItsOwnLanguage: a picker that says "Chinese (Simplified)" to a
// reader who only reads Chinese is a picker they cannot use.
func TestALocaleNamesItselfInItsOwnLanguage(t *testing.T) {
	if got := i18n.SimplifiedChinese.Name(); got != "简体中文" {
		t.Errorf("the simplified name is %q", got)
	}
	if got := i18n.TraditionalChinese.Name(); got != "繁體中文" {
		t.Errorf("the traditional name is %q", got)
	}
	if got := i18n.English.Name(); got != "English" {
		t.Errorf("the English name is %q", got)
	}
}

// firstFew lists at most n values, for a failure message that stays readable.
func firstFew(values []string, n int) []string {
	if len(values) <= n {
		return values
	}
	return values[:n]
}
