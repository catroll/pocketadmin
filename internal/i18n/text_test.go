package i18n_test

import (
	"testing"

	"pocketadmin/internal/i18n"
)

func TestTitlesResolveByEnglish(t *testing.T) {
	for _, tc := range []struct{ en, zhCN, zhTW string }{
		{"New order", "新建订单", "新增訂單"},
		{"Sign in", "登录", "登入"},
		{"Two-factor authentication", "双因素认证", "雙因素驗證"},
		{"Dashboard", "仪表盘", "儀表板"},
	} {
		if got := i18n.Text(i18n.SimplifiedChinese, tc.en); got != tc.zhCN {
			t.Errorf("Text(zh-CN, %q) = %q, want %q", tc.en, got, tc.zhCN)
		}
		if got := i18n.Text(i18n.TraditionalChinese, tc.en); got != tc.zhTW {
			t.Errorf("Text(zh-TW, %q) = %q, want %q", tc.en, got, tc.zhTW)
		}
		// English is the source, so it is returned as it was.
		if got := i18n.Text(i18n.English, tc.en); got != tc.en {
			t.Errorf("Text(en, %q) = %q", tc.en, got)
		}
	}

	// A sentence that is not catalogued is returned unchanged, so wrapping a string
	// speculatively cannot replace it with a key.
	if got := i18n.Text(i18n.SimplifiedChinese, "A sentence nobody catalogued"); got != "A sentence nobody catalogued" {
		t.Errorf("an unknown sentence became %q", got)
	}
}
