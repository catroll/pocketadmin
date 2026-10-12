package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
)

// identityMigration extends PocketBase's built-in users collection and adds the
// profile extensions, sessions, preferences and the sign-in log.
//
// There is one account per person. employees and admins are 1:1 profile
// extensions of that account, not separate identities: a person has one
// password, one reset flow and one set of sessions, while authority is granted
// by attaching a profile.
func init() {
	migrations.Register(func(app core.App) error {
		if err := extendUsers(app); err != nil {
			return err
		}
		if err := createEmployees(app); err != nil {
			return err
		}
		if err := createAdmins(app); err != nil {
			return err
		}
		if err := createUserPreferences(app); err != nil {
			return err
		}
		if err := createSessions(app); err != nil {
			return err
		}
		return createLoginLogs(app)
	}, func(app core.App) error {
		// The users collection is PocketBase's own; only the added fields are
		// removed so a rollback cannot delete the account table.
		if col := findCollection(app, "users"); col != nil {
			for _, name := range []string{
				"nickname", "phone", "gender", "birthday", "status", "tags", "source",
				"referrer", "registeredAt", "invitationCode", "failedAttempts",
				"lockedUntil", "lastLoginAt", "lastLoginIp", "passwordChangedAt",
				"remarks", "deletedAt",
			} {
				col.Fields.RemoveByName(name)
			}
			if err := app.Save(col); err != nil {
				return err
			}
		}
		for _, name := range []string{"login_logs", "sessions", "user_preferences", "admins", "employees"} {
			if col := findCollection(app, name); col != nil {
				if err := app.Delete(col); err != nil {
					return err
				}
			}
		}
		return nil
	}, "1700000003_identity.go")
}

// extendUsers adds the platform's fields to PocketBase's users collection and
// replaces its factory API rules.
//
// PocketBase seeds users with a public create rule and owner-scoped read, update
// and delete rules. This platform ships no public sign-up endpoint, so those
// defaults are replaced with superuser-only rules.
func extendUsers(app core.App) error {
	col := findCollection(app, "users")
	if col == nil {
		col = newAuth("users")
		ensureTimestamps(col)
	}

	addField(col, &core.TextField{Name: "nickname"})
	addField(col, &core.TextField{Name: "phone"})
	addField(col, selectOf("gender", false, "unknown", "male", "female", "other"))
	addField(col, &core.DateField{Name: "birthday"})
	addField(col, selectOf("status", false, "active", "inactive", "banned"))
	// tags is a select field because PocketBase stores enumerated values as a
	// constrained column. The `user_tag` dictionary is the runtime source of
	// truth for the editor; this list is the storage constraint and must be kept
	// in sync with it when tags change. maxSelect may not exceed the number of
	// declared values.
	addField(col, multiSelectOf("tags", 4, "vip", "staff", "partner", "trial"))
	addField(col, selectOf("source", false, "web", "ios", "android", "admin", "import", "panel"))
	addField(col, &core.TextField{Name: "referrer"})
	addField(col, &core.DateField{Name: "registeredAt"})
	addField(col, &core.TextField{Name: "invitationCode"})
	addField(col, &core.NumberField{Name: "failedAttempts"})
	addField(col, &core.DateField{Name: "lockedUntil"})
	addField(col, &core.DateField{Name: "lastLoginAt"})
	addField(col, &core.TextField{Name: "lastLoginIp"})
	addField(col, &core.DateField{Name: "passwordChangedAt"})
	addField(col, &core.TextField{Name: "remarks"})
	addField(col, &core.DateField{Name: "deletedAt"})

	col.AddIndex("idx_users_phone", true, "phone", "phone != ''")
	col.AddIndex("idx_users_status", false, "status", "")
	col.AddIndex("idx_users_registered", false, "registeredAt", "")

	// Replace PocketBase's factory rules: no public sign-up, no owner access.
	lockDown(col)

	return app.Save(col)
}

// createEmployees creates the HR profile extension, 1:1 with a user.
func createEmployees(app core.App) error {
	col := findCollection(app, "employees")
	if col == nil {
		col = newBase("employees")
	}

	ensureTimestamps(col)
	addField(col, rel(app, "user", "users", true))
	addField(col, &core.TextField{Name: "employeeNo"})
	addField(col, rel(app, "department", "departments", false))
	addField(col, rel(app, "position", "positions", false))
	addField(col, &core.TextField{Name: "workEmail"})
	addField(col, &core.TextField{Name: "workPhone"})
	addField(col, &core.DateField{Name: "hireDate"})
	addField(col, &core.DateField{Name: "leaveDate"})
	addField(col, selectOf("employmentType", false, "fulltime", "parttime", "intern", "contractor"))
	addField(col, selectOf("status", false, "probation", "active", "on_leave", "resigned"))
	addField(col, &core.TextField{Name: "remarks"})
	addField(col, &core.DateField{Name: "deletedAt"})

	col.AddIndex("idx_employees_user", true, "user", "")
	col.AddIndex("idx_employees_no", true, "employeeNo", "employeeNo != ''")
	col.AddIndex("idx_employees_department", false, "department, status", "")
	col.AddIndex("idx_employees_position", false, "position", "")
	disableCascade(col, "user", "department", "position")

	if err := app.Save(col); err != nil {
		return err
	}

	// Self-relation: a line manager is another employee.
	if addField(col, &core.RelationField{
		Name:         "manager",
		CollectionId: col.Id,
		MaxSelect:    1,
	}) {
		col.AddIndex("idx_employees_manager", false, "manager", "")
		return app.Save(col)
	}

	return nil
}

