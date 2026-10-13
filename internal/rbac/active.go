package rbac

// ActivePath returns the names of the nodes on the path from a root to the named node,
// including the node itself.
//
// It is what the sidebar needs to open the branch an operator is standing in. The test
// it replaces was a string-prefix comparison against the node's name, which only works
// when a child's name happens to begin with its parent's -- `identity.accounts` under
// `identity` did, and `catalog.products` under the top-level `business` does not. So a
// page three levels down expanded its immediate parent and left the group above it shut.
//
// The result is a set rather than a list because the only question asked of it is
// membership, once per rendered node.
func ActivePath(nodes []MenuNode, name string) map[string]bool {
	if name == "" {
		return nil
	}

	path := make(map[string]bool, 4)
	if walkActive(nodes, name, path) {
		return path
	}
	return nil
}

// walkActive fills path with the branch leading to name, reporting whether it found it.
//
// The walk is depth-first and records a node before descending, then **undoes** the
// record when the branch turns out not to contain the target. Without the undo, every
// node visited on the way to a dead end would be left in the set and the sidebar would
// open branches the operator is not in.
func walkActive(nodes []MenuNode, name string, path map[string]bool) bool {
	for _, node := range nodes {
		path[node.Name] = true

		if node.Name == name {
			return true
		}
		if walkActive(node.Children, name, path) {
			return true
		}

		delete(path, node.Name)
	}
	return false
}

// ActiveAncestors returns the set for a node, or an empty set when there is none.
//
// A page whose active name matches no node -- an error page, or a menu an operator has
// hidden -- gets an empty set rather than a nil one, so the template can index into it
// without a guard.
func ActiveAncestors(nodes []MenuNode, name string) map[string]bool {
	if path := ActivePath(nodes, name); path != nil {
		return path
	}
	return map[string]bool{}
}
