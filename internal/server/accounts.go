package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	"pocketadmin/internal/permission"
	"pocketadmin/internal/rbac"
)

// accountDetailPage is the account screen: the identity itself plus its two
// optional profile extensions.
//
// This is where the users-centric model becomes visible. The account holds the
// credentials; the profiles are separate records that grant organisation
// membership and back-office access, and each can be attached or detached
// without touching the account.
type accountDetailPage struct {
	base

	Account *core.Record

	// Employee and Admin are nil when the corresponding profile is not attached.
	Employee *core.Record
	Admin    *core.Record

	// RoleOptions lists every active role with its granted state, so the form can
	// render a checkbox per role rather than a multi-select.
	RoleOptions []roleOption

	Departments []formOption
	Positions   []formOption
	Managers    []formOption

	Sessions     []*core.Record
	RecentLogins []*core.Record

	// Capability flags, so the page hides what the actor cannot do.
	CanEditProfile    bool
	CanAssignRoles    bool
	CanViewSessions   bool
	CanRevokeSessions bool
	CanViewLogins     bool
}

// roleOption is a role with its selection state on the account form.
type roleOption struct {
	ID          string
	Code        string
	Name        string
	Description string
	Selected    bool
	IsSystem    bool
}

// registerAccountRoutes mounts the account detail screen and its profile
// actions.
//
// Profile changes and role assignment are separated deliberately: attaching a
// profile is an identity operation, while granting roles decides what that
// profile may do, so the two carry different permissions.
func (s *Server) registerAccountRoutes(panel *gin.RouterGroup) {
	s.readGuarded(panel, http.MethodGet, "/identity/accounts/:id",
		permission.IdentityAccountView, s.handleAccountDetail)

	s.guarded(panel, http.MethodPost, "/identity/accounts/:id/employee",
		permission.IdentityProfileAttach, s.handleEmployeeProfileSave)
	s.guarded(panel, http.MethodPost, "/identity/accounts/:id/employee/detach",
		permission.IdentityProfileAttach, s.handleEmployeeProfileDetach)

	s.guarded(panel, http.MethodPost, "/identity/accounts/:id/admin",
		permission.IdentityProfileAttach, s.handleAdminProfileSave)
	s.guarded(panel, http.MethodPost, "/identity/accounts/:id/admin/detach",
		permission.IdentityProfileAttach, s.handleAdminProfileDetach)

	s.guarded(panel, http.MethodPost, "/identity/accounts/:id/roles",
		permission.RBACRoleManage, s.handleAccountRolesSave)

	s.readGuarded(panel, http.MethodGet, "/identity/accounts/:id/sessions",
		permission.IdentitySessionList, s.handleAccountDetail)

	s.guarded(panel, http.MethodPost, "/identity/sessions/:id/revoke",
		permission.IdentitySessionRevoke, s.handleSessionRevoke)
}

// handleAccountDetail renders the account and its profiles.
func (s *Server) handleAccountDetail(c *gin.Context) {
	user, ok := s.findAccount(c)
	if !ok {
		return
	}

	actor := s.actor(c)
	employee := s.profileFor("employees", user.Id)
	admin := s.profileFor("admins", user.Id)

	vm := accountDetailPage{
		base:              s.newBase(c, "Account", "identity.accounts"),
		Account:           user,
		Employee:          employee,
		Admin:             admin,
		RoleOptions:       s.roleOptions(admin),
		Departments:       s.relationOptionsFor("departments", "name"),
		Positions:         s.relationOptionsFor("positions", "name"),
		Managers:          s.relationOptionsFor("employees", "employeeNo"),
		CanEditProfile:    actor.Can(permission.IdentityProfileAttach),
		CanAssignRoles:    actor.Can(permission.RBACRoleManage),
		CanViewSessions:   actor.Can(permission.IdentitySessionList),
		CanRevokeSessions: actor.Can(permission.IdentitySessionRevoke),
		CanViewLogins:     actor.Can(permission.IdentityLoginLogList),
	}

	if vm.CanViewSessions {
		vm.Sessions = s.recentRecords("sessions", "user = {:u}", user.Id, 10)
	}
	if vm.CanViewLogins {
		vm.RecentLogins = s.recentRecords("login_logs", "user = {:u}", user.Id, 10)
	}

	s.render(c, http.StatusOK, "account_detail.html", vm)
}

