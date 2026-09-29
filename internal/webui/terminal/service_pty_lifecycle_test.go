package terminal

import (
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

type recordingMutationBroadcaster struct {
	payloads []*realtime.MutationPayload
}

func (b *recordingMutationBroadcaster) Broadcast(payload *realtime.MutationPayload) {
	b.payloads = append(b.payloads, payload)
}

func TestPTYLifecycleBroadcasterConvertsToGenericEnvelope(t *testing.T) {
	broadcaster := &recordingMutationBroadcaster{}
	now := time.Date(2026, time.September, 2, 12, 34, 56, 123, time.UTC)
	observer := newPTYLifecycleBroadcaster(broadcaster, func() time.Time { return now })

	observer.OnPTYLifecycle(PTYLifecycleEvent{
		Key:        SessionKey{Workspace: "ws-1", Name: "shell-1"},
		Action:     PTYLifecycleExited,
		PTYAlive:   false,
		ExitReason: ExitReasonExited,
		Kind:       PTYKind,
		Agent:      false,
	})

	if len(broadcaster.payloads) != 1 {
		t.Fatalf("broadcast count = %d, want 1", len(broadcaster.payloads))
	}
	got := broadcaster.payloads[0]
	if got.Type != "terminal_session_change" || got.EntityType != "terminal" || got.EntityID != "shell-1" {
		t.Errorf("identity envelope = %+v", got)
	}
	if got.Action != PTYLifecycleExited || got.WorkspaceID != "ws-1" || got.Timestamp != now.Format(time.RFC3339Nano) {
		t.Errorf("routing envelope = %+v", got)
	}
	if got.PTYAlive == nil || *got.PTYAlive {
		t.Errorf("pty_alive = %v, want explicit false", got.PTYAlive)
	}
	if got.ExitReason != ExitReasonExited || got.Kind != PTYKind {
		t.Errorf("lifecycle fields = %+v", got)
	}
	if got.Agent == nil || *got.Agent {
		t.Errorf("agent = %v, want explicit false", got.Agent)
	}
	if got.IssueID != "" {
		t.Errorf("issue_id = %q, want empty", got.IssueID)
	}
}
