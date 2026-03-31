package webui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/tysonthomas9/loomcli/internal/configlock"
)

// WorkspaceBackendPatchRequest is the JSON body for PATCH /api/workspaces/{ws}/config/backend.
type WorkspaceBackendPatchRequest struct {
	Backend string `json:"backend"`
}

// loomConfigForBackend is a minimal representation of ~/.loom/config.yaml
// sufficient for per-workspace backend update operations. Uses yaml.Node for fields
// we don't need to interpret, preserving them on round-trip.
type loomConfigForBackend struct {
	Version          int                                `yaml:"version,omitempty"`
	DefaultWorkspace string                             `yaml:"default_workspace,omitempty"`
	WorkspaceOrder   []string                           `yaml:"workspace_order,omitempty"`
	Backend          string                             `yaml:"backend,omitempty"`
	Workspaces       map[string]loomWorkspaceForBackend `yaml:"workspaces"`
	Daemon           yaml.Node                          `yaml:"daemon,omitempty"`
}

// loomWorkspaceForBackend preserves all workspace fields via yaml.Node round-trip,
// while exposing the Backend field for modification.
type loomWorkspaceForBackend struct {
	ID      string    `yaml:"id,omitempty"`
	Path    string    `yaml:"path"`
	Backend string    `yaml:"backend,omitempty"`
	Repos   yaml.Node `yaml:"repos,omitempty"`
}

// loadLoomConfigForBackend reads and parses ~/.loom/config.yaml for backend operations.
func loadLoomConfigForBackend() (*loomConfigForBackend, error) {
	dir := loomConfigDir()
	unlock, err := configlock.ConfigLock(dir)
	if err != nil {
		return nil, fmt.Errorf("config lock: %w", err)
	}
	defer unlock()

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

// saveLoomConfigForBackend writes the config back to ~/.loom/config.yaml using atomic write.
func saveLoomConfigForBackend(cfg *loomConfigForBackend) error {
	dir := loomConfigDir()
	if dir == "" {
		return fmt.Errorf("cannot determine config directory")
	}
	unlock, err := configlock.ConfigLock(dir)
	if err != nil {
		return fmt.Errorf("config lock: %w", err)
	}
	defer unlock()

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

// resolveWorkspaceNameByIDForBackend looks up a workspace name from the config by matching the UUID.
func resolveWorkspaceNameByIDForBackend(cfg *loomConfigForBackend, wsID string) (string, loomWorkspaceForBackend, bool) {
	for name, ws := range cfg.Workspaces {
		if ws.ID == wsID {
			return name, ws, true
		}
	}
	return "", loomWorkspaceForBackend{}, false
}

// handleWorkspaceBackendPatch returns a handler that updates a workspace's backend
// in the global config. The workspace is identified by UUID from WorkspaceMiddleware context.
// After updating, it calls workspaceConfigFn to return refreshed workspace data.
func handleWorkspaceBackendPatch(workspaceConfigFn func() (*WorkspaceData, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := WorkspaceFromContext(r.Context())
		if wsID == "" {
			respondJSON(w, http.StatusBadRequest, workspaceResponse{Success: false, Error: "workspace ID is required"})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)

		var req WorkspaceBackendPatchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				respondJSON(w, http.StatusRequestEntityTooLarge, workspaceResponse{Success: false, Error: "request body too large"})
				return
			}
			respondJSON(w, http.StatusBadRequest, workspaceResponse{Success: false, Error: "invalid request body"})
			return
		}

		if req.Backend == "" {
			respondJSON(w, http.StatusBadRequest, workspaceResponse{Success: false, Error: "backend is required"})
			return
		}

		if !isValidBackend(req.Backend) {
			respondJSON(w, http.StatusBadRequest, workspaceResponse{
				Success: false,
				Error:   fmt.Sprintf("invalid backend %q; valid options: %v", req.Backend, validBackends),
			})
			return
		}

		cfg, err := loadLoomConfigForBackend()
		if err != nil {
			respondJSON(w, http.StatusInternalServerError, workspaceResponse{Success: false, Error: "failed to load config"})
			return
		}
		if cfg == nil {
			respondJSON(w, http.StatusNotFound, workspaceResponse{Success: false, Error: "no config found"})
			return
		}

		name, ws, found := resolveWorkspaceNameByIDForBackend(cfg, wsID)
		if !found {
			respondJSON(w, http.StatusNotFound, workspaceResponse{Success: false, Error: fmt.Sprintf("workspace with ID %q not found", wsID)})
			return
		}

		ws.Backend = req.Backend
		cfg.Workspaces[name] = ws

		if err := saveLoomConfigForBackend(cfg); err != nil {
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
