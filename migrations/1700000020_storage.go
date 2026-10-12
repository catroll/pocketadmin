package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
)

// storageMigration creates the file library.
//
// The separation in database-design.md § 1.2 is the point of this module:
// record-owned files (an avatar, a product image) stay on native PocketBase
// `file` fields on their owning record and are edited as part of that record,
// while library files are rows here whose bytes are written by a storage driver
// and which can be referenced from anywhere through `attachments`. A library
// file never appears as a record-owned field and a record-owned file never
// appears in the library.
//
// `files.provider` is deliberately absent. It points at `integration_providers`,
// which the integrations module creates in a later wave; per D-012 in
// docs/decisions.md the seam is defined now (the `storage` select) and the
// relation is added by the module that owns the target.
func init() {
	migrations.Register(func(app core.App) error {
		if err := createFileFolders(app); err != nil {
			return err
		}
		if err := createFiles(app); err != nil {
			return err
		}
		if err := createAttachments(app); err != nil {
			return err
		}

		// Existing databases have already run the seed migration, so it will not
		// run again and would never see the storage permission points or menu
		// nodes added above. Both helpers are idempotent upserts, so re-running
		// them here backfills a live database and is a no-op on a fresh one.
		if err := seedPermissionPoints(app); err != nil {
			return err
		}
		return seedMenus(app)
	}, func(app core.App) error {
		for _, name := range []string{"attachments", "files", "file_folders"} {
			if col := findCollection(app, name); col != nil {
				if err := app.Delete(col); err != nil {
					return err
				}
			}
		}
		return nil
	}, "1700000020_storage.go")
}

// createFileFolders creates the folder tree.
func createFileFolders(app core.App) error {
	col := findCollection(app, "file_folders")
	if col == nil {
		col = newBase("file_folders")
	}
	ensureTimestamps(col)

	addField(col, text("name", true))
	addField(col, &core.TextField{Name: "description"})
	addField(col, text("path", false))
	addField(col, &core.NumberField{Name: "level"})
	addField(col, rel(app, "owner", "users", false))
	addField(col, selectOf("scope", false, "private", "public", "shared"))
	addField(col, &core.NumberField{Name: "sortOrder"})
	addField(col, selectOf("status", false, "active", "archived"))
	addField(col, &core.DateField{Name: "deletedAt"})

	col.AddIndex("idx_file_folders_path", false, "path", "")
	col.AddIndex("idx_file_folders_owner", false, "owner", "")

	disableCascade(col, "owner")

	if err := app.Save(col); err != nil {
		return err
	}

	// The self-relation is added on a second save because the collection has no
	// id to point at until it exists. The same pattern is used for menus.parent.
	// Its index has to come after the field, or the index is created against a
	// column that does not exist yet.
	addField(col, &core.RelationField{
		Name:         "parent",
		CollectionId: col.Id,
		MaxSelect:    1,
	})
	col.AddIndex("idx_file_folders_parent", false, "parent", "")
	return app.Save(col)
}

// createFiles creates the library file records.
//
// The row holds metadata only; the bytes live wherever the driver put them,
// addressed by `storedName`. `checksum` is what makes deduplication possible and
// `refCount` is what makes deletion safe.
func createFiles(app core.App) error {
	col := findCollection(app, "files")
	if col == nil {
		col = newBase("files")
	}
	ensureTimestamps(col)

	addField(col, text("name", true))
	addField(col, text("storedName", false))
	addField(col, rel(app, "folder", "file_folders", false))
	addField(col, &core.NumberField{Name: "size"})
	// mimeType is sniffed from the bytes, never taken from the client: the
	// extension is whatever the uploader typed, and content served back under a
	// claimed type is how a stored file becomes a stored script.
	addField(col, text("mimeType", false))
	addField(col, text("extension", false))
	addField(col, text("checksum", false))
	addField(col, selectOf("storage", false, "local"))
	addField(col, &core.TextField{Name: "url"})
	addField(col, &core.TextField{Name: "thumbnail"})
	addField(col, rel(app, "owner", "users", false))
	addField(col, &core.NumberField{Name: "refCount"})
	addField(col, selectOf("scope", false, "private", "public", "shared"))
	addField(col, selectOf("status", false, "active", "quarantined", "deleted"))
	addField(col, multiSelectOf("tags", 6, "image", "document", "archive", "video", "audio", "other"))
	addField(col, &core.DateField{Name: "deletedAt"})

	col.AddIndex("idx_files_checksum", false, "checksum", "")
	col.AddIndex("idx_files_folder_name", false, "folder, name", "")
	col.AddIndex("idx_files_owner", false, "owner", "")
	col.AddIndex("idx_files_status", false, "status", "")
	col.AddIndex("idx_files_mime", false, "mimeType", "")

	disableCascade(col, "folder", "owner")

	return app.Save(col)
}

// createAttachments creates the polymorphic links that reference a library file.
//
// This is what reference counting counts. A file with any attachment row cannot
// be deleted, because something else is still pointing at it.
func createAttachments(app core.App) error {
	col := findCollection(app, "attachments")
	if col == nil {
		col = newBase("attachments")
	}
	ensureTimestamps(col)

	addField(col, rel(app, "file", "files", true))
	addField(col, text("ownerType", true))
	addField(col, text("ownerId", true))
	addField(col, text("field", false))
	addField(col, &core.NumberField{Name: "sortOrder"})
	addField(col, rel(app, "createdBy", "users", false))

	col.AddIndex("idx_attachments_owner", false, "ownerType, ownerId", "")
	col.AddIndex("idx_attachments_file", false, "file", "")
	col.AddIndex("idx_attachments_unique", true, "file, ownerType, ownerId, field", "")

	disableCascade(col, "file", "createdBy")

	return app.Save(col)
}
