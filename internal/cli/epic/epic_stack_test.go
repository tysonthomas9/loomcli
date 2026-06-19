package epic

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	sl "github.com/tysonthomas9/loomcli/internal/stacklineage"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
)

// baseOf returns the planned BaseTaskID for a task, or "<absent>" if the task
// is not in the plan.
func baseOf(plan []projectedNode, taskID string) string {
	for _, n := range plan {
		if n.TaskID == taskID {
			return n.BaseTaskID
		}
	}
	return "<absent>"
}

func TestPlanEpicForest_LinearChain(t *testing.T) {
	plan, stats := planEpicForest([]epicTask{
		{ID: "A"},
		{ID: "B", BlockedBy: []string{"A"}},
		{ID: "C", BlockedBy: []string{"B"}},
	})
	if got := baseOf(plan, "A"); got != "" {
		t.Errorf("A base = %q, want root", got)
	}
	if got := baseOf(plan, "B"); got != "A" {
		t.Errorf("B base = %q, want A", got)
	}
	if got := baseOf(plan, "C"); got != "B" {
		t.Errorf("C base = %q, want B", got)
	}
	if stats.Roots != 1 || stats.LinearLinks != 2 || stats.FanInBreaks != 0 || stats.FanOutBreaks != 0 {
		t.Errorf("stats = %+v, want 1 root / 2 links / 0 breaks", stats)
	}
	assertOrderable(t, plan)
}

func TestPlanEpicForest_TwoIndependentChains(t *testing.T) {
	plan, stats := planEpicForest([]epicTask{
		{ID: "A"}, {ID: "B", BlockedBy: []string{"A"}},
		{ID: "C"}, {ID: "D", BlockedBy: []string{"C"}},
	})
	if baseOf(plan, "B") != "A" || baseOf(plan, "D") != "C" {
		t.Errorf("expected two chains A→B, C→D, got %+v", plan)
	}
	if stats.Roots != 2 || stats.LinearLinks != 2 {
		t.Errorf("stats = %+v, want 2 roots / 2 links", stats)
	}
	assertOrderable(t, plan)
}

func TestPlanEpicForest_FanInBecomesRoot(t *testing.T) {
	// C depends on both A and B — can't stay linear, so C roots.
	plan, stats := planEpicForest([]epicTask{
		{ID: "A"}, {ID: "B"}, {ID: "C", BlockedBy: []string{"A", "B"}},
	})
	if got := baseOf(plan, "C"); got != "" {
		t.Errorf("C base = %q, want root (fan-in)", got)
	}
	if stats.FanInBreaks != 1 || stats.Roots != 3 {
		t.Errorf("stats = %+v, want 1 fan-in break / 3 roots", stats)
	}
	assertOrderable(t, plan)
}

func TestPlanEpicForest_FanOutBecomesRoots(t *testing.T) {
	// A blocks both B and C — a base can't have two successors, so B and C root.
	plan, stats := planEpicForest([]epicTask{
		{ID: "A"}, {ID: "B", BlockedBy: []string{"A"}}, {ID: "C", BlockedBy: []string{"A"}},
	})
	if baseOf(plan, "B") != "" || baseOf(plan, "C") != "" {
		t.Errorf("expected B and C to root (fan-out), got %+v", plan)
	}
	if stats.FanOutBreaks != 2 || stats.Roots != 3 || stats.LinearLinks != 0 {
		t.Errorf("stats = %+v, want 2 fan-out breaks / 3 roots", stats)
	}
	assertOrderable(t, plan)
}

func TestPlanEpicForest_IgnoresOutOfEpicAndSelfEdges(t *testing.T) {
	plan, stats := planEpicForest([]epicTask{
		{ID: "A", BlockedBy: []string{"EXTERNAL-99", "A"}}, // out-of-epic + self → ignored
		{ID: "B", BlockedBy: []string{"A", "A"}},           // duplicate predecessor deduped
	})
	if baseOf(plan, "A") != "" {
		t.Errorf("A should root (only out-of-epic/self deps), got %q", baseOf(plan, "A"))
	}
	if baseOf(plan, "B") != "A" {
		t.Errorf("B base = %q, want A", baseOf(plan, "B"))
	}
	if stats.FanInBreaks != 0 || stats.LinearLinks != 1 {
		t.Errorf("stats = %+v, want clean A→B link", stats)
	}
	assertOrderable(t, plan)
}

