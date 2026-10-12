// Package permission declares the platform's permission points.
//
// A permission point is a stable string of the form
// "<module>:<resource>:<action>". The constants here are the single source of
// truth in code; a seed migration mirrors them into the `permissions`
// collection, and a drift test asserts the two stay in sync in both directions.
//
// Declaring them as constants rather than string literals at the call site means
// a typo is a compile error instead of a silently ineffective authorisation
// check.
package permission

// Module identifiers. These match the module ids in CAPABILITY-MAP.md and are
// stored on each permission record.
const (
	ModuleIdentity     = "identity"
	ModuleOrganization = "organization"
	ModuleRBAC         = "rbac"
)

// Point is a single permission point.
type Point struct {
	// Code is the canonical "<module>:<resource>:<action>" string.
	Code string `json:"code"`

	// Name is the human label shown in the role editor.
	Name string `json:"name"`

	// Module is the owning module id.
	Module string `json:"module"`

	// Resource is the resource within the module.
	Resource string `json:"resource"`

	// Action is the permitted operation.
	Action string `json:"action"`

	// Type is menu, button, api or data.
	Type string `json:"type"`
}

// Permission point codes.
const (
	// --- identity ---

	IdentityAccountList   = "identity:account:list"
	IdentityAccountView   = "identity:account:view"
	IdentityAccountCreate = "identity:account:create"
	IdentityAccountUpdate = "identity:account:update"
	IdentityAccountDelete = "identity:account:delete"
	IdentityAccountExport = "identity:account:export"

	IdentityProfileAttach = "identity:profile:update"

	IdentitySessionList   = "identity:session:list"
	IdentitySessionRevoke = "identity:session:revoke"

	IdentityLoginLogList = "identity:loginlog:list"

	IdentityPreferenceManage = "identity:preference:manage"

	IdentityImpersonate = "identity:account:execute"

	// --- organization ---

	OrganizationDepartmentList   = "organization:department:list"
	OrganizationDepartmentView   = "organization:department:view"
	OrganizationDepartmentCreate = "organization:department:create"
	OrganizationDepartmentUpdate = "organization:department:update"
	OrganizationDepartmentDelete = "organization:department:delete"

	OrganizationPositionList   = "organization:position:list"
	OrganizationPositionCreate = "organization:position:create"
	OrganizationPositionUpdate = "organization:position:update"
	OrganizationPositionDelete = "organization:position:delete"

	OrganizationEmployeeList   = "organization:employee:list"
	OrganizationEmployeeView   = "organization:employee:view"
	OrganizationEmployeeCreate = "organization:employee:create"
	OrganizationEmployeeUpdate = "organization:employee:update"
	OrganizationEmployeeDelete = "organization:employee:delete"

	// --- rbac ---

	RBACRoleList   = "rbac:role:list"
	RBACRoleCreate = "rbac:role:create"
	RBACRoleUpdate = "rbac:role:update"
	RBACRoleDelete = "rbac:role:delete"
	RBACRoleManage = "rbac:role:manage" // grant permissions and menus

	RBACPermissionList = "rbac:permission:list"
	RBACPermissionView = "rbac:permission:view"

	RBACMenuList   = "rbac:menu:list"
	RBACMenuCreate = "rbac:menu:create"
	RBACMenuUpdate = "rbac:menu:update"
	RBACMenuDelete = "rbac:menu:delete"

	RBACDictionaryList   = "rbac:dictionary:list"
	RBACDictionaryCreate = "rbac:dictionary:create"
	RBACDictionaryUpdate = "rbac:dictionary:update"
	RBACDictionaryDelete = "rbac:dictionary:delete"

	// RBACSettingsManage guards the settings surfaces. The surfaces themselves
	// are separate modules; this point only covers who may change configuration.
	RBACSettingsManage = "rbac:settings:manage"
)

