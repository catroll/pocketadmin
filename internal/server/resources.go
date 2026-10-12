package server

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/pocketbase/pocketbase/core"

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
			DeletePerm: permission.IdentityAccountDelete,
			Order:      "-created",
			Filter:     "deletedAt = ''",
			LabelField: "email",
		},
		{
			// The sign-in history is append-only through the application, so the
			// screen is read-only.
			Collection: "login_logs",
			BasePath:   "/identity/login-logs",
			Title:      "Sign-in history",
			Singular:   "log entry",
			Active:     "identity.accounts",
			ListPerm:   permission.IdentityLoginLogList,
			Order:      "-created",
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
			// path and level are derived from the tree position, never entered.
			DerivedFields: []string{"path", "level"},
			BeforeSave:    validateDepartmentParent,
			BeforeDelete: func(s *Server, rec *core.Record) error {
				return refuseIfReferenced(s, "departments", "parent", rec.Id,
					"This department still has sub-departments. Move or delete them first.")
			},
			AfterSave: reindexDepartmentPath,
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
			Collection: "menus",
			BasePath:   "/rbac/menus",
			Title:      "Menus",
			Singular:   "menu",
			Active:     "rbac.menus",
			ListPerm:   permission.RBACMenuList,
			CreatePerm: permission.RBACMenuCreate,
			UpdatePerm: permission.RBACMenuUpdate,
			DeletePerm: permission.RBACMenuDelete,
			Order:      "sortOrder",
			LabelField: "title",
			BeforeDelete: func(s *Server, rec *core.Record) error {
				return refuseIfReferenced(s, "menus", "parent", rec.Id,
					"This menu still has child nodes. Move or delete them first.")
			},
			// Navigation is cached; a menu change must drop it.
			AfterSave: func(s *Server, _ *core.Record, _ bool) { s.menus.Invalidate() },
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
		},
	}
}

// ---------------------------------------------------------------------------
// Resource hooks
// ---------------------------------------------------------------------------

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

// validateDepartmentParent checks a department's parent and derives its depth.
//
// The materialised path is not computed here: it embeds the record's own id,
// which PocketBase assigns at save time. See reindexDepartmentPath.
func validateDepartmentParent(s *Server, rec *core.Record, isNew bool) error {
	parentID := rec.GetString("parent")

	if parentID == "" {
		rec.Set("level", 1)
		return nil
	}

	if parentID == rec.Id && rec.Id != "" {
		return fmt.Errorf("a department cannot be its own parent")
	}

	parent, err := s.app.FindRecordById("departments", parentID)
	if err != nil || parent == nil {
		return fmt.Errorf("the selected parent department no longer exists")
	}

	// Refuse to make a department a child of one of its own descendants, which
	// would detach that subtree from the tree entirely.
	if !isNew && rec.Id != "" && strings.Contains(parent.GetString("path"), "/"+rec.Id+"/") {
		return fmt.Errorf("a department cannot be moved under one of its own sub-departments")
	}

	rec.Set("level", parent.GetInt("level")+1)
	return nil
}

// reindexDepartmentPath writes the department's own materialised path and
// rewrites every descendant's, inside one transaction.
//
// A re-parent changes the path of the whole subtree, and a half-applied move
// would corrupt the scope predicate that row-level filtering depends on.
func reindexDepartmentPath(s *Server, rec *core.Record, _ bool) {
	if rec == nil || rec.Id == "" {
		return
	}

	oldPath := rec.GetString("path")

	desired := "/" + rec.Id + "/"
	if parentID := rec.GetString("parent"); parentID != "" {
		if parent, err := s.app.FindRecordById("departments", parentID); err == nil && parent != nil {
			desired = parent.GetString("path") + rec.Id + "/"
		}
	}

	if oldPath == desired {
		return
	}

	err := s.app.RunInTransaction(func(txApp core.App) error {
		affected := []*core.Record{rec}

		if oldPath != "" {
			descendants, err := txApp.FindRecordsByFilter("departments",
				"path ~ {:old} && id != {:id}", "", 0, 0,
				map[string]any{"old": oldPath, "id": rec.Id})
			if err != nil {
				return err
			}
			affected = append(affected, descendants...)
		}

		rec.Set("path", desired)
		if err := txApp.Save(rec); err != nil {
			return err
		}

		for _, d := range affected[1:] {
			if oldPath == "" {
				continue
			}
			d.Set("path", strings.Replace(d.GetString("path"), oldPath, desired, 1))
			if err := txApp.Save(d); err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		slog.Warn("could not reindex the department tree", "department", rec.Id, "error", err)
	}
}