// handleEmployeeProfileSave attaches or updates the employee profile.
func (s *Server) handleEmployeeProfileSave(c *gin.Context) {
	user, ok := s.findAccount(c)
	if !ok {
		return
	}

	col, err := s.app.FindCollectionByNameOrId("employees")
	if err != nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Schema missing", err.Error())
		return
	}

	rec := s.profileFor("employees", user.Id)
	isNew := rec == nil
	if isNew {
		rec = core.NewRecord(col)
		rec.Set("user", user.Id)
	}

	rec.Set("employeeNo", strings.TrimSpace(c.PostForm("employeeNo")))
	rec.Set("department", c.PostForm("department"))
	rec.Set("position", c.PostForm("position"))
	rec.Set("manager", c.PostForm("manager"))
	rec.Set("workEmail", strings.TrimSpace(c.PostForm("workEmail")))
	rec.Set("workPhone", strings.TrimSpace(c.PostForm("workPhone")))
	rec.Set("employmentType", c.PostForm("employmentType"))
	rec.Set("status", c.PostForm("status"))
	rec.Set("remarks", strings.TrimSpace(c.PostForm("remarks")))
	rec.Set("hireDate", nullableDate(c.PostForm("hireDate")))

	if err := s.app.Save(rec); err != nil {
		s.setFlash(c, "danger", "Could not save the employee profile: "+strings.Join(validationMessages(err), "; "))
		c.Redirect(http.StatusSeeOther, "/identity/accounts/"+user.Id)
		return
	}

	// The admin profile denormalises the department for fast scope filtering, so
	// an employee change has to follow through.
	s.syncAdminDepartment(user.Id, rec.GetString("department"))
	s.resolver.Invalidate(user.Id)

	if isNew {
		s.setFlash(c, "success", "Employee profile attached.")
	} else {
		s.setFlash(c, "success", "Employee profile updated.")
	}
	c.Redirect(http.StatusSeeOther, "/identity/accounts/"+user.Id)
}

// handleEmployeeProfileDetach removes the employee profile, keeping the account
// and its history.
func (s *Server) handleEmployeeProfileDetach(c *gin.Context) {
	user, ok := s.findAccount(c)
	if !ok {
		return
	}

	rec := s.profileFor("employees", user.Id)
	if rec == nil {
		s.setFlash(c, "warning", "That account has no employee profile.")
		c.Redirect(http.StatusSeeOther, "/identity/accounts/"+user.Id)
		return
	}

	if err := s.app.Delete(rec); err != nil {
		s.setFlash(c, "danger", "Could not detach the employee profile: "+err.Error())
	} else {
		s.setFlash(c, "success", "Employee profile detached. The account is unchanged.")
	}
	c.Redirect(http.StatusSeeOther, "/identity/accounts/"+user.Id)
}

// handleAdminProfileSave attaches or updates the admin profile.
func (s *Server) handleAdminProfileSave(c *gin.Context) {
	user, ok := s.findAccount(c)
	if !ok {
		return
	}

	col, err := s.app.FindCollectionByNameOrId("admins")
	if err != nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Schema missing", err.Error())
		return
	}

	rec := s.profileFor("admins", user.Id)
	isNew := rec == nil
	if isNew {
		rec = core.NewRecord(col)
		rec.Set("user", user.Id)
	}

	rec.Set("status", c.PostForm("status"))
	rec.Set("department", c.PostForm("department"))
	rec.Set("mfaRequired", c.PostForm("mfaRequired") != "")
	rec.Set("remarks", strings.TrimSpace(c.PostForm("remarks")))

	if err := s.app.Save(rec); err != nil {
		s.setFlash(c, "danger", "Could not save the admin profile: "+strings.Join(validationMessages(err), "; "))
		c.Redirect(http.StatusSeeOther, "/identity/accounts/"+user.Id)
		return
	}

	s.resolver.Invalidate(user.Id)

	if isNew {
		s.setFlash(c, "success", "Admin profile attached. Assign roles to grant access.")
	} else {
		s.setFlash(c, "success", "Admin profile updated.")
	}
	c.Redirect(http.StatusSeeOther, "/identity/accounts/"+user.Id)
}

