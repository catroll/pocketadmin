package rbac_test

import (
	"testing"

	"pocketadmin/internal/rbac"
)

// tree is the shape the sidebar has: two levels of grouping above the pages, which is
// what the prefix test could not express.
func tree() []rbac.MenuNode {
	return []rbac.MenuNode{
		{Name: "dashboard"},
		{Name: "business", Children: []rbac.MenuNode{
			{Name: "catalog", Children: []rbac.MenuNode{
				{Name: "catalog.products"},
				{Name: "catalog.categories"},
			}},
			{Name: "orders"},
			{Name: "sales", Children: []rbac.MenuNode{
				{Name: "payments.transactions"},
				{Name: "refunds"},
			}},
		}},
		{Name: "system", Children: []rbac.MenuNode{
			{Name: "audit", Children: []rbac.MenuNode{
				{Name: "audit.operations"},
			}},
		}},
	}
}

// TestThePathReachesThroughEveryLevel is a regression test for a real defect.
//
// The sidebar decided whether to open a branch by asking whether the active node's name
// began with the branch's. That works when a child is named after its parent --
// `identity.accounts` under `identity` -- and fails everywhere else, which after the
// navigation was grouped meant the top-level groups never opened at all and a page three
// levels down unfolded only its immediate parent.
func TestThePathReachesThroughEveryLevel(t *testing.T) {
	cases := []struct {
		active string
		want   []string
	}{
		// A page directly inside a group, which is the case that showed nothing at all.
		{"orders", []string{"business", "orders"}},

		// A page with two groups above it.
		{"catalog.products", []string{"business", "catalog", "catalog.products"}},
		{"audit.operations", []string{"system", "audit", "audit.operations"}},

		// A second branch, to show the walk does not leak across siblings.
		{"refunds", []string{"business", "sales", "refunds"}},

		// A top-level page is its own path.
		{"dashboard", []string{"dashboard"}},
	}

	for _, tc := range cases {
		path := rbac.ActivePath(tree(), tc.active)

		for _, want := range tc.want {
			if !path[want] {
				t.Errorf("%s: %q is not on the path", tc.active, want)
			}
		}
		if len(path) != len(tc.want) {
			t.Errorf("%s: the path has %d entries, want %d: %v",
				tc.active, len(path), len(tc.want), keys(path))
		}
	}
}

// TestTheWalkUndoesABranchItBacktracksOutOf.
//
// A depth-first walk that records as it descends and never undoes leaves every node it
// passed through in the set, so the sidebar opens branches the operator is not in.
func TestTheWalkUndoesABranchItBacktracksOutOf(t *testing.T) {
	path := rbac.ActivePath(tree(), "refunds")

	if path["catalog"] || path["catalog.products"] || path["orders"] {
		t.Errorf("a sibling branch was left open: %v", keys(path))
	}
	if path["system"] || path["audit"] {
		t.Errorf("an unrelated group was left open: %v", keys(path))
	}
}

// TestAnUnknownActiveNodeOpensNothing: an error page, or a menu an operator has hidden,
// must not leave the sidebar in an arbitrary state.
func TestAnUnknownActiveNodeOpensNothing(t *testing.T) {
	if path := rbac.ActivePath(tree(), "does.not.exist"); path != nil {
		t.Errorf("an unknown name produced a path: %v", keys(path))
	}

	// The template indexes into the result without a guard, so it gets an empty set
	// rather than a nil map.
	if path := rbac.ActiveAncestors(tree(), "does.not.exist"); path == nil {
		t.Error("the guarded accessor returned nil")
	} else if len(path) != 0 {
		t.Errorf("the guarded accessor returned %v", keys(path))
	}

	if path := rbac.ActiveAncestors(tree(), ""); len(path) != 0 {
		t.Errorf("an empty name produced %v", keys(path))
	}
}

// keys lists a set for a failure message.
func keys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
}
