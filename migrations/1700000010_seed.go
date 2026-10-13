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
// It is grouped into four surfaces rather than listed flat. A flat list of thirty
// entries is a list nobody reads: an operator looking for the refund screen should be
// able to reason "that is about money, so it is under Business" instead of scanning
// every row.
//
//   - Business is the storefront's own domain: what is sold, what was ordered, what
//     was paid and what was given away.
//   - Back office is people and access: who works here, what they may reach, and
//     what they are told.
//   - System is the platform itself: how it is configured, what it stores, what it
//     runs, and what it recorded.
//
// Dashboard and Workbench stay at the top level. They are where an operator starts
// and what they look at first, and burying either one inside a folder would cost a
// click on every visit.
//
// Parents are listed before their children so a single pass can resolve them.
func seededMenus() []menuSeed {
	return []menuSeed{
		{Name: "dashboard", Title: "Dashboard", Type: "menu", Path: "/dashboard",
			Icon: "fas fa-tachometer-alt", Sort: 10},
		{Name: "workbench", Title: "Workbench", Type: "menu", Path: "/workbench",
			Icon: "fas fa-columns", Sort: 20, Permission: permission.WorkbenchTodoList},

		// -- Business ---------------------------------------------------------
		{Name: "business", Title: "Business", Type: "directory", Icon: "fas fa-store", Sort: 30},

		{Name: "catalog", Title: "Catalog", Parent: "business", Type: "directory",
			Icon: "fas fa-boxes-stacked", Sort: 10},
		{Name: "catalog.products", Title: "Products", Parent: "catalog", Type: "menu",
			Path: "/catalog/products", Icon: "far fa-circle",
			Permission: permission.CatalogProductList, Sort: 10},
		{Name: "catalog.categories", Title: "Categories", Parent: "catalog", Type: "menu",
			Path: "/catalog/categories", Icon: "far fa-circle",
			Permission: permission.CatalogCategoryList, Sort: 20},

		{Name: "orders", Title: "Orders", Parent: "business", Type: "menu", Path: "/orders",
			Icon: "fas fa-receipt", Sort: 20, Permission: permission.OrderList},

		{Name: "sales", Title: "Sales", Parent: "business", Type: "directory",
			Icon: "fas fa-credit-card", Sort: 30},
		{Name: "payments.transactions", Title: "Payments", Parent: "sales", Type: "menu",
			Path: "/payments", Icon: "far fa-circle",
			Permission: permission.PaymentTransactionList, Sort: 10},
		{Name: "refunds", Title: "Refunds", Parent: "sales", Type: "menu", Path: "/refunds",
			Icon: "far fa-circle", Sort: 20, Permission: permission.RefundList},

		{Name: "promotions", Title: "Promotions", Parent: "business", Type: "directory",
			Icon: "fas fa-ticket", Sort: 40},
		{Name: "promotions.coupons", Title: "Coupons", Parent: "promotions", Type: "menu",
			Path: "/promotions/coupons", Icon: "far fa-circle",
			Permission: permission.PromotionCouponList, Sort: 10},
		{Name: "promotions.campaigns", Title: "Campaigns", Parent: "promotions", Type: "menu",
			Path: "/promotions/campaigns", Icon: "far fa-circle",
			Permission: permission.PromotionCampaignList, Sort: 20},
		{Name: "promotions.grants", Title: "Issued coupons", Parent: "promotions", Type: "menu",
			Path: "/promotions/grants", Icon: "far fa-circle",
			Permission: permission.PromotionGrantList, Sort: 30},

		{Name: "members", Title: "Membership", Parent: "business", Type: "directory",
			Icon: "fas fa-id-card", Sort: 50},
		{Name: "membership.members", Title: "Members", Parent: "members", Type: "menu",
			Path: "/membership/members", Icon: "far fa-circle",
			Permission: permission.MembershipMemberList, Sort: 10},
		{Name: "membership.tiers", Title: "Tiers", Parent: "members", Type: "menu",
			Path: "/membership/tiers", Icon: "far fa-circle",
			Permission: permission.MembershipTierList, Sort: 20},
		{Name: "membership.plans", Title: "Plans", Parent: "members", Type: "menu",
			Path: "/membership/plans", Icon: "far fa-circle",
			Permission: permission.MembershipPlanList, Sort: 30},

		{Name: "subscriptions", Title: "Subscriptions", Parent: "business", Type: "directory",
			Icon: "fas fa-rotate", Sort: 60},
		{Name: "subscriptions.list", Title: "Subscribers", Parent: "subscriptions", Type: "menu",
			Path: "/subscriptions", Icon: "far fa-circle",
			Permission: permission.SubscriptionList, Sort: 10},
		{Name: "subscriptions.plans", Title: "Plans", Parent: "subscriptions", Type: "menu",
			Path: "/subscriptions/plans", Icon: "far fa-circle",
			Permission: permission.SubscriptionPlanList, Sort: 20},

		// -- Back office ------------------------------------------------------
		{Name: "backoffice", Title: "Back office", Type: "directory", Icon: "fas fa-briefcase",
			Sort: 40},

		{Name: "identity", Title: "Identity", Parent: "backoffice", Type: "directory",
			Icon: "fas fa-users", Sort: 10},
		{Name: "identity.accounts", Title: "Accounts", Parent: "identity", Type: "menu",
			Path: "/identity/accounts", Icon: "far fa-circle",
			Permission: permission.IdentityAccountList, Sort: 10},

		{Name: "organization", Title: "Organization", Parent: "backoffice", Type: "directory",
			Icon: "fas fa-sitemap", Sort: 20},
		{Name: "organization.departments", Title: "Departments", Parent: "organization", Type: "menu",
			Path: "/organization/departments", Icon: "far fa-circle",
			Permission: permission.OrganizationDepartmentList, Sort: 10},
		{Name: "organization.positions", Title: "Positions", Parent: "organization", Type: "menu",
			Path: "/organization/positions", Icon: "far fa-circle",
			Permission: permission.OrganizationPositionList, Sort: 20},
		{Name: "organization.staff", Title: "Staff", Parent: "organization", Type: "menu",
			Path: "/organization/staff", Icon: "far fa-circle",
			Permission: permission.OrganizationEmployeeList, Sort: 30},

		{Name: "rbac", Title: "Access control", Parent: "backoffice", Type: "directory",
			Icon: "fas fa-user-shield", Sort: 30},
		{Name: "rbac.roles", Title: "Roles", Parent: "rbac", Type: "menu",
			Path: "/rbac/roles", Icon: "far fa-circle", Permission: permission.RBACRoleList, Sort: 10},
		{Name: "rbac.permissions", Title: "Permissions", Parent: "rbac", Type: "menu",
			Path: "/rbac/permissions", Icon: "far fa-circle",
			Permission: permission.RBACPermissionList, Sort: 20},
		{Name: "rbac.menus", Title: "Menus", Parent: "rbac", Type: "menu",
			Path: "/rbac/menus", Icon: "far fa-circle", Permission: permission.RBACMenuList, Sort: 30},
		{Name: "rbac.dictionaries", Title: "Dictionaries", Parent: "rbac", Type: "menu",
			Path: "/rbac/dictionaries", Icon: "far fa-circle",
			Permission: permission.RBACDictionaryList, Sort: 40},

		{Name: "announcement", Title: "Announcements", Parent: "backoffice", Type: "menu",
			Path: "/announcements", Icon: "fas fa-bullhorn", Sort: 40,
			Permission: permission.AnnouncementList},

		// -- System -----------------------------------------------------------
		{Name: "system", Title: "System", Type: "directory", Icon: "fas fa-server", Sort: 50},

		{Name: "settings", Title: "Settings", Parent: "system", Type: "directory",
			Icon: "fas fa-gears", Sort: 10},
		{Name: "settings.admin", Title: "Back office", Parent: "settings", Type: "menu",
			Path: "/settings/admin", Icon: "far fa-circle",
			Permission: permission.SettingsAdminView, Sort: 10},
		{Name: "settings.product", Title: "Storefront", Parent: "settings", Type: "menu",
			Path: "/settings/product", Icon: "far fa-circle",
			Permission: permission.SettingsProductView, Sort: 20},

		{Name: "storage", Title: "Files", Parent: "system", Type: "directory",
			Icon: "fas fa-folder-open", Sort: 20},
		{Name: "storage.files", Title: "Library", Parent: "storage", Type: "menu",
			Path: "/storage/files", Icon: "far fa-circle",
			Permission: permission.StorageFileList, Sort: 10},
		{Name: "storage.folders", Title: "Folders", Parent: "storage", Type: "menu",
			Path: "/storage/folders", Icon: "far fa-circle",
			Permission: permission.StorageFolderList, Sort: 20},

		{Name: "jobs", Title: "Scheduled tasks", Parent: "system", Type: "menu", Path: "/jobs",
			Icon: "fas fa-clock", Sort: 30, Permission: permission.JobsJobList},

		{Name: "integrations", Title: "Integrations", Parent: "system", Type: "menu",
			Path: "/integrations", Icon: "fas fa-plug", Sort: 40,
			Permission: permission.IntegrationProviderList},

		{Name: "monitoring", Title: "Monitoring", Parent: "system", Type: "directory",
			Icon: "fas fa-gauge-high", Sort: 50},
		{Name: "monitoring.dashboard", Title: "Overview", Parent: "monitoring", Type: "menu",
			Path: "/monitoring", Icon: "far fa-circle",
			Permission: permission.MonitoringDashboard, Sort: 10},
		{Name: "monitoring.nodes", Title: "Machines", Parent: "monitoring", Type: "menu",
			Path: "/monitoring/nodes", Icon: "far fa-circle",
			Permission: permission.NodeList, Sort: 20},
		{Name: "monitoring.system", Title: "System information", Parent: "monitoring", Type: "menu",
			Path: "/monitoring/system", Icon: "far fa-circle",
			Permission: permission.MonitoringSystemInfo, Sort: 30},

		{Name: "developer", Title: "Developer", Parent: "system", Type: "directory",
			Icon: "fas fa-code", Sort: 60},
		{Name: "developer.keys", Title: "API keys", Parent: "developer", Type: "menu",
			Path: "/developer/keys", Icon: "far fa-circle",
			Permission: permission.DeveloperKeyList, Sort: 10},
		{Name: "developer.api", Title: "API reference", Parent: "developer", Type: "menu",
			Path: "/developer/api", Icon: "far fa-circle",
			Permission: permission.DeveloperAPIView, Sort: 20},

		{Name: "audit", Title: "Audit", Parent: "system", Type: "directory",
			Icon: "fas fa-clipboard-list", Sort: 70},
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
		{Name: "collections", Title: "Collections", Parent: "system", Type: "menu",
			Path: "/collections", Icon: "fas fa-database", Sort: 80, SuperuserOnly: true},
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

	// A system menu that is no longer in the seed is removed. The seed claims to be
	// the navigation, and a restructure that leaves the old rows behind produces a
	// sidebar containing both the new tree and the old flat list. Only system menus
	// are pruned: a menu an operator added by hand is theirs to keep.
	existing, err := app.FindRecordsByFilter("menus", "isSystem = true", "", 0, 0)
	if err != nil {
		return err
	}

	for _, rec := range existing {
		if _, ok := ids[rec.GetString("name")]; ok {
			continue
		}
		if err := app.Delete(rec); err != nil {
			return err
		}
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