// handleAdminProfileDetach removes back-office access.
//
// Detaching revokes the account's admin sessions immediately, so access ends on
// the next request instead of at the next token expiry.
func (s *Server) handleAdminProfileDetach(c *gin.Context) {
	user, ok := s.findAccount(c)
	if !ok {
		return
	}

	rec := s.profileFor("admins", user.Id)
	if rec == nil {
		s.setFlash(c, "warning", "That account has no admin profile.")
		c.Redirect(http.StatusSeeOther, "/identity/accounts/"+user.Id)
		return
	}

	if rec.GetBool("isProtected") {
		s.setFlash(c, "danger", "The bootstrap administrator cannot lose back-office access.")
		c.Redirect(http.StatusSeeOther, "/identity/accounts/"+user.Id)
		return
	}

	if err := s.app.Delete(rec); err != nil {
		s.setFlash(c, "danger", "Could not detach the admin profile: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/identity/accounts/"+user.Id)
		return
	}

	if err := s.revokeSessionsForUser(user.Id, sessionContextAdmin, sessionRevokedProfileGone); err != nil {
		slog.Warn("could not revoke sessions after detaching an admin profile", "user", user.Id, "error", err)
	}

	s.resolver.Invalidate(user.Id)
	s.setFlash(c, "success", "Admin profile detached and its sessions revoked.")
	c.Redirect(http.StatusSeeOther, "/identity/accounts/"+user.Id)
}

// handleAccountRolesSave replaces the roles granted to an admin profile.
func (s *Server) handleAccountRolesSave(c *gin.Context) {
	user, ok := s.findAccount(c)
	if !ok {
		return
	}

	rec := s.profileFor("admins", user.Id)
	if rec == nil {
		s.setFlash(c, "warning", "Attach an admin profile before assigning roles.")
		c.Redirect(http.StatusSeeOther, "/identity/accounts/"+user.Id)
		return
	}

	roles := c.PostFormArray("roles")

	// Refuse to strip the last role that can still manage roles, which would lock
	// everyone out of access control.
	if err := s.guardLastRoleManager(rec.Id, roles); err != nil {
		s.setFlash(c, "danger", err.Error())
		c.Redirect(http.StatusSeeOther, "/identity/accounts/"+user.Id)
		return
	}

	rec.Set("roles", roles)
	if err := s.app.Save(rec); err != nil {
		s.setFlash(c, "danger", "Could not save roles: "+strings.Join(validationMessages(err), "; "))
		c.Redirect(http.StatusSeeOther, "/identity/accounts/"+user.Id)
		return
	}

	s.resolver.Invalidate(user.Id)
	s.setFlash(c, "success", "Roles updated.")
	c.Redirect(http.StatusSeeOther, "/identity/accounts/"+user.Id)
}

