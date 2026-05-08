package epic

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestWorkerNameAddsHashToAvoidSanitizedCollisions(t *testing.T) {
	a := workerName("epic", "TASK/1")
	b := workerName("epic", "TASK:1")
	if a == b {
		t.Fatalf("workerName collision: %q", a)
	}
	if len(a) > 63 || len(b) > 63 {
		t.Fatalf("workerName length = %d/%d, want <= 63", len(a), len(b))
	}
	if strings.ContainsAny(a, "/:") || strings.ContainsAny(b, "/:") {
		t.Fatalf("workerName contains unsanitized chars: %q %q", a, b)
	}
}

func TestSelectTargetNodeIDRequiresSingleActiveNode(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	_, err := st.Nodes().Create(ctx, store.NodeCreate{
		WorkspaceKey:    "ws",
		NodeID:          "node-1",
		RuntimeProvider: domain.RuntimeProviderLocal,
		DrainState:      domain.NodeDrainActive,
		TTL:             time.Minute,
	})
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	got, err := selectTargetNodeID(ctx, st, "ws")
	if err != nil {
		t.Fatalf("selectTargetNodeID() error = %v", err)
	}
	if got != "node-1" {
		t.Fatalf("selectTargetNodeID() = %q, want node-1", got)
	}

	_, err = st.Nodes().Create(ctx, store.NodeCreate{
		WorkspaceKey:    "ws",
		NodeID:          "node-2",
		RuntimeProvider: domain.RuntimeProviderLocal,
		DrainState:      domain.NodeDrainActive,
		TTL:             time.Minute,
	})
	if err != nil {
		t.Fatalf("create second node: %v", err)
	}
	if _, err := selectTargetNodeID(ctx, st, "ws"); err == nil {
		t.Fatal("selectTargetNodeID() error = nil, want multiple-node error")
	}
}
