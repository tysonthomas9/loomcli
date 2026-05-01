package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	webuterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

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
	Available []string               `json:"available"` // All backends available for tab creation (includes "shell"); PATCH only accepts AI backends (webuterminal.ValidBackends).
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
	Discard(client configClient)
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

func (p *configPoolAdapter) Discard(client configClient) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Discard(c)
	}
}

// WorkspacePathResolver resolves a workspace ID (from the {ws} path
// segment) to the workspace's filesystem path. Used by the config-backend
// handler so it can read loom.yaml without going through a daemon RPC —
// daemon-less deployments (fleet mode) don't have a Status() endpoint.
// Nil falls back to the daemon path.
type WorkspacePathResolver func(workspaceID string) (string, bool)

// HandleGetBackendConfig returns a handler that reads backend config from loom.yaml.
func HandleGetBackendConfig(pool daemon.Pool) http.HandlerFunc {
	return HandleGetBackendConfigWithResolver(pool, nil)
}

// HandleGetBackendConfigWithResolver is the daemon-less variant. When the
// resolver returns a path for the request's {ws} param, loom.yaml is read
// directly without acquiring a daemon connection. Falls back to the
// daemon-based resolution when the resolver returns false.
func HandleGetBackendConfigWithResolver(pool daemon.Pool, resolver WorkspacePathResolver) http.HandlerFunc {
	var poolGetter configConnectionGetter
	if pool != nil {
		poolGetter = &configPoolAdapter{pool: pool}
	}
	return handleGetBackendConfigWithPool(poolGetter, resolver)
}

// handleGetBackendConfigWithPool is the internal testable implementation.
func handleGetBackendConfigWithPool(pool configConnectionGetter, resolver WorkspacePathResolver) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsPath, status, err := resolveWorkspacePath(r, pool, resolver)
		if err != nil {
			handler.WriteJSON(w, status, BackendConfigResponse{Success: false, Error: err.Error()})
			return
		}

		// Read loom.yaml
		pf, err := loadProjectFile(wsPath)
		if err != nil {
			handler.WriteJSON(w, http.StatusInternalServerError, BackendConfigResponse{Success: false, Error: fmt.Sprintf("failed to parse config: %v", err)})
			return
		}

		// Build response
		backend := pf.Backend
		source := "project"
		if backend == "" {
			backend = "claude"
			source = "default"
		}

		handler.WriteJSON(w, http.StatusOK, BackendConfigResponse{
			Success: true,
			Data:    buildBackendConfigData(backend, source, pf.Agents),
		})
	}
}

// HandlePatchBackendConfig returns a handler that updates the project-level backend.
func HandlePatchBackendConfig(pool daemon.Pool) http.HandlerFunc {
	if pool == nil {
		return handlePatchBackendConfigWithPool(nil)
	}
	return handlePatchBackendConfigWithPool(&configPoolAdapter{pool: pool})
}

// handlePatchBackendConfigWithPool is the internal testable implementation.
func handlePatchBackendConfigWithPool(pool configConnectionGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if pool == nil {
			handler.WriteJSON(w, http.StatusServiceUnavailable, BackendConfigResponse{Success: false, Error: "connection pool not initialized"})
			return
		}

		backend, err := decodePatchBackendRequest(r, w)
		if err != nil {
			return // response already written
		}

		wsPath, err := getWorkspacePath(pool, r.Context())
		if err != nil {
			status := http.StatusServiceUnavailable
			if errors.Is(err, context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
			}
			handler.WriteJSON(w, status, BackendConfigResponse{Success: false, Error: "workspace service unavailable"})
			return
		}

		pf, err := loadProjectFile(wsPath)
		if err != nil {
			handler.WriteJSON(w, http.StatusInternalServerError, BackendConfigResponse{Success: false, Error: fmt.Sprintf("failed to parse config: %v", err)})
			return
		}

		pf.Backend = backend
		if err := saveProjectFile(wsPath, pf); err != nil {
			handler.WriteJSON(w, http.StatusInternalServerError, BackendConfigResponse{Success: false, Error: "failed to save config"})
			return
		}

		handler.WriteJSON(w, http.StatusOK, BackendConfigResponse{
			Success: true, Data: buildBackendConfigData(pf.Backend, "project", pf.Agents),
		})
	}
}

// decodePatchBackendRequest reads and validates the PATCH request body.
// On error, writes the HTTP response and returns a non-nil error.
func decodePatchBackendRequest(r *http.Request, w http.ResponseWriter) (string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)
	var req BackendConfigPatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			handler.WriteJSON(w, http.StatusRequestEntityTooLarge, BackendConfigResponse{Success: false, Error: "request body too large"})
			return "", err
		}
		handler.WriteJSON(w, http.StatusBadRequest, BackendConfigResponse{Success: false, Error: "invalid request body"})
		return "", err
	}
	if !isValidBackend(req.Backend) {
		err := fmt.Errorf("invalid backend %q", req.Backend)
		handler.WriteJSON(w, http.StatusBadRequest, BackendConfigResponse{Success: false, Error: fmt.Sprintf("invalid backend %q; valid options: %s", req.Backend, strings.Join(webuterminal.ValidBackends, ", "))})
		return "", err
	}
	return req.Backend, nil
}

// buildBackendConfigData constructs the response data shared by GET and PATCH handlers.
func buildBackendConfigData(backend, source string, agents []agentEntry) *BackendConfigData {
	overrides := make([]AgentBackendOverride, 0, len(agents))
	for _, a := range agents {
		overrides = append(overrides, AgentBackendOverride{
			Worktree: a.Worktree, Role: a.Role, Backend: a.Backend,
		})
	}
	available := append(append([]string(nil), webuterminal.ValidBackends...), "shell")
	return &BackendConfigData{
		Backend: backend, Source: source, Available: available, Agents: overrides,
	}
}

// isValidBackend checks if the backend name is in the allowed list.
func isValidBackend(name string) bool {
	for _, b := range webuterminal.ValidBackends {
		if b == name {
			return true
		}
	}
	return false
}

// resolveWorkspacePath returns the workspace's filesystem path for the
// request's {ws} param. Tries the resolver first (daemon-less / fleet
// path); falls back to acquiring a daemon connection and reading
// Status().WorkspacePath. Returns (path, httpStatus, err) — err is non-nil
// only when neither path produced a usable answer.
func resolveWorkspacePath(r *http.Request, pool configConnectionGetter, resolver WorkspacePathResolver) (string, int, error) {
	if resolver != nil {
		if p, ok := resolver(r.PathValue("ws")); ok && p != "" {
			return p, 0, nil
		}
	}
	if pool == nil {
		return "", http.StatusServiceUnavailable, fmt.Errorf("connection pool not initialized")
	}
	p, err := getWorkspacePath(pool, r.Context())
	if err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
		}
		return "", status, fmt.Errorf("workspace service unavailable")
	}
	return p, 0, nil
}

// getWorkspacePath acquires a daemon connection and returns the workspace path.
func getWorkspacePath(pool configConnectionGetter, ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	client, err := pool.Get(ctx)
	if err != nil {
		return "", err
	}
	rpcOK := false
	defer func() {
		if rpcOK {
			pool.Put(client)
		} else {
			pool.Discard(client)
		}
	}()

	status, err := client.Status()
	if err != nil {
		return "", err
	}
	rpcOK = true
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
