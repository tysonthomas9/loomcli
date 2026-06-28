// Package stackstore persists stack lineage for the stack-aware PR publisher.
//
// This iteration ships a loomcli-side LocalStore backed by ~/.loom/stacks.json,
// using the same configlock + atomic-write discipline as the state cache. The
// Store interface is the seam: a future FleetDBStore can implement it without
// touching the reconciler or CLI. See
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
	sl "github.com/tysonthomas9/loomcli/internal/stacklineage"
)

const stacksFileVersion = 1

// Store is the lineage persistence contract every caller depends on.
type Store interface {
	EnsureStack(ctx context.Context, s sl.Stack) error
	GetStack(ctx context.Context, ws string, id sl.StackID) (*sl.Stack, error)
	ListStacks(ctx context.Context, ws string) ([]sl.Stack, error)
	DeleteStack(ctx context.Context, ws string, id sl.StackID) error

	ListNodes(ctx context.Context, ws string, id sl.StackID) ([]sl.Node, error)
	AddNode(ctx context.Context, ws string, id sl.StackID, taskID, baseTaskID string, mode sl.CommitMode) (sl.Node, error)
	SetBase(ctx context.Context, ws string, id sl.StackID, taskID, baseTaskID string) error
	RemoveNode(ctx context.Context, ws string, id sl.StackID, taskID string) error
	UpdateNode(ctx context.Context, ws string, id sl.StackID, taskID string, fn func(*sl.Node) error) error
}

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
	Stack sl.Stack            `json:"stack"`
	Nodes map[string]*sl.Node `json:"nodes,omitempty"` // key = TaskID
}

// LocalStore -----------------------------------------------------------------

// LocalStore implements Store against a single JSON file in a loom directory.
type LocalStore struct{ dir string }

var _ Store = (*LocalStore)(nil)

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
	st, ok := w.Stacks[string(id)]
	return st, ok
}

// reads ----------------------------------------------------------------------

func (s *LocalStore) GetStack(_ context.Context, ws string, id sl.StackID) (*sl.Stack, error) {
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

func (s *LocalStore) ListStacks(_ context.Context, ws string) ([]sl.Stack, error) {
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

func (s *LocalStore) ListNodes(_ context.Context, ws string, id sl.StackID) ([]sl.Node, error) {
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

func sortedNodes(st *storedStack) []sl.Node {
	out := make([]sl.Node, 0, len(st.Nodes))
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

// EnsureStack creates the stack header if absent, or updates its mutable fields
// (RootBase, DefaultCommitMode) if present.
func (s *LocalStore) EnsureStack(_ context.Context, in sl.Stack) error {
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
		if st, ok := w.Stacks[string(in.ID)]; ok {
			st.Stack.RootBase = in.RootBase
			st.Stack.RepoName = in.RepoName
			if in.DefaultCommitMode != "" {
				st.Stack.DefaultCommitMode = in.DefaultCommitMode
			}
			st.Stack.UpdatedAt = now
			return nil
		}
		in.CreatedAt = now
		in.UpdatedAt = now
		w.Stacks[string(in.ID)] = &storedStack{Stack: in, Nodes: map[string]*sl.Node{}}
		return nil
	})
}

func (s *LocalStore) DeleteStack(_ context.Context, ws string, id sl.StackID) error {
	return s.withLock(func(f *stacksFile) error {
		w := f.Workspaces[ws]
		if w == nil {
			return ErrStackNotFound
		}
		if _, ok := w.Stacks[string(id)]; !ok {
			return ErrStackNotFound
		}
		delete(w.Stacks, string(id))
		return nil
	})
}

// AddNode registers taskID in the stack, assigns a collision-free output branch,
// validates the resulting lineage stays linear+acyclic, and persists — all under
// the lock. baseTaskID == "" means the root unit.
func (s *LocalStore) AddNode(_ context.Context, ws string, id sl.StackID, taskID, baseTaskID string, mode sl.CommitMode) (sl.Node, error) {
	var created sl.Node
	err := s.withLock(func(f *stacksFile) error {
		st, ok := f.stack(ws, id)
		if !ok {
			return ErrStackNotFound
		}
		if st.Nodes == nil {
			st.Nodes = map[string]*sl.Node{}
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
		node := sl.Node{
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

// SetBase repoints taskID's predecessor to baseTaskID after validating the
// change keeps the lineage linear+acyclic.
func (s *LocalStore) SetBase(_ context.Context, ws string, id sl.StackID, taskID, baseTaskID string) error {
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

func (s *LocalStore) RemoveNode(_ context.Context, ws string, id sl.StackID, taskID string) error {
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

// MoveNode splices taskID to sit immediately after afterTaskID in the linear
// chain, atomically: it detaches the node (reparenting its child onto its old
// base), inserts it after the target (reparenting the target's old child onto
// the node), then validates the result stays linear+acyclic before persisting.
func (s *LocalStore) MoveNode(_ context.Context, ws string, id sl.StackID, taskID, afterTaskID string) error {
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
		nodes := make([]sl.Node, 0, len(st.Nodes))
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

// UpdateNode applies fn to the stored node under the lock (reconciler state writes).
func (s *LocalStore) UpdateNode(_ context.Context, ws string, id sl.StackID, taskID string, fn func(*sl.Node) error) error {
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

// validateWith checks that replacing/adding `node` keeps the stack's lineage valid.
func validateWith(st *storedStack, node sl.Node) error {
	nodes := make([]sl.Node, 0, len(st.Nodes)+1)
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
