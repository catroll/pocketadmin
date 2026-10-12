// Package tree maintains a materialised-path hierarchy.
//
// A department tree and a folder tree are the same structure with different
// nouns, and the rules are not obvious: the path embeds the record's own id,
// which PocketBase only assigns during the save, so it cannot be computed before
// one; moving a node has to rewrite every descendant inside a transaction or the
// prefix predicate that row-level scoping depends on is left half-applied; and a
// node must not be moved beneath one of its own descendants.
//
// That logic lives here once rather than once per hierarchy, because the second
// copy is where the cycle check gets forgotten.
package tree

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

// maxDepth bounds a walk so a pre-existing cycle cannot hang a save.
const maxDepth = 32

// Options describes one hierarchy.
type Options struct {
	// Collection is the collection that forms the tree.
	Collection string

	// ParentField is the self-relation naming a node's parent. Defaults to
	// "parent".
	ParentField string

	// PathField is the column holding the materialised path. Defaults to "path".
	PathField string

	// LevelField is the column holding the depth, one-based. Defaults to "level".
	LevelField string

	// Label names the hierarchy in error messages, for example "department" or
	// "folder".
	Label string
}

func (o Options) parent() string {
	if o.ParentField != "" {
		return o.ParentField
	}
	return "parent"
}

func (o Options) path() string {
	if o.PathField != "" {
		return o.PathField
	}
	return "path"
}

func (o Options) level() string {
	if o.LevelField != "" {
		return o.LevelField
	}
	return "level"
}

func (o Options) label() string {
	if o.Label != "" {
		return o.Label
	}
	return "record"
}

// Register binds a hierarchy's invariants to the application.
//
// Call it once per hierarchy, after Bootstrap and before serving traffic. Every
// write path is covered -- the panel, PocketBase's REST API, a future importer --
// which is the point: this was originally a handler concern, and a record created
// through the API ended up with an empty path that silently disabled the data
// scope that reads it.
func Register(app core.App, opts Options) {
	app.OnRecordCreate(opts.Collection).BindFunc(func(e *core.RecordEvent) error {
		if err := validateParent(e.App, e.Record, opts, true); err != nil {
			return err
		}
		if err := e.Next(); err != nil {
			return err
		}
		// The path embeds the record's own id, which exists only now.
		return reindex(e.App, e.Record, opts)
	})

	app.OnRecordUpdate(opts.Collection).BindFunc(func(e *core.RecordEvent) error {
		if err := validateParent(e.App, e.Record, opts, false); err != nil {
			return err
		}
		if err := e.Next(); err != nil {
			return err
		}
		return reindex(e.App, e.Record, opts)
	})
}

// validateParent checks the chosen parent and derives the node's depth.
func validateParent(app core.App, rec *core.Record, opts Options, isNew bool) error {
	parentID := rec.GetString(opts.parent())

	if parentID == "" {
		rec.Set(opts.level(), 1)
		return nil
	}

	if rec.Id != "" && parentID == rec.Id {
		return fmt.Errorf("a %s cannot be its own parent", opts.label())
	}

	parent, err := app.FindRecordById(opts.Collection, parentID)
	if err != nil || parent == nil {
		return fmt.Errorf("the selected parent %s no longer exists", opts.label())
	}

	// Moving a node beneath one of its own descendants would detach that subtree
	// from the tree entirely.
	if !isNew && rec.Id != "" && strings.Contains(parent.GetString(opts.path()), "/"+rec.Id+"/") {
		return fmt.Errorf("a %s cannot be moved under one of its own descendants", opts.label())
	}

	rec.Set(opts.level(), parent.GetInt(opts.level())+1)
	return nil
}

// reindex writes the node's own path and rewrites every descendant's, inside one
// transaction.
//
// A move changes the path of the whole subtree. A half-applied move corrupts the
// prefix predicate that row-level filtering reads, so the writes share a
// transaction or none of them happen.
func reindex(app core.App, rec *core.Record, opts Options) error {
	if rec == nil || rec.Id == "" {
		return nil
	}

	oldPath := rec.GetString(opts.path())

	desired := "/" + rec.Id + "/"
	if parentID := rec.GetString(opts.parent()); parentID != "" {
		parent, err := app.FindRecordById(opts.Collection, parentID)
		if err != nil || parent == nil {
			return fmt.Errorf("the selected parent %s no longer exists", opts.label())
		}
		desired = parent.GetString(opts.path()) + rec.Id + "/"
	}

	if oldPath == desired {
		return nil
	}

	return app.RunInTransaction(func(txApp core.App) error {
		var descendants []*core.Record
		if oldPath != "" {
			found, err := txApp.FindRecordsByFilter(opts.Collection,
				opts.path()+" ~ {:old} && id != {:id}", "", 0, 0,
				map[string]any{"old": oldPath, "id": rec.Id})
			if err != nil {
				return err
			}
			descendants = found
		}

		rec.Set(opts.path(), desired)
		if err := txApp.Save(rec); err != nil {
			return err
		}

		for _, d := range descendants {
			d.Set(opts.path(), strings.Replace(d.GetString(opts.path()), oldPath, desired, 1))
			if err := txApp.Save(d); err != nil {
				return err
			}
		}

		slog.Debug("reindexed a tree subtree",
			"collection", opts.Collection, "node", rec.Id,
			"path", desired, "descendants", len(descendants))

		return nil
	})
}

// Depth returns how far a node sits below the root of its hierarchy, for a view
// that indents rows.
//
// A stored level is trusted when present because the invariants above maintain
// it; otherwise the depth is walked up the parent chain, which is what a
// hierarchy without a materialised path needs.
func Depth(app core.App, col *core.Collection, rec *core.Record, parentField, levelField string) int {
	if levelField == "" {
		levelField = "level"
	}
	if parentField == "" {
		parentField = "parent"
	}

	if col.Fields.GetByName(levelField) != nil {
		if level := rec.GetInt(levelField); level > 0 {
			return level - 1
		}
	}

	if app == nil {
		return 0
	}

	depth := 0
	current := rec
	for i := 0; i < maxDepth; i++ {
		parentID := current.GetString(parentField)
		if parentID == "" {
			break
		}
		depth++
		next, err := app.FindRecordById(col, parentID)
		if err != nil || next == nil {
			break
		}
		current = next
	}
	return depth
}
