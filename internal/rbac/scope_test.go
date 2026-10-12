package rbac_test

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/permission"
	"pocketadmin/internal/rbac"
)

// scopedActor builds an actor with a data scope and a department.
func scopedActor(scope, departmentID, departmentPath string, custom ...string) *rbac.Actor {
	return &rbac.Actor{
		UserID:              "user-1",
		AdminProfileID:      "profile-1",
		AdminStatus:         rbac.StatusActive,
		DataScope:           scope,
		DepartmentID:        departmentID,
		DepartmentPath:      departmentPath,
		CustomDepartmentIDs: custom,
		Permissions:         permission.NewSet(),
	}
}

func collection(t *testing.T, app core.App, name string) *core.Collection {
	t.Helper()
	col, err := app.FindCollectionByNameOrId(name)
	if err != nil {
		t.Fatalf("collection %q missing: %v", name, err)
	}
	return col
}

// TestScopeAllIsUnrestricted covers the widest scope.
func TestScopeAllIsUnrestricted(t *testing.T) {
	app := newTestApp(t)
	employees := collection(t, app, "employees")

	got := rbac.ScopeFor(scopedActor(rbac.ScopeAll, "d1", "/d1/"), employees)
	if got.Filter != "" || got.DenyAll {
		t.Errorf("scope=all should not narrow anything, got filter=%q denyAll=%v", got.Filter, got.DenyAll)
	}
}

// TestScopeSuperuserIsUnrestricted: break-glass must never be narrowed.
func TestScopeSuperuserIsUnrestricted(t *testing.T) {
	app := newTestApp(t)
	employees := collection(t, app, "employees")

	actor := scopedActor(rbac.ScopeDepartment, "", "")
	actor.IsSuperuser = true

	if got := rbac.ScopeFor(actor, employees); got.Filter != "" || got.DenyAll {
		t.Errorf("a superuser should be unrestricted, got filter=%q denyAll=%v", got.Filter, got.DenyAll)
	}
}

// TestScopeDepartmentNarrowsToOwnDepartment is the basic row restriction.
func TestScopeDepartmentNarrowsToOwnDepartment(t *testing.T) {
	app := newTestApp(t)
	employees := collection(t, app, "employees")

	got := rbac.ScopeFor(scopedActor(rbac.ScopeDepartment, "dept-9", "/root/dept-9/"), employees)
	if got.Filter == "" || got.DenyAll {
		t.Fatalf("expected a narrowing filter, got filter=%q denyAll=%v", got.Filter, got.DenyAll)
	}
	if got.Params["scopeDept"] != "dept-9" {
		t.Errorf("filter should bind the actor's department, got %v", got.Params)
	}
}

// TestScopeDepartmentAndBelowUsesTheMaterialisedPath: "and below" is a prefix
// match on the path, which is the whole reason the path column exists.
func TestScopeDepartmentAndBelowUsesTheMaterialisedPath(t *testing.T) {
	app := newTestApp(t)
	employees := collection(t, app, "employees")

	got := rbac.ScopeFor(scopedActor(rbac.ScopeDepartmentAndBelow, "dept-9", "/root/dept-9/"), employees)
	if got.DenyAll {
		t.Fatal("unexpected deny-all")
	}
	if got.Params["scopePath"] != "/root/dept-9/" {
		t.Errorf("expected a path prefix parameter, got %v", got.Params)
	}
}

// TestScopeWithoutTheDimensionIsNotApplied: a department scope on a collection
// that has no department column narrows nothing, because data scope limits rows
// within a granted capability rather than withholding the capability.
func TestScopeWithoutTheDimensionIsNotApplied(t *testing.T) {
	app := newTestApp(t)
	roles := collection(t, app, "roles")

	if got := rbac.ScopeFor(scopedActor(rbac.ScopeDepartment, "dept-9", "/root/dept-9/"), roles); got.Filter != "" || got.DenyAll {
		t.Errorf("a collection with no department dimension should be unnarrowed, got %+v", got)
	}
}

// TestScopeNamingAMissingDimensionDenies: a scope whose dimension the actor does
// not have must show nothing rather than everything.
func TestScopeNamingAMissingDimensionDenies(t *testing.T) {
	app := newTestApp(t)
	employees := collection(t, app, "employees")

	got := rbac.ScopeFor(scopedActor(rbac.ScopeDepartment, "", ""), employees)
	if !got.DenyAll {
		t.Errorf("a scope with no department must deny, got filter=%q", got.Filter)
	}
}

// TestCustomScopeBindsEveryNamedDepartment covers the custom scope.
func TestCustomScopeBindsEveryNamedDepartment(t *testing.T) {
	app := newTestApp(t)
	employees := collection(t, app, "employees")

	got := rbac.ScopeFor(scopedActor(rbac.ScopeCustom, "", "", "d1", "d2", "d1"), employees)
	if got.DenyAll {
		t.Fatal("unexpected deny-all")
	}
	if len(got.Params) != 2 {
		t.Errorf("expected two deduplicated department bindings, got %v", got.Params)
	}
}

// TestCustomScopeWithNoSelectionDenies: "custom" with nothing chosen is an empty
// set, not an absent rule.
func TestCustomScopeWithNoSelectionDenies(t *testing.T) {
	app := newTestApp(t)
	employees := collection(t, app, "employees")

	if got := rbac.ScopeFor(scopedActor(rbac.ScopeCustom, "", ""), employees); !got.DenyAll {
		t.Error("custom scope with no departments must deny")
	}
}

// TestSelfScopeUsesTheOwningColumn covers the narrowest scope.
func TestSelfScopeUsesTheOwningColumn(t *testing.T) {
	app := newTestApp(t)
	sessions := collection(t, app, "sessions")

	got := rbac.ScopeFor(scopedActor(rbac.ScopeSelf, "d1", "/d1/"), sessions)
	if got.DenyAll || got.Filter == "" {
		t.Fatalf("expected a self filter, got %+v", got)
	}
	if got.Params["scopeOwner"] != "user-1" {
		t.Errorf("self scope should bind the actor, got %v", got.Params)
	}
}

// TestNilActorDenies: a missing actor fails closed.
func TestNilActorDenies(t *testing.T) {
	app := newTestApp(t)
	if got := rbac.ScopeFor(nil, collection(t, app, "employees")); !got.DenyAll {
		t.Error("a nil actor must be denied")
	}
}

// TestUnknownScopeDenies: an unrecognised scope is a data error, so it fails
// closed rather than silently widening.
func TestUnknownScopeDenies(t *testing.T) {
	app := newTestApp(t)
	employees := collection(t, app, "employees")

	if got := rbac.ScopeFor(scopedActor("something_else", "d1", "/d1/"), employees); !got.DenyAll {
		t.Error("an unrecognised scope must be denied")
	}
}
