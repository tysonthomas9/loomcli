package hooks

import (
	"fmt"
	"log/slog"

	"github.com/tysonthomas9/loomcli/internal/webui/coordinator"
	"github.com/tysonthomas9/loomcli/internal/webui/subscription"
)

// NotificationSubscriberHook implements coordinator.LifecycleHook for
// per-workspace DaemonSubscriber lifecycle. On workspace registration, it
// creates and starts a DaemonSubscriber that polls the workspace's daemon
// and broadcasts workspace-tagged mutations to the SSE hub.
type NotificationSubscriberHook struct {
	multiSub *subscription.MultiWorkspaceSubscriber
	logger   *slog.Logger
}

// NewNotificationSubscriberHook creates a NotificationSubscriberHook. multiSub
// must not be nil (panics). A nil logger defaults to slog.Default().
func NewNotificationSubscriberHook(multiSub *subscription.MultiWorkspaceSubscriber, logger *slog.Logger) *NotificationSubscriberHook {
	if multiSub == nil {
		panic("NewNotificationSubscriberHook: multiSub must not be nil")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &NotificationSubscriberHook{
		multiSub: multiSub,
		logger:   logger,
	}
}

// Name returns "notification-subscriber".
func (h *NotificationSubscriberHook) Name() string { return "notification-subscriber" }

// Critical returns false — a failure to start the subscriber degrades SSE
// push but does not prevent the workspace from being usable.
func (h *NotificationSubscriberHook) Critical() bool { return false }

// OnRegister provides the MultiWorkspaceSubscriber to the resource bag for
// downstream hooks but does NOT start the per-workspace subscriber — call
// Activate after the daemon is confirmed reachable to avoid priming the
// circuit breaker with connection failures during startup.
func (h *NotificationSubscriberHook) OnRegister(ctx *coordinator.RegistrationContext) error {
	ctx.Provide(coordinator.ResourceKeySubscriber, h.multiSub)
	h.logger.Debug("notification subscriber hook registered for workspace (deferred)", "workspace", ctx.WorkspaceID)
	return nil
}

// Activate starts the SSE subscriber for a workspace whose pool is already
// registered. Call this after the daemon is confirmed reachable. Safe to call
// multiple times for the same workspace (AddWorkspace replaces the existing
// subscriber).
func (h *NotificationSubscriberHook) Activate(wsID string) error {
	if wsID == "" {
		return fmt.Errorf("activate notification subscriber: empty workspace id")
	}
	if err := h.multiSub.AddWorkspace(wsID); err != nil {
		h.logger.Warn("failed to activate notification subscriber for workspace",
			"workspace", wsID, "err", err)
		return fmt.Errorf("add workspace subscriber %q: %w", wsID, err)
	}
	h.logger.Info("notification subscriber activated for workspace", "workspace", wsID)
	return nil
}

// OnDeregister stops and removes the workspace's subscriber.
func (h *NotificationSubscriberHook) OnDeregister(ctx coordinator.DeregistrationContext) {
	h.multiSub.RemoveWorkspace(ctx.WorkspaceID)
	h.logger.Debug("deregistered notification subscriber for workspace", "workspace", ctx.WorkspaceID)
}

// OnRollback undoes OnRegister — same as OnDeregister.
func (h *NotificationSubscriberHook) OnRollback(ctx coordinator.DeregistrationContext) {
	h.OnDeregister(ctx)
}
