package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
)

// auditMigration creates the security audit log.
//
// The capability map assigns this collection to the audit module, but the events
// that must land in it are produced here: impersonation started, password
// changed, account locked, profile attached or detached, session revoked. Those
// are identity requirements, so identity creates the collection and the audit
// module later builds the browsing, filtering and export on top -- the same
// arrangement already used for login_logs.
//
// The log is append-only through the application: no code path updates or
// deletes a row, and retention will be the only deletion path once the audit
// module owns it.
func init() {
	migrations.Register(func(app core.App) error {
		col := findCollection(app, "audit_logs")
		if col == nil {
			col = newBase("audit_logs")
		}
		ensureTimestamps(col)

		// The actor is optional: a failed sign-in for an address that matches no
		// account still has an audit entry, with only the identifier recorded.
		addField(col, rel(app, "user", "users", false))
		addField(col, &core.TextField{Name: "actorEmail"})
		addField(col, &core.TextField{Name: "identifier"})
		addField(col, selectOf("context", false, "admin", "product", "api", "system"))
		addField(col, &core.TextField{Name: "module"})
		addField(col, &core.TextField{Name: "action"})
		addField(col, &core.TextField{Name: "resource"})
		addField(col, &core.TextField{Name: "resourceId"})
		addField(col, selectOf("result", true, "success", "failure", "denied"))
		addField(col, selectOf("riskLevel", false, "low", "medium", "high"))
		addField(col, &core.TextField{Name: "ip"})
		addField(col, &core.TextField{Name: "userAgent"})
		addField(col, &core.TextField{Name: "summary"})
		addField(col, jsonField("detail", 20000))

		col.AddIndex("idx_audit_created", false, "created", "")
		col.AddIndex("idx_audit_actor", false, "user, created", "")
		col.AddIndex("idx_audit_module_action", false, "module, action", "")
		col.AddIndex("idx_audit_result", false, "result", "")

		disableCascade(col, "user")
		lockDown(col)

		return app.Save(col)
	}, func(app core.App) error {
		if col := findCollection(app, "audit_logs"); col != nil {
			return app.Delete(col)
		}
		return nil
	}, "1700000006_audit.go")
}
