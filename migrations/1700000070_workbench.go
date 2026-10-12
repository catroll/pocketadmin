package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"

	"pocketadmin/internal/workbench"
)

// workbenchMigration creates the personal workbench: todos, the message inbox and
// its per-recipient delivery records, recently visited pages and quick links.
//
// The four are one module because they are one screen. Splitting them would mean
// four migrations and four permission sets for a page a person uses as a single
// thing.
func init() {
	migrations.Register(func(app core.App) error {
		if err := createTodos(app); err != nil {
			return err
		}
		if err := createMessages(app); err != nil {
			return err
		}
		if err := createMessageRecipients(app); err != nil {
			return err
		}
		if err := createRecentVisits(app); err != nil {
			return err
		}
		if err := createQuickLinks(app); err != nil {
			return err
		}
		if err := seedPermissionPoints(app); err != nil {
			return err
		}
		return seedMenus(app)
	}, func(app core.App) error {
		for _, name := range []string{"quick_links", "recent_visits", "message_recipients", "messages", "todos"} {
			if col := findCollection(app, name); col != nil {
				if err := app.Delete(col); err != nil {
					return err
				}
			}
		}
		return nil
	}, "1700000070_workbench.go")
}

// createTodos creates the personal todo list.
func createTodos(app core.App) error {
	col := findCollection(app, "todos")
	if col == nil {
		col = newBase("todos")
	}
	ensureTimestamps(col)

	addField(col, text("title", true))
	addField(col, &core.EditorField{Name: "description"})
	addField(col, rel(app, "owner", "users", true))
	addField(col, selectOf("status", true, "pending", "in_progress", "done", "cancelled"))
	addField(col, selectOf("priority", false, "low", "normal", "high", "urgent"))
	addField(col, &core.DateField{Name: "dueAt"})
	addField(col, &core.DateField{Name: "remindAt"})
	addField(col, &core.DateField{Name: "completedAt"})
	// A deep link target, kept as a collection name and an id rather than a
	// foreign key: a todo can point at any record in the platform, and a relation
	// field cannot span collections.
	addField(col, text("relatedType", false))
	addField(col, text("relatedId", false))
	// The tag list widens with every TodoTag value the seed declares, so the two
	// cannot drift.
	addField(col, multiSelectOf("tags", len(workbench.TodoTags()), workbench.TodoTags()...))
	addField(col, &core.NumberField{Name: "sortOrder"})

	col.AddIndex("idx_todos_owner_status", false, "owner, status", "")
	col.AddIndex("idx_todos_due", false, "dueAt", "")
	col.AddIndex("idx_todos_remind", false, "remindAt", "")

	disableCascade(col, "owner")
	lockDown(col)

	return app.Save(col)
}

// createMessages creates the messages an administrator may receive.
func createMessages(app core.App) error {
	col := findCollection(app, "messages")
	if col == nil {
		col = newBase("messages")
	}
	ensureTimestamps(col)

	addField(col, text("title", true))
	addField(col, &core.EditorField{Name: "content"})
	addField(col, selectOf("type", true, "system", "notice", "todo", "announcement"))
	addField(col, selectOf("level", true, "info", "success", "warning", "error"))
	// An empty sender means the platform sent it. Keeping the field optional is
	// what distinguishes "the system told you" from "a colleague told you".
	addField(col, rel(app, "sender", "users", false))
	addField(col, text("link", false))
	addField(col, selectOf("audience", true, "direct", "role", "department", "all"))
	// The audience targets, used to expand the recipient list when the message is
	// sent. They are kept so a resend or an audit can see who it was meant for.
	addField(col, multiRel(app, "roles", "roles", 50))
	addField(col, multiRel(app, "departments", "departments", 50))

	col.AddIndex("idx_messages_created", false, "created", "")
	col.AddIndex("idx_messages_type", false, "type", "")

	disableCascade(col, "sender", "roles", "departments")
	lockDown(col)

	return app.Save(col)
}

// createMessageRecipients creates one delivery record per recipient.
//
// Delivery is materialised rather than computed. A broadcast that stays a query
// cannot record that somebody read it, cannot be marked deleted by one recipient
// without affecting the others, and changes meaning when the audience changes --
// a new member of a department would retroactively receive last year's notices.
func createMessageRecipients(app core.App) error {
	col := findCollection(app, "message_recipients")
	if col == nil {
		col = newBase("message_recipients")
	}
	ensureTimestamps(col)

	addField(col, rel(app, "message", "messages", true))
	addField(col, rel(app, "user", "users", true))
	addField(col, &core.DateField{Name: "readAt"})
	// Deleted per recipient: one person clearing their inbox says nothing about
	// anybody else's.
	addField(col, &core.DateField{Name: "deletedAt"})

	col.AddIndex("idx_recipients_unique", true, "message, user", "")
	col.AddIndex("idx_recipients_user_read", false, "user, readAt", "")
	col.AddIndex("idx_recipients_user_deleted", false, "user, deletedAt", "")

	// Cascading is deliberate here and the opposite of the announcement's own
	// relations: a delivery record is about one message and one account, and
	// neither outliving the other leaves it meaning anything.
	col.AddIndex("idx_recipients_message", false, "message", "")

	lockDown(col)

	return app.Save(col)
}

// createRecentVisits creates the recently visited pages.
func createRecentVisits(app core.App) error {
	col := findCollection(app, "recent_visits")
	if col == nil {
		col = newBase("recent_visits")
	}
	ensureTimestamps(col)

	addField(col, rel(app, "user", "users", true))
	addField(col, text("title", false))
	addField(col, text("path", true))
	addField(col, text("icon", false))
	addField(col, &core.NumberField{Name: "visitCount"})
	addField(col, &core.DateField{Name: "visitedAt"})

	// One row per person per page, so a page visited twenty times is one entry
	// with a count rather than twenty entries.
	col.AddIndex("idx_visits_unique", true, "user, path", "")
	col.AddIndex("idx_visits_user_time", false, "user, visitedAt", "")

	lockDown(col)

	return app.Save(col)
}

// createQuickLinks creates the quick-link tiles.
func createQuickLinks(app core.App) error {
	col := findCollection(app, "quick_links")
	if col == nil {
		col = newBase("quick_links")
	}
	ensureTimestamps(col)

	// An empty owner means the row is a default shown to an administrator who has
	// not customised their own links. That is why the relation is optional.
	addField(col, rel(app, "user", "users", false))
	addField(col, text("title", true))
	addField(col, text("path", true))
	addField(col, text("icon", false))
	addField(col, text("color", false))
	// A link whose target the reader may not open is hidden rather than shown and
	// then refused, which is the difference between a shortcut and a trap.
	addField(col, text("permissionCode", false))
	addField(col, &core.NumberField{Name: "sortOrder"})

	col.AddIndex("idx_quicklinks_user_sort", false, "user, sortOrder", "")
	col.AddIndex("idx_quicklinks_path", false, "path", "")

	disableCascade(col, "user")
	lockDown(col)

	return app.Save(col)
}
