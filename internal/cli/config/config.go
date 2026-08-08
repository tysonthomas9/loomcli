package config

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// LoomConfig is a FleetDB-backed workspace view used by older command code
// while the internal DTOs are collapsed onto domain types.
type LoomConfig struct {
	// DefaultWorkspace is a legacy field populated from the per-machine
	// LastWorkspace UI hint. Runtime selection must use LOOM_WORKSPACE or
	// --workspace.
	DefaultWorkspace string                     `yaml:"default_workspace,omitempty"`
	Workspaces       map[string]WorkspaceConfig `yaml:"workspaces"`

	// DefaultWorkspaceID is the ID for the legacy DefaultWorkspace hint.
	DefaultWorkspaceID string `yaml:"-" json:"-"`
}

// WorkspaceState represents the lifecycle state of a workspace.
type WorkspaceState string

const (
	WorkspaceStateCreating     WorkspaceState = "creating"
	WorkspaceStateCloning      WorkspaceState = "cloning"
	WorkspaceStateInitializing WorkspaceState = "initializing"
	WorkspaceStateReady        WorkspaceState = "ready"
	WorkspaceStateError        WorkspaceState = "error"
)

// WorkspaceConfig defines a named workspace containing multiple repos.
type WorkspaceConfig struct {
	ID           string         `yaml:"id,omitempty" json:"id,omitempty"`                       // Stable UUID, generated at creation, never changes
	Path         string         `yaml:"path" json:"path"`                                       // Directory path for this workspace
	Backend      string         `yaml:"backend,omitempty" json:"backend,omitempty"`             // AI backend override for this workspace
	Repos        []RepoConfig   `yaml:"repos" json:"repos"`                                     // Repositories in this workspace
	State        WorkspaceState `yaml:"state,omitempty" json:"state,omitempty"`                 // Lifecycle state (empty/"" = ready)
	ErrorMessage string         `yaml:"error_message,omitempty" json:"error_message,omitempty"` // Error detail when State=error
	DesignFormat string         `yaml:"design_format,omitempty" json:"design_format,omitempty"` // Planner design output format ("markdown" default, "html")
}

// RepoConfig defines a single repository within a workspace
type RepoConfig struct {
	Name          string   `yaml:"name" json:"name"`                                         // Display name / identifier
	Path          string   `yaml:"path" json:"path"`                                         // Path to the repo (absolute or relative to workspace)
	DefaultBranch string   `yaml:"default_branch,omitempty" json:"default_branch,omitempty"` // Override default branch (defaults to "main")
	Remote        string   `yaml:"remote,omitempty" json:"remote,omitempty"`                 // Git remote name (defaults to "origin")
	Groups        []string `yaml:"groups,omitempty" json:"groups,omitempty"`                 // Logical groups (e.g., backend, infra)
	SourceRepoID  string   `yaml:"source_repo_id,omitempty" json:"source_repo_id,omitempty"` // Stable identifier for server-side filtering (defaults to Name)
}

// ResolveAbsPath returns the absolute path for this repo.
// If repo.Path is already absolute, returns it as-is.
// Otherwise, joins it with workspacePath.
func (rc RepoConfig) ResolveAbsPath(workspacePath string) string {
	if filepath.IsAbs(rc.Path) {
		return rc.Path
	}
	return filepath.Join(workspacePath, rc.Path)
}

// ValidateRemoteName checks if a remote name is safe for use in git commands.
// Empty is allowed (resolveRemote defaults it to "origin").
func ValidateRemoteName(name string) error {
	if name == "" {
		return nil
	}
	if len(name) > 255 {
		return fmt.Errorf("remote name too long (max 255 characters)")
	}
	if name[0] == '-' {
		return fmt.Errorf("remote name %q must not start with '-'", name)
	}
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.') {
			return fmt.Errorf("remote name %q contains invalid character %q; use only alphanumeric, hyphens, underscores, and dots", name, string(c))
		}
	}
	return nil
}

// WorkspaceByID finds a workspace by its stable UUID.
// Returns the workspace name, config, and true if found; empty name, nil, false otherwise.
func WorkspaceByID(cfg *LoomConfig, id string) (string, *WorkspaceConfig, bool) {
	if cfg == nil || id == "" {
		return "", nil, false
	}
	for name, ws := range cfg.Workspaces {
		if ws.ID == id {
			wsCopy := ws
			return name, &wsCopy, true
		}
	}
	return "", nil, false
}

// GetConfigDir returns the loom config directory path.
// Respects LOOM_CONFIG_DIR env var, otherwise defaults to ~/.loom.
func GetConfigDir() string {
	return bootstrap.LoomDir()
}

