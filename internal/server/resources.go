package server

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/identity"
	"pocketadmin/internal/permission"
)

// identityResources declares the identity module's managed collections.
func identityResources() []resource {
	return []resource{
		{
			Collection: "users",
			BasePath:   "/identity/accounts",
			Title:      "Accounts",
			Singular:   "account",
			Active:     "identity.accounts",
			ListPerm:   permission.IdentityAccountList,
			CreatePerm: permission.IdentityAccountCreate,
			UpdatePerm: permission.IdentityAccountUpdate,
			// No generic delete: an account is soft-deleted through its own action,
			// because audit history, orders and HR records reference the row.
			DeletePerm: "",
			Order:      "-created",
			Filter:     "deletedAt = ''",
			LabelField: "email",
			HasDetail:  true,
			BeforeSave: normaliseAccountPhone,
		},
		{
			Collection: "sessions",
			BasePath:   "/identity/sessions",
			Title:      "Sessions",
			Singular:   "session",
			Active:     "identity.accounts",
			ListPerm:   permission.IdentitySessionList,
			Order:      "-created",
		},
	}
}

// organizationResources declares the organization module's managed collections.
func organizationResources() []resource {
	return []resource{
		{
			Collection:  "departments",
			BasePath:    "/organization/departments",
			Title:       "Departments",
			Singular:    "department",
			Active:      "organization.departments",
			ListPerm:    permission.OrganizationDepartmentList,
			CreatePerm:  permission.OrganizationDepartmentCreate,
			UpdatePerm:  permission.OrganizationDepartmentUpdate,
			DeletePerm:  permission.OrganizationDepartmentDelete,
			Order:       "path,sortOrder",
			Filter:      "deletedAt = ''",
			ParentField: "parent",
			LabelField:  "name",
			// path and level are derived from the tree position, never entered. The
			// tree invariants themselves live in internal/organization, bound as
			// model hooks so every write path maintains them; this resource only
			// declares the fields as read-only and refuses an orphan-producing
			// delete.
			DerivedFields: []string{"path", "level"},
			TreeView:      true,
			TreePathField: "path",
			CountOf: &countSpec{
				Collection: "employees",
				Field:      "department",
				Label:      "Members",
				Filter:     "deletedAt = ''",
			},
			BeforeDelete: func(s *Server, rec *core.Record) error {
				return refuseIfReferenced(s, "departments", "parent", rec.Id,
					"This department still has sub-departments. Move or delete them first.")
			},
		},
		{
			Collection: "positions",
			BasePath:   "/organization/positions",
			Title:      "Positions",
			Singular:   "position",
			Active:     "organization.positions",
			ListPerm:   permission.OrganizationPositionList,
			CreatePerm: permission.OrganizationPositionCreate,
			UpdatePerm: permission.OrganizationPositionUpdate,
			DeletePerm: permission.OrganizationPositionDelete,
			Order:      "-created",
			Filter:     "deletedAt = ''",
			BeforeDelete: func(s *Server, rec *core.Record) error {
				return refuseIfReferenced(s, "employees", "position", rec.Id,
					"This position is still assigned to staff. Reassign them first.")
			},
		},
		{
			Collection: "employees",
			BasePath:   "/organization/staff",
			Title:      "Staff",
			Singular:   "staff record",
			Active:     "organization.staff",
			ListPerm:   permission.OrganizationEmployeeList,
			CreatePerm: permission.OrganizationEmployeeCreate,
			UpdatePerm: permission.OrganizationEmployeeUpdate,
			DeletePerm: permission.OrganizationEmployeeDelete,
			Order:      "-created",
			Filter:     "deletedAt = ''",
			LabelField: "employeeNo",
		},
	}
}

