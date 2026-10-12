package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"

	"pocketadmin/internal/permission"
)

// seedMigration populates the vocabulary the panel cannot run without: the
// permission catalogue, the navigation tree, the dictionaries and the built-in
// roles.
//
// It is separate from the schema migrations so that schema and content can be
// reasoned about independently, and it is idempotent: every insert is an upsert
// keyed on the natural key (code, or name for menus).
func init() {
	migrations.Register(func(app core.App) error {
		if err := seedPermissionPoints(app); err != nil {
			return err
		}
		if err := seedMenus(app); err != nil {
			return err
		}
		if err := seedDictionaries(app); err != nil {
			return err
		}
		return seedRoles(app)
	}, func(app core.App) error {
		// Seeded content is left in place on rollback: removing it would strip
		// permission grants from roles that an operator may have customised.
		return nil
	}, "1700000010_seed.go")
}

// permissionPoints bridges the permission package into the migration.
func permissionPoints() []permission.Point {
	return permission.All()
}

// menuSeed is one node of the seeded navigation tree.
type menuSeed struct {
	Name       string // stable route name
	Title      string
	Parent     string // parent's route name, "" for a root
	Type       string // directory | menu | button | link
	Path       string
	Icon       string
	Permission string
	Sort       int

	// SuperuserOnly marks a node only a break-glass superuser may see.
	SuperuserOnly bool
}

// seededMenus is the panel's navigation, mirroring the sidebar an operator sees.
//
// Parents are listed before their children so a single pass can resolve them.
func seededMenus() []menuSeed {
	return []menuSeed{
		{Name: "dashboard", Title: "Dashboard", Type: "menu", Path: "/dashboard",
			Icon: "fas fa-tachometer-alt", Sort: 10},

		{Name: "identity", Title: "Identity", Type: "directory", Icon: "fas fa-users", Sort: 20},
		{Name: "identity.accounts", Title: "Accounts", Parent: "identity", Type: "menu",
			Path: "/identity/accounts", Icon: "fas fa-user", Permission: permission.IdentityAccountList, Sort: 10},

		{Name: "organization", Title: "Organization", Type: "directory", Icon: "fas fa-sitemap", Sort: 30},
		{Name: "organization.departments", Title: "Departments", Parent: "organization", Type: "menu",
			Path: "/organization/departments", Icon: "far fa-circle",
			Permission: permission.OrganizationDepartmentList, Sort: 10},
		{Name: "organization.positions", Title: "Positions", Parent: "organization", Type: "menu",
			Path: "/organization/positions", Icon: "far fa-circle",
			Permission: permission.OrganizationPositionList, Sort: 20},
		{Name: "organization.staff", Title: "Staff", Parent: "organization", Type: "menu",
			Path: "/organization/staff", Icon: "far fa-circle",
			Permission: permission.OrganizationEmployeeList, Sort: 30},

		{Name: "rbac", Title: "Access control", Type: "directory", Icon: "fas fa-user-shield", Sort: 40},
		{Name: "rbac.roles", Title: "Roles", Parent: "rbac", Type: "menu",
			Path: "/rbac/roles", Icon: "far fa-circle", Permission: permission.RBACRoleList, Sort: 10},
		{Name: "rbac.permissions", Title: "Permissions", Parent: "rbac", Type: "menu",
			Path: "/rbac/permissions", Icon: "far fa-circle", Permission: permission.RBACPermissionList, Sort: 20},
		{Name: "rbac.menus", Title: "Menus", Parent: "rbac", Type: "menu",
			Path: "/rbac/menus", Icon: "far fa-circle", Permission: permission.RBACMenuList, Sort: 30},
		{Name: "rbac.dictionaries", Title: "Dictionaries", Parent: "rbac", Type: "menu",
			Path: "/rbac/dictionaries", Icon: "far fa-circle", Permission: permission.RBACDictionaryList, Sort: 40},

		{Name: "storage", Title: "Files", Type: "directory", Icon: "fas fa-folder-open", Sort: 50},
		{Name: "storage.files", Title: "Library", Parent: "storage", Type: "menu",
			Path: "/storage/files", Icon: "far fa-circle",
			Permission: permission.StorageFileList, Sort: 10},
		{Name: "storage.folders", Title: "Folders", Parent: "storage", Type: "menu",
			Path: "/storage/folders", Icon: "far fa-circle",
			Permission: permission.StorageFolderList, Sort: 20},

		{Name: "audit", Title: "Audit", Type: "directory", Icon: "fas fa-clipboard-list", Sort: 60},
		{Name: "audit.operations", Title: "Operations", Parent: "audit", Type: "menu",
			Path: "/audit/operations", Icon: "far fa-circle",
			Permission: permission.AuditOperationList, Sort: 10},
		{Name: "audit.security", Title: "Security events", Parent: "audit", Type: "menu",
			Path: "/audit/security", Icon: "far fa-circle",
			Permission: permission.AuditSecurityList, Sort: 20},
		{Name: "audit.logins", Title: "Sign-ins", Parent: "audit", Type: "menu",
			Path: "/audit/logins", Icon: "far fa-circle",
			Permission: permission.AuditLoginList, Sort: 30},
		{Name: "audit.exceptions", Title: "Exceptions", Parent: "audit", Type: "menu",
			Path: "/audit/exceptions", Icon: "far fa-circle",
			Permission: permission.AuditExceptionList, Sort: 40},

		// The raw data browser is not delegated to roles, so it is flagged rather
		// than permission-gated. Without a code an unflagged node would be visible
		// to everyone.
		{Name: "collections", Title: "Collections", Type: "menu", Path: "/collections",
			Icon: "fas fa-database", Sort: 90, SuperuserOnly: true},
	}
}

