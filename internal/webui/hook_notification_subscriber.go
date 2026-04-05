package webui

import (
	"fmt"
	"log/slog"

	"github.com/tysonthomas9/loomcli/internal/webui/coordinator"
)

// NotificationSubscriberHook implements coordinator.LifecycleHook for
// per-workspace DaemonSubscriber lifecycle. On workspace registration, it
// creates and starts a DaemonSubscriber that polls the workspace's daemon
// and broadcasts workspace-tagged mutations to the SSE hub.
type NotificationSubscriberHook struct {
	multiSub *MultiWorkspaceSubscriber
	logger   *slog.Logger
}

// NewNotificationSubscriberHook creates a NotificationSubscriberHook. multiSub
// must not be nil (panics). A nil logger defaults to slog.Default().
func NewNotificationSubscriberHook(multiSub *MultiWorkspaceSubscriber, logger *slog.Logger) *NotificationSubscriberHook {
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

// OnRegister creates and starts a DaemonSubscriber for the workspace by
// delegating to MultiWorkspaceSubscriber.AddWorkspace. Provides the
// multi-subscriber to the resource bag for downstream hooks.
func (h *NotificationSubscriberHook) OnRegister(ctx *coordinator.RegistrationContext) error {
	id := ctx.WorkspaceID

	if err := h.multiSub.AddWorkspace(id); err != nil {
		return fmt.Errorf("add workspace subscriber %q: %w", id, err)
	}

	ctx.Provide(coordinator.ResourceKeySubscriber, h.multiSub)
	h.logger.Info("registered notification subscriber for workspace", "workspace", id)
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
