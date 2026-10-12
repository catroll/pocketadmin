package settings_test

import (
	"testing"

	"pocketadmin/internal/settings"
	"pocketadmin/internal/testsupport"
)

// TestRegistriesDoNotShareKeys is the isolation invariant at the registry level.
//
// The two surfaces are separate concerns that share one table, and the guarantee
// that neither can write the other's keys rests on their key sets being disjoint.
// If a name appeared on both, writing it through either registry would reach the
// same row and the separation would be a fiction.
func TestRegistriesDoNotShareKeys(t *testing.T) {
	admin := settings.Admin()
	product := settings.Product()

	if admin.Surface() == product.Surface() {
		t.Fatal("the two registries describe the same surface")
	}

	productNames := map[string]bool{}
	for _, key := range product.Keys() {
		productNames[key.QualifiedName()] = true
	}

	for _, key := range admin.Keys() {
		if productNames[key.QualifiedName()] {
			t.Errorf("%s is declared on both surfaces", key.QualifiedName())
		}
	}
}

// TestLookupIsScopedToItsSurface: a key belonging to the other surface is not
// reachable, which is what makes a cross-surface write impossible rather than
// merely discouraged.
func TestLookupIsScopedToItsSurface(t *testing.T) {
	admin := settings.Admin()
	product := settings.Product()

	// A product key, looked up on the admin surface.
	if _, ok := admin.Lookup("site", "siteName"); ok {
		t.Error("the admin registry resolved a product key")
	}
	// And the reverse.
	if _, ok := product.Lookup("console", "consoleName"); ok {
		t.Error("the product registry resolved an admin key")
	}

	// Each resolves its own.
	if _, ok := admin.Lookup("console", "consoleName"); !ok {
		t.Error("the admin registry did not resolve its own key")
	}
	if _, ok := product.Lookup("site", "siteName"); !ok {
		t.Error("the product registry did not resolve its own key")
	}
}

// TestEveryKeyHasADefaultAndDescription: the default is the documentation. A key
// without one leaves a consumer guessing what happens when nobody has set it.
func TestEveryKeyHasADefaultAndDescription(t *testing.T) {
	for _, surface := range settings.Surfaces() {
		reg, ok := settings.For(surface)
		if !ok {
			t.Fatalf("no registry for %s", surface)
		}

		for _, key := range reg.Keys() {
			if key.Default == nil {
				t.Errorf("%s.%s has no default", surface, key.QualifiedName())
			}
			if key.Description == "" {
				t.Errorf("%s.%s has no description", surface, key.QualifiedName())
			}
			if key.Category == "" || key.Key == "" {
				t.Errorf("%s has a key with an empty category or name", surface)
			}
		}
	}
}

// TestSelectsDeclareOptions: a select with no options can never be satisfied,
// which would be a setting nobody can change.
func TestSelectsDeclareOptions(t *testing.T) {
	for _, surface := range settings.Surfaces() {
		reg, _ := settings.For(surface)

		for _, key := range reg.Keys() {
			if key.Type != settings.TypeSelect {
				continue
			}
			if len(key.Options) == 0 {
				t.Errorf("%s.%s is a select with no options", surface, key.QualifiedName())
			}
			if key.Default != nil && key.Default != "" {
				found := false
				for _, option := range key.Options {
					if option == key.Default {
						found = true
					}
				}
				if !found {
					t.Errorf("%s.%s defaults to %q, which is not one of its options",
						surface, key.QualifiedName(), key.Default)
				}
			}
		}
	}
}

// TestNumberBoundsAreSane: a minimum above its maximum would reject every value.
func TestNumberBoundsAreSane(t *testing.T) {
	for _, surface := range settings.Surfaces() {
		reg, _ := settings.For(surface)

		for _, key := range reg.Keys() {
			if key.Min != nil && key.Max != nil && *key.Min > *key.Max {
				t.Errorf("%s.%s has a minimum above its maximum", surface, key.QualifiedName())
			}
		}
	}
}

// TestValidateCoercesFormInput: a form submits strings, and a setting has to
// become its declared type before it is stored.
func TestValidateCoercesFormInput(t *testing.T) {
	reg := settings.Product()

	enabled, _ := reg.Lookup("registration", "enabled")
	value, err := enabled.Validate("on")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if value != true {
		t.Errorf("a checkbox value coerced to %v, want true", value)
	}

	attempts, _ := reg.Lookup("auth", "maxLoginAttempts")
	value, err = attempts.Validate("12")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if value != float64(12) {
		t.Errorf("a number coerced to %v, want 12", value)
	}
}

// TestValidateRejectsOutOfRange: a bound that is not enforced is decoration.
func TestValidateRejectsOutOfRange(t *testing.T) {
	reg := settings.Product()

	attempts, _ := reg.Lookup("auth", "maxLoginAttempts")

	if _, err := attempts.Validate("0"); err == nil {
		t.Error("a value below the minimum was accepted")
	}
	if _, err := attempts.Validate("100000"); err == nil {
		t.Error("a value above the maximum was accepted")
	}
}