// rbacResources declares the rbac module's managed collections.
func rbacResources() []resource {
	return []resource{
		{
			Collection: "roles",
			BasePath:   "/rbac/roles",
			Title:      "Roles",
			Singular:   "role",
			Active:     "rbac.roles",
			ListPerm:   permission.RBACRoleList,
			CreatePerm: permission.RBACRoleCreate,
			UpdatePerm: permission.RBACRoleUpdate,
			DeletePerm: permission.RBACRoleDelete,
			Order:      "sortOrder",
			Filter:     "deletedAt = ''",
			LabelField: "name",
			BeforeSave: func(s *Server, rec *core.Record, isNew bool) error {
				if rec.GetString("code") == "" {
					return fmt.Errorf("a role needs a code")
				}
				if rec.GetBool("isSystem") && rec.GetString("status") == "disabled" {
					return fmt.Errorf("a system role cannot be disabled")
				}
				return nil
			},
			BeforeDelete: func(s *Server, rec *core.Record) error {
				if rec.GetBool("isSystem") {
					return fmt.Errorf("system roles cannot be deleted")
				}
				return refuseIfReferenced(s, "admins", "roles", rec.Id,
					"This role is still assigned to administrators. Revoke it first.")
			},
			// A role's grants decide what every holder may do, so any change must
			// drop the cached authority of everyone affected.
			AfterSave: func(s *Server, _ *core.Record, _ bool) { s.resolver.InvalidateAll() },
		},
		{
			Collection: "permissions",
			BasePath:   "/rbac/permissions",
			Title:      "Permission points",
			Singular:   "permission",
			Active:     "rbac.permissions",
			ListPerm:   permission.RBACPermissionList,
			Order:      "module,resource,action",
		},
		{
			Collection:  "menus",
			BasePath:    "/rbac/menus",
			Title:       "Menus",
			Singular:    "menu",
			Active:      "rbac.menus",
			ListPerm:    permission.RBACMenuList,
			CreatePerm:  permission.RBACMenuCreate,
			UpdatePerm:  permission.RBACMenuUpdate,
			DeletePerm:  permission.RBACMenuDelete,
			Order:       "sortOrder",
			LabelField:  "title",
			ParentField: "parent",
			TreeView:    true,
			BeforeDelete: func(s *Server, rec *core.Record) error {
				return refuseIfReferenced(s, "menus", "parent", rec.Id,
					"This menu still has child nodes. Move or delete them first.")
			},
			// Navigation is cached; a menu change must drop it.
			AfterSave: func(s *Server, _ *core.Record, _ bool) { s.menus.Invalidate() },
		},
		{
			// A dictionary's items are managed under the dictionary itself, so the
			// parent is taken from the query string rather than chosen in a form.
			// The dictionary permissions guard them: managing a dictionary's entries
			// is managing the dictionary.
			Collection: "dictionary_items",
			BasePath:   "/rbac/dictionary-items",
			Title:      "Dictionary items",
			Singular:   "item",
			Active:     "rbac.dictionaries",
			ListPerm:   permission.RBACDictionaryList,
			CreatePerm: permission.RBACDictionaryCreate,
			UpdatePerm: permission.RBACDictionaryUpdate,
			DeletePerm: permission.RBACDictionaryDelete,
			Order:      "sortOrder,label",
			ChildOf:    "dictionary",
			LabelField: "label",
		},
		{
			Collection: "dictionaries",
			BasePath:   "/rbac/dictionaries",
			Title:      "Dictionaries",
			Singular:   "dictionary",
			Active:     "rbac.dictionaries",
			ListPerm:   permission.RBACDictionaryList,
			CreatePerm: permission.RBACDictionaryCreate,
			UpdatePerm: permission.RBACDictionaryUpdate,
			DeletePerm: permission.RBACDictionaryDelete,
			Order:      "name",
			LabelField: "name",

			// A dictionary is only useful because of its entries, so the list links
			// straight to them.
			RowActionPath:  "/rbac/dictionary-items?parent=",
			RowActionLabel: "Items",
			RowActionIcon:  "fas fa-list",
		},
	}
}

// ---------------------------------------------------------------------------
// Resource hooks
// ---------------------------------------------------------------------------

// normaliseAccountPhone stores a phone number in one shape.
//
// The unique index compares stored values, so two accounts whose numbers differ
// only by formatting would both be accepted and then be indistinguishable to a
// notification transport.
func normaliseAccountPhone(_ *Server, rec *core.Record, _ bool) error {
	rec.Set("phone", identity.NormalisePhone(rec.GetString("phone")))
	return nil
}

// refuseIfReferenced blocks a delete while another collection still points at
// the record.
//
// Relations are declared without cascade delete precisely so that a mistake here
// produces a message instead of silently removing depended-on rows.
func refuseIfReferenced(s *Server, collection, field, id, message string) error {
	records, err := s.app.FindRecordsByFilter(collection,
		field+" = {:id}", "", 1, 0, map[string]any{"id": id})
	if err != nil {
		return nil
	}
	if len(records) > 0 {
		return fmt.Errorf("%s", message)
	}
	return nil
}
