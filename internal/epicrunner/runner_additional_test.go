package epicrunner

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestRunnerValidationAndFormattingBranches(t *testing.T) {
	ctx := context.Background()
	if ErrorKindOf(validateRunnerConfig(ctx, RunnerConfig{})) != ErrorKindUnavailable {
		t.Fatal("nil store should be unavailable")
	}
	if ErrorKindOf(validateRunnerConfig(ctx, RunnerConfig{Store: newTestStore(t)})) != ErrorKindUnavailable {
		t.Fatal("nil issue backend should be unavailable")
	}
	ib := clitest.NewMockIssueBackend()
	if ErrorKindOf(validateRunnerConfig(ctx, RunnerConfig{Store: newTestStore(t), IssueBackend: ib})) != ErrorKindValidation {
		t.Fatal("missing workspace should be validation")
	}
	if ErrorKindOf(validateRunnerConfig(ctx, RunnerConfig{Store: newTestStore(t), IssueBackend: ib, WorkspaceKey: "ws"})) != ErrorKindValidation {
		t.Fatal("missing epic should be validation")
	}

	st := &noCommandStore{Store: newTestStore(t)}
	err := validateRunnerConfig(ctx, RunnerConfig{
		Store:               st,
		IssueBackend:        ib,
		WorkspaceKey:        "ws",
		EpicID:              "EPIC-1",
		RequireCommandStore: true,
	})
	if ErrorKindOf(err) != ErrorKindUnavailable || !strings.Contains(err.Error(), "agent command store") {
		t.Fatalf("RequireCommandStore err = %v", err)
	}

	var out bytes.Buffer
	r := &Runner{
		workspace:      "ws",
		parent:         "EPIC-1",
		role:           "task",
		prefix:         "epic",
		maxConcurrency: 2,
		out:            &out,
	}
	r.PrintHeader()
	if !strings.Contains(out.String(), "orchestrator:     (none") {
		t.Fatalf("header without orchestrator = %q", out.String())
	}
	r.targetNodeID = "node-1"
	out.Reset()
	r.PrintHeader()
	if !strings.Contains(out.String(), "target node:      node-1") {
		t.Fatalf("header with target node = %q", out.String())
	}

	tasks := []backend.IssueData{
		{Title: "no id"},
		{ID: "T-1", Title: "one"},
		{ID: "T-2"},
		{ID: "T-3"},
		{ID: "T-4"},
		{ID: "T-5"},
		{ID: "T-6"},
	}
	summary := blockedTaskSummary(tasks)
	if strings.Contains(summary, "no id") || !strings.Contains(summary, "T-1 (one)") || !strings.Contains(summary, "+2 more") {
		t.Fatalf("blocked summary = %q", summary)
	}
	if got := WorkerName(strings.Repeat("!", 120), ""); !strings.HasPrefix(got, "task-") {
		t.Fatalf("WorkerName empty sanitized prefix = %q", got)
	}
	if got := WorkerName(strings.Repeat("x", 120), "TASK"); len(got) > 63 {
		t.Fatalf("WorkerName long prefix length = %d", len(got))
	}
}

func TestDispatchReadyTasksSkipBranches(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	activeTask := "T-ACTIVE"
	activeName := WorkerName("epic", activeTask)
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "ws",
		Name:         activeName,
		RoleName:     "task",
		Parent:       "EPIC-1",
		Mode:         domain.AgentModeEphemeral,
	}); err != nil {
		t.Fatalf("create active worker: %v", err)
	}
	if _, err := st.Agents().Update(ctx, "ws", activeName, store.AgentUpdate{State: statePtr(domain.AgentStateActive)}); err != nil {
		t.Fatalf("activate worker: %v", err)
	}

	var out bytes.Buffer
	r := &Runner{
		store:          st,
		workspace:      "ws",
		parent:         "EPIC-1",
		prefix:         "epic",
		role:           "task",
		maxConcurrency: 2,
		dryRun:         true,
		out:            &out,
	}
	dispatched, failures := r.dispatchReadyTasks(ctx, []backend.IssueData{
		{ID: "T-INPROGRESS", Status: "in_progress"},
		{ID: "T-CLOSED", Status: "closed"},
		{ID: "T-DEFERRED", Status: "deferred"},
		{ID: activeTask, Status: "open"},
		{ID: "T-DISPATCH", Title: "dispatch me", Status: "open"},
		{ID: "T-SKIPPED-BY-SLOTS", Status: "open"},
	}, 1)
	if len(failures) != 0 {
		t.Fatalf("failures = %#v", failures)
	}
	if len(dispatched) != 1 || dispatched[0].TaskID != "T-DISPATCH" {
		t.Fatalf("dispatched = %#v", dispatched)
	}
	if !strings.Contains(out.String(), "DRY-RUN would spawn") {
		t.Fatalf("dispatch output = %q", out.String())
	}

	if active, err := r.workerActiveForTask(ctx, "", "T-EMPTY"); err != nil || active {
		t.Fatalf("empty worker active=%t err=%v", active, err)
	}
}

type noCommandStore struct {
	store.Store
}

func (s *noCommandStore) AgentCommands() store.AgentCommandStore { return nil }

func statePtr(s domain.AgentState) *domain.AgentState { return &s }
