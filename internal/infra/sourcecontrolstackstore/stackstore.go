// Package stackstore persists stack lineage for the stack-aware PR publisher.
//
// This iteration ships a loomcli-side LocalStore backed by ~/.loom/stacks.json,
// using the same configlock + atomic-write discipline as the state cache. The
// Source Control's owner-owned ports are the seam: a future FleetDB adapter can
// implement them without touching the reconciler or CLI. See
// docs/design/2026-06-18-stack-aware-pr-publisher.md.
package stackstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/tysonthomas9/loomcli/internal/atomicfile"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/configlock"
	sl "github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
)

const stacksFileVersion = 1

// Sentinel errors.
var (
	ErrStackNotFound  = errors.New("stackstore: stack not found")
	ErrStackExists    = errors.New("stackstore: stack already exists")
	ErrNodeNotFound   = errors.New("stackstore: node not found")
	ErrNodeExists     = errors.New("stackstore: task already in stack")
	ErrLoomDirMissing = errors.New("stackstore: cannot resolve loom directory")
)

// on-disk shape ------------------------------------------------------------

type stacksFile struct {
	Version    int                         `json:"version"`
	Workspaces map[string]*workspaceStacks `json:"workspaces,omitempty"`
}

type workspaceStacks struct {
	Stacks map[string]*storedStack `json:"stacks,omitempty"` // key = StackID
}

type storedStack struct {
	Stack sl.Stack                 `json:"stack"`
	Nodes map[string]*sl.StackNode `json:"nodes,omitempty"` // key = TaskID
}

// LocalStore -----------------------------------------------------------------

// LocalStore implements Source Control's persistence ports against a single
// JSON file in a loom directory.
type LocalStore struct{ dir string }

var _ sl.StackLifecycleStore = (*LocalStore)(nil)
var _ sl.TaskOutcomeStore = (*LocalStore)(nil)

// New returns a LocalStore rooted at dir (the directory holding stacks.json).
func New(dir string) *LocalStore { return &LocalStore{dir: dir} }

// Default returns a LocalStore rooted at the loom per-user directory.
func Default() (*LocalStore, error) {
	dir := bootstrap.LoomDir()
	if dir == "" {
		return nil, ErrLoomDirMissing
	}
	return &LocalStore{dir: dir}, nil
}

func (s *LocalStore) path() string { return filepath.Join(s.dir, "stacks.json") }

func (s *LocalStore) load() (*stacksFile, error) {
	data, err := os.ReadFile(s.path()) //nolint:gosec // path from loom dir
	if err != nil {
		if os.IsNotExist(err) {
			return &stacksFile{Version: stacksFileVersion, Workspaces: map[string]*workspaceStacks{}}, nil
		}
		return nil, fmt.Errorf("stackstore: read %s: %w", s.path(), err)
	}
	var f stacksFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("stackstore: parse %s: %w", s.path(), err)
	}
	if f.Version == 0 {
		f.Version = stacksFileVersion
	}
	if f.Workspaces == nil {
		f.Workspaces = map[string]*workspaceStacks{}
	}
	return &f, nil
}

func (s *LocalStore) save(f *stacksFile) error {
	if f.Version == 0 {
		f.Version = stacksFileVersion
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("stackstore: mkdir %s: %w", s.dir, err)
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("stackstore: marshal: %w", err)
	}
	if err := atomicfile.WriteFile(s.path(), data, 0o600); err != nil {
		return fmt.Errorf("stackstore: write %s: %w", s.path(), err)
	}
	return nil
}

// withLock serializes load+mutate+save against concurrent CLI invocations.
func (s *LocalStore) withLock(fn func(*stacksFile) error) error {
	if s.dir == "" {
		return ErrLoomDirMissing
	}
	return configlock.WithLock(s.dir, func() error {
		f, err := s.load()
		if err != nil {
			return err
		}
		if err := fn(f); err != nil {
			return err
		}
		return s.save(f)
	})
}

func (f *stacksFile) stack(ws string, id sl.StackID) (*storedStack, bool) {
	w := f.Workspaces[ws]
	if w == nil {
		return nil, false
	}
	st, ok := w.Stacks[id]
	return st, ok
}

// reads ----------------------------------------------------------------------

func (s *LocalStore) GetStackRecord(_ context.Context, ws string, id sl.StackID) (*sl.Stack, error) {
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	st, ok := f.stack(ws, id)
	if !ok {
		return nil, ErrStackNotFound
	}
	cp := st.Stack
	return &cp, nil
}

func (s *LocalStore) ListStackRecords(_ context.Context, ws string) ([]sl.Stack, error) {
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	w := f.Workspaces[ws]
	if w == nil {
		return nil, nil
	}
	out := make([]sl.Stack, 0, len(w.Stacks))
	for _, st := range w.Stacks {
		out = append(out, st.Stack)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *LocalStore) ListStackNodeRecords(_ context.Context, ws string, id sl.StackID) ([]sl.StackNode, error) {
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	st, ok := f.stack(ws, id)
	if !ok {
		return nil, ErrStackNotFound
	}
	return sortedNodes(st), nil
}

func sortedNodes(st *storedStack) []sl.StackNode {
	out := make([]sl.StackNode, 0, len(st.Nodes))
	for _, n := range st.Nodes {
		out = append(out, *n)
	}
	// Best-effort lineage order; falls back to TaskID order on invalid lineage.
	if ordered, err := sl.Ordered(out); err == nil {
		return ordered
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TaskID < out[j].TaskID })
	return out
}

