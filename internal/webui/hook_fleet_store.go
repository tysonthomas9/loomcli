package webui

import (
	"fmt"
	"log/slog"

	"github.com/tysonthomas9/loomcli/internal/webui/coordinator"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
)

// FleetStoreHook implements coordinator.LifecycleHook for per-workspace fleet
// store lifecycle. On workspace registration, it registers the workspace in the
// fleet StoreRegistry and provides the fleet Store to downstream hooks via the
// resource bag. On deregistration, it removes the workspace from fleet.
type FleetStoreHook struct {
	registry *fleet.StoreRegistry
	logger   *slog.Logger
}

// NewFleetStoreHook creates a FleetStoreHook. registry must not be nil (panics).
// A nil logger defaults to slog.Default().
func NewFleetStoreHook(registry *fleet.StoreRegistry, logger *slog.Logger) *FleetStoreHook {
	if registry == nil {
		panic("NewFleetStoreHook: registry must not be nil")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &FleetStoreHook{
		registry: registry,
		logger:   logger,
	}
}

// Name returns "fleet-store".
func (h *FleetStoreHook) Name() string { return "fleet-store" }

// Critical returns false — fleet registration failure degrades fleet features
// but does not prevent the workspace from being usable.
func (h *FleetStoreHook) Critical() bool { return false }

// OnRegister registers the workspace in the fleet StoreRegistry and provides
// the fleet Store to the resource bag for downstream hooks.
func (h *FleetStoreHook) OnRegister(ctx *coordinator.RegistrationContext) error {
	id := ctx.WorkspaceID

	if err := h.registry.Register(id); err != nil {
		return fmt.Errorf("register workspace %q in fleet: %w", id, err)
	}

	store, ok := h.registry.Get(id)
	if ok && store != nil {
		ctx.Provide(coordinator.ResourceKeyFleetStore, store)
	}

	h.logger.Info("registered workspace in fleet store", "workspace", id)
	return nil
}

// OnDeregister removes the workspace from the fleet StoreRegistry.
func (h *FleetStoreHook) OnDeregister(ctx coordinator.DeregistrationContext) {
	h.registry.Deregister(ctx.WorkspaceID)
	h.logger.Debug("deregistered workspace from fleet store", "workspace", ctx.WorkspaceID)
}

// OnRollback undoes OnRegister — same as OnDeregister.
func (h *FleetStoreHook) OnRollback(ctx coordinator.DeregistrationContext) {
	h.OnDeregister(ctx)
}
