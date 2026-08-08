package sourcecontrol

import (
	"fmt"
	"sort"
	"strings"
)

// ByTask indexes nodes by their task identity.
func ByTask(nodes []StackNode) map[string]StackNode {
	result := make(map[string]StackNode, len(nodes))
	for _, node := range nodes {
		result[node.TaskID] = node
	}
	return result
}

// Ordered returns the stack forest as deterministic root-to-leaf linear chains.
// It is the single validation policy used by persistence, application services,
// publishers, and CLI reconciliation.
func Ordered(nodes []StackNode) ([]StackNode, error) {
	if len(nodes) == 0 {
		return nil, nil
	}
	byTask, err := indexStackNodes(nodes)
	if err != nil {
		return nil, err
	}
	children, roots, err := indexStackTopology(nodes, byTask)
	if err != nil {
		return nil, err
	}
	if len(roots) == 0 {
		return nil, ErrNoRoot
	}
	sort.Strings(roots)
	return walkStackForest(nodes, byTask, children, roots)
}

func indexStackNodes(nodes []StackNode) (map[string]StackNode, error) {
	byTask := make(map[string]StackNode, len(nodes))
	for _, node := range nodes {
		if strings.TrimSpace(node.TaskID) == "" {
			return nil, fmt.Errorf("stack contains empty task identity: %w", ErrInvalidMaterialization)
		}
		if node.BaseTaskID == node.TaskID {
			return nil, ErrCycle
		}
		if _, duplicate := byTask[node.TaskID]; duplicate {
			return nil, fmt.Errorf("stack contains duplicate task %q: %w", node.TaskID, ErrInvalidMaterialization)
		}
		byTask[node.TaskID] = node
	}
	return byTask, nil
}

func indexStackTopology(nodes []StackNode, byTask map[string]StackNode) (map[string][]string, []string, error) {
	children := make(map[string][]string, len(nodes))
	roots := make([]string, 0)
	for _, node := range nodes {
		if node.BaseTaskID == "" {
			roots = append(roots, node.TaskID)
			continue
		}
		if _, ok := byTask[node.BaseTaskID]; !ok {
			return nil, nil, ErrMissingPredecessor
		}
		children[node.BaseTaskID] = append(children[node.BaseTaskID], node.TaskID)
		if len(children[node.BaseTaskID]) > 1 {
			return nil, nil, ErrBranching
		}
	}
	return children, roots, nil
}

func walkStackForest(
	nodes []StackNode,
	byTask map[string]StackNode,
	children map[string][]string,
	roots []string,
) ([]StackNode, error) {
	ordered := make([]StackNode, 0, len(nodes))
	visited := make(map[string]struct{}, len(nodes))
	for _, root := range roots {
		for current := root; current != ""; {
			if _, seen := visited[current]; seen {
				return nil, ErrCycle
			}
			visited[current] = struct{}{}
			ordered = append(ordered, byTask[current])
			next := children[current]
			if len(next) == 0 {
				current = ""
			} else {
				current = next[0]
			}
		}
	}
	if len(ordered) != len(nodes) {
		return nil, ErrCycle
	}
	return ordered, nil
}

// BaseBranch returns a node's strict predecessor branch or the root base.
func BaseBranch(stack Stack, node StackNode, byTask map[string]StackNode) (string, error) {
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

// BaseBranchSliding skips empty, closed, or branchless ancestors while keeping
// corruption fail-closed.
func BaseBranchSliding(stack Stack, node StackNode, byTask map[string]StackNode) (string, error) {
	current := node
	for hops := 0; hops <= len(byTask); hops++ {
		if current.BaseTaskID == "" {
			return stack.RootBase, nil
		}
		base, ok := byTask[current.BaseTaskID]
		if !ok {
			return "", ErrMissingPredecessor
		}
		if base.OutputBranch != "" && base.State != NodeStateEmpty && base.State != NodeStateClosed {
			return base.OutputBranch, nil
		}
		current = base
	}
	return "", ErrCycle
}

// NextToMergeUnits returns the bottom-most unlanded task of every chain.
func NextToMergeUnits(ordered []StackNode) map[string]bool {
	byTask := ByTask(ordered)
	result := map[string]bool{}
	for _, node := range ordered {
		if node.State == NodeStateMerged || node.State == NodeStateClosed {
			continue
		}
		if node.BaseTaskID == "" || byTask[node.BaseTaskID].State == NodeStateMerged {
			result[node.TaskID] = true
		}
	}
	return result
}

// WouldCycle validates the topology that would result from changing one base.
func WouldCycle(nodes []StackNode, taskID, newBase string) error {
	updated := append([]StackNode(nil), nodes...)
	found := false
	for index := range updated {
		if updated[index].TaskID == taskID {
			updated[index].BaseTaskID = newBase
			found = true
			break
		}
	}
	if !found {
		updated = append(updated, StackNode{TaskID: taskID, BaseTaskID: newBase})
	}
	_, err := Ordered(updated)
	return err
}
