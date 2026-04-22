package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/tysonthomas9/loomcli/internal/atomicfile"
	"github.com/tysonthomas9/loomcli/internal/configlock"
)

var configVersionWarnOnce sync.Once

// Validator hooks — set by root package init to break import cycle.
var (
	// globalConfigValidator validates a LoomConfig after loading. Set by root.
	globalConfigValidator func(cfg *LoomConfig) error
	// projectConfigValidator validates a DaemonConfig after loading. Set by root.
	projectConfigValidator func(dc *DaemonConfig, projectDir string) error
)

// SetGlobalConfigValidator registers the global config validator.
func SetGlobalConfigValidator(fn func(cfg *LoomConfig) error) {
	globalConfigValidator = fn
}

// SetProjectConfigValidator registers the project config validator.
func SetProjectConfigValidator(fn func(dc *DaemonConfig, projectDir string) error) {
	projectConfigValidator = fn
}

func resetConfigVersionWarnOnce() {
	configVersionWarnOnce = sync.Once{}
}

// LoomConfig is the top-level configuration from ~/.loom/config.yaml
type LoomConfig struct {
	Version          int                        `yaml:"version,omitempty"`
	DefaultWorkspace string                     `yaml:"default_workspace,omitempty"`
	WorkspaceOrder   []string                   `yaml:"workspace_order,omitempty"`
	Backend          string                     `yaml:"backend,omitempty"`
	Workspaces       map[string]WorkspaceConfig `yaml:"workspaces"`
	Daemon           *DaemonSettings            `yaml:"daemon,omitempty"`

	// DefaultWorkspaceID is the UUID of the default workspace, resolved at load time.
	// Not serialized to YAML.
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
	CloneURLs    []string       `yaml:"clone_urls,omitempty" json:"clone_urls,omitempty"`       // Original clone URLs (for retry)
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

// NewWorkspaceID generates a new UUID v4 for a workspace.
func NewWorkspaceID() string {
	return uuid.New().String()
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
	if dir := os.Getenv("LOOM_CONFIG_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".loom")
}

// GetConfigPath returns the full path to the loom config file.
func GetConfigPath() string {
	return filepath.Join(GetConfigDir(), "config.yaml")
}

// WithConfigLock acquires an exclusive file lock on the config lock file,
// runs fn, then releases the lock. Use this to wrap load-mutate-save
// sequences to prevent concurrent config mutations from clobbering each other.
//
// Within fn, use loadConfigUnlocked and saveConfigUnlocked instead of
// LoadConfig and SaveConfig to avoid deadlock from nested lock acquisition
// (POSIX flock does NOT allow re-locking from the same process via a
// different file descriptor). Likewise, do not call LoadConfigCached from
// inside fn: its miss path calls LoadConfig, which would re-acquire this
// flock. When fn calls SaveConfigUnlocked, InvalidateConfigCache runs — that
// is a single atomic store and is safe under the flock. See the cache.go
// header comment for the full lock-order invariant (loomcli-rc1s2).
//
// WithConfigLock is NOT reentrant — do not call from within an already-locked
// section.
func WithConfigLock(fn func() error) error {
	dir := GetConfigDir()
	if dir == "" {
		return fmt.Errorf("cannot determine config directory for lock")
	}
	return configlock.WithLock(dir, fn)
}

// loadConfigUnlocked reads and parses the config file without acquiring
// the config lock. Must only be called while the lock is held (e.g., within
// WithConfigLock) or by LoadConfig which acquires its own lock.
func LoadConfigUnlocked() (*LoomConfig, error) {
	path := GetConfigPath()
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is from known config directory
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	data, err = preprocessConfigBytes(path, data)
	if err != nil {
		return nil, err
	}

	var cfg LoomConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}

	if err := validateWorkspaceRepos(&cfg, path); err != nil {
		return nil, err
	}

	// Resolve DefaultWorkspaceID from the default workspace name
	if cfg.DefaultWorkspace != "" {
		if ws, ok := cfg.Workspaces[cfg.DefaultWorkspace]; ok {
			cfg.DefaultWorkspaceID = ws.ID
		}
	}

	// Run comprehensive validation if a validator is registered.
	if globalConfigValidator != nil {
		if err := globalConfigValidator(&cfg); err != nil {
			return nil, err
		}
	}

	return &cfg, nil
}

// preprocessConfigBytes runs auto-migration, env expansion, and secret resolution on raw config bytes.
func preprocessConfigBytes(path string, data []byte) ([]byte, error) {
	// Auto-migrate non-destructive config changes before parsing
	data, err := autoMigrateFile(path, data)
	if err != nil {
		return nil, err
	}

	data, err = ExpandConfigBytes(data)
	if err != nil {
		return nil, fmt.Errorf("expanding env vars in %s: %w", path, err)
	}

	resolver := NewSecretResolver()
	data, err = ResolveSecretsInBytes(data, resolver)
	if err != nil {
		return nil, fmt.Errorf("resolving secrets in %s: %w", path, err)
	}

	return data, nil
}

// validateWorkspaceRepos validates remote names and defaults SourceRepoID for all workspace repos.
func validateWorkspaceRepos(cfg *LoomConfig, path string) error {
	for wsName, ws := range cfg.Workspaces {
		for i, repo := range ws.Repos {
			if err := ValidateRemoteName(repo.Remote); err != nil {
				return fmt.Errorf("invalid config %s: workspace %q repo %d (%q): %w", path, wsName, i, repo.Name, err)
			}
			// Default SourceRepoID to Name if not explicitly set
			if repo.SourceRepoID == "" {
				ws.Repos[i].SourceRepoID = repo.Name
			}
		}
		cfg.Workspaces[wsName] = ws
	}
	return nil
}

// LoadConfig reads and parses the loom config file.
// Returns (nil, nil) if the config file does not exist.
// Returns (nil, error) on read or parse errors.
func LoadConfig() (*LoomConfig, error) {
	dir := GetConfigDir()
	if dir == "" {
		return nil, fmt.Errorf("cannot determine config directory for lock")
	}
	unlock, err := configlock.ConfigLock(dir)
	if err != nil {
		return nil, fmt.Errorf("config lock: %w", err)
	}
	defer unlock()

	return LoadConfigUnlocked()
}

// GetWorkspaceDir returns the directory path for a named workspace.
func GetWorkspaceDir(name string) string {
	return filepath.Join(GetConfigDir(), "workspaces", name)
}

// saveConfigUnlocked writes the config file without acquiring the config lock.
// Must only be called while the lock is held (e.g., within WithConfigLock)
// or by SaveConfig which acquires its own lock.
func SaveConfigUnlocked(cfg *LoomConfig) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	dir := GetConfigDir()
	if dir == "" {
		return fmt.Errorf("cannot determine config directory")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating config dir %s: %w", dir, err)
	}
	path := GetConfigPath()
	if err := atomicfile.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing config %s: %w", path, err)
	}
	InvalidateConfigCache()
	return nil
}

