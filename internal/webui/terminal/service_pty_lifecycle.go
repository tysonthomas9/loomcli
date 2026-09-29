package terminal

import (
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

type lifecycleMutationBroadcaster interface {
	Broadcast(*realtime.MutationPayload)
}

type ptyLifecycleBroadcaster struct {
	broadcaster lifecycleMutationBroadcaster
	now         func() time.Time
}

// NewPTYLifecycleBroadcaster adapts PTY lifecycle observations to the
// workspace-scoped generic SSE envelope.
func NewPTYLifecycleBroadcaster(hub *realtime.Hub) PTYLifecycleObserver {
	if hub == nil {
		return nil
	}
	return newPTYLifecycleBroadcaster(hub, time.Now)
}

func newPTYLifecycleBroadcaster(b lifecycleMutationBroadcaster, now func() time.Time) PTYLifecycleObserver {
	if b == nil {
		return nil
	}
	return &ptyLifecycleBroadcaster{broadcaster: b, now: now}
}

func (b *ptyLifecycleBroadcaster) OnPTYLifecycle(event PTYLifecycleEvent) {
	ptyAlive := event.PTYAlive
	agent := event.Agent
	b.broadcaster.Broadcast(&realtime.MutationPayload{
		Type:        "terminal_session_change",
		EntityType:  "terminal",
		EntityID:    event.Key.Name,
		Action:      event.Action,
		Timestamp:   b.now().UTC().Format(time.RFC3339Nano),
		WorkspaceID: event.Key.Workspace,
		PTYAlive:    &ptyAlive,
		ExitReason:  event.ExitReason,
		Kind:        event.Kind,
		Agent:       &agent,
	})
}
