package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
)

// announcementMigration creates rich-text announcements and their per-reader read
// state.
//
// Announcements are soft-deleted rather than removed. A reader's read state is
// evidence that they were told something -- which is the point of a read count on
// a maintenance notice -- and deleting the announcement would take that with it.
func init() {
	migrations.Register(func(app core.App) error {
		if err := createAnnouncements(app); err != nil {
			return err
		}
		if err := createAnnouncementReads(app); err != nil {
			return err
		}
		if err := seedPermissionPoints(app); err != nil {
			return err
		}
		return seedMenus(app)
	}, func(app core.App) error {
		for _, name := range []string{"announcement_reads", "announcements"} {
			if col := findCollection(app, name); col != nil {
				if err := app.Delete(col); err != nil {
					return err
				}
			}
		}
		return nil
	}, "1700000060_announcement.go")
}

// createAnnouncements creates the announcements.
func createAnnouncements(app core.App) error {
	col := findCollection(app, "announcements")
	if col == nil {
		col = newBase("announcements")
	}
	ensureTimestamps(col)

	addField(col, text("title", true))
	// The editor's output, kept as HTML. It is sanitised on the way in rather than
	// on the way out: a reader's browser is not the place to discover that the
	// stored markup was hostile.
	addField(col, &core.EditorField{Name: "content"})
	addField(col, selectOf("type", true, "notice", "announcement", "maintenance", "update"))
	addField(col, selectOf("level", true, "normal", "important", "urgent"))
	addField(col, selectOf("status", true, "draft", "scheduled", "published", "archived", "expired"))
	addField(col, &core.DateField{Name: "publishAt"})
	addField(col, &core.DateField{Name: "expireAt"})
	addField(col, rel(app, "author", "users", false))

	addField(col, selectOf("audience", true, "all", "admins", "users", "departments"))
	// Only meaningful when the audience is a set of departments, which is why it
	// is optional rather than required.
	addField(col, multiRel(app, "departments", "departments", 50))

	addField(col, &core.BoolField{Name: "pinned"})
	// A denormalised counter, kept in step with announcement_reads by the read
	// path. It exists because the list shows a count for every row, and counting
	// the relation per row would be a query per announcement.
	addField(col, &core.NumberField{Name: "readCount"})

	// Soft delete: the read state is evidence and must outlive the announcement.
	addField(col, &core.DateField{Name: "deletedAt"})
	addField(col, rel(app, "deletedBy", "users", false))

	col.AddIndex("idx_ann_status", false, "status, publishAt", "")
	col.AddIndex("idx_ann_expire", false, "expireAt", "")
	col.AddIndex("idx_ann_pinned", false, "pinned, publishAt", "")
	col.AddIndex("idx_ann_audience", false, "audience", "")
	col.AddIndex("idx_ann_deleted", false, "deletedAt", "")

	disableCascade(col, "author", "departments", "deletedBy")
	lockDown(col)

	return app.Save(col)
}

// createAnnouncementReads creates the per-reader read state.
func createAnnouncementReads(app core.App) error {
	col := findCollection(app, "announcement_reads")
	if col == nil {
		col = newBase("announcement_reads")
	}
	ensureTimestamps(col)

	addField(col, rel(app, "announcement", "announcements", true))
	addField(col, rel(app, "user", "users", true))
	addField(col, &core.DateField{Name: "readAt"})

	// A reader has one read state per announcement. Without the unique index a
	// second read would add a row and inflate the count, which is the number the
	// screen exists to show.
	col.AddIndex("idx_annread_unique", true, "announcement, user", "")
	col.AddIndex("idx_annread_user", false, "user", "")

	// Cascading here is deliberate and the opposite of the announcement's own
	// relations: a read record is about one announcement and one account, and
	// neither outliving the other leaves it meaning anything. The announcement
	// itself is soft-deleted, so this fires only on a genuine purge.
	col.AddIndex("idx_annread_announcement", false, "announcement", "")

	lockDown(col)

	return app.Save(col)
}
