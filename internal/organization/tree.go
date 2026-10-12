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

// Collection names owned by the organisation model.
const (
	CollectionDepartments = "departments"
	CollectionEmployees   = "employees"
)

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

	app.OnRecordCreate(CollectionEmployees).BindFunc(guardManagerCycle)
	app.OnRecordUpdate(CollectionEmployees).BindFunc(guardManagerCycle)
}

// guardManagerCycle refuses a line-manager assignment that would create a loop.
//
// A cycle in the reporting line is not merely untidy: every traversal of it --
// an organisation chart, a approval chain, a "who reports to whom" query -- would
// either loop forever or silently stop at the depth cap. The check walks up from
// the proposed manager and refuses if it reaches the employee being saved.
func guardManagerCycle(e *core.RecordEvent) error {
	managerID := e.Record.GetString("manager")
	if managerID == "" || e.Record.Id == "" {
		return e.Next()
	}

	if managerID == e.Record.Id {
		return fmt.Errorf("an employee cannot be their own line manager")
	}

	current := managerID
	for depth := 0; depth < maxReportingDepth; depth++ {
		if current == e.Record.Id {
			return fmt.Errorf("that manager already reports to this employee, directly or through someone else")
		}

		manager, err := e.App.FindRecordById(CollectionEmployees, current)
		if err != nil || manager == nil {
			// The chain ends at someone outside the set, or at a record that no
			// longer exists; either way there is nothing further to loop through.
			break
		}
		current = manager.GetString("manager")
		if current == "" {
			break
		}
	}

	return e.Next()
}

// maxReportingDepth bounds the walk so a pre-existing loop cannot hang a save.
const maxReportingDepth = 32

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