// seedMenus inserts the navigation tree, resolving parents by route name.
func seedMenus(app core.App) error {
	col := findCollection(app, "menus")
	if col == nil {
		return nil
	}

	ids := make(map[string]string, len(seededMenus()))

	for _, m := range seededMenus() {
		rec, err := app.FindFirstRecordByFilter("menus", "name = {:n}", map[string]any{"n": m.Name})
		if err != nil || rec == nil {
			rec = core.NewRecord(col)
			rec.Set("name", m.Name)
			rec.Set("isSystem", true)
			rec.Set("platform", "admin")
		}

		rec.Set("title", m.Title)
		rec.Set("type", m.Type)
		rec.Set("path", m.Path)
		rec.Set("icon", m.Icon)
		rec.Set("permissionCode", m.Permission)
		rec.Set("sortOrder", m.Sort)
		rec.Set("visible", true)
		rec.Set("status", "active")
		rec.Set("superuserOnly", m.SuperuserOnly)
		if m.Parent != "" {
			rec.Set("parent", ids[m.Parent])
		}

		if err := app.Save(rec); err != nil {
			return err
		}
		ids[m.Name] = rec.Id
	}

	return nil
}

// seedDictionaries inserts the option sets referenced by the modules built so
// far.
func seedDictionaries(app core.App) error {
	type item = struct{ Value, Label string }

	sets := []struct {
		Code  string
		Name  string
		Items []item
	}{
		{"gender", "Gender", []item{
			{"unknown", "Unknown"}, {"male", "Male"}, {"female", "Female"}, {"other", "Other"},
		}},
		{"user_tag", "Account tag", []item{
			{"vip", "VIP"}, {"staff", "Staff"}, {"partner", "Partner"}, {"trial", "Trial"},
		}},
		{"account_status", "Account status", []item{
			{"active", "Active"}, {"inactive", "Inactive"}, {"banned", "Banned"},
		}},
		{"employment_type", "Employment type", []item{
			{"fulltime", "Full time"}, {"parttime", "Part time"},
			{"intern", "Intern"}, {"contractor", "Contractor"},
		}},
		{"employment_status", "Employment status", []item{
			{"probation", "Probation"}, {"active", "Active"},
			{"on_leave", "On leave"}, {"resigned", "Resigned"},
		}},
		{"position_category", "Position category", []item{
			{"management", "Management"}, {"professional", "Professional"},
			{"technical", "Technical"}, {"support", "Support"},
		}},
		{"data_scope", "Data scope", []item{
			{"all", "All records"}, {"department", "Own department"},
			{"department_and_below", "Own department and below"},
			{"self", "Own records"}, {"custom", "Custom departments"},
		}},
	}

	for _, s := range sets {
		if err := seedDictionary(app, s.Code, s.Name, s.Items); err != nil {
			return err
		}
	}

	return nil
}

// seedRoles inserts the built-in roles.
//
// super_admin is protected and holds every permission and every menu, so the
// platform is usable immediately after the first account is granted it.
func seedRoles(app core.App) error {
	col := findCollection(app, "roles")
	if col == nil {
		return nil
	}

	allPermissions, err := app.FindAllRecords("permissions")
	if err != nil {
		return err
	}
	allMenus, err := app.FindAllRecords("menus")
	if err != nil {
		return err
	}

	permIDs := make([]string, 0, len(allPermissions))
	for _, r := range allPermissions {
		permIDs = append(permIDs, r.Id)
	}
	menuIDs := make([]string, 0, len(allMenus))
	for _, r := range allMenus {
		menuIDs = append(menuIDs, r.Id)
	}

	rec, err := app.FindFirstRecordByFilter("roles", "code = {:c}", map[string]any{"c": "super_admin"})
	if err != nil || rec == nil {
		rec = core.NewRecord(col)
		rec.Set("code", "super_admin")
		rec.Set("isSystem", true)
	}
	rec.Set("name", "Super administrator")
	rec.Set("description", "Full access to every module, including access control.")
	rec.Set("dataScope", "all")
	rec.Set("status", "active")
	rec.Set("sortOrder", 0)
	rec.Set("permissions", permIDs)
	rec.Set("menus", menuIDs)

	return app.Save(rec)
}
