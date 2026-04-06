package hooks

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backend/fleet"
	"github.com/tysonthomas9/loomcli/internal/webui/coordinator"
)

// FleetBackendHook implements coordinator.LifecycleHook for per-workspace fleet
// backend lifecycle. On workspace registration, it creates a FleetBackend scoped
// to the configured fleet workspace and provides it to downstream hooks via the
// resource bag. The hook stores backends internally for consumption via
// BackendForWorkspace.
//
// All backends created by this hook use the same fleet workspace ID (from config),
// not the local workspace UUID. This ensures requests hit the correct fleet server
// workspace endpoint, matching the CLI backend path.
type FleetBackendHook struct {
	baseURL     string
	workspaceID string // fleet server workspace (e.g., "default"), not local UUID
	apiKey      string
	logger      *slog.Logger

	mu       sync.RWMutex
	backends map[string]backend.IssueBackend
}

// NewFleetBackendHook creates a FleetBackendHook. baseURL must not be empty.
// workspaceID is the fleet server workspace identifier (defaults to "default"
// if empty). A nil logger defaults to slog.Default().
func NewFleetBackendHook(baseURL, workspaceID, apiKey string, logger *slog.Logger) *FleetBackendHook {
	if logger == nil {
		logger = slog.Default()
	}
	if workspaceID == "" {
		workspaceID = "default"
	}
	return &FleetBackendHook{
		baseURL:     baseURL,
		workspaceID: workspaceID,
		apiKey:      apiKey,
		logger:      logger,
		backends:    make(map[string]backend.IssueBackend),
	}
}

// Name returns "fleet-backend".
func (h *FleetBackendHook) Name() string { return "fleet-backend" }

// Critical returns false — fleet backend creation failure degrades fleet features
// but does not prevent the workspace from being registered.
func (h *FleetBackendHook) Critical() bool { return false }

// OnRegister creates a FleetBackend for the workspace and stores it.
func (h *FleetBackendHook) OnRegister(ctx *coordinator.RegistrationContext) error {
	id := ctx.WorkspaceID

	fb, err := fleet.New(fleet.Config{
		BaseURL:     h.baseURL,
		WorkspaceID: h.workspaceID,
		APIKey:      h.apiKey,
	})
	if err != nil {
		return fmt.Errorf("create fleet backend for %q: %w", id, err)
	}

	h.mu.Lock()
	h.backends[id] = fb
	h.mu.Unlock()

	ctx.Provide(coordinator.ResourceKeyFleetBackend, fb)
	h.logger.Info("created fleet backend for workspace", "workspace", id)
	return nil
}

// OnDeregister removes the stored backend for the workspace.
func (h *FleetBackendHook) OnDeregister(ctx coordinator.DeregistrationContext) {
	h.mu.Lock()
	delete(h.backends, ctx.WorkspaceID)
	h.mu.Unlock()

	h.logger.Debug("removed fleet backend for workspace", "workspace", ctx.WorkspaceID)
}

// OnRollback undoes OnRegister — same as OnDeregister.
func (h *FleetBackendHook) OnRollback(ctx coordinator.DeregistrationContext) {
	h.OnDeregister(ctx)
}

// BackendForWorkspace returns the FleetBackend for the given workspace, or
// (nil, false) if no backend exists for that workspace.
func (h *FleetBackendHook) BackendForWorkspace(wsID string) (backend.IssueBackend, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	be, ok := h.backends[wsID]
	return be, ok
}
