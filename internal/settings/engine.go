package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"sync"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
)

// CollectionSettings is the settings collection's name.
const CollectionSettings = "settings"

// Engine reads and writes settings through a registry.
//
// Every read merges the stored rows over the compiled-in defaults, so a key that
// has never been set still answers with a usable value and a page never has to
// special-case a missing row.
type Engine struct {
	app core.App

	mu    sync.RWMutex
	cache map[Surface]map[string]any
	// version changes whenever anything is written, so a cached surface can be
	// discarded without knowing which key changed.
	version uint64
}

// NewEngine returns an engine bound to an application.
func NewEngine(app core.App) *Engine {
	return &Engine{app: app, cache: make(map[Surface]map[string]any)}
}

// Invalidate drops every cached value.
//
// Called after a write. It is deliberately coarse: the values are read on every
// page render and written rarely, so a whole-surface refresh is cheaper to reason
// about than tracking which key changed.
func (e *Engine) Invalidate() {
	e.mu.Lock()
	e.cache = make(map[Surface]map[string]any)
	e.version++
	e.mu.Unlock()
}

// Values returns every key of a surface, stored values merged over defaults.
func (e *Engine) Values(reg *Registry) map[string]any {
	surface := reg.Surface()

	e.mu.RLock()
	if cached, ok := e.cache[surface]; ok {
		e.mu.RUnlock()
		return cached
	}
	e.mu.RUnlock()

	values := e.load(reg)

	e.mu.Lock()
	e.cache[surface] = values
	e.mu.Unlock()

	return values
}

// load reads the stored rows and merges them over the defaults.
func (e *Engine) load(reg *Registry) map[string]any {
	values := make(map[string]any, len(reg.Keys()))

	// Defaults first, coerced so a consumer always sees the declared type.
	for _, key := range reg.Keys() {
		values[key.QualifiedName()] = key.Coerce(key.Default)
	}

	records, err := e.app.FindRecordsByFilter(CollectionSettings,
		"surface = {:s}", "", 0, 0, map[string]any{"s": string(reg.Surface())})
	if err != nil {
		// A missing collection or a query failure leaves the defaults in place,
		// which is the safe direction: the panel works, it just is not configured.
		slog.Warn("could not read settings; using defaults", "surface", reg.Surface(), "error", err)
		return values
	}

	for _, rec := range records {
		category := rec.GetString("category")
		name := rec.GetString("key")

		key, known := reg.Lookup(category, name)
		if !known {
			// A row with no declaration is ignored rather than surfaced. This is
			// what lets a migration add a key without an older binary tripping on
			// it, and it is the right default in both directions: a stale row
			// cannot inject a value nothing validates.
			continue
		}

		values[key.QualifiedName()] = key.Coerce(decodeStored(rec.Get("value")))
	}

	return values
}

// decodeStored turns a value read from the JSON column back into a Go value.
//
// PocketBase stores a json field as JSON text, so reading one gives the raw bytes
// rather than the value that was written. Without decoding, a string setting comes
// back with its surrounding quotes -- it would render as "Renamed" on the screen,
// and saving it again would store the quotes as part of the value.
func decodeStored(value any) any {
	switch v := value.(type) {
	case nil:
		return nil
	case types.JSONRaw:
		return decodeRaw([]byte(v))
	case json.RawMessage:
		return decodeRaw(v)
	case []byte:
		return decodeRaw(v)
	default:
		return value
	}
}

// decodeRaw unmarshals JSON text, leaving it as-is when it is not valid JSON.
func decodeRaw(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return string(raw)
	}
	return decoded
}

// Get returns one value, falling back to the declared default.
func (e *Engine) Get(reg *Registry, category, name string) any {
	key, ok := reg.Lookup(category, name)
	if !ok {
		return nil
	}
	return e.Values(reg)[key.QualifiedName()]
}

// StringGet returns a setting as text.
func (e *Engine) StringGet(reg *Registry, category, name string) string {
	return coerceString(e.Get(reg, category, name))
}

// IntGet returns a setting as an integer.
func (e *Engine) IntGet(reg *Registry, category, name string) int {
	value := e.Get(reg, category, name)
	f, err := coerceNumber(value)
	if err != nil {
		return 0
	}
	return int(f)
}