// createAdmins creates the back-office profile extension, 1:1 with a user.
//
// It holds no password. Authority is granted here; identity lives on the
// account.
func createAdmins(app core.App) error {
	col := findCollection(app, "admins")
	if col == nil {
		col = newBase("admins")
	}

	ensureTimestamps(col)
	addField(col, rel(app, "user", "users", true))
	addField(col, multiRel(app, "roles", "roles", 50))
	addField(col, rel(app, "department", "departments", false))
	addField(col, selectOf("status", false, "active", "disabled", "locked"))
	addField(col, &core.BoolField{Name: "isProtected"})
	addField(col, &core.BoolField{Name: "mfaRequired"})
	addField(col, &core.DateField{Name: "lastAdminLoginAt"})
	addField(col, &core.TextField{Name: "lastAdminLoginIp"})
	addField(col, &core.TextField{Name: "remarks"})
	addField(col, &core.DateField{Name: "deletedAt"})

	col.AddIndex("idx_admins_user", true, "user", "")
	col.AddIndex("idx_admins_status", false, "status", "")
	col.AddIndex("idx_admins_department", false, "department", "")
	disableCascade(col, "user", "roles", "department")

	return app.Save(col)
}

// createUserPreferences creates per-account, per-surface preferences.
func createUserPreferences(app core.App) error {
	col := findCollection(app, "user_preferences")
	if col == nil {
		col = newBase("user_preferences")
	}

	ensureTimestamps(col)
	addField(col, rel(app, "user", "users", true))
	addField(col, selectOf("context", true, "admin", "product"))
	addField(col, selectOf("theme", false, "light", "dark", "auto"))
	addField(col, &core.TextField{Name: "language"})
	addField(col, &core.NumberField{Name: "pageSize"})
	addField(col, &core.BoolField{Name: "sidebarCollapsed"})
	addField(col, &core.TextField{Name: "timezone"})
	addField(col, &core.BoolField{Name: "notifyEmail"})
	addField(col, &core.BoolField{Name: "notifySms"})
	addField(col, jsonField("extra", 20000))

	col.AddIndex("idx_prefs_user_context", true, "user, context", "")
	disableCascade(col, "user")

	return app.Save(col)
}

// createSessions creates the revocable session records.
//
// PocketBase's auth tokens are stateless JWTs with no server-side revocation.
// Listing and revoking sessions is a requirement, so the panel owns its own
// records. Only the SHA-256 of the session identifier is stored.
func createSessions(app core.App) error {
	col := findCollection(app, "sessions")
	if col == nil {
		col = newBase("sessions")
	}

	ensureTimestamps(col)
	addField(col, rel(app, "user", "users", true))
	addField(col, selectOf("context", true, "admin", "product", "api"))
	addField(col, &core.TextField{Name: "tokenHash", Required: true})
	addField(col, &core.TextField{Name: "ip"})
	addField(col, &core.TextField{Name: "userAgent"})
	addField(col, &core.TextField{Name: "device"})
	addField(col, &core.TextField{Name: "location"})
	addField(col, &core.BoolField{Name: "impersonated"})
	addField(col, rel(app, "impersonatedBy", "users", false))
	addField(col, &core.DateField{Name: "lastSeenAt"})
	addField(col, &core.DateField{Name: "expiresAt"})
	addField(col, &core.DateField{Name: "revokedAt"})
	addField(col, &core.TextField{Name: "revokedReason"})

	col.AddIndex("idx_sessions_token", true, "tokenHash", "")
	col.AddIndex("idx_sessions_user", false, "user, context, revokedAt", "")
	col.AddIndex("idx_sessions_expiry", false, "expiresAt", "")
	disableCascade(col, "user", "impersonatedBy")

	return app.Save(col)
}

// createLoginLogs creates the sign-in history.
//
// The capability map assigns this collection to the audit module. It is created
// here because identity owns the sign-in path that writes to it, and audit is
// built later on top.
func createLoginLogs(app core.App) error {
	col := findCollection(app, "login_logs")
	if col == nil {
		col = newBase("login_logs")
	}

	ensureTimestamps(col)
	addField(col, rel(app, "user", "users", false))
	addField(col, selectOf("context", true, "admin", "product"))
	addField(col, &core.TextField{Name: "identifier"})
	addField(col, &core.TextField{Name: "ip"})
	addField(col, &core.TextField{Name: "userAgent"})
	addField(col, &core.TextField{Name: "device"})
	addField(col, &core.TextField{Name: "location"})
	addField(col, selectOf("result", true,
		"success", "failure", "locked", "captcha_error", "mfa_required", "mfa_failed"))
	addField(col, &core.TextField{Name: "reason"})
	addField(col, selectOf("riskLevel", false, "low", "medium", "high"))

	col.AddIndex("idx_login_context", false, "context, created", "")
	col.AddIndex("idx_login_user", false, "user, created", "")
	col.AddIndex("idx_login_identifier", false, "identifier", "")
	col.AddIndex("idx_login_ip", false, "ip", "")
	col.AddIndex("idx_login_result", false, "result", "")
	disableCascade(col, "user")

	return app.Save(col)
}
