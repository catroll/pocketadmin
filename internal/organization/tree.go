// Package organization holds the invariants of the organisation model.
//
// The department tree stores a materialised path: every department carries the
// ids of its ancestors, so "this department and everything beneath it" is a
// single indexed prefix match. That path is what the row-level data scope
// depends on, which makes keeping it correct a property of the data rather than
// of one screen.
//
// It is therefore maintained by model hooks rather than by the web layer: a
// department created through the panel, through the REST API or by a future
// importer all get a correct path, and a move rewrites the whole subtree in one
// transaction. Keeping this in a handler would mean any other write path could
// silently corrupt the tree.
package organization

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

// CollectionDepartments is the tree's collection name.
const CollectionDepartments = "departments"

// Register binds the department tree's invariants to the application.
//
// Call it once, after Bootstrap and before serving traffic.
func Register(app core.App) {
	app.OnRecordCreate(CollectionDepartments).BindFunc(func(e *core.RecordEvent) error {
		if err := validateParent(e.App, e.Record, true); err != nil {
			return err
		}
		if err := e.Next(); err != nil {
			return err
		}
		// The path embeds the record's own id, which PocketBase assigns during
		// the save, so it can only be written once the record exists.
		return reindex(e.App, e.Record)
	})

	app.OnRecordUpdate(CollectionDepartments).BindFunc(func(e *core.RecordEvent) error {
		if err := validateParent(e.App, e.Record, false); err != nil {
			return err
		}
		if err := e.Next(); err != nil {
			return err
		}
		return reindex(e.App, e.Record)
	})
}

// validateParent checks the chosen parent and derives the department's depth.
//
// The path is not computed here because it embeds the record's own id, which is
// only known after the save; see reindex.
func validateParent(app core.App, rec *core.Record, isNew bool) error {
	parentID := rec.GetString("parent")

	if parentID == "" {
		rec.Set("level", 1)
		return nil
	}

	if rec.Id != "" && parentID == rec.Id {
		return fmt.Errorf("a department cannot be its own parent")
	}

	parent, err := app.FindRecordById(CollectionDepartments, parentID)
	if err != nil || parent == nil {
		return fmt.Errorf("the selected parent department no longer exists")
	}

	// Moving a department under one of its own descendants would detach that
	// subtree from the tree entirely.
	if !isNew && rec.Id != "" && strings.Contains(parent.GetString("path"), "/"+rec.Id+"/") {
		return fmt.Errorf("a department cannot be moved under one of its own sub-departments")
	}

	rec.Set("level", parent.GetInt("level")+1)
	return nil
}

// reindex writes the department's own path and rewrites every descendant's,
// inside one transaction.
//
// A move changes the path of the whole subtree, and a half-applied move would
// corrupt the prefix predicate the data scope filters on.
func reindex(app core.App, rec *core.Record) error {
	if rec == nil || rec.Id == "" {
		return nil
	}

	oldPath := rec.GetString("path")

	desired := "/" + rec.Id + "/"
	if parentID := rec.GetString("parent"); parentID != "" {
		parent, err := app.FindRecordById(CollectionDepartments, parentID)
		if err != nil || parent == nil {
			return fmt.Errorf("the selected parent department no longer exists")
		}
		desired = parent.GetString("path") + rec.Id + "/"
	}

	if oldPath == desired {
		return nil
	}

	return app.RunInTransaction(func(txApp core.App) error {
		var descendants []*core.Record
		if oldPath != "" {
			found, err := txApp.FindRecordsByFilter(CollectionDepartments,
				"path ~ {:old} && id != {:id}", "", 0, 0,
				map[string]any{"old": oldPath, "id": rec.Id})
			if err != nil {
				return err
			}
			descendants = found
		}

		rec.Set("path", desired)
		if err := txApp.Save(rec); err != nil {
			return err
		}

		for _, d := range descendants {
			d.Set("path", strings.Replace(d.GetString("path"), oldPath, desired, 1))
			if err := txApp.Save(d); err != nil {
				return err
			}
		}

		slog.Debug("reindexed the department subtree",
			"department", rec.Id, "path", desired, "descendants", len(descendants))

		return nil
	})
}
