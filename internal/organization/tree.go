// Package organization holds the organisation model's invariants.
//
// The department tree is the shared tree implementation with the organisation's
// nouns attached; see internal/tree for what it does and why.
package organization

import (
	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/tree"
)

// Collection names owned by the organisation model.
const (
	CollectionDepartments = "departments"
	CollectionEmployees   = "employees"
)

// Options is the department hierarchy's configuration.
var Options = tree.Options{
	Collection: CollectionDepartments,
	Label:      "department",
}

// Register binds the organisation model's invariants to the application.
//
// Call it once, after Bootstrap and before serving traffic.
func Register(app core.App) {
	// The department tree: a materialised path, re-parented as one transaction,
	// refusing a move beneath the node's own descendants.
	tree.Register(app, Options)

	// The reporting line is a hierarchy too, but it carries no path: a cycle in
	// it would make every traversal of "who reports to whom" spin or stop at the
	// depth cap, so the same check applies without the reindexing.
	app.OnRecordCreate(CollectionEmployees).BindFunc(guardManagerCycle)
	app.OnRecordUpdate(CollectionEmployees).BindFunc(guardManagerCycle)
}

// maxReportingDepth bounds the walk so a pre-existing loop cannot hang a save.
const maxReportingDepth = 32

// guardManagerCycle refuses a line-manager assignment that would create a loop.
func guardManagerCycle(e *core.RecordEvent) error {
	managerID := e.Record.GetString("manager")
	if managerID == "" || e.Record.Id == "" {
		return e.Next()
	}

	if managerID == e.Record.Id {
		return errSelfManager
	}

	current := managerID
	for depth := 0; depth < maxReportingDepth; depth++ {
		if current == e.Record.Id {
			return errManagerCycle
		}

		manager, err := e.App.FindRecordById(CollectionEmployees, current)
		if err != nil || manager == nil {
			break
		}
		current = manager.GetString("manager")
		if current == "" {
			break
		}
	}

	return e.Next()
}
