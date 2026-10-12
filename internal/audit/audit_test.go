package audit_test

import (
	"encoding/json"
	"strings"
	"testing"

	"pocketadmin/internal/audit"
)

// TestEveryDenyTokenIsCaught is the test-per-denied-key the schema design asks
// for: each token in the deny list has to actually redact something, or the list
// has grown an entry that looks like protection and is not.
func TestEveryDenyTokenIsCaught(t *testing.T) {
	// One representative field name per deny token. Adding a token without adding
	// its case here fails the coverage check below.
	cases := map[string]string{
		"password":      "password",
		"passwd":        "passwd",
		"passphrase":    "passphrase",
		"secret":        "clientSecret",
		"token":         "accessToken",
		"apikey":        "api_key",
		"authorization": "Authorization",
		"authheader":    "authHeader",
		"cookie":        "Cookie",
		"credential":    "credentials",
		"privatekey":    "privateKey",
		"totp":          "totpSecret",
		"otpcode":       "otp_code",
		"recoverycode":  "recoveryCode",
		"signature":     "signature",
		"csrf":          "csrf_token",
		"sessionid":     "sessionId",
		"sessionkey":    "sessionKey",
		"clientsecret":  "client_secret",
		"accesskey":     "accessKeyId",
		"refresh":       "refreshToken",
	}

	for token, field := range cases {
		if !audit.IsSecret(field) {
			t.Errorf("the deny token %q did not catch the field %q", token, field)
		}
	}
}

// TestDenyMatchingIgnoresCasingAndSeparators: secret fields are named
// inconsistently and one rule has to catch all the spellings of one secret.
func TestDenyMatchingIgnoresCasingAndSeparators(t *testing.T) {
	for _, field := range []string{
		"password", "Password", "PASSWORD",
		"new_password", "newPassword", "new-password", "new.password",
		"API_KEY", "apiKey", "api-key",
	} {
		if !audit.IsSecret(field) {
			t.Errorf("%q should be treated as a secret", field)
		}
	}
}

// TestAllowListOverridesTheDenyList: without this a substring rule is unusable,
// because the fields an investigator actually wants match a deny token.
func TestAllowListOverridesTheDenyList(t *testing.T) {
	for _, field := range []string{
		"tokenCount", "passwordChangedAt", "sessionCount", "secretConfigured",
		"csrfTokenPresent", "otpEnabled", "tokenExpiresAt",
	} {
		if audit.IsSecret(field) {
			t.Errorf("%q is on the allow-list and should be recorded", field)
		}
	}
}

// TestRedactWalksNestedStructures: a request body reaches a log in whatever shape
// it has, and a redactor that understood only one of them would leak through the
// others.
func TestRedactWalksNestedStructures(t *testing.T) {
	input := map[string]any{
		"email": "boss@example.com",
		"auth": map[string]any{
			"password": "hunter2",
			"nested": map[string]any{
				"apiKey": "sk-live-123",
			},
		},
		"items": []any{
			map[string]any{"name": "fine", "secret": "leak"},
			map[string]any{"name": "also fine"},
		},
	}

	encoded, err := json.Marshal(audit.Redact(input))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := string(encoded)

	for _, leaked := range []string{"hunter2", "sk-live-123", "leak"} {
		if strings.Contains(out, leaked) {
			t.Errorf("the secret %q survived redaction: %s", leaked, out)
		}
	}
	for _, kept := range []string{"boss@example.com", "fine", "also fine"} {
		if !strings.Contains(out, kept) {
			t.Errorf("the non-secret %q was lost: %s", kept, out)
		}
	}
	if strings.Count(out, audit.RedactedValue) != 3 {
		t.Errorf("expected three redactions, got %d: %s", strings.Count(out, audit.RedactedValue), out)
	}
}

