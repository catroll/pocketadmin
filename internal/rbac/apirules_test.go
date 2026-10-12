package rbac_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// This file enforces the other half of the authorisation model.
//
// The panel authorises through internal/rbac. Callers arriving through the
// public REST API are authorised by PocketBase's collection rules instead, which
// the panel never triggers because it reads through core.App. The division is
// stated in docs/database-design.md section 5 and docs/design.md section 12.2;
// these tests are what make it hold, because a rule that is opened by accident
// produces no error, no log line and no failing screen.
//
// They live in this package because it owns the authorisation model, and the
// rules are the same model enforced at the other door.

// ruleSlots returns a collection's five API rules by name.
func ruleSlots(col *core.Collection) map[string]*string {
	return map[string]*string{
		"listRule":   col.ListRule,
		"viewRule":   col.ViewRule,
		"createRule": col.CreateRule,
		"updateRule": col.UpdateRule,
		"deleteRule": col.DeleteRule,
	}
}

// platformCollections returns the collections this platform owns: everything
// that is not one of PocketBase's own system collections.
func platformCollections(t *testing.T, app core.App) []*core.Collection {
	t.Helper()

	all, err := app.FindAllCollections()
	if err != nil {
		t.Fatalf("list collections: %v", err)
	}

	out := make([]*core.Collection, 0, len(all))
	for _, col := range all {
		if col.System {
			continue
		}
		out = append(out, col)
	}
	return out
}

// TestAPISurfaceIsClosedOrDeliberatelyOpen is the invariant that keeps the
// public surface from drifting open.
//
// A collection is either closed (every rule null, so only a superuser can reach
// it) or deliberately open (every rule written). A half-open collection is the
// dangerous state: nothing reports it, and it is easy to create by setting one
// rule and intending to come back.
func TestAPISurfaceIsClosedOrDeliberatelyOpen(t *testing.T) {
	app := newTestApp(t)

	var closed, open int

	for _, col := range platformCollections(t, app) {
		slots := ruleSlots(col)

		var set []string
		for name, rule := range slots {
			if rule != nil {
				set = append(set, name)
			}
		}

		switch len(set) {
		case 0:
			closed++
		case len(slots):
			open++
		default:
			t.Errorf("collection %q is half-open: %d of %d rules are set (%s). "+
				"A collection is either closed (all null) or deliberately open (all written); "+
				"see docs/database-design.md section 5",
				col.Name, len(set), len(slots), strings.Join(set, ", "))
		}
	}

	t.Logf("API surface: %d collections closed, %d deliberately open", closed, open)
}

// permissionCodePattern matches the shape of a permission point,
// <module>:<resource>:<action>.
var permissionCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*:[a-z][a-z0-9_]*:[a-z][a-z0-9_]*$`)

// quotedLiteral matches a double-quoted string inside a filter expression.
var quotedLiteral = regexp.MustCompile(`"([^"]*)"`)

// authReference matches an @request.auth.<identifier> reference and captures the
// identifier.
var authReference = regexp.MustCompile(`@request\.auth\.([A-Za-z_][A-Za-z0-9_]*)[^A-Za-z0-9_]`)

// bodyReference matches an @request.body.<identifier> reference and captures the
// identifier.
var bodyReference = regexp.MustCompile(`@request\.body\.([A-Za-z_][A-Za-z0-9_]*)`)

// validateRule reports every problem it can find in one filter expression.
//
// It is deliberately conservative: a PocketBase filter with an unknown field
// name does not fail, it simply does not match what the author expected, and in
// the case that prompted this check the result was the rule matching everything.
// A rule that silently admits everyone is worse than a rule that errors, so the
// fields it names are checked against the schema rather than trusted.
func validateRule(app core.App, col *core.Collection, rule string) []string {
	var problems []string

	// A blank rule is not "no rule" -- it admits everyone. Closed is expressed
	// with null, which no string can spell.
	if strings.TrimSpace(rule) == "" {
		problems = append(problems,
			"a blank rule admits every caller; a closed collection uses null instead")
	}

	// Every code-shaped literal must be a declared permission point, or a rule
	// gates on something no role can ever hold.
	for _, match := range quotedLiteral.FindAllStringSubmatch(rule, -1) {
		value := match[1]
		if !permissionCodePattern.MatchString(value) {
			continue
		}
		if !permissionExists(app, value) {
			problems = append(problems,
				"references permission code "+value+", which is not a declared permission point")
		}
	}

	// @request.auth.<name> must resolve to a real field on users, or to a
	// back-relation that exists.
	for _, match := range authReference.FindAllStringSubmatch(rule+" ", -1) {
		if !authFieldExists(app, match[1]) {
			problems = append(problems,
				"references @request.auth."+match[1]+", which is neither a field on users nor a back-relation")
		}
	}

	// @request.body.<name> must be a field on the collection being guarded.
	for _, match := range bodyReference.FindAllStringSubmatch(rule, -1) {
		if col.Fields.GetByName(match[1]) == nil {
			problems = append(problems,
				"references @request.body."+match[1]+", which is not a field on "+col.Name)
		}
	}

	return problems
}

