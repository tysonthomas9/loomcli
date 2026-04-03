package service

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// loomConfigForRename is a minimal representation of ~/.loom/config.yaml
// sufficient for workspace rename operations.
type loomConfigForRename struct {
	Version          int                               `yaml:"version,omitempty"`
	DefaultWorkspace string                            `yaml:"default_workspace,omitempty"`
	WorkspaceOrder   []string                          `yaml:"workspace_order,omitempty"`
	Backend          string                            `yaml:"backend,omitempty"`
	Workspaces       map[string]loomWorkspaceForRename `yaml:"workspaces"`
	Daemon           yaml.Node                         `yaml:"daemon,omitempty"`
}

type loomWorkspaceForRename struct {
	ID      string    `yaml:"id,omitempty"`
	Path    string    `yaml:"path"`
	Backend string    `yaml:"backend,omitempty"`
	Repos   yaml.Node `yaml:"repos,omitempty"`
}

func loomConfigDir() string {
	if dir := os.Getenv("LOOM_CONFIG_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".loom")
}

func loadLoomConfigUnlocked() (*loomConfigForRename, error) {
	dir := loomConfigDir()
	path := filepath.Join(dir, "config.yaml")
	data, err := os.ReadFile(path) //nolint:gosec // path constructed from known config dir + fixed filename
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var cfg loomConfigForRename
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &cfg, nil
}

func saveLoomConfigUnlocked(cfg *loomConfigForRename) error {
	dir := loomConfigDir()
	if dir == "" {
		return fmt.Errorf("cannot determine config directory")
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "config-*.yaml.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("closing temp file: %w", err)
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("renaming temp file: %w", err)
	}
	return nil
}

func resolveWorkspaceNameByID(cfg *loomConfigForRename, wsID string) (string, loomWorkspaceForRename, bool) {
	for name, ws := range cfg.Workspaces {
		if ws.ID == wsID {
			return name, ws, true
		}
	}
	return "", loomWorkspaceForRename{}, false
}

func applyWorkspaceRename(cfg *loomConfigForRename, oldName, newName string, ws loomWorkspaceForRename) {
	delete(cfg.Workspaces, oldName)
	cfg.Workspaces[newName] = ws

	if cfg.DefaultWorkspace == oldName {
		cfg.DefaultWorkspace = newName
	}

	for i, n := range cfg.WorkspaceOrder {
		if n == oldName {
			cfg.WorkspaceOrder[i] = newName
			break
		}
	}
}

// --- Backend config file operations ---

type loomConfigForBackend struct {
	Version          int                                `yaml:"version,omitempty"`
	DefaultWorkspace string                             `yaml:"default_workspace,omitempty"`
	WorkspaceOrder   []string                           `yaml:"workspace_order,omitempty"`
	Backend          string                             `yaml:"backend,omitempty"`
	Workspaces       map[string]loomWorkspaceForBackend `yaml:"workspaces"`
	Daemon           yaml.Node                          `yaml:"daemon,omitempty"`
}

type loomWorkspaceForBackend struct {
	ID      string    `yaml:"id,omitempty"`
	Path    string    `yaml:"path"`
	Backend string    `yaml:"backend,omitempty"`
	Repos   yaml.Node `yaml:"repos,omitempty"`
}

func loadLoomConfigForBackendUnlocked() (*loomConfigForBackend, error) {
	dir := loomConfigDir()
	path := filepath.Join(dir, "config.yaml")
	data, err := os.ReadFile(path) //nolint:gosec // path constructed from known config dir + fixed filename
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var cfg loomConfigForBackend
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &cfg, nil
}

func saveLoomConfigForBackendUnlocked(cfg *loomConfigForBackend) error {
	dir := loomConfigDir()
	if dir == "" {
		return fmt.Errorf("cannot determine config directory")
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "config-*.yaml.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("closing temp file: %w", err)
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("renaming temp file: %w", err)
	}
	return nil
}

func resolveWorkspaceNameByIDForBackend(cfg *loomConfigForBackend, wsID string) (string, loomWorkspaceForBackend, bool) {
	for name, ws := range cfg.Workspaces {
		if ws.ID == wsID {
			return name, ws, true
		}
	}
	return "", loomWorkspaceForBackend{}, false
}
