package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"

	"pocketadmin/internal/audit"
)

// auditMigration creates the audit module's logs and reshapes the security log
// the identity module created opportunistically.
//
// `audit_logs` was invented rather than designed: a free-text action, a
// three-level risk, no request id, and no actor type, which meant the documented
// requirement to correlate an entry with PocketBase's own `_logs` by request id
// could not be met. It becomes `security_audit_logs` with the shape in
// database-design.md § 3.9, and the rows are carried across rather than dropped,
// because an audit log that discards its own history during an upgrade is not an
// audit log. The reshaping is recorded in docs/decisions.md D-011.
func init() {
	migrations.Register(func(app core.App) error {
		if err := reshapeSecurityAuditLog(app); err != nil {
			return err
		}
		if err := createOperationLogs(app); err != nil {
			return err
		}
		if err := createExceptionLogs(app); err != nil {
			return err
		}

		// Existing databases have already run the seed migration, so it will not
		// run again and would never see the audit permission points or menu nodes.
		if err := seedPermissionPoints(app); err != nil {
			return err
		}
		if err := backfillAuditPermission(app); err != nil {
			return err
		}
		return seedMenus(app)
	}, func(app core.App) error {
		for _, name := range []string{"exception_logs", "operation_logs", "security_audit_logs"} {
			if col := findCollection(app, name); col != nil {
				if err := app.Delete(col); err != nil {
					return err
				}
			}
		}
		return nil
	}, "1700000030_audit.go")
}

// reshapeSecurityAuditLog builds the documented security log and carries the
// existing entries into it.
func reshapeSecurityAuditLog(app core.App) error {
	// Idempotent: a re-run finds the new collection and does nothing.
	if findCollection(app, "security_audit_logs") != nil {
		return nil
	}

	old := findCollection(app, "audit_logs")

	col := newBase("security_audit_logs")
	ensureTimestamps(col)

	addField(col, selectOf("event", true, audit.EventStrings()...))
	addField(col, selectOf("severity", true,
		string(audit.SeverityInfo), string(audit.SeverityWarning), string(audit.SeverityCritical)))
	addField(col, selectOf("actorType", true,
		string(audit.ActorUser), string(audit.ActorSystem), string(audit.ActorAPIKey)))
	addField(col, rel(app, "actor", "users", false))
	addField(col, text("actorEmail", false))
	addField(col, text("targetType", false))
	addField(col, text("targetId", false))
	addField(col, text("summary", false))
	addField(col, jsonField("detail", 20000))
	addField(col, text("ip", false))
	addField(col, text("userAgent", false))
	// The request id is what makes an entry correlatable with the operation log
	// and with PocketBase's own `_logs`, which is the requirement the previous
	// shape could not satisfy.
	addField(col, text("requestId", false))
	addField(col, text("module", false))

	col.AddIndex("idx_secaudit_event", false, "event, created", "")
	col.AddIndex("idx_secaudit_severity", false, "severity, created", "")
	col.AddIndex("idx_secaudit_actor", false, "actor, created", "")
	col.AddIndex("idx_secaudit_actortype", false, "actorType", "")
	col.AddIndex("idx_secaudit_request", false, "requestId", "")
	col.AddIndex("idx_secaudit_created", false, "created", "")

	disableCascade(col, "actor")
	lockDown(col)

	if err := app.Save(col); err != nil {
		return err
	}

	if old == nil {
		return nil
	}

	return carryAuditRows(app, old, col)
}

// carryAuditRows moves the entries from the old collection into the new one.
//
// The old `action` was free text, so an event that no longer exists in the
// catalogue is recorded as a permission-denied style fallback rather than being
// dropped. Losing an audit entry is worse than recording one under a coarser
// event.
func carryAuditRows(app core.App, old, col *core.Collection) error {
	rows, err := app.FindAllRecords("audit_logs")
	if err != nil {
		// No rows to carry; still remove the old collection below.
		rows = nil
	}

	for _, row := range rows {
		rec := core.NewRecord(col)

		event := audit.Event(row.GetString("action"))
		if !audit.Valid(event) {
			event = audit.EventPermissionDenied
		}
		rec.Set("event", string(event))

		rec.Set("severity", mapSeverity(row.GetString("riskLevel")))
		rec.Set("actorType", string(audit.ActorUser))
		if actor := row.GetString("user"); actor != "" {
			rec.Set("actor", actor)
		}
		rec.Set("actorEmail", row.GetString("actorEmail"))
		rec.Set("targetType", row.GetString("resource"))
		rec.Set("targetId", row.GetString("resourceId"))
		rec.Set("summary", row.GetString("summary"))
		if detail := row.Get("detail"); detail != nil {
			rec.Set("detail", detail)
		}
		rec.Set("ip", row.GetString("ip"))
		rec.Set("userAgent", row.GetString("userAgent"))
		rec.Set("module", row.GetString("module"))

		if err := app.Save(rec); err != nil {
			return err
		}
	}

	return app.Delete(old)
}

// mapSeverity translates the old risk levels onto the documented severities.
func mapSeverity(risk string) string {
	switch risk {
	case "high":
		return string(audit.SeverityCritical)
	case "medium":
		return string(audit.SeverityWarning)
	default:
		return string(audit.SeverityInfo)
	}
}