// TestValidateRejectsAnUnknownOption: a select is a closed set.
func TestValidateRejectsAnUnknownOption(t *testing.T) {
	reg := settings.Product()

	position, _ := reg.Lookup("announcement", "position")

	if _, err := position.Validate("somewhere-else"); err == nil {
		t.Error("an option outside the declared set was accepted")
	}
	if _, err := position.Validate("top"); err != nil {
		t.Errorf("a declared option was rejected: %v", err)
	}
}

// TestValidateRejectsInvalidJSON: a JSON setting that does not parse would break
// every reader of it.
func TestValidateRejectsInvalidJSON(t *testing.T) {
	reg := settings.Product()

	methods, _ := reg.Lookup("auth", "methods")

	if _, err := methods.Validate("{not json"); err == nil {
		t.Error("malformed JSON was accepted")
	}
	if _, err := methods.Validate(`["password"]`); err != nil {
		t.Errorf("valid JSON was rejected: %v", err)
	}
}

// TestSaveIgnoresKeysOutsideTheCategory is the isolation invariant at the write
// path, and the one that matters: a crafted form names keys that were never
// rendered, and none of them may be written.
func TestSaveIgnoresKeysOutsideTheCategory(t *testing.T) {
	app := testsupport.NewApp(t)
	engine := settings.NewEngine(app)

	admin := settings.Admin()
	product := settings.Product()

	before := engine.StringGet(product, "site", "siteName")

	// A submission to the admin console category that also names a product key,
	// a key from another admin category, and a key that does not exist at all.
	_, err := engine.Save(nil, admin, "console", map[string]any{
		"consoleName": "Renamed",
		// The product surface's key.
		"siteName": "Hijacked",
		// Another admin category's key.
		"passwordMinLength": "3",
		// A key nobody declares.
		"notARealKey": "whatever",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	// The declared key in the submitted category was written.
	if got := engine.StringGet(admin, "console", "consoleName"); got != "Renamed" {
		t.Errorf("consoleName = %q, want Renamed", got)
	}

	// The product key was not.
	if got := engine.StringGet(product, "site", "siteName"); got != before {
		t.Errorf("a product key was written through the admin surface: %q", got)
	}

	// The other admin category was not.
	if got := engine.IntGet(admin, "security", "passwordMinLength"); got == 3 {
		t.Error("a key from another category was written")
	}
}

// TestSaveReportsOnlyRealChanges: a save that changed nothing should say so, and
// the audit entry is built from what it reports.
func TestSaveReportsOnlyRealChanges(t *testing.T) {
	app := testsupport.NewApp(t)
	engine := settings.NewEngine(app)

	admin := settings.Admin()

	result, err := engine.Save(nil, admin, "console", map[string]any{
		"consoleName": "PocketAdmin", // already the default
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if len(result.Changed) != 0 {
		t.Errorf("a save that changed nothing reported %d changes", len(result.Changed))
	}

	result, err = engine.Save(nil, admin, "console", map[string]any{
		"consoleName": "Something else",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if len(result.Changed) != 1 {
		t.Errorf("a save that changed one key reported %d", len(result.Changed))
	}
}

// TestAbsentFieldKeepsItsValue: a form that renders part of a category must not
// clear the rest, which is what an absent map entry would otherwise mean.
func TestAbsentFieldKeepsItsValue(t *testing.T) {
	app := testsupport.NewApp(t)
	engine := settings.NewEngine(app)

	admin := settings.Admin()

	if _, err := engine.Save(nil, admin, "console", map[string]any{"consoleName": "Kept"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// A later save of the same category that mentions only one key.
	if _, err := engine.Save(nil, admin, "console", map[string]any{"footerText": "Bye"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if got := engine.StringGet(admin, "console", "consoleName"); got != "Kept" {
		t.Errorf("consoleName was cleared by a save that did not mention it: %q", got)
	}
}

// TestPublicReturnsOnlyProductKeys: the back office is never public, so asking
// for public values must not disclose it.
func TestPublicReturnsOnlyProductKeys(t *testing.T) {
	app := testsupport.NewApp(t)
	engine := settings.NewEngine(app)

	public := engine.Public()
	if len(public) == 0 {
		t.Fatal("no public product values were returned")
	}

	admin := settings.Admin()
	adminNames := map[string]bool{}
	for _, key := range admin.Keys() {
		adminNames[key.QualifiedName()] = true
	}

	for name := range public {
		if adminNames[name] {
			t.Errorf("a back-office key %q was exposed as public", name)
		}
	}
}

// TestUnknownCategoryIsRejected: a category that does not exist on a surface is
// not a silent no-op.
func TestUnknownCategoryIsRejected(t *testing.T) {
	app := testsupport.NewApp(t)
	engine := settings.NewEngine(app)

	if _, err := engine.Save(nil, settings.Admin(), "site", map[string]any{}); err == nil {
		t.Error("an admin save against a product category was accepted")
	}
	if _, err := engine.Save(nil, settings.Product(), "console", map[string]any{}); err == nil {
		t.Error("a product save against an admin category was accepted")
	}
}