// All returns every declared permission point.
//
// The order is stable, and the seed migration walks it, so permission rows are
// created deterministically.
func All() []Point {
	return []Point{
		{IdentityAccountList, "List accounts", ModuleIdentity, "account", "list", "menu"},
		{IdentityAccountView, "View an account", ModuleIdentity, "account", "view", "api"},
		{IdentityAccountCreate, "Create an account", ModuleIdentity, "account", "create", "button"},
		{IdentityAccountUpdate, "Update an account", ModuleIdentity, "account", "update", "button"},
		{IdentityAccountDelete, "Delete an account", ModuleIdentity, "account", "delete", "button"},
		{IdentityAccountExport, "Export accounts", ModuleIdentity, "account", "export", "button"},
		{IdentityProfileAttach, "Attach or detach a profile", ModuleIdentity, "profile", "update", "button"},
		{IdentitySessionList, "List sessions", ModuleIdentity, "session", "list", "api"},
		{IdentitySessionRevoke, "Revoke a session", ModuleIdentity, "session", "revoke", "button"},
		{IdentityLoginLogList, "View sign-in history", ModuleIdentity, "loginlog", "list", "api"},
		{IdentityPreferenceManage, "Manage preferences", ModuleIdentity, "preference", "manage", "api"},
		{IdentityImpersonate, "Impersonate an account", ModuleIdentity, "account", "execute", "button"},

		{OrganizationDepartmentList, "List departments", ModuleOrganization, "department", "list", "menu"},
		{OrganizationDepartmentView, "View a department", ModuleOrganization, "department", "view", "api"},
		{OrganizationDepartmentCreate, "Create a department", ModuleOrganization, "department", "create", "button"},
		{OrganizationDepartmentUpdate, "Update a department", ModuleOrganization, "department", "update", "button"},
		{OrganizationDepartmentDelete, "Delete a department", ModuleOrganization, "department", "delete", "button"},
		{OrganizationPositionList, "List positions", ModuleOrganization, "position", "list", "menu"},
		{OrganizationPositionCreate, "Create a position", ModuleOrganization, "position", "create", "button"},
		{OrganizationPositionUpdate, "Update a position", ModuleOrganization, "position", "update", "button"},
		{OrganizationPositionDelete, "Delete a position", ModuleOrganization, "position", "delete", "button"},
		{OrganizationEmployeeList, "List staff", ModuleOrganization, "employee", "list", "menu"},
		{OrganizationEmployeeView, "View a staff record", ModuleOrganization, "employee", "view", "api"},
		{OrganizationEmployeeCreate, "Create a staff record", ModuleOrganization, "employee", "create", "button"},
		{OrganizationEmployeeUpdate, "Update a staff record", ModuleOrganization, "employee", "update", "button"},
		{OrganizationEmployeeDelete, "Delete a staff record", ModuleOrganization, "employee", "delete", "button"},

		{RBACRoleList, "List roles", ModuleRBAC, "role", "list", "menu"},
		{RBACRoleCreate, "Create a role", ModuleRBAC, "role", "create", "button"},
		{RBACRoleUpdate, "Update a role", ModuleRBAC, "role", "update", "button"},
		{RBACRoleDelete, "Delete a role", ModuleRBAC, "role", "delete", "button"},
		{RBACRoleManage, "Grant permissions and menus to a role", ModuleRBAC, "role", "manage", "button"},
		{RBACPermissionList, "List permission points", ModuleRBAC, "permission", "list", "menu"},
		{RBACPermissionView, "View a permission point", ModuleRBAC, "permission", "view", "api"},
		{RBACMenuList, "List menus", ModuleRBAC, "menu", "list", "menu"},
		{RBACMenuCreate, "Create a menu", ModuleRBAC, "menu", "create", "button"},
		{RBACMenuUpdate, "Update a menu", ModuleRBAC, "menu", "update", "button"},
		{RBACMenuDelete, "Delete a menu", ModuleRBAC, "menu", "delete", "button"},
		{RBACDictionaryList, "List dictionaries", ModuleRBAC, "dictionary", "list", "menu"},
		{RBACDictionaryCreate, "Create a dictionary", ModuleRBAC, "dictionary", "create", "button"},
		{RBACDictionaryUpdate, "Update a dictionary", ModuleRBAC, "dictionary", "update", "button"},
		{RBACDictionaryDelete, "Delete a dictionary", ModuleRBAC, "dictionary", "delete", "button"},
		{RBACSettingsManage, "Change platform settings", ModuleRBAC, "settings", "manage", "menu"},
	}
}

// Set is an in-memory lookup of permission codes.
type Set map[string]struct{}

// NewSet builds a Set from codes.
func NewSet(codes ...string) Set {
	out := make(Set, len(codes))
	for _, c := range codes {
		out[c] = struct{}{}
	}
	return out
}

// Has reports whether the set contains a code.
func (s Set) Has(code string) bool {
	_, ok := s[code]
	return ok
}

// HasAny reports whether the set contains any of the codes.
func (s Set) HasAny(codes ...string) bool {
	for _, c := range codes {
		if s.Has(c) {
			return true
		}
	}
	return false
}

// Codes returns the set's members in a stable order.
func (s Set) Codes() []string {
	out := make([]string, 0, len(s))
	for c := range s {
		out = append(out, c)
	}
	return out
}
