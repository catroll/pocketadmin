package server

import "testing"

// The tree ordering algorithm is pure: it reads ids and parent ids and returns
// an order, so it is tested without a database.

func nodeIDs(nodes []treeNode, order []int) []string {
	out := make([]string, 0, len(order))
	for _, i := range order {
		out = append(out, nodes[i].ID)
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestOrderTreeNodesPutsParentsFirst covers the reason a tree view needs its own
// ordering: a flat sort interleaves parents and children, so the indent would
// draw depths that do not nest.
func TestOrderTreeNodesPutsParentsFirst(t *testing.T) {
	// Interleaved, as a sortOrder sort produces.
	nodes := []treeNode{
		{ID: "dashboard"},
		{ID: "accounts", Parent: "identity"},
		{ID: "departments", Parent: "organization"},
		{ID: "roles", Parent: "rbac"},
		{ID: "identity"},
		{ID: "positions", Parent: "organization"},
		{ID: "organization"},
		{ID: "rbac"},
	}

	got := nodeIDs(nodes, orderTreeNodes(nodes))
	want := []string{
		"dashboard",
		"identity", "accounts",
		"organization", "departments", "positions",
		"rbac", "roles",
	}

	if !equal(got, want) {
		t.Errorf("tree order mismatch\n got: %v\nwant: %v", got, want)
	}
}

// TestOrderTreeNodesKeepsEveryNode: a parent can be outside the current page or
// filtered out, and a child must still be listed rather than dropped.
func TestOrderTreeNodesKeepsEveryNode(t *testing.T) {
	nodes := []treeNode{
		{ID: "orphan", Parent: "missing-parent"},
		{ID: "root"},
		{ID: "child", Parent: "root"},
	}

	order := orderTreeNodes(nodes)
	if len(order) != len(nodes) {
		t.Fatalf("every node must survive ordering, got %v", nodeIDs(nodes, order))
	}

	got := nodeIDs(nodes, order)
	if got[len(got)-1] != "child" {
		t.Errorf("a child should follow its parent, got %v", got)
	}
}

// TestOrderTreeNodesSurvivesACycle: a parent cycle must not hang or lose rows.
func TestOrderTreeNodesSurvivesACycle(t *testing.T) {
	nodes := []treeNode{
		{ID: "a", Parent: "b"},
		{ID: "b", Parent: "a"},
	}

	order := orderTreeNodes(nodes)
	if len(order) != 2 {
		t.Errorf("a cycle must not drop nodes, got %v", nodeIDs(nodes, order))
	}
}

// TestOrderTreeNodesDeepChainIsBounded: a chain longer than the depth cap must
// still return every node exactly once.
func TestOrderTreeNodesDeepChainIsBounded(t *testing.T) {
	var nodes []treeNode
	for i := 0; i < maxTreeDepth*3; i++ {
		parent := ""
		if i > 0 {
			parent = string(rune('a' + i - 1))
		}
		nodes = append(nodes, treeNode{ID: string(rune('a' + i)), Parent: parent})
	}

	order := orderTreeNodes(nodes)
	if len(order) != len(nodes) {
		t.Errorf("expected %d nodes back, got %d", len(nodes), len(order))
	}
}