// SaveConfig writes the loom config to the config file.
// Creates the config directory if it doesn't exist.
func SaveConfig(cfg *LoomConfig) error {
	dir := GetConfigDir()
	if dir == "" {
		return fmt.Errorf("cannot determine config directory")
	}
	unlock, err := configlock.ConfigLock(dir)
	if err != nil {
		return fmt.Errorf("config lock: %w", err)
	}
	defer unlock()
	return SaveConfigUnlocked(cfg)
}

// IsWorkspaceMode returns true if a config file exists with at least one workspace defined.
func IsWorkspaceMode() bool {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		return false
	}
	return len(cfg.Workspaces) > 0
}

// ResolveActiveWorkspace loads the config and returns the active workspace config.
// Returns (nil, nil) if not in workspace mode (no config or no workspaces defined).
// Uses DefaultWorkspace if set, otherwise uses the first workspace in the map.
func ResolveActiveWorkspace() (*WorkspaceConfig, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	if cfg == nil || len(cfg.Workspaces) == 0 {
		return nil, nil
	}

	// Use default workspace if set
	if cfg.DefaultWorkspace != "" {
		if ws, ok := cfg.Workspaces[cfg.DefaultWorkspace]; ok {
			return &ws, nil
		}
		return nil, fmt.Errorf("default workspace %q not found in config", cfg.DefaultWorkspace)
	}

	// Use first workspace in map
	for _, ws := range cfg.Workspaces {
		wsCopy := ws
		return &wsCopy, nil
	}
	return nil, nil
}
