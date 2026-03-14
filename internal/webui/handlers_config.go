package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

// validBackends is the list of supported AI backend names.
var validBackends = []string{"claude", "codex", "opencode"}

// BackendInfo describes a registered backend with metadata and health status.
type BackendInfo struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Provider    string `json:"provider"`
	Status      string `json:"status"`
	BrandColor  string `json:"brand_color"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version,omitempty"`
}

type backendsResponse struct {
	Success bool          `json:"success"`
	Data    []BackendInfo `json:"data"`
	Error   string        `json:"error,omitempty"`
}

// handleGetBackends returns a handler that lists registered backends with metadata.
// If listFn is nil, falls back to returning validBackends as basic entries.
func handleGetBackends(listFn func() ([]BackendInfo, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if listFn == nil {
			// Graceful fallback: return validBackends as basic entries
			backends := make([]BackendInfo, 0, len(validBackends))
			for _, name := range validBackends {
				backends = append(backends, BackendInfo{
					Name:   name,
					Status: "unavailable",
				})
			}
			respondJSON(w, http.StatusOK, backendsResponse{
				Success: true,
				Data:    backends,
			})
			return
		}

		backends, err := listFn()
		if err != nil {
			respondJSON(w, http.StatusInternalServerError, backendsResponse{
				Success: false,
				Error:   "failed to list backends",
			})
			return
		}

		// Ensure empty slice for JSON [] marshaling
		if backends == nil {
			backends = []BackendInfo{}
		}

		respondJSON(w, http.StatusOK, backendsResponse{
			Success: true,
			Data:    backends,
		})
	}
}

// BackendConfigResponse wraps the backend config data for JSON response.
type BackendConfigResponse struct {
	Success bool               `json:"success"`
	Data    *BackendConfigData `json:"data,omitempty"`
	Error   string             `json:"error,omitempty"`
}

// BackendConfigData is the response payload for backend config endpoints.
type BackendConfigData struct {
	Backend   string                 `json:"backend"`
	Source    string                 `json:"source"`
	Available []string               `json:"available"`
	Agents    []AgentBackendOverride `json:"agents"`
}

// AgentBackendOverride represents a per-agent backend override entry.
type AgentBackendOverride struct {
	Worktree string `json:"worktree"`
	Role     string `json:"role"`
	Backend  string `json:"backend"`
}

// BackendConfigPatchRequest is the JSON body for PATCH /api/config/backend.
type BackendConfigPatchRequest struct {
	Backend string `json:"backend"`
}

// projectFile is a local YAML struct mirroring cli.ProjectFile.
// We define it locally to avoid coupling webui to the cli package.
type projectFile struct {
	Backend string       `yaml:"backend,omitempty"`
	Daemon  yaml.Node    `yaml:"daemon,omitempty"`
	Roles   yaml.Node    `yaml:"roles,omitempty"`
	Agents  []agentEntry `yaml:"agents,omitempty"`
}

// agentEntry mirrors cli.AgentEntry for YAML serialization.
type agentEntry struct {
	Worktree string `yaml:"worktree"`
	Role     string `yaml:"role"`
	Auto     bool   `yaml:"auto,omitempty"`
	Backend  string `yaml:"backend,omitempty"`
}

// configClient is an internal interface for testing config operations.
type configClient interface {
	Status() (*rpc.StatusResponse, error)
}

// configConnectionGetter is an internal interface for testing pool operations.
type configConnectionGetter interface {
	Get(ctx context.Context) (configClient, error)
	Put(client configClient)
}

// configPoolAdapter wraps daemon.Pool to implement configConnectionGetter.
type configPoolAdapter struct {
	pool daemon.Pool
}

func (p *configPoolAdapter) Get(ctx context.Context) (configClient, error) {
	return p.pool.Get(ctx)
}

func (p *configPoolAdapter) Put(client configClient) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Put(c)
	}
}

// handleGetBackendConfig returns a handler that reads backend config from loom.yaml.
func handleGetBackendConfig(pool daemon.Pool) http.HandlerFunc {
	if pool == nil {
		return handleGetBackendConfigWithPool(nil)
	}
	return handleGetBackendConfigWithPool(&configPoolAdapter{pool: pool})
}