// writes ---------------------------------------------------------------------

// EnsureStackRecord creates the stack header if absent, or updates its mutable fields
// (RootBase, DefaultCommitMode) if present.
func (s *LocalStore) EnsureStackRecord(_ context.Context, in sl.Stack) error {
	if in.WorkspaceKey == "" || in.ID == "" {
		return errors.New("stackstore: stack workspaceKey and id are required")
	}
	now := time.Now().UTC()
	return s.withLock(func(f *stacksFile) error {
		w := f.Workspaces[in.WorkspaceKey]
		if w == nil {
			w = &workspaceStacks{Stacks: map[string]*storedStack{}}
			f.Workspaces[in.WorkspaceKey] = w
		}
		if st, ok := w.Stacks[in.ID]; ok {
			st.Stack.RootBase = in.RootBase
			st.Stack.Repository = in.Repository
			if in.DefaultCommitMode != "" {
				st.Stack.DefaultCommitMode = in.DefaultCommitMode
			}
			st.Stack.UpdatedAt = now
			return nil
		}
		in.CreatedAt = now
		in.UpdatedAt = now
		w.Stacks[in.ID] = &storedStack{Stack: in, Nodes: map[string]*sl.StackNode{}}
		return nil
	})
}

// AddStackNodeRecord registers taskID in the stack, assigns a collision-free output branch,
// validates the resulting lineage stays linear+acyclic, and persists — all under
// the lock. baseTaskID == "" means the root unit.
func (s *LocalStore) AddStackNodeRecord(_ context.Context, ws string, id sl.StackID, taskID, baseTaskID string, mode sl.CommitMode) (sl.StackNode, error) {
	var created sl.StackNode
	err := s.withLock(func(f *stacksFile) error {
		st, ok := f.stack(ws, id)
		if !ok {
			return ErrStackNotFound
		}
		if st.Nodes == nil {
			st.Nodes = map[string]*sl.StackNode{}
		}
		if _, exists := st.Nodes[taskID]; exists {
			return ErrNodeExists
		}
		taken := make(map[string]struct{}, len(st.Nodes))
		for _, n := range st.Nodes {
			taken[n.OutputBranch] = struct{}{}
		}
		if mode == "" {
			mode = st.Stack.DefaultCommitMode
		}
		if mode == "" {
			mode = sl.CommitModeLoom
		}
		now := time.Now().UTC()
		node := sl.StackNode{
			StackID:      id,
			TaskID:       taskID,
			BaseTaskID:   baseTaskID,
			OutputBranch: sl.AssignBranch(id, taskID, taken),
			CommitMode:   mode,
			State:        sl.NodeStatePending,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if err := validateWith(st, node); err != nil {
			return err
		}
		st.Nodes[taskID] = &node
		st.Stack.UpdatedAt = now
		created = node
		return nil
	})
	return created, err
}

// SetStackNodeBaseRecord repoints taskID's predecessor to baseTaskID after validating the
// change keeps the lineage linear+acyclic.
func (s *LocalStore) SetStackNodeBaseRecord(_ context.Context, ws string, id sl.StackID, taskID, baseTaskID string) error {
	return s.withLock(func(f *stacksFile) error {
		st, ok := f.stack(ws, id)
		if !ok {
			return ErrStackNotFound
		}
		node, ok := st.Nodes[taskID]
		if !ok {
			return ErrNodeNotFound
		}
		if baseTaskID != "" {
			if _, ok := st.Nodes[baseTaskID]; !ok {
				return sl.ErrMissingPredecessor
			}
		}
		updated := *node
		updated.BaseTaskID = baseTaskID
		if err := validateWith(st, updated); err != nil {
			return err
		}
		node.BaseTaskID = baseTaskID
		node.UpdatedAt = time.Now().UTC()
		st.Stack.UpdatedAt = node.UpdatedAt
		return nil
	})
}

func (s *LocalStore) RemoveStackNodeRecord(_ context.Context, ws string, id sl.StackID, taskID string) error {
	return s.withLock(func(f *stacksFile) error {
		st, ok := f.stack(ws, id)
		if !ok {
			return ErrStackNotFound
		}
		removed, ok := st.Nodes[taskID]
		if !ok {
			return ErrNodeNotFound
		}
		// Reparent direct children onto the removed node's predecessor so the
		// chain stays linear (descendants slide down). RootBase-rooted if it was root.
		for _, n := range st.Nodes {
			if n.BaseTaskID == taskID {
				n.BaseTaskID = removed.BaseTaskID
				n.UpdatedAt = time.Now().UTC()
			}
		}
		delete(st.Nodes, taskID)
		st.Stack.UpdatedAt = time.Now().UTC()
		return nil
	})
}

// MoveStackNodeRecord splices taskID to sit immediately after afterTaskID in the linear
// chain, atomically: it detaches the node (reparenting its child onto its old
// base), inserts it after the target (reparenting the target's old child onto
// the node), then validates the result stays linear+acyclic before persisting.
func (s *LocalStore) MoveStackNodeRecord(_ context.Context, ws string, id sl.StackID, taskID, afterTaskID string) error {
	if taskID == afterTaskID {
		return sl.ErrCycle
	}
	return s.withLock(func(f *stacksFile) error {
		st, ok := f.stack(ws, id)
		if !ok {
			return ErrStackNotFound
		}
		node, ok := st.Nodes[taskID]
		if !ok {
			return ErrNodeNotFound
		}
		if _, ok := st.Nodes[afterTaskID]; !ok {
			return ErrNodeNotFound
		}
		// Detach: node's current child reparents onto node's current base.
		for _, n := range st.Nodes {
			if n.TaskID != taskID && n.BaseTaskID == taskID {
				n.BaseTaskID = node.BaseTaskID
			}
		}
		// Insert after target: target's current child reparents onto node.
		for _, n := range st.Nodes {
			if n.TaskID != taskID && n.BaseTaskID == afterTaskID {
				n.BaseTaskID = taskID
			}
		}
		node.BaseTaskID = afterTaskID
		nodes := make([]sl.StackNode, 0, len(st.Nodes))
		for _, n := range st.Nodes {
			nodes = append(nodes, *n)
		}
		if _, err := sl.Ordered(nodes); err != nil {
			return err
		}
		st.Stack.UpdatedAt = time.Now().UTC()
		return nil
	})
}

// updateStackNodeRecord applies fn to the stored node under the lock (reconciler state writes).
func (s *LocalStore) updateStackNodeRecord(_ context.Context, ws string, id sl.StackID, taskID string, fn func(*sl.StackNode) error) error {
	return s.withLock(func(f *stacksFile) error {
		st, ok := f.stack(ws, id)
		if !ok {
			return ErrStackNotFound
		}
		node, ok := st.Nodes[taskID]
		if !ok {
			return ErrNodeNotFound
		}
		if err := fn(node); err != nil {
			return err
		}
		node.UpdatedAt = time.Now().UTC()
		return nil
	})
}

// UpdateStackNodePublicationRecord applies the bounded publication mutation
// exposed by Source Control's owner port.
func (s *LocalStore) UpdateStackNodePublicationRecord(
	ctx context.Context,
	workspace,
	stackID,
	taskID string,
	mutation sl.StackNodePublicationMutation,
) error {
	return s.updateStackNodeRecord(ctx, workspace, stackID, taskID, func(node *sl.StackNode) error {
		node.State = sl.NodeState(mutation.State)
		if mutation.State == sl.StackPublicationPublished {
			node.PRNumber = mutation.PRNumber
			node.PRURL = mutation.PRURL
		}
		if mutation.OutputSHA != "" {
			node.OutputSHA = mutation.OutputSHA
		}
		if mutation.PublishedAt != nil {
			node.LastPublishedAt = cloneTime(mutation.PublishedAt)
		}
		return nil
	})
}

func (s *LocalStore) ListTaskStacks(ctx context.Context, workspace string) ([]sl.TaskStack, error) {
	values, err := s.ListStackRecords(ctx, workspace)
	if err != nil {
		return nil, err
	}
	result := make([]sl.TaskStack, len(values))
	for index, value := range values {
		result[index] = sl.TaskStack{
			StackID: value.ID, WorkspaceKey: value.WorkspaceKey, Repository: value.Repository,
		}
	}
	return result, nil
}

func (s *LocalStore) ListTaskStackNodes(
	ctx context.Context,
	workspace,
	stackID string,
) ([]sl.TaskStackNode, error) {
	values, err := s.ListStackNodeRecords(ctx, workspace, stackID)
	if err != nil {
		return nil, err
	}
	result := make([]sl.TaskStackNode, len(values))
	for index, value := range values {
		result[index] = sl.TaskStackNode{TaskID: value.TaskID}
	}
	return result, nil
}

func (s *LocalStore) UpdateTaskStackOutcome(
	ctx context.Context,
	workspace,
	stackID,
	taskID string,
	mutation sl.TaskStackOutcomeMutation,
) error {
	return s.updateStackNodeRecord(ctx, workspace, stackID, taskID, func(node *sl.StackNode) error {
		node.State = sl.NodeState(mutation.State)
		if mutation.OutputSHA != "" {
			node.OutputSHA = mutation.OutputSHA
		}
		if mutation.PublishedAt != nil {
			node.LastPublishedAt = cloneTime(mutation.PublishedAt)
		}
		return nil
	})
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

// validateWith checks that replacing/adding `node` keeps the stack's lineage valid.
func validateWith(st *storedStack, node sl.StackNode) error {
	nodes := make([]sl.StackNode, 0, len(st.Nodes)+1)
	for tid, n := range st.Nodes {
		if tid == node.TaskID {
			continue
		}
		nodes = append(nodes, *n)
	}
	nodes = append(nodes, node)
	_, err := sl.Ordered(nodes)
	return err
}
