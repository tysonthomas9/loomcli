package stacklineage

import "sort"

// ByTask indexes nodes by their TaskID.
func ByTask(nodes []Node) map[string]Node {
	m := make(map[string]Node, len(nodes))
	for _, n := range nodes {
		m[n.TaskID] = n
	}
	return m
}

// Ordered returns the stack's nodes as a forest of linear chains: each root's
// chain walked root→leaf, with roots in deterministic (task-ID) order. It is the
// single guard the reconciler and the `set-base`/`add` mutations call before
// acting, so every cycle / missing-predecessor / branching case fails closed here.
//
// The stack may contain multiple independent chains rooted at the base (parallel
// sub-stacks), but a unit may have at most one successor, so every chain stays
// linear and every PR's base is unambiguous.
//
// Validations, in order: self-reference, missing base task, at least one root,
// no unit with multiple successors (chains stay linear), full reachability (cycle).
func Ordered(nodes []Node) ([]Node, error) {
	if len(nodes) == 0 {
		return nil, nil
	}
	byTask := make(map[string]Node, len(nodes))
	children := make(map[string][]string, len(nodes))
	var roots []string
	for _, n := range nodes {
		if n.BaseTaskID == n.TaskID {
			return nil, ErrCycle // self-parent
		}
		byTask[n.TaskID] = n
	}
	for _, n := range nodes {
		if n.BaseTaskID == "" {
			roots = append(roots, n.TaskID)
			continue
		}
		if _, ok := byTask[n.BaseTaskID]; !ok {
			return nil, ErrMissingPredecessor
		}
		children[n.BaseTaskID] = append(children[n.BaseTaskID], n.TaskID)
	}
	if len(roots) == 0 {
		return nil, ErrNoRoot // every node has a parent → a cycle exists
	}
	for _, kids := range children {
		if len(kids) > 1 {
			return nil, ErrBranching // a unit may have at most one successor
		}
	}
	sort.Strings(roots)
	ordered := make([]Node, 0, len(nodes))
	for _, root := range roots {
		for cur := root; ; {
			ordered = append(ordered, byTask[cur])
			next := children[cur]
			if len(next) == 0 {
				break
			}
			cur = next[0]
		}
	}
	if len(ordered) != len(nodes) {
		return nil, ErrCycle // unreachable nodes → a cycle among them
	}
	return ordered, nil
}

// BaseBranch returns the git base ref for a node: the predecessor's assigned
// OutputBranch, or the stack's RootBase for the root unit. It fails closed
// (never falling back to RootBase) when a non-root node names a predecessor that
// is absent or has no assigned branch — matching the proposal's invariant that a
// dependent unit must build on its predecessor's output, not the default branch.
func BaseBranch(stack Stack, node Node, byTask map[string]Node) (string, error) {
	if node.BaseTaskID == "" {
		return stack.RootBase, nil
	}
	base, ok := byTask[node.BaseTaskID]
	if !ok {
		return "", ErrMissingPredecessor
	}
	if base.OutputBranch == "" {
		return "", ErrNoOutputBranch
	}
	return base.OutputBranch, nil
}

// NextToMergeUnits returns the set of task IDs that are next to land — the
// bottom-most not-yet-merged unit of each chain. A unit qualifies when it is not
// merged/closed and is either a root or sits directly on a merged predecessor.
// Returns one entry per parallel chain (a forest can have several next units).
func NextToMergeUnits(ordered []Node) map[string]bool {
	byTask := ByTask(ordered)
	out := map[string]bool{}
	for _, n := range ordered {
		if n.State == NodeStateMerged || n.State == NodeStateClosed {
			continue
		}
		if n.BaseTaskID == "" || byTask[n.BaseTaskID].State == NodeStateMerged {
			out[n.TaskID] = true
		}
	}
	return out
}

// WouldCycle reports whether setting node.BaseTaskID = newBase would introduce a
// cycle or non-linear lineage, given the current nodes. Used by `set-base`/`move`
// to reject bad mutations at write time.
func WouldCycle(nodes []Node, taskID, newBase string) error {
	updated := make([]Node, len(nodes))
	copy(updated, nodes)
	found := false
	for i := range updated {
		if updated[i].TaskID == taskID {
			updated[i].BaseTaskID = newBase
			found = true
			break
		}
	}
	if !found {
		updated = append(updated, Node{TaskID: taskID, BaseTaskID: newBase})
	}
	_, err := Ordered(updated)
	return err
}