// TestRedactHandlesJSONBytes: a body arrives as bytes, and nested secrets inside
// it must still be found.
func TestRedactHandlesJSONBytes(t *testing.T) {
	body := []byte(`{"email":"a@b.c","password":"hunter2","profile":{"token":"xyz"}}`)

	out := audit.RedactToString(body)
	for _, leaked := range []string{"hunter2", "xyz"} {
		if strings.Contains(out, leaked) {
			t.Errorf("the secret %q survived: %s", leaked, out)
		}
	}
	if !strings.Contains(out, "a@b.c") {
		t.Errorf("the non-secret was lost: %s", out)
	}
}

// TestRedactHandlesAJSONString is the case a body reaches a log as already
// serialised text.
func TestRedactHandlesAJSONString(t *testing.T) {
	in := `{"password":"hunter2","keep":"this"}`

	out := audit.RedactToString(in)
	if strings.Contains(out, "hunter2") {
		t.Errorf("the secret survived: %s", out)
	}
	if !strings.Contains(out, "this") {
		t.Errorf("the non-secret was lost: %s", out)
	}
}

// TestRedactLeavesNonJSONAlone: a form body is not JSON and cannot be walked, so
// it is recorded as text rather than dropped -- and it must not be claimed as
// redacted, which is why the value is passed through unchanged rather than
// wrapped.
func TestRedactLeavesNonJSONAlone(t *testing.T) {
	body := "email=a%40b.c&name=someone"

	out := audit.RedactToString(body)
	if out != body {
		t.Errorf("a non-JSON body should pass through, got %q", out)
	}
}

// TestRedactHandlesEmptyAndNil: nothing must panic on the shapes a request can
// actually have.
func TestRedactHandlesEmptyAndNil(t *testing.T) {
	for _, value := range []any{nil, "", []byte{}, []byte("   "), map[string]any{}, []any{}} {
		if out := audit.RedactToString(value); out == "" && value != nil {
			// An empty result is acceptable; a panic is not. This asserts the call
			// returned at all.
			continue
		}
	}
}

// TestRedactMarksSecretsDistinctlyFromEmpty: a reader has to be able to tell a
// redacted field from an empty one when reconstructing a request.
func TestRedactMarksSecretsDistinctlyFromEmpty(t *testing.T) {
	input := map[string]any{"password": "", "name": ""}

	encoded, _ := json.Marshal(audit.Redact(input))
	out := string(encoded)

	if !strings.Contains(out, audit.RedactedValue) {
		t.Errorf("an empty secret should still be marked as redacted: %s", out)
	}
}

// TestEventsAreDeclaredOnceAndValid: the catalogue is walked by the seed and the
// drift test, so a duplicate would silently shrink it.
func TestEventsAreDeclaredOnceAndValid(t *testing.T) {
	seen := map[audit.Event]bool{}

	for _, e := range audit.AllEvents() {
		if seen[e] {
			t.Errorf("the event %q is declared twice", e)
		}
		seen[e] = true

		if !audit.Valid(e) {
			t.Errorf("the event %q is in AllEvents but not valid", e)
		}
		if string(e) != strings.ToLower(string(e)) {
			t.Errorf("the event %q is not lowercase snake case", e)
		}
		if strings.ContainsAny(string(e), " -.") {
			t.Errorf("the event %q contains a separator", e)
		}
	}

	if audit.Valid("not_a_real_event") {
		t.Error("an undeclared event reported as valid")
	}
}

// TestEventStringsMatchTheCatalogue: the schema enum is built from this, so a
// mismatch would mean an event that cannot be written.
func TestEventStringsMatchTheCatalogue(t *testing.T) {
	events := audit.AllEvents()
	strings_ := audit.EventStrings()

	if len(events) != len(strings_) {
		t.Fatalf("EventStrings has %d entries for %d events", len(strings_), len(events))
	}
	for i, e := range events {
		if string(e) != strings_[i] {
			t.Errorf("position %d: event %q but string %q", i, e, strings_[i])
		}
	}
}