// authFieldExists reports whether an @request.auth reference resolves.
//
// Beyond the built-in auth fields it accepts PocketBase's back-relation syntax,
// <collection>_via_<field>, and checks that the relation really exists.
func authFieldExists(app core.App, name string) bool {
	switch name {
	case "id", "email", "username", "verified", "collectionId", "collectionName", "created", "updated":
		return true
	}

	users, err := app.FindCachedCollectionByNameOrId("users")
	if err != nil || users == nil {
		return false
	}
	if users.Fields.GetByName(name) != nil {
		return true
	}

	collection, field, ok := strings.Cut(name, "_via_")
	if !ok || collection == "" || field == "" {
		return false
	}

	source, err := app.FindCachedCollectionByNameOrId(collection)
	if err != nil || source == nil {
		return false
	}

	rel, ok := source.Fields.GetByName(field).(*core.RelationField)
	if !ok {
		return false
	}
	// The relation has to point back at the authenticated collection, or the
	// traversal cannot start from @request.auth.
	return rel.CollectionId == users.Id
}

// permissionExists reports whether a permission point is declared.
func permissionExists(app core.App, code string) bool {
	rec, err := app.FindFirstRecordByFilter("permissions", "code = {:c}", map[string]any{"c": code})
	return err == nil && rec != nil
}

// TestRuleValidatorCatchesTheFailOpenCase proves the validator detects the
// mistake that motivated it.
//
// This is checked against a synthetic rule rather than a real one because no
// collection is open today: without it the validator would be untested code
// waiting for the first business collection, which is exactly when a bug in it
// would be least welcome.
func TestRuleValidatorCatchesTheFailOpenCase(t *testing.T) {
	app := newTestApp(t)

	col, err := app.FindCollectionByNameOrId("positions")
	if err != nil {
		t.Fatalf("positions collection missing: %v", err)
	}

	problems := validateRule(app, col, `@request.auth.adminProfile ?= ""`)
	if len(problems) == 0 {
		t.Fatal("an unknown @request.auth field was accepted; this is the fail-open case")
	}
	if !strings.Contains(strings.Join(problems, " "), "adminProfile") {
		t.Errorf("the problem should name the unknown field, got %v", problems)
	}
}

// TestRuleValidatorAcceptsAValidRule is the other half: the validator must not
// reject the shape of rule this platform actually intends to write, or it will
// be disabled the first time it is inconvenient.
func TestRuleValidatorAcceptsAValidRule(t *testing.T) {
	app := newTestApp(t)

	col, err := app.FindCollectionByNameOrId("positions")
	if err != nil {
		t.Fatalf("positions collection missing: %v", err)
	}

	valid := `@request.auth.admins_via_user.status = "active" && ` +
		`@request.auth.admins_via_user.roles.permissions.code ?= "organization:position:list"`

	if problems := validateRule(app, col, valid); len(problems) > 0 {
		t.Errorf("a valid rule was rejected: %v", problems)
	}
}

// TestRuleValidatorRejectsABlankRule: an empty rule admits everyone, and is the
// most likely way a collection ends up exposed by accident.
func TestRuleValidatorRejectsABlankRule(t *testing.T) {
	app := newTestApp(t)

	col, err := app.FindCollectionByNameOrId("positions")
	if err != nil {
		t.Fatalf("positions collection missing: %v", err)
	}

	if problems := validateRule(app, col, ""); len(problems) == 0 {
		t.Error("a blank rule was accepted, but it admits every caller")
	}
}

// TestRuleValidatorRejectsAnUndeclaredPermission: a rule gating on a permission
// point that does not exist can never be satisfied, so the collection would be
// silently invisible to everyone.
func TestRuleValidatorRejectsAnUndeclaredPermission(t *testing.T) {
	app := newTestApp(t)

	col, err := app.FindCollectionByNameOrId("positions")
	if err != nil {
		t.Fatalf("positions collection missing: %v", err)
	}

	rule := `@request.auth.admins_via_user.roles.permissions.code ?= "rbac:nonsense:list"`
	problems := validateRule(app, col, rule)
	if len(problems) == 0 {
		t.Error("a rule naming an undeclared permission point was accepted")
	}
}

// TestEveryOpenCollectionHasValidRules runs the validator over whatever is open.
//
// It passes trivially while every collection is closed, which is the current
// state and is the point: the moment a collection is opened, deliberately or
// otherwise, its rules are checked.
func TestEveryOpenCollectionHasValidRules(t *testing.T) {
	app := newTestApp(t)

	checked := 0
	for _, col := range platformCollections(t, app) {
		for name, rule := range ruleSlots(col) {
			if rule == nil {
				continue
			}
			checked++
			for _, problem := range validateRule(app, col, *rule) {
				t.Errorf("%s.%s: %s", col.Name, name, problem)
			}
		}
	}

	t.Logf("validated %d open API rules", checked)
}