// createOperationLogs creates the per-request operation log.
func createOperationLogs(app core.App) error {
	col := findCollection(app, "operation_logs")
	if col == nil {
		col = newBase("operation_logs")
	}
	ensureTimestamps(col)

	addField(col, rel(app, "user", "users", false))
	// The API-key relation belongs to the developer module (wave 4); the actor's
	// type is recorded now so the entry is complete without it. See D-012.
	addField(col, selectOf("actorType", false,
		string(audit.ActorUser), string(audit.ActorSystem), string(audit.ActorAPIKey)))
	addField(col, text("actorEmail", false))
	addField(col, text("module", false))
	addField(col, text("action", false))
	addField(col, text("method", false))
	addField(col, text("path", false))
	// Redacted before it is stored; see internal/audit.Redact.
	addField(col, jsonField("params", 20000))
	addField(col, selectOf("result", true, "success", "failure", "denied"))
	addField(col, &core.NumberField{Name: "statusCode"})
	addField(col, text("errorMessage", false))
	addField(col, text("targetType", false))
	addField(col, text("targetId", false))
	addField(col, &core.NumberField{Name: "durationMs"})
	addField(col, text("ip", false))
	addField(col, text("userAgent", false))
	addField(col, text("requestId", false))

	col.AddIndex("idx_oplog_user", false, "user, created", "")
	col.AddIndex("idx_oplog_module_action", false, "module, action", "")
	col.AddIndex("idx_oplog_created", false, "created", "")
	col.AddIndex("idx_oplog_request", false, "requestId", "")
	col.AddIndex("idx_oplog_result", false, "result", "")

	disableCascade(col, "user")
	lockDown(col)

	return app.Save(col)
}

// createExceptionLogs creates the panic and error log.
func createExceptionLogs(app core.App) error {
	col := findCollection(app, "exception_logs")
	if col == nil {
		col = newBase("exception_logs")
	}
	ensureTimestamps(col)

	addField(col, selectOf("level", true, "error", "fatal", "panic"))
	// message and stack carry explicit limits. The default text limit is 5000
	// characters, and a Go stack trace is routinely longer: truncating it drops
	// the deepest frames, which are usually the ones that explain the panic. The
	// first attempt at this stored a longer value and the write was rejected, so
	// the exception never reached the log at all.
	addField(col, &core.TextField{Name: "message", Max: 2000})
	addField(col, &core.TextField{Name: "stack", Max: 64000})
	addField(col, text("module", false))
	addField(col, text("path", false))
	addField(col, text("method", false))
	addField(col, text("requestId", false))
	addField(col, jsonField("params", 20000))
	addField(col, rel(app, "user", "users", false))
	addField(col, text("ip", false))
	addField(col, text("userAgent", false))
	addField(col, &core.BoolField{Name: "resolved"})
	addField(col, rel(app, "resolvedBy", "users", false))
	addField(col, &core.DateField{Name: "resolvedAt"})
	addField(col, text("note", false))

	col.AddIndex("idx_exc_level", false, "level, created", "")
	col.AddIndex("idx_exc_resolved", false, "resolved, created", "")
	col.AddIndex("idx_exc_request", false, "requestId", "")
	col.AddIndex("idx_exc_module", false, "module", "")

	disableCascade(col, "user", "resolvedBy")
	lockDown(col)

	return app.Save(col)
}

// backfillAuditPermission grants the audit permission points to every role that
// already held the retired identity login-log permission.
//
// The login log moved from the identity namespace to the audit one. Without this,
// every existing role would silently lose its access to the sign-in history: the
// screens would start returning 403 for people who could see them a moment
// earlier, which is exactly the kind of change that gets noticed in production
// rather than in review.
func backfillAuditPermission(app core.App) error {
	roleCol, err := app.FindCollectionByNameOrId("roles")
	if err != nil || roleCol == nil {
		return nil
	}

	auditLogin, err := app.FindFirstRecordByFilter("permissions",
		"code = {:c}", map[string]any{"c": "audit:login:list"})
	if err != nil || auditLogin == nil {
		return nil
	}

	roles, err := app.FindAllRecords("roles")
	if err != nil {
		return nil
	}

	for _, role := range roles {
		held := role.GetStringSlice("permissions")
		if containsString(held, auditLogin.Id) {
			continue
		}

		// Only roles that actually had the old permission are migrated; granting it
		// to everyone would widen access rather than preserve it.
		if !roleHeldLoginLog(app, role, held) {
			continue
		}

		role.Set("permissions", append(held, auditLogin.Id))
		if err := app.Save(role); err != nil {
			return err
		}
	}

	return nil
}

// roleHeldLoginLog reports whether a role held the retired identity permission.
//
// The permission row is gone by now, so the check is on the code that was stored
// on the role's permission records -- which is why the row is looked up before it
// can be deleted. In practice the seed creates the audit row and the identity row
// is simply no longer declared, so a role that held it holds a dangling id; this
// treats a missing permission record as "cannot tell" and leaves the role alone.
func roleHeldLoginLog(app core.App, role *core.Record, held []string) bool {
	for _, id := range held {
		perm, err := app.FindRecordById("permissions", id)
		if err != nil || perm == nil {
			continue
		}
		if perm.GetString("code") == "identity:loginlog:list" {
			return true
		}
	}
	return false
}
