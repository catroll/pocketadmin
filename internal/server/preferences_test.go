package server

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/rbac"
	"pocketadmin/internal/testsupport"
)

// account creates a user record.
func account(t *testing.T, app core.App, email string) *core.Record {
	t.Helper()

	col, _ := app.FindCollectionByNameOrId("users")
	rec := core.NewRecord(col)
	rec.Set("email", email)
	rec.Set("password", "change-me-please")
	rec.Set("passwordConfirm", "change-me-please")
	rec.Set("status", "active")
	if err := app.Save(rec); err != nil {
		t.Fatalf("save user: %v", err)
	}
	return rec
}

// TestPreferencePrincipalKeepsTheTwoKindsApart is a regression test for a real
// defect that made the preferences screen unusable for the account the README tells
// you to create first.
//
// The `user` relation points at `users`. A break-glass superuser has no `users`
// record -- the identity model gives every administrator a profile and a superuser is
// deliberately not one -- so writing its `_superusers` id into that relation failed
// with "Failed to find all relation records with the provided ids". The read path had
// been returning nil for a superuser while the write path tried to create a row for
// one, so the two halves disagreed and only the write half was wrong.
func TestPreferencePrincipalKeepsTheTwoKindsApart(t *testing.T) {
	app := testsupport.NewApp(t)

	user := account(t, app, "staff@example.com")
	admin := &rbac.Actor{UserID: user.Id, Email: user.Email(), AdminStatus: rbac.StatusActive}
	superuser := &rbac.Actor{UserID: "superuser-record-id", IsSuperuser: true}

	t.Run("an administrator is stored in the relation", func(t *testing.T) {
		rec, err := ensurePreferenceRecord(app, admin)
		if err != nil {
			t.Fatalf("ensurePreferenceRecord: %v", err)
		}
		if rec.GetString("user") != user.Id {
			t.Errorf("the user column holds %q, want the account id", rec.GetString("user"))
		}
		if rec.GetString("superuserId") != "" {
			t.Error("an administrator was also written to the superuser column")
		}
		if err := app.Save(rec); err != nil {
			t.Fatalf("saving an administrator's preferences failed: %v", err)
		}
	})

	t.Run("a superuser is stored in its own column", func(t *testing.T) {
		rec, err := ensurePreferenceRecord(app, superuser)
		if err != nil {
			t.Fatalf("ensurePreferenceRecord: %v", err)
		}
		if rec.GetString("superuserId") != "superuser-record-id" {
			t.Errorf("the superuser column holds %q", rec.GetString("superuserId"))
		}
		if rec.GetString("user") != "" {
			t.Error("a superuser was written into the users relation, which is the defect")
		}

		// The save is the assertion that matters: the relation validates on write and
		// is what refused the whole thing before.
		if err := app.Save(rec); err != nil {
			t.Fatalf("saving a superuser's preferences failed: %v", err)
		}
	})
}

// TestPreferencesRoundTripForBothKinds: what is saved is what is read back, and the
// two principals do not see each other's row.
func TestPreferencesRoundTripForBothKinds(t *testing.T) {
	app := testsupport.NewApp(t)

	user := account(t, app, "staff@example.com")
	admin := &rbac.Actor{UserID: user.Id, Email: user.Email(), AdminStatus: rbac.StatusActive}
	superuser := &rbac.Actor{UserID: "superuser-record-id", IsSuperuser: true}

	for _, tc := range []struct {
		name  string
		actor *rbac.Actor
		lang  string
	}{
		{"administrator", admin, "fr"},
		{"superuser", superuser, "zh-CN"},
	} {
		rec, err := ensurePreferenceRecord(app, tc.actor)
		if err != nil {
			t.Fatalf("%s: ensurePreferenceRecord: %v", tc.name, err)
		}
		rec.Set("language", tc.lang)
		rec.Set("theme", "dark")
		if err := app.Save(rec); err != nil {
			t.Fatalf("%s: save: %v", tc.name, err)
		}

		read := preferenceRecord(app, tc.actor)
		if read == nil {
			t.Fatalf("%s: nothing was read back", tc.name)
		}
		if read.GetString("language") != tc.lang {
			t.Errorf("%s: the language is %q, want %q", tc.name, read.GetString("language"), tc.lang)
		}
	}

	// The second save did not overwrite the first, and there are exactly two rows.
	count, err := app.CountRecords("user_preferences")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Errorf("%d preference rows exist, want one per principal", count)
	}

	if got := preferenceRecord(app, admin); got == nil || got.GetString("language") != "fr" {
		t.Error("the administrator's row was disturbed by the superuser's")
	}
	if got := preferenceRecord(app, superuser); got == nil || got.GetString("language") != "zh-CN" {
		t.Error("the superuser's row was disturbed by the administrator's")
	}
}

// TestNoPrincipalIsNotAnErrorWorthSaving: a request with no actor must not write a
// nameless row.
func TestNoPrincipalIsNotAnErrorWorthSaving(t *testing.T) {
	app := testsupport.NewApp(t)

	if got := preferenceRecord(app, nil); got != nil {
		t.Error("a nil actor read a preferences row")
	}
	if _, err := ensurePreferenceRecord(app, nil); err == nil {
		t.Error("a nil actor was given a preferences row to save")
	}
}

// TestTheStoredThemeIsOneTheStylesheetUnderstands.
//
// The value becomes the `data-bs-theme` attribute, which Bootstrap and AdminLTE match
// against exactly: both stylesheets contain rules for `light` and `dark` and no rule for
// anything else, including `auto`. A value outside that set therefore matches nothing and
// renders as the default, so a stored choice that is not recognised is worse than no
// choice -- the account believes it set something.
//
// `auto` is ours rather than Bootstrap's. It is accepted because it means something to a
// reader, and the layout resolves it into one of the two values the framework accepts.
func TestTheStoredThemeIsOneTheStylesheetUnderstands(t *testing.T) {
	cases := map[string]string{
		"light":          "light",
		"dark":           "dark",
		"auto":           "auto",
		"":               "light",
		"  dark  ":       "dark",
		"Dark":           "light", // the stylesheet matches case-sensitively
		"garbage":        "light",
		"light\" onload": "light",
	}

	for input, want := range cases {
		if got := normaliseTheme(input); got != want {
			t.Errorf("normaliseTheme(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestEveryOfferedThemeIsAccepted keeps the picker and the validator in step.
//
// The two are written in different languages in different files, so a fourth option added
// to the form and not to the validator would store the default instead of the choice --
// silently, and only for the people who picked it.
func TestEveryOfferedThemeIsAccepted(t *testing.T) {
	offered := []string{
		"light", // preferences.html
		"dark",
		"auto",
	}

	for _, value := range offered {
		if got := normaliseTheme(value); got != value {
			t.Errorf("the form offers %q but it is stored as %q", value, got)
		}
	}

	// And the reverse: a value the validator accepts that the form does not offer is
	// unreachable, which is dead code rather than a bug, but it should be deliberate.
	for _, allowed := range preferenceThemes() {
		found := false
		for _, value := range offered {
			if value == allowed {
				found = true
			}
		}
		if !found {
			t.Errorf("%q is accepted but the form does not offer it", allowed)
		}
	}
}