// handleSessionRevoke revokes one session.
func (s *Server) handleSessionRevoke(c *gin.Context) {
	rec, err := s.app.FindRecordById("sessions", c.Param("id"))
	if err != nil || rec == nil {
		s.setFlash(c, "warning", "That session no longer exists.")
		c.Redirect(http.StatusSeeOther, "/identity/sessions")
		return
	}

	reason := c.PostForm("reason")
	if reason == "" {
		reason = sessionRevokedAdmin
	}

	if err := s.revokeSessionRecord(rec, reason); err != nil {
		s.setFlash(c, "danger", "Could not revoke the session: "+err.Error())
	} else {
		s.setFlash(c, "success", "Session revoked.")
	}

	if back := c.PostForm("back"); back != "" {
		c.Redirect(http.StatusSeeOther, back)
		return
	}
	c.Redirect(http.StatusSeeOther, "/identity/sessions")
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// findAccount resolves the :id account and writes a 404 when it is unknown.
func (s *Server) findAccount(c *gin.Context) (*core.Record, bool) {
	rec, err := s.app.FindRecordById(rbac.CollectionUsers, c.Param("id"))
	if err != nil || rec == nil {
		s.renderErrorPage(c, http.StatusNotFound, "Account not found",
			"No account with that id exists.")
		return nil, false
	}
	return rec, true
}

// profileFor loads a 1:1 profile for an account, or nil when it is not attached.
func (s *Server) profileFor(collection, userID string) *core.Record {
	rec, err := s.app.FindFirstRecordByFilter(collection,
		"user = {:u}", map[string]any{"u": userID})
	if err != nil || rec == nil {
		return nil
	}
	return rec
}

// syncAdminDepartment mirrors the employee profile's department onto the admin
// profile.
//
// The admin profile denormalises the department so row-level scope filtering can
// read it without joining through the employee record on every query.
func (s *Server) syncAdminDepartment(userID, department string) {
	rec := s.profileFor("admins", userID)
	if rec == nil {
		return
	}
	if rec.GetString("department") == department {
		return
	}
	rec.Set("department", department)
	if err := s.app.Save(rec); err != nil {
		slog.Warn("could not mirror the department onto the admin profile", "user", userID, "error", err)
	}
}

// roleOptions lists every active role with the account's granted state.
func (s *Server) roleOptions(admin *core.Record) []roleOption {
	roles, err := s.app.FindRecordsByFilter("roles", "status = 'active'", "sortOrder,name", 0, 0)
	if err != nil {
		return nil
	}

	granted := map[string]bool{}
	if admin != nil {
		for _, id := range admin.GetStringSlice("roles") {
			granted[id] = true
		}
	}

	out := make([]roleOption, 0, len(roles))
	for _, r := range roles {
		out = append(out, roleOption{
			ID:          r.Id,
			Code:        r.GetString("code"),
			Name:        r.GetString("name"),
			Description: r.GetString("description"),
			Selected:    granted[r.Id],
			IsSystem:    r.GetBool("isSystem"),
		})
	}
	return out
}

// relationOptionsFor lists a collection's records as select options.
func (s *Server) relationOptionsFor(collection, labelField string) []formOption {
	records, err := s.app.FindRecordsByFilter(collection, "deletedAt = '' || deletedAt = null", "created", 0, 0)
	if err != nil || len(records) == 0 {
		records, err = s.app.FindRecordsByFilter(collection, "", "created", 0, 0)
		if err != nil {
			return nil
		}
	}

	out := make([]formOption, 0, len(records))
	for _, rec := range records {
		label := rec.GetString(labelField)
		if label == "" {
			label = recordLabel(rec.Collection(), rec)
		}
		out = append(out, formOption{Value: rec.Id, Label: label})
	}
	return out
}

// recentRecords returns the newest records matching a filter.
func (s *Server) recentRecords(collection, filter, userID string, limit int) []*core.Record {
	records, err := s.app.FindRecordsByFilter(collection, filter, "-created", limit, 0,
		map[string]any{"u": userID})
	if err != nil {
		return nil
	}
	return records
}

// guardLastRoleManager refuses a change that would remove the final account able
// to manage roles.
//
// Access control is the one area where a well-meaning edit can lock every
// administrator out of the panel, so the transition is checked rather than
// discovered later.
func (s *Server) guardLastRoleManager(adminID string, newRoles []string) error {
	roleCanManage := func(roleID string) bool {
		role, err := s.app.FindRecordById("roles", roleID)
		if err != nil || role == nil || role.GetString("status") != "active" {
			return false
		}
		for _, pid := range role.GetStringSlice("permissions") {
			point, err := s.app.FindRecordById("permissions", pid)
			if err == nil && point != nil && point.GetString("code") == permission.RBACRoleManage {
				return true
			}
		}
		return false
	}

	stillManages := false
	for _, id := range newRoles {
		if roleCanManage(id) {
			stillManages = true
			break
		}
	}
	if stillManages {
		return nil
	}

	// How many other admins can still manage roles?
	admins, err := s.app.FindRecordsByFilter("admins",
		"status = 'active' && deletedAt = ''", "", 0, 0)
	if err != nil {
		return nil
	}

	for _, admin := range admins {
		if admin.Id == adminID {
			continue
		}
		for _, roleID := range admin.GetStringSlice("roles") {
			if roleCanManage(roleID) {
				return nil
			}
		}
	}

	return fmt.Errorf("this is the last account that can manage roles; grant that permission to someone else first")
}

// nullableDate converts a form date into a PocketBase value, mapping an empty
// string to "no date" rather than to the zero time.
func nullableDate(raw string) any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	dt, err := types.ParseDateTime(raw)
	if err != nil {
		return nil
	}
	return dt
}
