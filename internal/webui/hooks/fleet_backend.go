package hooks

import (
	"fmt"
	"log/slog"

	"github.com/tysonthomas9/loomcli/internal/backend/fleet"
	"github.com/tysonthomas9/loomcli/internal/webui/coordinator"
)

// FleetBackendHook implements coordinator.LifecycleHook for per-workspace fleet
// backend lifecycle. On workspace registration, it creates a FleetBackend scoped
// to the configured fleet workspace and provides it to downstream hooks via the
// resource bag.
//
// All backends created by this hook use the same fleet workspace ID (from config),
// not the local workspace UUID. This ensures requests hit the correct fleet server
// workspace endpoint, matching the CLI backend path.
type FleetBackendHook struct {
	baseURL     string
	workspaceID string // fleet server workspace (e.g., "default"), not local UUID
	apiKey      string
	actor       string // X-Actor header (fleet-db --auth-dev-mode); empty = no header
	logger      *slog.Logger
}

// NewFleetBackendHook creates a FleetBackendHook. baseURL must not be empty.
// workspaceID is the fleet server workspace identifier (defaults to "default"
// if empty). actor is the X-Actor header value used for fleet-db's
// --auth-dev-mode (typically the loom agent name); empty means no header
// is sent and fleet-db will reject the request unless a JWT is configured
// via apiKey. A nil logger defaults to slog.Default().
func NewFleetBackendHook(baseURL, workspaceID, apiKey, actor string, logger *slog.Logger) *FleetBackendHook {
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
		actor:       actor,
		logger:      logger,
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
		Actor:       h.actor,
	})
	if err != nil {
		return fmt.Errorf("create fleet backend for %q: %w", id, err)
	}

	ctx.Provide(coordinator.ResourceKeyFleetBackend, fb)
	h.logger.Info("created fleet backend for workspace", "workspace", id)
	return nil
}

// OnDeregister logs workspace deregistration. Resource cleanup is handled by
// the registry when the workspace handle is removed.
func (h *FleetBackendHook) OnDeregister(ctx coordinator.DeregistrationContext) {
	h.logger.Debug("removed fleet backend for workspace", "workspace", ctx.WorkspaceID)
}

// OnRollback undoes OnRegister — same as OnDeregister.
func (h *FleetBackendHook) OnRollback(ctx coordinator.DeregistrationContext) {
	h.OnDeregister(ctx)
}
