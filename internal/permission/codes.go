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
	ModuleStorage      = "storage"
	ModuleAudit        = "audit"
	ModuleSettings     = "settings"
	ModuleJobs         = "jobs"
	ModuleAnnouncement = "announcement"
	ModuleWorkbench    = "workbench"
	ModuleDeveloper    = "developer"
	ModuleIntegrations = "integrations"
	ModuleMonitoring   = "monitoring"
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

	// Storage governs the file library. Record-owned files -- an avatar, a
	// product image -- use native PocketBase file fields on their owning record
	// and are deliberately not covered here: they are part of editing that
	// record, not of the library.
	StorageFolderList       = "storage:folder:list"
	StorageFolderCreate     = "storage:folder:create"
	StorageFolderUpdate     = "storage:folder:update"
	StorageFolderDelete     = "storage:folder:delete"
	StorageFileList         = "storage:file:list"
	StorageFileUpload       = "storage:file:upload"
	StorageFileUpdate       = "storage:file:update"
	StorageFileDelete       = "storage:file:delete"
	StorageFileDownload     = "storage:file:download"
	StorageAttachmentList   = "storage:attachment:list"
	StorageAttachmentManage = "storage:attachment:manage"

	// Audit owns the four logs. The sign-in history lives here rather than under
	// identity because it is one of the audit surfaces; identity reads it when
	// showing an account's history, which is a different screen over the same
	// rows, not a different permission.
	AuditOperationList = "audit:operation:list"
	AuditLoginList     = "audit:login:list"
	AuditSecurityList  = "audit:security:list"
	AuditExceptionList = "audit:exception:list"
	AuditExport        = "audit:log:export"

	// Settings has two surfaces that share one table and one engine. They carry
	// separate permissions because they are separate concerns: configuring the
	// back office is not the same authority as configuring the storefront, and an
	// operator may reasonably hold one and not the other.
	SettingsAdminView     = "settings:admin:view"
	SettingsAdminUpdate   = "settings:admin:update"
	SettingsProductView   = "settings:product:view"
	SettingsProductUpdate = "settings:product:update"

	// Jobs: the catalogue of task handlers is fixed in code, so these govern
	// scheduling and history rather than what a task does.
	JobsJobList    = "jobs:job:list"
	JobsJobUpdate  = "jobs:job:update"
	JobsJobExecute = "jobs:job:execute"
	JobsRunList    = "jobs:run:list"

	// Announcements. Publishing is separate from editing because writing an
	// announcement and deciding that everyone should see it are different
	// authorities -- a drafter should be able to prepare one without being able to
	// send it.
	AnnouncementList    = "announcement:announcement:list"
	AnnouncementCreate  = "announcement:announcement:create"
	AnnouncementUpdate  = "announcement:announcement:update"
	AnnouncementDelete  = "announcement:announcement:delete"
	AnnouncementPublish = "announcement:announcement:publish"
	AnnouncementReads   = "announcement:read:list"

	// Workbench. A todo is personal, so these govern the screen rather than who
	// may see whose todos: ownership is enforced separately and is not a
	// permission anybody can be granted.
	WorkbenchTodoList     = "workbench:todo:list"
	WorkbenchMessageList  = "workbench:message:list"
	WorkbenchMessageSend  = "workbench:message:send"
	WorkbenchQuickLinkOwn = "workbench:quicklink:own"
	WorkbenchQuickLinkAll = "workbench:quicklink:all"

	// Developer. Issuing a key is separate from revoking one: an operator who can
	// hand out access should not automatically be able to withdraw somebody
	// else's, and the reverse is equally true. Seeing the specification is open to
	// anyone who can read the API.
	DeveloperKeyList   = "developer:key:list"
	DeveloperKeyCreate = "developer:key:create"
	DeveloperKeyRevoke = "developer:key:revoke"
	DeveloperAPIView   = "developer:api:view"

	// Integrations. Seeing a provider is separate from changing one, because a
	// change here redirects where the platform sends mail, files and money.
	// Testing is separate again: it makes an outbound request with the stored
	// credentials, which is a different act from editing them.
	IntegrationProviderList   = "integrations:provider:list"
	IntegrationProviderCreate = "integrations:provider:create"
	IntegrationProviderUpdate = "integrations:provider:update"
	IntegrationProviderDelete = "integrations:provider:delete"
	IntegrationProviderTest   = "integrations:provider:test"
	IntegrationLogList        = "integrations:log:list"

	// Monitoring. The health endpoint's detailed view is a permission of its own:
	// it names the database, the storage directory and the reason a component is
	// unwell, which is more than knowing the service is up.
	MonitoringDashboard   = "monitoring:dashboard:view"
	MonitoringHealth      = "monitoring:health:view"
	MonitoringSystemInfo  = "monitoring:system:info"
	MonitoringSnapshotRun = "monitoring:snapshot:run"
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

		{StorageFolderList, "List folders", ModuleStorage, "folder", "list", "menu"},
		{StorageFolderCreate, "Create a folder", ModuleStorage, "folder", "create", "button"},
		{StorageFolderUpdate, "Rename or move a folder", ModuleStorage, "folder", "update", "button"},
		{StorageFolderDelete, "Delete a folder", ModuleStorage, "folder", "delete", "button"},
		{StorageFileList, "List files", ModuleStorage, "file", "list", "menu"},
		{StorageFileUpload, "Upload files", ModuleStorage, "file", "upload", "button"},
		{StorageFileUpdate, "Update file metadata", ModuleStorage, "file", "update", "button"},
		{StorageFileDelete, "Delete a file", ModuleStorage, "file", "delete", "button"},
		{StorageFileDownload, "Download a file", ModuleStorage, "file", "download", "api"},
		{StorageAttachmentList, "List an attachment's references", ModuleStorage, "attachment", "list", "api"},
		{StorageAttachmentManage, "Attach or detach a file", ModuleStorage, "attachment", "manage", "button"},

		{AuditOperationList, "View the operation log", ModuleAudit, "operation", "list", "menu"},
		{AuditLoginList, "View the sign-in history", ModuleAudit, "login", "list", "menu"},
		{AuditSecurityList, "View the security audit log", ModuleAudit, "security", "list", "menu"},
		{AuditExceptionList, "View the exception log", ModuleAudit, "exception", "list", "menu"},
		{AuditExport, "Export a log as CSV", ModuleAudit, "log", "export", "button"},

		{SettingsAdminView, "View the back-office settings", ModuleSettings, "admin", "view", "menu"},
		{SettingsAdminUpdate, "Change the back-office settings", ModuleSettings, "admin", "update", "button"},
		{SettingsProductView, "View the product settings", ModuleSettings, "product", "view", "menu"},
		{SettingsProductUpdate, "Change the product settings", ModuleSettings, "product", "update", "button"},

		{JobsJobList, "List scheduled tasks", ModuleJobs, "job", "list", "menu"},
		{JobsJobUpdate, "Change a task's schedule or state", ModuleJobs, "job", "update", "button"},
		{JobsJobExecute, "Run a task now", ModuleJobs, "job", "execute", "button"},
		{JobsRunList, "View task run history", ModuleJobs, "run", "list", "api"},

		{AnnouncementList, "List announcements", ModuleAnnouncement, "announcement", "list", "menu"},
		{AnnouncementCreate, "Write an announcement", ModuleAnnouncement, "announcement", "create", "button"},
		{AnnouncementUpdate, "Edit an announcement", ModuleAnnouncement, "announcement", "update", "button"},
		{AnnouncementDelete, "Delete an announcement", ModuleAnnouncement, "announcement", "delete", "button"},
		{AnnouncementPublish, "Publish or withdraw an announcement", ModuleAnnouncement, "announcement", "publish", "button"},
		{AnnouncementReads, "See who has read an announcement", ModuleAnnouncement, "read", "list", "button"},

		{WorkbenchTodoList, "Use the personal todo list", ModuleWorkbench, "todo", "list", "menu"},
		{WorkbenchMessageList, "Read the workbench inbox", ModuleWorkbench, "message", "list", "menu"},
		{WorkbenchMessageSend, "Send a message to administrators", ModuleWorkbench, "message", "send", "button"},
		{WorkbenchQuickLinkOwn, "Edit your own quick links", ModuleWorkbench, "quicklink", "own", "button"},
		{WorkbenchQuickLinkAll, "Edit the default quick links", ModuleWorkbench, "quicklink", "all", "button"},

		{DeveloperKeyList, "List API keys", ModuleDeveloper, "key", "list", "menu"},
		{DeveloperKeyCreate, "Issue an API key", ModuleDeveloper, "key", "create", "button"},
		{DeveloperKeyRevoke, "Revoke an API key", ModuleDeveloper, "key", "revoke", "button"},
		{DeveloperAPIView, "Open the API reference", ModuleDeveloper, "api", "view", "menu"},

		{IntegrationProviderList, "List integration providers", ModuleIntegrations, "provider", "list", "menu"},
		{IntegrationProviderCreate, "Add an integration provider", ModuleIntegrations, "provider", "create", "button"},
		{IntegrationProviderUpdate, "Change an integration provider", ModuleIntegrations, "provider", "update", "button"},
		{IntegrationProviderDelete, "Remove an integration provider", ModuleIntegrations, "provider", "delete", "button"},
		{IntegrationProviderTest, "Test an integration provider", ModuleIntegrations, "provider", "test", "button"},
		{IntegrationLogList, "Read the integration log", ModuleIntegrations, "log", "list", "api"},
		{MonitoringDashboard, "View the monitoring dashboard", ModuleMonitoring, "dashboard", "view", "menu"},
		{MonitoringHealth, "See detailed component health", ModuleMonitoring, "health", "view", "button"},
		{MonitoringSystemInfo, "See system information", ModuleMonitoring, "system", "info", "api"},
		{MonitoringSnapshotRun, "Record a metric snapshot", ModuleMonitoring, "snapshot", "run", "button"},
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