// LoadConfig reads the workspace topology from FleetDB and overlays
// machine-local checkout paths from bootstrap state.json.
func LoadConfig() (*LoomConfig, error) {
	ctx := cmdstore.RootContext()
	dataDir := bootstrap.LoomDir()
	if dataDir == "" {
		return nil, errors.New("cannot determine loom data directory")
	}
	handle, err := bootstrap.OpenStore(ctx, dataDir, nil)
	if err != nil {
		return nil, fmt.Errorf("open fleet-db store: %w", err)
	}
	defer func() { _ = handle.Close() }()
	return loadConfigFromStore(ctx, handle.Store)
}

// GetWorkspaceDir returns the directory path for a named workspace.
func GetWorkspaceDir(name string) string {
	return bootstrap.WorkspaceDir(name)
}

// ResolveActiveWorkspace returns the active FleetDB workspace projected into the
// historical WorkspaceConfig DTO used by prompt and daemon code.
func ResolveActiveWorkspace() (*WorkspaceConfig, error) {
	ctx := cmdstore.RootContext()
	key, err := bootstrap.ResolveActiveWorkspaceKey(ctx, nil)
	if err != nil {
		if errors.Is(err, bootstrap.ErrNoActiveWorkspace) {
			return nil, nil
		}
		return nil, err
	}
	dataDir := bootstrap.LoomDir()
	if dataDir == "" {
		return nil, errors.New("cannot determine loom data directory")
	}
	handle, err := bootstrap.OpenStore(ctx, dataDir, nil)
	if err != nil {
		return nil, fmt.Errorf("open fleet-db store: %w", err)
	}
	defer func() { _ = handle.Close() }()

	cfg, err := loadConfigFromStore(ctx, handle.Store)
	if err != nil {
		return nil, err
	}
	ws, ok := cfg.Workspaces[key]
	if !ok {
		return nil, fmt.Errorf("active workspace %q not found in fleet-db", key)
	}
	return &ws, nil
}

type configStore interface {
	Workspaces() store.WorkspaceStore
	Repos() store.RepoStore
}

func loadConfigFromStore(ctx context.Context, st configStore) (*LoomConfig, error) {
	workspaces, err := st.Workspaces().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list fleet-db workspaces: %w", err)
	}
	sc, _ := bootstrap.LoadStateCache()
	cfg := &LoomConfig{
		Workspaces: make(map[string]WorkspaceConfig, len(workspaces)),
	}
	if sc != nil {
		cfg.DefaultWorkspace = sc.LastWorkspace
	}
	for _, ws := range workspaces {
		if ws == nil {
			continue
		}
		local := bootstrap.WorkspaceLocalState{}
		if sc != nil {
			local = sc.Workspaces[ws.Key]
		}
		wsc, err := workspaceConfigFromStore(ctx, st, ws, local)
		if err != nil {
			return nil, err
		}
		cfg.Workspaces[ws.Key] = wsc
	}
	if cfg.DefaultWorkspace != "" {
		if ws, ok := cfg.Workspaces[cfg.DefaultWorkspace]; ok {
			cfg.DefaultWorkspaceID = ws.ID
		}
	}
	return cfg, nil
}

func workspaceConfigFromStore(
	ctx context.Context,
	st configStore,
	ws *workspacemodule.Workspace,
	local bootstrap.WorkspaceLocalState,
) (WorkspaceConfig, error) {
	repos, err := repoConfigsFromStore(ctx, st, ws.Key, local)
	if err != nil {
		return WorkspaceConfig{}, err
	}
	wsc := WorkspaceConfig{
		ID:           ws.Key,
		Path:         local.Path,
		Repos:        repos,
		State:        WorkspaceState(ws.State),
		ErrorMessage: ws.ErrorMessage,
		DesignFormat: ws.DesignFormat,
	}
	wsc.Backend = local.DefaultRuntimeProvider
	return wsc, nil
}

func repoConfigsFromStore(
	ctx context.Context,
	st configStore,
	wsKey string,
	local bootstrap.WorkspaceLocalState,
) ([]RepoConfig, error) {
	repoRows, err := st.Repos().List(ctx, wsKey)
	if err != nil {
		return nil, fmt.Errorf("list repos for workspace %s: %w", wsKey, err)
	}
	repos := make([]RepoConfig, 0, len(repoRows))
	for _, r := range repoRows {
		if r == nil {
			continue
		}
		repos = append(repos, repoConfigFromStore(r, local))
	}
	return repos, nil
}

func repoConfigFromStore(r *workspacemodule.Repository, local bootstrap.WorkspaceLocalState) RepoConfig {
	path := local.Repos[r.Name]
	if path == "" {
		path = r.Name
	}
	sourceRepoID := r.SourceRepoID
	if sourceRepoID == "" {
		sourceRepoID = r.Name
	}
	return RepoConfig{
		Name:          r.Name,
		Path:          path,
		DefaultBranch: r.DefaultBranch,
		Remote:        r.Remote,
		Groups:        append([]string(nil), r.Groups...),
		SourceRepoID:  sourceRepoID,
	}
}
