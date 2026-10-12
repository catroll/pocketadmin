// Package settings holds the typed configuration engine.
//
// The back office and the product surface are separate concerns -- separate
// pages, separate permissions, separate audit categories -- that share one table
// and one validation engine, discriminated by surface. The keys are declared in
// code rather than discovered from the database, so every setting has a known
// type, default and validator, and a row with no declaration is ignored. That
// last property is what lets a migration add a key without an older binary
// tripping over it.
//
// The engine is built so that a page cannot write the other surface's keys. It is
// not a check that could be forgotten: a write walks a Registry, and a Registry
// only contains its own surface's declarations, so a key belonging to the other
// surface is not reachable from it.
package settings

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Surface identifies which configuration a key belongs to.
type Surface string

// The two surfaces.
const (
	SurfaceAdmin   Surface = "admin"
	SurfaceProduct Surface = "product"
)

// ValueType is how a setting is stored and validated.
type ValueType string

// Value types.
const (
	TypeString ValueType = "string"
	TypeNumber ValueType = "number"
	TypeBool   ValueType = "bool"
	TypeJSON   ValueType = "json"
	TypeFile   ValueType = "file"
	TypeSelect ValueType = "select"
)

// Key is one declared setting.
type Key struct {
	// Surface and Category place the key; the pair with Key is unique.
	Surface  Surface
	Category string
	Key      string

	// Type decides how a value is validated and coerced.
	Type ValueType

	// Default is the value used when no row exists. It also documents the
	// intended value, which is why it is required rather than optional.
	Default any

	// Description is the help text shown beside the field.
	Description string

	// Public marks a product key readable without authentication. It has no
	// meaning on the admin surface, which is never public.
	Public bool

	// System marks a key that cannot be removed from the UI. Every declared key
	// is effectively system, since removing the row only reveals the default
	// again; the flag exists for keys a future UI should refuse to delete.
	System bool

	// Options lists the permitted values of a select.
	Options []string

	// Min and Max bound a number.
	Min *float64
	Max *float64

	// Pattern is an optional regular expression a string must match.
	Pattern string
}

// QualifiedName is the key's full name, "category.key".
func (k Key) QualifiedName() string { return k.Category + "." + k.Key }

// Registry is the set of keys one surface declares.
//
// A Registry is the only way to reach a key for writing, which is what makes
// cross-surface writes impossible rather than merely discouraged.
type Registry struct {
	surface Surface
	keys    map[string]Key
	order   []string
}

// newRegistry indexes a surface's declarations.
func newRegistry(surface Surface, keys []Key) *Registry {
	r := &Registry{
		surface: surface,
		keys:    make(map[string]Key, len(keys)),
		order:   make([]string, 0, len(keys)),
	}

	for _, k := range keys {
		k.Surface = surface
		name := k.QualifiedName()
		if _, dup := r.keys[name]; dup {
			// A duplicate would silently shadow, and the shadowed default would
			// never be seen. Fail loudly at construction instead.
			panic(fmt.Sprintf("settings: %s is declared twice on the %s surface", name, surface))
		}
		r.keys[name] = k
		r.order = append(r.order, name)
	}
	return r
}

// Surface returns which surface this registry describes.
func (r *Registry) Surface() Surface { return r.surface }

// Lookup returns a key by category and name.
func (r *Registry) Lookup(category, key string) (Key, bool) {
	k, ok := r.keys[category+"."+key]
	return k, ok
}

// Keys returns every key, in declaration order.
func (r *Registry) Keys() []Key {
	out := make([]Key, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.keys[name])
	}
	return out
}

// KeysIn returns the keys of one category, in declaration order.
func (r *Registry) KeysIn(category string) []Key {
	out := make([]Key, 0, len(r.order))
	for _, name := range r.order {
		if r.keys[name].Category == category {
			out = append(out, r.keys[name])
		}
	}
	return out
}

// Categories returns the distinct categories, in declaration order.
func (r *Registry) Categories() []string {
	seen := map[string]bool{}
	out := []string{}
	for _, name := range r.order {
		category := r.keys[name].Category
		if seen[category] {
			continue
		}
		seen[category] = true
		out = append(out, category)
	}
	return out
}

