package rbac_test

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/permission"
	"pocketadmin/internal/rbac"

	// Registers the platform's migrations as a side effect.
	_ "pocketadmin/migrations"
)

// newTestApp boots an isolated application with the platform's migrations
// applied: a fresh temporary data directory, so every test sees the seeded
// schema and nothing else.
func newTestApp(t *testing.T) core.App {
	t.Helper()

	app := core.NewBaseApp(core.BaseAppConfig{
		DataDir:       t.TempDir(),
		EncryptionEnv: "pocketadmin_test",
	})

	if err := app.Bootstrap(); err != nil {
		t.Fatalf("bootstrap test app: %v", err)
	}
	if err := app.RunAllMigrations(); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	// The data directory comes from t.TempDir, so there is nothing else to
	// release once the test ends.
	return app
}

// TestPermissionConstantsMatchDatabase is the drift guard the design calls for.
//
// The permission constants are the source of truth in code and a seed migration
// mirrors them into the database. If the two ever diverge, either an
// authorisation check references a point no role can be granted, or a row exists
// that nothing enforces. Both are silent failures, so the mismatch is asserted
// in both directions.
func TestPermissionConstantsMatchDatabase(t *testing.T) {
	app := newTestApp(t)

	declared := make(map[string]bool)
	for _, p := range permission.All() {
		if declared[p.Code] {
			t.Errorf("permission %q is declared twice in permission.All()", p.Code)
		}
		declared[p.Code] = true
	}

	rows, err := app.FindAllRecords("permissions")
	if err != nil {
		t.Fatalf("load permissions: %v", err)
	}

	seeded := make(map[string]bool, len(rows))
	for _, row := range rows {
		code := row.GetString("code")
		seeded[code] = true

		if !declared[code] {
			t.Errorf("permission %q exists in the database but is not declared in code", code)
		}
	}

	for code := range declared {
		if !seeded[code] {
			t.Errorf("permission %q is declared in code but was not seeded", code)
		}
	}

	if len(seeded) != len(declared) {
		t.Errorf("permission count mismatch: %d declared, %d seeded", len(declared), len(seeded))
	}
}

// TestSeededMenusReferenceDeclaredPermissions guards the navigation tree against
// naming a permission point that does not exist, which would hide the entry from
// everyone including a superuser-by-permission role.
func TestSeededMenusReferenceDeclaredPermissions(t *testing.T) {
	app := newTestApp(t)

	menus, err := app.FindAllRecords("menus")
	if err != nil {
		t.Fatalf("load menus: %v", err)
	}
	if len(menus) == 0 {
		t.Fatal("no menus were seeded")
	}

	for _, menu := range menus {
		code := menu.GetString("permissionCode")
		if code == "" {
			continue
		}
		found, err := app.FindFirstRecordByFilter("permissions", "code = {:c}", map[string]any{"c": code})
		if err != nil || found == nil {
			t.Errorf("menu %q requires %q, which is not a declared permission point",
				menu.GetString("name"), code)
		}
	}
}

// TestMenuProjectionFollowsPermissions is the core navigation rule: what an
// actor sees is derived from what it may do, never authored per role.
func TestMenuProjectionFollowsPermissions(t *testing.T) {
	app := newTestApp(t)
	projector := rbac.NewProjector(app)

	none := testActor()
	if got := projector.Visible(none); len(got) != 0 {
		// Dashboard carries no permission code, so it stays visible to any
		// signed-in account; anything else appearing would be a leak.
		for _, node := range got {
			if node.Name != "dashboard" {
				t.Errorf("an actor with no permissions can see %q", node.Name)
			}
		}
	}

	// One permission must reveal exactly that branch and nothing else.
	one := testActor(permission.OrganizationDepartmentList)
	visible := flatten(projector.Visible(one))

	if !visible["organization.departments"] {
		t.Error("the granted menu node is not visible")
	}
	if visible["organization.positions"] || visible["organization.staff"] {
		t.Error("a menu node was revealed without its permission")
	}
	if visible["rbac.roles"] || visible["identity.accounts"] {
		t.Error("a node from another module was revealed")
	}
	if !visible["organization"] {
		t.Error("the parent directory of a granted node is not visible")
	}
}

// TestMenuProjectionToleratesNilActor guards the unauthenticated path: the login
// page renders through the same layout and has no actor yet, so a nil must be
// treated as "holds nothing" rather than dereferenced.
func TestMenuProjectionToleratesNilActor(t *testing.T) {
	app := newTestApp(t)
	projector := rbac.NewProjector(app)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("projecting menus for a nil actor panicked: %v", r)
		}
	}()

	for _, node := range projector.Visible(nil) {
		// Nothing on this list may be gated, since the caller holds no permission.
		if node.Name != "dashboard" && node.Name != "collections" {
			continue
		}
		if node.Name == "collections" {
			t.Error("a superuser-only node is visible to a nil actor")
		}
	}
}

// TestMenuProjectionDropsEmptyDirectories covers a display rule: a directory
// whose children were all filtered out must not render, or the actor sees an
// expandable heading that leads nowhere.
func TestMenuProjectionDropsEmptyDirectories(t *testing.T) {
	app := newTestApp(t)
	projector := rbac.NewProjector(app)

	actor := testActor(permission.OrganizationDepartmentList)

	for _, node := range projector.Visible(actor) {
		if node.Type == "directory" && len(node.Children) == 0 {
			t.Errorf("directory %q is visible with no visible children", node.Name)
		}
	}
}

