package hooks

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/webui/coordinator"
	"github.com/tysonthomas9/loomcli/internal/webui/subscription"
)

// FleetSubscriberHook implements coordinator.LifecycleHook for per-workspace
// BackendMutationSubscriber lifecycle. It uses the same deferred-Activate
// pattern as other workspace hooks and sources mutations from a fleet
// IssueBackend provided by FleetBackendHook on OnRegister.
//
// Hook ordering matters: FleetBackendHook must register first so that by
// the time Activate fires, ResourceKeyFleetBackend is in the workspace's
// resource bag. app.RegisterHooks enforces this.
type FleetSubscriberHook struct {
	multiSub *subscription.MultiWorkspaceSubscriber
	registry *coordinator.WorkspaceRegistry
	logger   *slog.Logger
}

// NewFleetSubscriberHook constructs a FleetSubscriberHook. multiSub and
// registry must not be nil (panics). A nil logger defaults to slog.Default().
func NewFleetSubscriberHook(multiSub *subscription.MultiWorkspaceSubscriber, registry *coordinator.WorkspaceRegistry, logger *slog.Logger) *FleetSubscriberHook {
	if multiSub == nil {
		panic("NewFleetSubscriberHook: multiSub must not be nil")
	}
	if registry == nil {
		panic("NewFleetSubscriberHook: registry must not be nil")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &FleetSubscriberHook{
		multiSub: multiSub,
		registry: registry,
		logger:   logger,
	}
}

// Name returns "fleet-subscriber".
func (h *FleetSubscriberHook) Name() string { return "fleet-subscriber" }

// Critical returns false — failure to start the fleet subscriber degrades
// SSE push but does not prevent the workspace from being usable (REST
// endpoints continue to work; the SPA falls back to polling).
func (h *FleetSubscriberHook) Critical() bool { return false }

// OnRegister is a no-op. Subscriber start is deferred to Activate so the SSE
// token/stream routes can start it lazily instead of opening one long-poll per
// workspace at startup. Connected SSE clients retain the subscriber; the
// MultiWorkspaceSubscriber idle loop tears it down after the grace period.
func (h *FleetSubscriberHook) OnRegister(ctx *coordinator.RegistrationContext) error {
	h.logger.Debug("fleet subscriber hook registered for workspace (deferred)", "workspace", ctx.WorkspaceID)
	return nil
}

// Activate is the subscriberActivator interface method called by
// WorkspaceRegistry.ActivateSubscriber when an SSE route opens for a workspace.
// It looks up the FleetBackend that FleetBackendHook previously stored on the
// workspace handle and hands it to the
// MultiWorkspaceSubscriber to spin up a long-poll loop.
//
// Idempotent: if a subscriber already exists for wsID, returns nil
// without replacing it.
//
// Returns an error only when the resource lookup or subscriber start
// genuinely fails. A workspace that was deregistered between the SSE
// handler's lookup and this call yields a nil handle, which we treat as
// a no-op (mirrors WorkspaceRegistry.ActivateSubscriber's "best-effort"
// contract — must not resurrect a torn-down workspace).
func (h *FleetSubscriberHook) Activate(wsID string) error {
	if wsID == "" {
		return fmt.Errorf("activate fleet subscriber: empty workspace id")
	}
	if h.multiSub.HasSubscriber(wsID) {
		return nil
	}

	handle := h.registry.ForWorkspace(wsID)
	if handle == nil {
		// Workspace was deregistered between SSE open and Activate.
		h.logger.Debug("fleet subscriber activate skipped: workspace not registered",
			"workspace", wsID)
		return nil
	}

	res, ok := handle.Resource(coordinator.ResourceKeyFleetBackend)
	if !ok {
		// FleetBackendHook not registered for this workspace — possible
		// when fleet mode is off but the hook somehow ran. Log and skip
		// rather than crash.
		h.logger.Warn("fleet subscriber activate: no fleet backend resource on workspace handle",
			"workspace", wsID)
		return nil
	}
	be, ok := res.(backend.IssueBackend)
	if !ok {
		return fmt.Errorf("activate fleet subscriber: workspace %q resource is not backend.IssueBackend (got %T)", wsID, res)
	}

	if err := h.multiSub.EnsureActive(context.Background(), wsID, be, subscription.ActivationReasonRegistry); err != nil {
		h.logger.Warn("failed to activate fleet subscriber for workspace",
			"workspace", wsID, "err", err)
		return fmt.Errorf("add workspace fleet subscriber %q: %w", wsID, err)
	}

	h.logger.Info("fleet subscriber activated for workspace", "workspace", wsID)
	return nil
}

// OnDeregister stops and removes the workspace's subscriber.
func (h *FleetSubscriberHook) OnDeregister(ctx coordinator.DeregistrationContext) {
	h.multiSub.RemoveWorkspace(ctx.WorkspaceID)
	h.logger.Debug("deregistered fleet subscriber for workspace", "workspace", ctx.WorkspaceID)
}

// OnRollback undoes OnRegister — same as OnDeregister.
func (h *FleetSubscriberHook) OnRollback(ctx coordinator.DeregistrationContext) {
	h.OnDeregister(ctx)
}