// Validate checks a submitted value against a key and returns the value to store.
//
// It coerces as well as checks, because the value arrives from a form as a string
// and has to become the declared type before it is written. Returning the coerced
// value rather than mutating in place means a caller cannot store something that
// was never validated.
func (k Key) Validate(value any) (any, error) {
	switch k.Type {
	case TypeBool:
		return coerceBool(value)

	case TypeNumber:
		number, err := coerceNumber(value)
		if err != nil {
			return nil, err
		}
		if k.Min != nil && number < *k.Min {
			return nil, fmt.Errorf("%s must be at least %v", k.QualifiedName(), *k.Min)
		}
		if k.Max != nil && number > *k.Max {
			return nil, fmt.Errorf("%s must be at most %v", k.QualifiedName(), *k.Max)
		}
		return number, nil

	case TypeSelect:
		text := strings.TrimSpace(coerceString(value))
		if len(k.Options) == 0 {
			return nil, fmt.Errorf("%s is a select with no options declared", k.QualifiedName())
		}
		// An empty value clears the setting; anything else has to be permitted.
		if text == "" {
			return "", nil
		}
		for _, option := range k.Options {
			if option == text {
				return text, nil
			}
		}
		return nil, fmt.Errorf("%s must be one of %s", k.QualifiedName(), strings.Join(k.Options, ", "))

	case TypeJSON:
		// Accept either a decoded value or the text a form submitted.
		if raw, ok := value.(string); ok {
			trimmed := strings.TrimSpace(raw)
			if trimmed == "" {
				return nil, nil
			}
			var decoded any
			if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
				return nil, fmt.Errorf("%s must be valid JSON: %w", k.QualifiedName(), err)
			}
			return decoded, nil
		}
		return value, nil

	case TypeFile:
		// A file setting stores a path or key; the upload itself goes through the
		// storage module.
		return strings.TrimSpace(coerceString(value)), nil

	default: // TypeString
		text := coerceString(value)
		if k.Pattern != "" {
			matched, err := regexp.MatchString(k.Pattern, text)
			if err != nil {
				return nil, fmt.Errorf("%s has an invalid pattern: %w", k.QualifiedName(), err)
			}
			if !matched && text != "" {
				return nil, fmt.Errorf("%s does not match the required format", k.QualifiedName())
			}
		}
		return text, nil
	}
}

// Coerce converts a stored or default value to the declared type, without
// validating it.
//
// It is used when reading: a value already in the database was validated on the
// way in, and a default is trusted because it is compiled in. Re-validating on
// every read would turn a bad row written by an older version into a broken page.
func (k Key) Coerce(value any) any {
	switch k.Type {
	case TypeBool:
		b, err := coerceBool(value)
		if err != nil {
			return false
		}
		return b
	case TypeNumber:
		n, err := coerceNumber(value)
		if err != nil {
			return 0
		}
		return n
	case TypeSelect, TypeString, TypeFile:
		return coerceString(value)
	default:
		return value
	}
}

// coerceBool accepts the shapes a checkbox, a JSON value and a database row
// produce.
func coerceBool(value any) (bool, error) {
	switch v := value.(type) {
	case nil:
		return false, nil
	case bool:
		return v, nil
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "", "0", "false", "off", "no":
			return false, nil
		case "1", "true", "on", "yes":
			return true, nil
		default:
			return false, fmt.Errorf("%q is not a boolean", v)
		}
	case float64:
		return v != 0, nil
	case int:
		return v != 0, nil
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return false, err
		}
		return f != 0, nil
	default:
		return false, fmt.Errorf("%T is not a boolean", value)
	}
}

// coerceNumber accepts a number or its text form.
func coerceNumber(value any) (float64, error) {
	switch v := value.(type) {
	case nil:
		return 0, nil
	case float64:
		return v, nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case json.Number:
		return v.Float64()
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return 0, nil
		}
		f, err := strconv.ParseFloat(trimmed, 64)
		if err != nil {
			return 0, fmt.Errorf("%q is not a number", v)
		}
		return f, nil
	default:
		return 0, fmt.Errorf("%T is not a number", value)
	}
}

// coerceString renders a value as text.
func coerceString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case json.Number:
		return v.String()
	case float64:
		// Integers are stored as floats by JSON; rendering 30 as "30" rather than
		// "30.000000" keeps a page-size field readable.
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	default:
		return fmt.Sprint(v)
	}
}
