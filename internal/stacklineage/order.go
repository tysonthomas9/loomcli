package stacklineage

// ByTask indexes nodes by their TaskID.
func ByTask(nodes []Node) map[string]Node {
	m := make(map[string]Node, len(nodes))
	for _, n := range nodes {
		m[n.TaskID] = n
	}
	return m
}

// Ordered returns the stack's nodes from root to leaf for a strictly linear
// stack, or an error describing why the lineage is invalid. It is the single
// guard the reconciler and the `set-base`/`add` mutations call before acting,
// so every cycle / missing-predecessor / non-linear case fails closed here.
//
// Validations, in order: self-reference, missing base task, exactly one root,
// no unit with multiple children (linear only), full reachability (cycle).
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
	switch len(roots) {
	case 0:
		return nil, ErrNoRoot
	case 1:
	default:
		return nil, ErrMultipleRoots
	}
	for _, kids := range children {
		if len(kids) > 1 {
			return nil, ErrBranching
		}
	}
	ordered := make([]Node, 0, len(nodes))
	cur := roots[0]
	for {
		ordered = append(ordered, byTask[cur])
		next := children[cur]
		if len(next) == 0 {
			break
		}
		cur = next[0]
	}
	if len(ordered) != len(nodes) {
		// Some nodes were unreachable from the single root → a cycle exists among them.
		return nil, ErrCycle
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

// NextToMerge returns the task ID of the bottom-most unit not yet merged — the
// next candidate to land — and whether one exists. Merged/closed units are
// skipped (merged ones have already landed; closed ones were dropped).
func NextToMerge(ordered []Node) (string, bool) {
	for _, n := range ordered {
		if n.State == NodeStateMerged || n.State == NodeStateClosed {
			continue
		}
		return n.TaskID, true
	}
	return "", false
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
