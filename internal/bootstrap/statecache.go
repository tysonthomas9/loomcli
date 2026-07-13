package bootstrap

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/tysonthomas9/loomcli/internal/atomicfile"
	"github.com/tysonthomas9/loomcli/internal/configlock"
)

// stateCacheVersion is bumped whenever the on-disk schema gains a
// breaking change. The Load function tolerates older versions by
// applying defaults; truly incompatible files cause a fatal error so
// the user can decide whether to delete the cache.
const stateCacheVersion = 1

// StateCache is the per-user, per-machine state file at ~/.loom/state.json.
//
// This is NOT loom config. The source of truth for workspace/repo/agent
// existence is fleet-db. The cache exists to hold local-only
// information that fleet-db can't (and shouldn't) know:
//   - which workspace the user touched last (a UI hint only; runtime
//     commands require LOOM_WORKSPACE or --workspace)
//   - where each workspace's checkout lives on this machine
//   - where each repo within a workspace is checked out on this machine
//   - where each agent's git worktree lives on this machine
//
// All of these are regenerable: a missing state.json is recoverable by
// re-cloning + re-running `loom workspace use <name>`. The cache is a
// convenience, never load-bearing for correctness.
type StateCache struct {
	Version       int                            `json:"version"`
	LastWorkspace string                         `json:"last_workspace,omitempty"`
	Workspaces    map[string]WorkspaceLocalState `json:"workspaces,omitempty"`
}

// WorkspaceLocalState holds the per-workspace data that varies by
// machine. Path is the workspace's root directory on this machine.
// Repos and Agents map their workspace-scoped names to local paths.
type WorkspaceLocalState struct {
	Path   string                     `json:"path,omitempty"`
	Repos  map[string]string          `json:"repos,omitempty"`
	Agents map[string]AgentLocalState `json:"agents,omitempty"`
}

// AgentLocalState holds an agent's local-machine attributes. Worktree
// is the git worktree path the agent operates inside.
type AgentLocalState struct {
	Worktree string `json:"worktree,omitempty"`
}

// LoadStateCache reads ~/.loom/state.json. A missing file returns an
// empty StateCache (not an error) so callers can do
// `cache, _ := LoadStateCache(); ...` and treat first-run as normal.
//
// A malformed file IS an error — the cache is per-user state and the
// user should decide how to recover.
func LoadStateCache() (*StateCache, error) {
	path := StateFilePath()
	if path == "" {
		return nil, errors.New("statecache: cannot resolve loom directory")
	}
	data, err := os.ReadFile(path) //nolint:gosec // path is constructed from LoomDir
	if err != nil {
		if os.IsNotExist(err) {
			return &StateCache{Version: stateCacheVersion, Workspaces: make(map[string]WorkspaceLocalState)}, nil
		}
		return nil, fmt.Errorf("statecache: read %s: %w", path, err)
	}
	var sc StateCache
	if err := json.Unmarshal(data, &sc); err != nil {
		return nil, fmt.Errorf("statecache: parse %s: %w", path, err)
	}
	if sc.Version == 0 {
		sc.Version = stateCacheVersion
	}
	if sc.Workspaces == nil {
		sc.Workspaces = make(map[string]WorkspaceLocalState)
	}
	return &sc, nil
}

// saveStateCacheLocked writes the cache atomically. It is deliberately
// unexported: the only public write path is MutateStateCache (or
// MutateWorkspaceLocalState), which always loads the on-disk state
// under the flock before mutating — so no caller can replace the whole
// workspaces map from a stale or freshly-constructed struct.
func saveStateCacheLocked(sc *StateCache) error {
	if sc == nil {
		return errors.New("statecache: nil cache")
	}
	if sc.Version == 0 {
		sc.Version = stateCacheVersion
	}
	path := StateFilePath()
	if path == "" {
		return errors.New("statecache: cannot resolve loom directory")
	}
	if err := os.MkdirAll(LoomDir(), 0755); err != nil {
		return fmt.Errorf("statecache: mkdir %s: %w", LoomDir(), err)
	}
	data, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		return fmt.Errorf("statecache: marshal: %w", err)
	}
	if err := atomicfile.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("statecache: write %s: %w", path, err)
	}
	return nil
}

// WithStateLock acquires the loom directory lock, runs fn, releases.
// Use to wrap Load+mutate+Save sequences so concurrent CLI invocations
// don't clobber each other's writes. The lock file lives in LoomDir
// and is independent of other lock files.
func WithStateLock(fn func() error) error {
	dir := LoomDir()
	if dir == "" {
		return errors.New("statecache: cannot resolve loom directory")
	}
	return configlock.WithLock(dir, fn)
}

// MutateStateCache is the single public mutation entry point for
// state.json: it acquires the state lock, loads the on-disk cache,
// applies fn, and saves the result. Read-modify-write under the flock
// means callers can never drop other workspaces' entries by saving a
// stale or fresh struct. Deletions stay explicit: delete map keys
// inside fn. MutateStateCache acquires the state lock itself, so it
// must NOT be called from inside a WithStateLock block.
func MutateStateCache(fn func(*StateCache) error) error {
	if fn == nil {
		return errors.New("statecache: mutate function is required")
	}
	return WithStateLock(func() error {
		sc, err := LoadStateCache()
		if err != nil {
			return err
		}
		if err := fn(sc); err != nil {
			return err
		}
		return saveStateCacheLocked(sc)
	})
}

// MutateWorkspaceLocalState wraps the common state-cache transaction for
// updating one workspace's machine-local state.
func MutateWorkspaceLocalState(wsKey string, fn func(*WorkspaceLocalState) error) error {
	if wsKey == "" {
		return errors.New("statecache: workspace key is required")
	}
	if fn == nil {
		return errors.New("statecache: mutate function is required")
	}
	return MutateStateCache(func(sc *StateCache) error {
		if sc.Workspaces == nil {
			sc.Workspaces = make(map[string]WorkspaceLocalState)
		}
		local := sc.Workspaces[wsKey]
		if err := fn(&local); err != nil {
			return err
		}
		sc.Workspaces[wsKey] = local
		return nil
	})
}
