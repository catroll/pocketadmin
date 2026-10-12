package audit

import (
	"encoding/json"
	"strings"
)

// RedactedValue is what replaces a secret in a log entry.
//
// It is a fixed marker rather than an empty string so a reader can tell "this was
// a secret and has been removed" from "this field was empty", which matters when
// reconstructing what a request actually carried.
const RedactedValue = "[redacted]"

// denyTokens are matched against a normalised field name. A key containing any of
// them is redacted unless it is explicitly allowed.
//
// Matching is on substrings because secret fields are named inconsistently --
// `password`, `passwordConfirm`, `newPassword`, `user_password` all have to be
// caught by one rule, and a list of exact names would miss the next one somebody
// invents.
var denyTokens = []string{
	"password",
	"passwd",
	"passphrase",
	"secret",
	"token",
	"apikey",
	"authorization",
	"authheader",
	"cookie",
	"credential",
	"privatekey",
	"totp",
	"otpcode",
	"recoverycode",
	"signature",
	"csrf",
	"sessionid",
	"sessionkey",
	"clientsecret",
	"accesskey",
	"refresh",
}

// allowList names fields that are safe to record even though their name matches a
// deny token.
//
// Without it, a deny-by-substring rule is unusable: `tokenCount` and
// `passwordChangedAt` read as secrets to the matcher but are exactly what an
// investigator wants to see.
var allowList = map[string]bool{
	"tokencount":        true,
	"tokenexpiresat":    true,
	"passwordchangedat": true,
	"passwordexpiresat": true,
	"sessioncount":      true,
	"csrftokenpresent":  true,
	"secretconfigured":  true,
	"secretsconfigured": true,
	"otpenabled":        true,
}

// normaliseKey lowercases a field name and drops the separators people put in it,
// so `api_key`, `apiKey` and `API-KEY` all match one deny token.
func normaliseKey(key string) string {
	var b strings.Builder
	b.Grow(len(key))
	for _, r := range key {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case r == '_' || r == '-' || r == '.' || r == ' ':
			// dropped
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// IsSecret reports whether a field name must not be recorded.
func IsSecret(key string) bool {
	normalised := normaliseKey(key)
	if normalised == "" {
		return false
	}
	if allowList[normalised] {
		return false
	}
	for _, token := range denyTokens {
		if strings.Contains(normalised, token) {
			return true
		}
	}
	return false
}

// Redact returns a copy of value with every secret field replaced.
//
// It walks maps, slices and JSON, because a request body reaches a log in any of
// those shapes and a redactor that only understood one of them would leak through
// the others. Anything that cannot be walked is returned as a string, since the
// log column is text and an unrepresentable value must not become a silent gap.
func Redact(value any) any {
	switch v := value.(type) {
	case nil:
		return nil

	case map[string]any:
		out := make(map[string]any, len(v))
		for key, inner := range v {
			if IsSecret(key) {
				out[key] = RedactedValue
				continue
			}
			out[key] = Redact(inner)
		}
		return out

	case []any:
		out := make([]any, 0, len(v))
		for _, inner := range v {
			out = append(out, Redact(inner))
		}
		return out

	case json.RawMessage:
		return Redact(parseJSON(v))

	case []byte:
		// A body arrives as bytes; try to read it as JSON so nested secrets are
		// found, and fall back to a redacted placeholder when it is not.
		return Redact(parseJSON(v))

	case string:
		// A JSON string that was already serialised is still worth walking, since
		// that is how a body reaches a log when it is passed through as text.
		if looksLikeJSON(v) {
			return Redact(parseJSON([]byte(v)))
		}
		return v

	default:
		return v
	}
}

// parseJSON returns the decoded value, or the original bytes when they are not
// JSON.
func parseJSON(raw []byte) any {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return ""
	}

	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		// Not JSON. Returning it as a string means a non-JSON body is recorded as
		// text, which is better than dropping it -- but it has not been walked, so
		// it must not be presented as redacted.
		return trimmed
	}
	return decoded
}

// looksLikeJSON is a cheap test for whether a string is worth parsing.
func looksLikeJSON(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 2 {
		return false
	}
	return (s[0] == '{' && s[len(s)-1] == '}') || (s[0] == '[' && s[len(s)-1] == ']')
}

// RedactToString renders a redacted value as JSON text for storage in a log
// column, falling back to a plain string.
func RedactToString(value any) string {
	redacted := Redact(value)

	encoded, err := json.Marshal(redacted)
	if err != nil {
		return RedactedValue
	}

	// A JSON string marshals with surrounding quotes; store the raw text so a
	// plain body does not look like a JSON string.
	if s, ok := redacted.(string); ok {
		return s
	}
	return string(encoded)
}
