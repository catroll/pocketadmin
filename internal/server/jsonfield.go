package server

import (
	"encoding/json"
	"fmt"

	"github.com/pocketbase/pocketbase/tools/types"
)

// decodeJSON reads a stored json column into a Go value.
//
// PocketBase returns a json field as types.JSONRaw, which is a **named** []byte. A
// type switch matches exact types, so `case []byte` does not match it and the value
// falls through to the default branch and is silently lost. That is how a matrix
// generator produced no variants from two attributes with values: the list arrived,
// was not recognised, and came back empty with no error to show for it.
//
// It is one helper rather than a switch per call site because the mistake is
// invisible at the call site and was made three times before this existed.
func decodeJSON(value any) any {
	switch v := value.(type) {
	case nil:
		return nil
	case types.JSONRaw:
		return decodeJSONBytes([]byte(v))
	case json.RawMessage:
		return decodeJSONBytes(v)
	case []byte:
		return decodeJSONBytes(v)
	case string:
		return decodeJSONBytes([]byte(v))
	default:
		// A value set in this process is already decoded.
		return value
	}
}

// decodeJSONBytes unmarshals JSON text, returning it as-is when it does not parse.
func decodeJSONBytes(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return string(raw)
	}
	return out
}

// decodeJSONObject reads a stored json column into a map.
func decodeJSONObject(value any) map[string]any {
	switch v := decodeJSON(value).(type) {
	case map[string]any:
		return v
	case nil:
		return nil
	default:
		return nil
	}
}

// decodeJSONArray reads a stored json column into a slice.
func decodeJSONArray(value any) []any {
	switch v := decodeJSON(value).(type) {
	case []any:
		return v
	case map[string]any:
		// A single object where a list was expected is a shape an operator may
		// legitimately have stored; treating it as a one-element list is more
		// useful than refusing it.
		return []any{v}
	default:
		return nil
	}
}

// scalarText renders a decoded json value as text.
func scalarText(value any) string {
	switch v := decodeJSON(value).(type) {
	case nil:
		return ""
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	case float64:
		if v == float64(int64(v)) {
			return fmt.Sprintf("%d", int64(v))
		}
		return fmt.Sprintf("%v", v)
	default:
		return fmt.Sprint(v)
	}
}
