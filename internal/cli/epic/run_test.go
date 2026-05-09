package epic

import (
	"context"
	"errors"
	"strings"
	"sync"
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

func TestBindLeadAgentAssignsEmptyParent(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	createTestLead(t, st, "ws", "nova", "", "")

	leadName, orch, err := bindLeadAgent(ctx, st, "ws", "nova", "EPIC-1", "session-1", true)
	if err != nil {
		t.Fatalf("bindLeadAgent() error = %v", err)
	}
	if leadName != "nova" || orch != "session-1" {
		t.Fatalf("bindLeadAgent() = (%q, %q), want nova/session-1", leadName, orch)
	}
	got, err := st.Agents().Get(ctx, "ws", "nova")
	if err != nil {
		t.Fatalf("get lead: %v", err)
	}
	if got.Parent != "EPIC-1" {
		t.Fatalf("lead parent = %q, want EPIC-1", got.Parent)
	}
	if got.OrchestratorSessionID != "session-1" {
		t.Fatalf("lead orchestrator = %q, want session-1", got.OrchestratorSessionID)
	}
}

func TestBindLeadAgentAllowsSameParent(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	createTestLead(t, st, "ws", "nova", "EPIC-1", "session-1")

	_, orch, err := bindLeadAgent(ctx, st, "ws", "nova", "EPIC-1", "", true)
	if err != nil {
		t.Fatalf("bindLeadAgent() error = %v", err)
	}
	if orch != "session-1" {
		t.Fatalf("orchestrator = %q, want existing session-1", orch)
	}
}

func TestBindLeadAgentRejectsDifferentParent(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	createTestLead(t, st, "ws", "nova", "EPIC-1", "")

	_, _, err := bindLeadAgent(ctx, st, "ws", "nova", "EPIC-2", "", true)
	if err == nil {
		t.Fatal("bindLeadAgent() error = nil, want conflict")
	}
	if !strings.Contains(err.Error(), "already running epic EPIC-1") {
		t.Fatalf("error = %v, want active epic message", err)
	}
}

func TestBindLeadAgentRejectsNonLeadRole(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "ws",
		Name:         "worker",
		RoleName:     "task",
	}); err != nil {
		t.Fatalf("create task agent: %v", err)
	}

	_, _, err := bindLeadAgent(ctx, st, "ws", "worker", "EPIC-1", "", true)
	if err == nil {
		t.Fatal("bindLeadAgent() error = nil, want non-lead role error")
	}
	if !strings.Contains(err.Error(), "requires a lead agent") {
		t.Fatalf("error = %v, want lead role message", err)
	}
}

func TestBindLeadAgentDryRunDoesNotAssignParent(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	createTestLead(t, st, "ws", "nova", "", "")

	if _, _, err := bindLeadAgent(ctx, st, "ws", "nova", "EPIC-1", "", false); err != nil {
		t.Fatalf("bindLeadAgent() error = %v", err)
	}
	got, err := st.Agents().Get(ctx, "ws", "nova")
	if err != nil {
		t.Fatalf("get lead: %v", err)
	}
	if got.Parent != "" {
		t.Fatalf("lead parent = %q, want empty in dry-run", got.Parent)
	}
}

func TestBindLeadAgentMissingLead(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	_, _, err := bindLeadAgent(ctx, st, "ws", "missing", "EPIC-1", "", true)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("bindLeadAgent() error = %v, want ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "was not found") {
		t.Fatalf("error = %v, want not found message", err)
	}
}

func TestBindLeadAgentSerializesConcurrentParentClaims(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	createTestLead(t, st, "ws", "nova", "", "")

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	parents := []string{"EPIC-1", "EPIC-2"}
	for _, parent := range parents {
		parent := parent
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := bindLeadAgent(ctx, st, "ws", "nova", parent, "", true)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	successes := 0
	conflicts := 0
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case strings.Contains(err.Error(), "already running epic"):
			conflicts++
		default:
			t.Fatalf("unexpected bind error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes/conflicts = %d/%d, want 1/1", successes, conflicts)
	}
}

func TestSelectTargetNodeIDRequiresSingleActiveNode(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
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

func newTestStore(t *testing.T) store.Store {
	t.Helper()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	return memstore.New()
}

func createTestLead(t *testing.T, st store.Store, workspace, name, parent, orchestrator string) {
	t.Helper()
	_, err := st.Agents().Create(context.Background(), store.AgentCreate{
		WorkspaceKey:          workspace,
		Name:                  name,
		RoleName:              "lead",
		Parent:                parent,
		OrchestratorSessionID: orchestrator,
	})
	if err != nil {
		t.Fatalf("create lead: %v", err)
	}
}