// assertOrderable proves the projected forest is a valid set of linear chains —
// the only shape the publisher/resolver accept. It rebuilds nodes from the plan
// and runs the same Ordered() validation the stackstore uses.
func assertOrderable(t *testing.T, plan []projectedNode) {
	t.Helper()
	nodes := make([]sl.Node, 0, len(plan))
	for _, n := range plan {
		nodes = append(nodes, sl.Node{TaskID: n.TaskID, BaseTaskID: n.BaseTaskID})
	}
	if _, err := sl.Ordered(nodes); err != nil {
		t.Fatalf("projected forest is not a valid linear forest: %v\nplan=%+v", err, plan)
	}
}

func TestProjectEpicStack_BuildsForestAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	const ws, epicID, repo, root = "WS", "EPIC-1", "acme/widgets", "main"

	ib := clitest.NewMockIssueBackend()
	// List(parent) returns the full open universe with dependency edges.
	ib.ListResult = []backend.IssueData{
		{ID: "T-A", Status: "open", Parent: epicID},
		{ID: "T-B", Status: "open", Parent: epicID, BlockedBy: []string{"T-A"}, BlockedByCount: 1},
		{ID: "T-C", Status: "open", Parent: epicID, BlockedBy: []string{"T-B"}, BlockedByCount: 1},
		{ID: "T-X", Status: "closed", Parent: epicID}, // closed → excluded from the universe
	}
	ib.ReadyResult = []backend.IssueData{{ID: "T-A", Status: "open", Parent: epicID}}
	ib.BlockedResult = []backend.IssueData{
		{ID: "T-B", Status: "blocked", Parent: epicID, BlockedBy: []string{"T-A"}, BlockedByCount: 1},
		{ID: "T-C", Status: "blocked", Parent: epicID, BlockedBy: []string{"T-B"}, BlockedByCount: 1},
	}

	sstore := stackstore.New(t.TempDir())

	proj, err := projectEpicStack(ctx, ib, sstore, ws, epicID, repo, root)
	if err != nil {
		t.Fatalf("projectEpicStack: %v", err)
	}
	if proj.StackID != sl.StackID("epic:EPIC-1") {
		t.Fatalf("stack id = %q, want epic:EPIC-1", proj.StackID)
	}
	if len(proj.Created) != 3 {
		t.Fatalf("created = %v, want 3 nodes (closed task excluded)", proj.Created)
	}

	// The stored stack reflects the DAG as a linear chain on the right repo/base.
	stack, err := sstore.GetStack(ctx, ws, proj.StackID)
	if err != nil {
		t.Fatalf("GetStack: %v", err)
	}
	if stack.RepoName != repo || stack.RootBase != root {
		t.Fatalf("stack = %+v, want repo=%s root=%s", stack, repo, root)
	}
	nodes, err := sstore.ListNodes(ctx, ws, proj.StackID)
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	byTask := map[string]sl.Node{}
	for _, n := range nodes {
		byTask[n.TaskID] = n
	}
	if len(byTask) != 3 {
		t.Fatalf("stored %d nodes, want 3: %+v", len(byTask), nodes)
	}
	if byTask["T-A"].BaseTaskID != "" || byTask["T-B"].BaseTaskID != "T-A" || byTask["T-C"].BaseTaskID != "T-B" {
		t.Fatalf("lineage wrong: A=%q B=%q C=%q", byTask["T-A"].BaseTaskID, byTask["T-B"].BaseTaskID, byTask["T-C"].BaseTaskID)
	}
	for _, n := range nodes {
		if n.OutputBranch == "" {
			t.Fatalf("node %s has no output branch", n.TaskID)
		}
		if n.State != sl.NodeStatePending {
			t.Fatalf("node %s state = %q, want pending", n.TaskID, n.State)
		}
	}

	// Idempotency: a second projection creates nothing new and keeps every
	// node's stable OutputBranch.
	branchBefore := map[string]string{}
	for _, n := range nodes {
		branchBefore[n.TaskID] = n.OutputBranch
	}
	proj2, err := projectEpicStack(ctx, ib, sstore, ws, epicID, repo, root)
	if err != nil {
		t.Fatalf("projectEpicStack (re-run): %v", err)
	}
	if len(proj2.Created) != 0 || len(proj2.Reparented) != 0 {
		t.Fatalf("re-run mutated the stack: created=%v reparented=%v", proj2.Created, proj2.Reparented)
	}
	nodes2, _ := sstore.ListNodes(ctx, ws, proj.StackID)
	for _, n := range nodes2 {
		if branchBefore[n.TaskID] != n.OutputBranch {
			t.Fatalf("node %s branch changed on re-run: %q → %q", n.TaskID, branchBefore[n.TaskID], n.OutputBranch)
		}
	}
}
