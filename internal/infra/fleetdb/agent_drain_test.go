package fleetdb

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// TestAgentUpdateHasFleetDBFieldsIncludesDrain guards the sharpest trap in the
// drain change: this predicate short-circuits the PATCH entirely, so omitting
// the drain fields here would make a drain-only stamp a silent no-op.
func TestAgentUpdateHasFleetDBFieldsIncludesDrain(t *testing.T) {
	nodeID := "loom-supervisor-h-222"
	expires := time.Now().UTC().Add(time.Hour)

	if !agentUpdateHasFleetDBFields(store.AgentUpdate{DrainNodeID: &nodeID}) {
		t.Error("a drain_node_id-only patch was treated as having no fleet-db fields")
	}
	if !agentUpdateHasFleetDBFields(store.AgentUpdate{DrainExpiresAt: &expires}) {
		t.Error("a drain_expires_at-only patch was treated as having no fleet-db fields")
	}
	if agentUpdateHasFleetDBFields(store.AgentUpdate{}) {
		t.Error("an empty patch must not be treated as having fleet-db fields")
	}
}

// TestAgentStore_UpdateSendsDrainMetadata asserts the exact JSON that reaches
// fleet-db, whose decoder is strict about unknown fields.
func TestAgentStore_UpdateSendsDrainMetadata(t *testing.T) {
	hs := newHookWireServer(t, func() any {
		return domain.Agent{WorkspaceKey: "WS", Name: "falcon", RoleName: "task"}
	})
	c := newHookClient(t, hs.URL)

	desired := domain.AgentDesiredDraining
	nodeID := "loom-supervisor-h-222"
	expires := time.Date(2026, 8, 16, 14, 0, 0, 0, time.UTC)

	if _, err := c.Agents().Update(context.Background(), "WS", "falcon", store.AgentUpdate{
		DesiredState:   &desired,
		DrainNodeID:    &nodeID,
		DrainExpiresAt: &expires,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	if hs.method != "PATCH" {
		t.Errorf("method = %s, want PATCH", hs.method)
	}
	for _, want := range []string{
		`"desired_state":"draining"`,
		`"drain_node_id":"loom-supervisor-h-222"`,
		`"drain_expires_at":"2026-08-16T14:00:00Z"`,
	} {
		if !strings.Contains(hs.body, want) {
			t.Errorf("PATCH body missing %s:\n%s", want, hs.body)
		}
	}
}

// TestAgentStore_DecodesDrainMetadata covers the read half of the round-trip.
func TestAgentStore_DecodesDrainMetadata(t *testing.T) {
	expires := time.Date(2026, 8, 16, 14, 0, 0, 0, time.UTC)
	hs := newHookWireServer(t, func() any {
		return domain.Agent{
			WorkspaceKey: "WS", Name: "falcon", RoleName: "task",
			DesiredState: domain.AgentDesiredDraining,
			DrainNodeID:  "loom-supervisor-h-222", DrainExpiresAt: &expires,
			LiveStatus: domain.AgentLiveDraining,
		}
	})
	c := newHookClient(t, hs.URL)

	got, err := c.Agents().Get(context.Background(), "WS", "falcon")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.DrainNodeID != "loom-supervisor-h-222" {
		t.Errorf("DrainNodeID = %q, want loom-supervisor-h-222", got.DrainNodeID)
	}
	if got.DrainExpiresAt == nil || !got.DrainExpiresAt.Equal(expires) {
		t.Errorf("DrainExpiresAt = %v, want %v", got.DrainExpiresAt, expires)
	}
	if got.LiveStatus != domain.AgentLiveDraining {
		t.Errorf("LiveStatus = %q, want draining", got.LiveStatus)
	}
}
