package terminal

import (
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestPresentationStatePersistsPreferenceWithoutInferringTerminalLifecycle(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	state := NewPresentationState(client)
	if err := state.SetActiveTab(t.Context(), "WS", "terminal-without-live-process"); err != nil {
		t.Fatalf("SetActiveTab: %v", err)
	}
	active, err := state.GetActiveTab(t.Context(), "WS")
	if err != nil {
		t.Fatalf("GetActiveTab: %v", err)
	}
	if active != "terminal-without-live-process" {
		t.Fatalf("active tab = %q, want persisted presentation preference", active)
	}
}

func TestNewPresentationStateRejectsMissingPersistence(t *testing.T) {
	if state := NewPresentationState(nil); state != nil {
		t.Fatalf("state = %#v, want nil", state)
	}
}
