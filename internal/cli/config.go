package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// LoomConfig is the top-level configuration from ~/.loom/config.yaml
type LoomConfig struct {
	DefaultWorkspace string                     `yaml:"default_workspace,omitempty"`
	Workspaces       map[string]WorkspaceConfig `yaml:"workspaces"`
}

// WorkspaceConfig defines a named workspace containing multiple repos
type WorkspaceConfig struct {
	Path  string       `yaml:"path"`  // Directory path for this workspace
	Repos []RepoConfig `yaml:"repos"` // Repositories in this workspace
}

// RepoConfig defines a single repository within a workspace
type RepoConfig struct {
	Name          string `yaml:"name"`                     // Display name / identifier
	Path          string `yaml:"path"`                     // Path to the repo (absolute or relative to workspace)
	DefaultBranch string `yaml:"default_branch,omitempty"` // Override default branch (defaults to "main")
	Remote        string `yaml:"remote,omitempty"`         // Git remote name (defaults to "origin")
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

// LoadConfig reads and parses the loom config file.
// Returns (nil, nil) if the config file does not exist.
// Returns (nil, error) on read or parse errors.
func LoadConfig() (*LoomConfig, error) {
	path := GetConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	var cfg LoomConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}
	return &cfg, nil
}

// GetWorkspaceDir returns the directory path for a named workspace.
func GetWorkspaceDir(name string) string {
	return filepath.Join(GetConfigDir(), "workspaces", name)
}

// SaveConfig writes the loom config to the config file.
// Creates the config directory if it doesn't exist.
func SaveConfig(cfg *LoomConfig) error {
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
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing config %s: %w", path, err)
	}
	return nil
}

// IsWorkspaceMode returns true if a config file exists with at least one workspace defined.
func IsWorkspaceMode() bool {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		return false
	}
	return len(cfg.Workspaces) > 0
}