// BoolGet returns a setting as a boolean.
func (e *Engine) BoolGet(reg *Registry, category, name string) bool {
	value := e.Get(reg, category, name)
	b, err := coerceBool(value)
	if err != nil {
		return false
	}
	return b
}

// FloatGet returns a setting as a float.
func (e *Engine) FloatGet(reg *Registry, category, name string) float64 {
	f, err := coerceNumber(e.Get(reg, category, name))
	if err != nil {
		return 0
	}
	return f
}

// SaveResult reports what a save did.
type SaveResult struct {
	// Changed lists the keys whose value actually differs.
	Changed []Key

	// Values holds the submitted values for every key in the category, so a form
	// can be redisplayed with what the user typed.
	Values map[string]any
}

// Save validates and stores the submitted values for one category of one surface.
//
// It walks the registry rather than the submission. A key that is not declared
// for this category is not looked at, so a crafted form cannot write a key
// belonging to the other surface, or to a category it was not allowed to edit --
// the isolation is a property of the loop, not a check that could be skipped.
//
// A missing entry in values means "leave unchanged" only when the caller says so;
// by default an absent boolean is false and an absent field is empty, which is
// what an HTML form means when it submits a page.
func (e *Engine) Save(actor *core.Record, reg *Registry, category string, values map[string]any) (SaveResult, error) {
	result := SaveResult{Values: make(map[string]any)}

	col, err := e.app.FindCollectionByNameOrId(CollectionSettings)
	if err != nil {
		return result, fmt.Errorf("settings: the collection is missing: %w", err)
	}

	keys := reg.KeysIn(category)
	if len(keys) == 0 {
		return result, fmt.Errorf("settings: %s has no category %q", reg.Surface(), category)
	}

	current := e.Values(reg)

	for _, key := range keys {
		submitted, present := values[key.Key]
		if !present {
			// A field the form did not send keeps its current value. Without this
			// a page that renders only some of a category would clear the rest.
			result.Values[key.Key] = current[key.QualifiedName()]
			continue
		}

		validated, err := key.Validate(submitted)
		if err != nil {
			return result, err
		}
		result.Values[key.Key] = validated

		existing, err := e.app.FindFirstRecordByFilter(CollectionSettings,
			"surface = {:s} && category = {:c} && key = {:k}",
			map[string]any{"s": string(reg.Surface()), "c": category, "k": key.Key})
		if err != nil || existing == nil {
			existing = core.NewRecord(col)
			existing.Set("surface", string(reg.Surface()))
			existing.Set("category", category)
			existing.Set("key", key.Key)
		}

		// The stored side is JSON text and the submitted side is a Go value, so
		// comparing them directly is never equal and every save would report a
		// change. Decode first, then compare.
		if existing.Id != "" && reflect.DeepEqual(decodeStored(existing.Get("value")), validated) {
			continue
		}

		existing.Set("value", validated)
		existing.Set("valueType", string(key.Type))
		existing.Set("description", key.Description)
		existing.Set("isPublic", key.Public)
		existing.Set("isSystem", true)
		if actor != nil {
			existing.Set("updatedBy", actor.Id)
		}

		if err := e.app.Save(existing); err != nil {
			return result, fmt.Errorf("settings: save %s: %w", key.QualifiedName(), err)
		}
		result.Changed = append(result.Changed, key)
	}

	e.Invalidate()
	return result, nil
}

// Public returns the product values readable without authentication.
//
// Only the product surface has public keys; the back office is never public, so
// asking for its public values returns nothing rather than everything.
func (e *Engine) Public() map[string]any {
	reg := Product()
	all := e.Values(reg)

	out := make(map[string]any)
	for _, key := range reg.Keys() {
		if !key.Public {
			continue
		}
		out[key.QualifiedName()] = all[key.QualifiedName()]
	}
	return out
}

// ErrUnknownCategory is returned when a category does not exist on a surface.
var ErrUnknownCategory = errors.New("settings: unknown category")

// RenderJSON renders a value as indented JSON for a form control.
//
// A form cannot show a map or a list any other way, and showing it as Go's own
// formatting would produce something that is not valid JSON and cannot be pasted
// back.
func RenderJSON(value any) string {
	if value == nil {
		return ""
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return ""
	}
	return string(encoded)
}

// ScalarString renders a value as text for a form or a list.
func ScalarString(value any) string { return coerceString(value) }
