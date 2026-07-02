// Package worktreegroups provides Redis-backed persistence for terminal
// worktree group metadata.
//
// Each workspace gets one Redis key at "terminal:worktreegroups:{workspace}"
// storing a JSON-encoded []TerminalWorktreeGroup blob. The key has no TTL.
package worktreegroups

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const keyPrefix = "terminal:worktreegroups:"

// ErrGroupExists is returned when adding a group whose name already exists in
// the workspace.
var ErrGroupExists = errors.New("worktree group already exists")

// WorktreeGroupMember describes one repository checkout inside a terminal
// worktree group.
type WorktreeGroupMember struct {
	RepoName     string `json:"repo_name"`
	Path         string `json:"path"`
	BaseBranch   string `json:"base_branch"`
	BaseDetached bool   `json:"base_detached"`
	ReusedBranch bool   `json:"reused_branch"`
}

// TerminalWorktreeGroup is the persisted metadata for a terminal worktree
// group. The Root field is informational; launch cwd is recomputed from the
// workspace path and group name when tabs are assigned to a group.
type TerminalWorktreeGroup struct {
	ID        string                `json:"id"`
	Name      string                `json:"name"`
	Root      string                `json:"root"`
	Members   []WorktreeGroupMember `json:"members"`
	CreatedAt time.Time             `json:"created_at"`
}

// Store provides Redis-backed persistence for terminal worktree groups.
// Workspace identity is passed per operation. One Store instance serves all
// workspaces.
type Store struct {
	client *redis.Client
	logger *slog.Logger
	locks  sync.Map // map[string]*sync.Mutex
}

// LockedWorkspace exposes store operations while the per-workspace mutex is
// held by WithWorkspaceLock.
type LockedWorkspace struct {
	store       *Store
	workspaceID string
}

// NewStore creates a new terminal worktree group store.
func NewStore(client *redis.Client, logger *slog.Logger) *Store {
	if logger == nil {
		logger = slog.Default()
	}
	return &Store{client: client, logger: logger}
}

// Close closes the underlying Redis client.
func (s *Store) Close() error {
	return s.client.Close()
}

func workspaceKey(workspaceID string) string {
	return keyPrefix + workspaceID
}

func (s *Store) workspaceMutex(workspaceID string) *sync.Mutex {
	mu, _ := s.locks.LoadOrStore(workspaceID, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

// WithWorkspaceLock runs fn while holding the in-process mutex for workspaceID.
// Use this when callers need a duplicate check and later persist to share one
// critical section across non-Redis work.
func (s *Store) WithWorkspaceLock(workspaceID string, fn func(*LockedWorkspace) error) error {
	mu := s.workspaceMutex(workspaceID)
	mu.Lock()
	defer mu.Unlock()
	return fn(&LockedWorkspace{store: s, workspaceID: workspaceID})
}

// List returns all terminal worktree groups for a workspace. It returns a
// non-nil empty slice when no groups are stored.
func (s *Store) List(ctx context.Context, workspaceID string) ([]TerminalWorktreeGroup, error) {
	var groups []TerminalWorktreeGroup
	err := s.WithWorkspaceLock(workspaceID, func(locked *LockedWorkspace) error {
		var err error
		groups, err = locked.List(ctx)
		return err
	})
	return groups, err
}

// Get returns a group by name. It returns nil when no group with that name is
// stored.
func (s *Store) Get(ctx context.Context, workspaceID, name string) (*TerminalWorktreeGroup, error) {
	var group *TerminalWorktreeGroup
	err := s.WithWorkspaceLock(workspaceID, func(locked *LockedWorkspace) error {
		var err error
		group, err = locked.Get(ctx, name)
		return err
	})
	return group, err
}

// Add appends a group to the workspace. It fails with ErrGroupExists when the
// name already exists.
func (s *Store) Add(ctx context.Context, workspaceID string, group TerminalWorktreeGroup) error {
	return s.WithWorkspaceLock(workspaceID, func(locked *LockedWorkspace) error {
		return locked.Add(ctx, group)
	})
}

// DeleteWorkspace removes all terminal worktree group metadata for a workspace.
func (s *Store) DeleteWorkspace(ctx context.Context, workspaceID string) error {
	return s.WithWorkspaceLock(workspaceID, func(locked *LockedWorkspace) error {
		return locked.DeleteWorkspace(ctx)
	})
}

// List returns all terminal worktree groups for the locked workspace.
func (w *LockedWorkspace) List(ctx context.Context) ([]TerminalWorktreeGroup, error) {
	return w.store.list(ctx, w.workspaceID)
}

// Get returns a group by name for the locked workspace.
func (w *LockedWorkspace) Get(ctx context.Context, name string) (*TerminalWorktreeGroup, error) {
	return w.store.get(ctx, w.workspaceID, name)
}

// Add appends a group to the locked workspace.
func (w *LockedWorkspace) Add(ctx context.Context, group TerminalWorktreeGroup) error {
	return w.store.add(ctx, w.workspaceID, group)
}

// DeleteWorkspace removes all groups for the locked workspace.
func (w *LockedWorkspace) DeleteWorkspace(ctx context.Context) error {
	return w.store.deleteWorkspace(ctx, w.workspaceID)
}

func (s *Store) list(ctx context.Context, workspaceID string) ([]TerminalWorktreeGroup, error) {
	data, err := s.client.Get(ctx, workspaceKey(workspaceID)).Bytes()
	if err != nil {
		if err == redis.Nil {
			return []TerminalWorktreeGroup{}, nil
		}
		return nil, fmt.Errorf("get worktree groups for workspace %s: %w", workspaceID, err)
	}

	var groups []TerminalWorktreeGroup
	if err := json.Unmarshal(data, &groups); err != nil {
		return nil, fmt.Errorf("unmarshal worktree groups for workspace %s: %w", workspaceID, err)
	}
	if groups == nil {
		groups = []TerminalWorktreeGroup{}
	}
	return groups, nil
}

func (s *Store) get(ctx context.Context, workspaceID, name string) (*TerminalWorktreeGroup, error) {
	groups, err := s.list(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	for _, group := range groups {
		if group.Name == name {
			found := group
			return &found, nil
		}
	}
	return nil, nil
}

func (s *Store) add(ctx context.Context, workspaceID string, group TerminalWorktreeGroup) error {
	groups, err := s.list(ctx, workspaceID)
	if err != nil {
		return err
	}
	for _, existing := range groups {
		if existing.Name == group.Name {
			return fmt.Errorf("%w: %s", ErrGroupExists, group.Name)
		}
	}
	groups = append(groups, group)

	data, err := json.Marshal(groups)
	if err != nil {
		return fmt.Errorf("marshal worktree groups for workspace %s: %w", workspaceID, err)
	}
	if err := s.client.Set(ctx, workspaceKey(workspaceID), data, 0).Err(); err != nil {
		return fmt.Errorf("set worktree groups for workspace %s: %w", workspaceID, err)
	}
	return nil
}

func (s *Store) deleteWorkspace(ctx context.Context, workspaceID string) error {
	if err := s.client.Del(ctx, workspaceKey(workspaceID)).Err(); err != nil {
		return fmt.Errorf("del worktree groups for workspace %s: %w", workspaceID, err)
	}
	return nil
}
