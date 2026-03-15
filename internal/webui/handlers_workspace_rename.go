package webui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// WorkspaceRenameRequest is the JSON body for PATCH /api/workspace/rename.
type WorkspaceRenameRequest struct {
	OldName string `json:"old_name"`
	NewName string `json:"new_name"`
}

const maxWorkspaceNameLen = 64

// validWorkspaceName checks that a workspace name contains only safe characters
// (alphanumeric, hyphens, underscores). Mirrors cli.isValidWorkspaceName.
func validWorkspaceName(name string) bool {
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// loomConfigForRename is a minimal representation of ~/.loom/config.yaml
// sufficient for workspace rename operations. Uses yaml.Node for fields
// we don't need to interpret, preserving them on round-trip.
type loomConfigForRename struct {
	Version          int                               `yaml:"version,omitempty"`
	DefaultWorkspace string                            `yaml:"default_workspace,omitempty"`
	Backend          string                            `yaml:"backend,omitempty"`
	Workspaces       map[string]loomWorkspaceForRename `yaml:"workspaces"`
	Daemon           yaml.Node                         `yaml:"daemon,omitempty"`
}

// loomWorkspaceForRename preserves all workspace fields via yaml.Node round-trip.
type loomWorkspaceForRename struct {
	Path  string    `yaml:"path"`
	Repos yaml.Node `yaml:"repos,omitempty"`
}

// loomConfigDir returns the loom config directory path (~/.loom or LOOM_CONFIG_DIR).
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

// loadLoomConfig reads and parses ~/.loom/config.yaml for rename operations.
func loadLoomConfig() (*loomConfigForRename, error) {
	path := filepath.Join(loomConfigDir(), "config.yaml")
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

// saveLoomConfig writes the config back to ~/.loom/config.yaml using atomic write
// (write to temp file, then rename) to prevent corruption on crash or concurrent access.
func saveLoomConfig(cfg *loomConfigForRename) error {
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

// validateWorkspaceRenameRequest validates the rename request fields.
// Returns an HTTP status code and error message, or 0/"" if valid.
func validateWorkspaceRenameRequest(req *WorkspaceRenameRequest) (int, string) {
	if req.NewName == "" {
		return http.StatusBadRequest, "name cannot be empty"
	}
	if len(req.NewName) > maxWorkspaceNameLen {
		return http.StatusBadRequest, fmt.Sprintf("name too long (max %d characters)", maxWorkspaceNameLen)
	}
	if !validWorkspaceName(req.NewName) {
		return http.StatusBadRequest, "name must contain only alphanumeric characters, hyphens, and underscores"
	}
	return 0, ""
}

// handleWorkspaceRename returns a handler that renames a workspace in the global config.
// After renaming, it calls workspaceConfigFn to return refreshed workspace data.
func handleWorkspaceRename(workspaceConfigFn func() (*WorkspaceData, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)

		var req WorkspaceRenameRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				respondJSON(w, http.StatusRequestEntityTooLarge, workspaceResponse{Success: false, Error: "request body too large"})
				return
			}
			respondJSON(w, http.StatusBadRequest, workspaceResponse{Success: false, Error: "invalid request body"})
			return
		}

		if status, msg := validateWorkspaceRenameRequest(&req); status != 0 {
			respondJSON(w, status, workspaceResponse{Success: false, Error: msg})
			return
		}

		cfg, err := loadLoomConfig()
		if err != nil {
			respondJSON(w, http.StatusInternalServerError, workspaceResponse{Success: false, Error: "failed to load config"})
			return
		}
		if cfg == nil {
			respondJSON(w, http.StatusNotFound, workspaceResponse{Success: false, Error: "no config found"})
			return
		}

		ws, ok := cfg.Workspaces[req.OldName]
		if !ok {
			respondJSON(w, http.StatusNotFound, workspaceResponse{Success: false, Error: fmt.Sprintf("workspace %q not found", req.OldName)})
			return
		}

		if req.OldName == req.NewName {
			respondJSON(w, http.StatusOK, workspaceResponse{Success: true})
			return
		}

		if _, exists := cfg.Workspaces[req.NewName]; exists {
			respondJSON(w, http.StatusConflict, workspaceResponse{Success: false, Error: "workspace name already exists"})
			return
		}

		// Perform rename: delete old, insert new
		delete(cfg.Workspaces, req.OldName)
		cfg.Workspaces[req.NewName] = ws

		if cfg.DefaultWorkspace == req.OldName {
			cfg.DefaultWorkspace = req.NewName
		}

		if err := saveLoomConfig(cfg); err != nil {
			respondJSON(w, http.StatusInternalServerError, workspaceResponse{Success: false, Error: "failed to save config"})
			return
		}

		// Return refreshed workspace data
		if workspaceConfigFn != nil {
			data, err := workspaceConfigFn()
			if err == nil && data != nil {
				normalizeWorkspaceData(data)
			}
			respondJSON(w, http.StatusOK, workspaceResponse{Success: true, Data: data})
			return
		}

		respondJSON(w, http.StatusOK, workspaceResponse{Success: true})
	}
}
