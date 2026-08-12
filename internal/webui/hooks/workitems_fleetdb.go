package hooks

import (
	"fmt"
	"log/slog"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	fleet "github.com/tysonthomas9/loomcli/internal/modules/workitems/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/webui/coordinator"
)

type workspaceWorkItemsResource struct {
	*workitems.Service
	workitems.MutationStream
}

// WorkItemsFleetDBHook implements coordinator.LifecycleHook for per-workspace fleet
// backend lifecycle. On workspace registration, it creates a Work Items FleetDB adapter scoped
// to the workspace being registered and provides it to downstream hooks via the
// resource bag.
type WorkItemsFleetDBHook struct {
	baseURL string
	apiKey  string
	actor   string // X-Actor header (fleet-db --auth-dev-mode); empty = no header
	logger  *slog.Logger
}

// NewWorkItemsFleetDBHook creates a WorkItemsFleetDBHook. baseURL must not be empty.
// Every registration must supply its canonical workspace ID. actor is the
// X-Actor header value used for fleet-db's
// --auth-dev-mode (typically the loom agent name); empty means no header
// is sent and fleet-db will reject the request unless a JWT is configured
// via apiKey. A nil logger defaults to slog.Default().
func NewWorkItemsFleetDBHook(baseURL, apiKey, actor string, logger *slog.Logger) *WorkItemsFleetDBHook {
	if logger == nil {
		logger = slog.Default()
	}
	return &WorkItemsFleetDBHook{
		baseURL: baseURL,
		apiKey:  apiKey,
		actor:   actor,
		logger:  logger,
	}
}

// Name returns "workitems-fleetdb".
func (h *WorkItemsFleetDBHook) Name() string { return "workitems-fleetdb" }

// Critical returns false — fleet backend creation failure degrades fleet features
// but does not prevent the workspace from being registered.
func (h *WorkItemsFleetDBHook) Critical() bool { return false }

// OnRegister creates a Work Items FleetDB adapter for the workspace and stores it.
func (h *WorkItemsFleetDBHook) OnRegister(ctx *coordinator.RegistrationContext) error {
	id := ctx.WorkspaceID
	if id == "" {
		return fmt.Errorf("create fleet backend: workspace ID is required")
	}

	fb, err := fleet.New(fleet.Config{
		BaseURL:     h.baseURL,
		WorkspaceID: id,
		APIKey:      h.apiKey,
		Actor:       h.actor,
	})
	if err != nil {
		return fmt.Errorf("create fleet backend for %q: %w", id, err)
	}
	api, err := workitems.New(fb)
	if err != nil {
		return fmt.Errorf("compose Work Items for %q: %w", id, err)
	}

	ctx.Provide(coordinator.ResourceKeyWorkItemsFleetDB, workspaceWorkItemsResource{
		Service: api, MutationStream: fb,
	})
	h.logger.Info("created fleet backend for workspace", "workspace", id)
	return nil
}

// OnDeregister logs workspace deregistration. Resource cleanup is handled by
// the registry when the workspace handle is removed.
func (h *WorkItemsFleetDBHook) OnDeregister(ctx coordinator.DeregistrationContext) {
	h.logger.Debug("removed fleet backend for workspace", "workspace", ctx.WorkspaceID)
}

// OnRollback undoes OnRegister — same as OnDeregister.
func (h *WorkItemsFleetDBHook) OnRollback(ctx coordinator.DeregistrationContext) {
	h.OnDeregister(ctx)
}