// TestResolverDeniesWithoutAdminProfile covers the default-deny posture: an
// account with no profile resolves, but may do nothing.
func TestResolverDeniesWithoutAdminProfile(t *testing.T) {
	app := newTestApp(t)

	col, err := app.FindCollectionByNameOrId(rbac.CollectionUsers)
	if err != nil {
		t.Fatalf("users collection missing: %v", err)
	}

	user := core.NewRecord(col)
	user.SetEmail("nobody@example.com")
	user.SetPassword("change-me-please")
	user.Set("status", "active")
	if err := app.Save(user); err != nil {
		t.Fatalf("save user: %v", err)
	}

	resolver := rbac.NewResolver(app)
	actor, err := resolver.Resolve(user)
	if err != nil {
		t.Fatalf("resolve actor: %v", err)
	}

	if actor.IsAdmin() {
		t.Error("an account with no admin profile is treated as an administrator")
	}
	if actor.Can(permission.IdentityAccountList) {
		t.Error("an account with no admin profile holds a permission")
	}
}

// TestResolverUnionsRolePermissions checks that authority is the union of the
// account's active roles, and that a disabled role grants nothing.
func TestResolverUnionsRolePermissions(t *testing.T) {
	app := newTestApp(t)

	manager := seedAccount(t, app, "manager@example.com")
	roleA := seedRole(t, app, "reader_a", "active", permission.OrganizationDepartmentList)
	roleB := seedRole(t, app, "reader_b", "active", permission.RBACRoleList)
	disabled := seedRole(t, app, "disabled_c", "disabled", permission.IdentityAccountList)

	profile := seedAdminProfile(t, app, manager.Id, []string{roleA, roleB, disabled})

	resolver := rbac.NewResolver(app)
	actor, err := resolver.Resolve(manager)
	if err != nil {
		t.Fatalf("resolve actor: %v", err)
	}

	if !actor.IsAdmin() {
		t.Fatal("an account with an active admin profile is not treated as an administrator")
	}
	if !actor.Can(permission.OrganizationDepartmentList) || !actor.Can(permission.RBACRoleList) {
		t.Error("the union of the roles' permissions is incomplete")
	}
	if actor.Can(permission.IdentityAccountList) {
		t.Error("a disabled role granted a permission")
	}

	// A profile that is switched off must stop granting immediately.
	profile.Set("status", "disabled")
	if err := app.Save(profile); err != nil {
		t.Fatalf("disable profile: %v", err)
	}

	fresh, err := rbac.NewResolver(app).Resolve(manager)
	if err != nil {
		t.Fatalf("resolve actor: %v", err)
	}
	if fresh.IsAdmin() {
		t.Error("a disabled admin profile still grants back-office access")
	}
}

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

func seedAccount(t *testing.T, app core.App, email string) *core.Record {
	t.Helper()

	col, err := app.FindCollectionByNameOrId(rbac.CollectionUsers)
	if err != nil {
		t.Fatalf("users collection missing: %v", err)
	}

	user := core.NewRecord(col)
	user.SetEmail(email)
	user.SetPassword("change-me-please")
	user.Set("status", "active")
	if err := app.Save(user); err != nil {
		t.Fatalf("save account: %v", err)
	}
	return user
}

func seedRole(t *testing.T, app core.App, code, status string, codes ...string) string {
	t.Helper()

	col, err := app.FindCollectionByNameOrId(rbac.CollectionRoles)
	if err != nil {
		t.Fatalf("roles collection missing: %v", err)
	}

	var ids []string
	for _, c := range codes {
		point, err := app.FindFirstRecordByFilter("permissions", "code = {:c}", map[string]any{"c": c})
		if err != nil || point == nil {
			t.Fatalf("permission %q was not seeded", c)
		}
		ids = append(ids, point.Id)
	}

	role := core.NewRecord(col)
	role.Set("name", code)
	role.Set("code", code)
	role.Set("status", status)
	role.Set("dataScope", "all")
	role.Set("permissions", ids)
	if err := app.Save(role); err != nil {
		t.Fatalf("save role: %v", err)
	}
	return role.Id
}

func seedAdminProfile(t *testing.T, app core.App, userID string, roleIDs []string) *core.Record {
	t.Helper()

	col, err := app.FindCollectionByNameOrId(rbac.CollectionAdmins)
	if err != nil {
		t.Fatalf("admins collection missing: %v", err)
	}

	profile := core.NewRecord(col)
	profile.Set("user", userID)
	profile.Set("status", "active")
	profile.Set("roles", roleIDs)
	if err := app.Save(profile); err != nil {
		t.Fatalf("save admin profile: %v", err)
	}
	return profile
}

// testActor builds an actor that holds an active admin profile, since Can is
// default-deny and refuses an account without one.
func testActor(codes ...string) *rbac.Actor {
	return &rbac.Actor{
		UserID:         "test-user",
		Email:          "test@example.com",
		AdminProfileID: "test-profile",
		AdminStatus:    rbac.StatusActive,
		Permissions:    permission.NewSet(codes...),
	}
}

// flatten walks a projected tree into a name lookup.
func flatten(nodes []rbac.MenuNode) map[string]bool {
	out := make(map[string]bool)
	var walk func([]rbac.MenuNode)
	walk = func(ns []rbac.MenuNode) {
		for _, n := range ns {
			out[n.Name] = true
			walk(n.Children)
		}
	}
	walk(nodes)
	return out
}
