package audit_test

import (
	"strings"
	"testing"
	"time"

	"pocketadmin/internal/audit"
)

// TestRedactTerminatesOnMalformedJSONishStrings is the regression test for a defect
// that killed the process.
//
// looksLikeJSON accepts anything that starts with a brace and ends with one, and
// parseJSON used to hand an unparsable input straight back. The caller only reached
// parseJSON because the value looked like JSON, so it recognised the same string
// again and recursed until the stack was gone: a fatal error, not a caught panic.
//
// It was reachable from any request body, because the operation log redacts the
// body it is sent.
func TestRedactTerminatesOnMalformedJSONishStrings(t *testing.T) {
	inputs := []string{
		`{not valid json}`,
		`{"unterminated": `,
		`[1, 2, 3`,
		`{}{}`,
		`{}`,
		`[]`,
		`{`, `}`,
		`{"a":}`,
		`{single quotes: 'x'}`,
		`{ "nested": {not json} }`,
		`{{{{}}}}`,
		`["unclosed`,
	}

	for _, input := range inputs {
		// The assertion is that this returns at all.
		done := make(chan string, 1)
		go func(value string) {
			done <- audit.RedactToString(value)
		}(input)

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("Redact did not terminate on %q", input)
		}
	}
}

// TestRedactStillWalksWellFormedJSON: the fix must not have turned the walk off.
func TestRedactStillWalksWellFormedJSON(t *testing.T) {
	input := `{"user":"someone","password":"hunter2","nested":{"apiKey":"sk-live-1"}}`

	out := audit.RedactToString(input)

	if strings.Contains(out, "hunter2") || strings.Contains(out, "sk-live-1") {
		t.Errorf("a secret survived: %s", out)
	}
	if !strings.Contains(out, "someone") {
		t.Errorf("the non-secret was lost: %s", out)
	}
}

// TestRedactDoesNotClaimToHaveExaminedUnparsableBytes: handing the raw bytes back
// would present an unexamined payload as a redacted one.
func TestRedactDoesNotClaimToHaveExaminedUnparsableBytes(t *testing.T) {
	out := audit.RedactToString([]byte("{broken"))

	if strings.Contains(out, "broken") {
		t.Errorf("unparsable bytes were passed through as though they had been redacted: %q", out)
	}
}