// handleGetBackendConfigWithPool is the internal testable implementation.
func handleGetBackendConfigWithPool(pool configConnectionGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if pool == nil {
			respondJSON(w, http.StatusServiceUnavailable, BackendConfigResponse{Success: false, Error: "connection pool not initialized"})
			return
		}

		// Get workspace path from daemon status
		wsPath, err := getWorkspacePath(pool, r.Context())
		if err != nil {
			status := http.StatusServiceUnavailable
			if errors.Is(err, context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
			}
			respondJSON(w, status, BackendConfigResponse{Success: false, Error: "daemon not available"})
			return
		}

		// Read loom.yaml
		pf, err := loadProjectFile(wsPath)
		if err != nil {
			respondJSON(w, http.StatusInternalServerError, BackendConfigResponse{Success: false, Error: fmt.Sprintf("failed to parse config: %v", err)})
			return
		}

		// Build response
		backend := pf.Backend
		source := "project"
		if backend == "" {
			backend = "claude"
			source = "default"
		}

		agents := make([]AgentBackendOverride, 0, len(pf.Agents))
		for _, a := range pf.Agents {
			agents = append(agents, AgentBackendOverride{
				Worktree: a.Worktree,
				Role:     a.Role,
				Backend:  a.Backend,
			})
		}

		respondJSON(w, http.StatusOK, BackendConfigResponse{
			Success: true,
			Data: &BackendConfigData{
				Backend:   backend,
				Source:    source,
				Available: validBackends,
				Agents:    agents,
			},
		})
	}
}

// handlePatchBackendConfig returns a handler that updates the project-level backend.
func handlePatchBackendConfig(pool daemon.Pool) http.HandlerFunc {
	if pool == nil {
		return handlePatchBackendConfigWithPool(nil)
	}
	return handlePatchBackendConfigWithPool(&configPoolAdapter{pool: pool})
}

// handlePatchBackendConfigWithPool is the internal testable implementation.
func handlePatchBackendConfigWithPool(pool configConnectionGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if pool == nil {
			respondJSON(w, http.StatusServiceUnavailable, BackendConfigResponse{Success: false, Error: "connection pool not initialized"})
			return
		}

		// Read and validate request body
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		var req BackendConfigPatchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				respondJSON(w, http.StatusRequestEntityTooLarge, BackendConfigResponse{Success: false, Error: "request body too large"})
				return
			}
			respondJSON(w, http.StatusBadRequest, BackendConfigResponse{Success: false, Error: "invalid request body"})
			return
		}

		if !isValidBackend(req.Backend) {
			respondJSON(w, http.StatusBadRequest, BackendConfigResponse{Success: false, Error: fmt.Sprintf("invalid backend %q; valid options: claude, codex, opencode", req.Backend)})
			return
		}

		// Get workspace path from daemon status
		wsPath, err := getWorkspacePath(pool, r.Context())
		if err != nil {
			status := http.StatusServiceUnavailable
			if errors.Is(err, context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
			}
			respondJSON(w, status, BackendConfigResponse{Success: false, Error: "daemon not available"})
			return
		}

		// Read existing config (or start fresh)
		pf, err := loadProjectFile(wsPath)
		if err != nil {
			respondJSON(w, http.StatusInternalServerError, BackendConfigResponse{Success: false, Error: fmt.Sprintf("failed to parse config: %v", err)})
			return
		}

		// Update backend
		pf.Backend = req.Backend

		// Write back
		if err := saveProjectFile(wsPath, pf); err != nil {
			respondJSON(w, http.StatusInternalServerError, BackendConfigResponse{Success: false, Error: "failed to save config"})
			return
		}

		// Return updated config (same format as GET)
		agents := make([]AgentBackendOverride, 0, len(pf.Agents))
		for _, a := range pf.Agents {
			agents = append(agents, AgentBackendOverride{
				Worktree: a.Worktree,
				Role:     a.Role,
				Backend:  a.Backend,
			})
		}

		respondJSON(w, http.StatusOK, BackendConfigResponse{
			Success: true,
			Data: &BackendConfigData{
				Backend:   pf.Backend,
				Source:    "project",
				Available: validBackends,
				Agents:    agents,
			},
		})
	}
}

// isValidBackend checks if the backend name is in the allowed list.
func isValidBackend(name string) bool {
	for _, b := range validBackends {
		if b == name {
			return true
		}
	}
	return false
}

// getWorkspacePath acquires a daemon connection and returns the workspace path.
func getWorkspacePath(pool configConnectionGetter, ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	client, err := pool.Get(ctx)
	if err != nil {
		return "", err
	}
	defer pool.Put(client)

	status, err := client.Status()
	if err != nil {
		return "", err
	}
	return status.WorkspacePath, nil
}

// loadProjectFile reads and parses loom.yaml from dir.
// Returns an empty projectFile if the file does not exist.
func loadProjectFile(dir string) (*projectFile, error) {
	path := filepath.Join(dir, "loom.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &projectFile{}, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var pf projectFile
	if err := yaml.Unmarshal(data, &pf); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &pf, nil
}

// saveProjectFile marshals and writes loom.yaml to dir.
func saveProjectFile(dir string, pf *projectFile) error {
	data, err := yaml.Marshal(pf)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	path := filepath.Join(dir, "loom.yaml")
	return os.WriteFile(path, data, 0644)
}
