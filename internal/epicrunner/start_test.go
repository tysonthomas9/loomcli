package epicrunner

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

func TestStartAssignsEmptyLead(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	createLead(t, st, "ws", "nova", "", "")

	res, err := Start(ctx, st, StartInput{
		WorkspaceKey:          "ws",
		EpicID:                "EPIC-1",
		LeadName:              "nova",
		OrchestratorSessionID: "session-1",
		Mutate:                true,
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if res.State != StartStateAssigned || res.LeadName != "nova" || res.OrchestratorSessionID != "session-1" {
		t.Fatalf("result = %+v, want assigned nova/session-1", res)
	}
	got, err := st.Agents().Get(ctx, "ws", "nova")
	if err != nil {
		t.Fatalf("get lead: %v", err)
	}
	if got.Parent != "EPIC-1" {
		t.Fatalf("lead parent = %q, want EPIC-1", got.Parent)
	}
}

func TestStartResumesSameEpic(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	createLead(t, st, "ws", "nova", "EPIC-1", "session-1")

	res, err := Start(ctx, st, StartInput{
		WorkspaceKey: "ws",
		EpicID:       "EPIC-1",
		LeadName:     "nova",
		Mutate:       true,
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if res.State != StartStateResumed || res.OrchestratorSessionID != "session-1" {
		t.Fatalf("result = %+v, want resumed existing session-1", res)
	}
}

func TestStartRejectsDifferentEpicForLead(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	createLead(t, st, "ws", "nova", "EPIC-1", "")

	_, err := Start(ctx, st, StartInput{WorkspaceKey: "ws", EpicID: "EPIC-2", LeadName: "nova", Mutate: true})
	if ErrorKindOf(err) != ErrorKindConflict {
		t.Fatalf("Start() error = %v, want conflict", err)
	}
	if !strings.Contains(err.Error(), "already running epic EPIC-1") {
		t.Fatalf("error = %v, want active epic message", err)
	}
}

func TestStartRejectsEpicClaimedByOtherLead(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	createLead(t, st, "ws", "atlas", "EPIC-1", "")
	createLead(t, st, "ws", "nova", "", "")

	_, err := Start(ctx, st, StartInput{WorkspaceKey: "ws", EpicID: "EPIC-1", LeadName: "nova", Mutate: true})
	if ErrorKindOf(err) != ErrorKindConflict {
		t.Fatalf("Start() error = %v, want conflict", err)
	}
	if !strings.Contains(err.Error(), "already claimed by lead atlas") {
		t.Fatalf("error = %v, want owner conflict", err)
	}
}

func TestStartRejectsNonLeadRole(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "ws",
		Name:         "worker",
		RoleName:     "task",
	}); err != nil {
		t.Fatalf("create task agent: %v", err)
	}

	_, err := Start(ctx, st, StartInput{WorkspaceKey: "ws", EpicID: "EPIC-1", LeadName: "worker", Mutate: true})
	if ErrorKindOf(err) != ErrorKindValidation {
		t.Fatalf("Start() error = %v, want validation", err)
	}
	if !strings.Contains(err.Error(), "requires a lead agent") {
		t.Fatalf("error = %v, want lead role message", err)
	}
}

func TestStartDryRunDoesNotAssignParent(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	createLead(t, st, "ws", "nova", "", "")

	res, err := Start(ctx, st, StartInput{WorkspaceKey: "ws", EpicID: "EPIC-1", LeadName: "nova"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if res.State != StartStateDryRun {
		t.Fatalf("state = %q, want dry_run", res.State)
	}
	got, err := st.Agents().Get(ctx, "ws", "nova")
	if err != nil {
		t.Fatalf("get lead: %v", err)
	}
	if got.Parent != "" {
		t.Fatalf("lead parent = %q, want empty in dry-run", got.Parent)
	}
}

func TestStartMissingLead(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	_, err := Start(ctx, st, StartInput{WorkspaceKey: "ws", EpicID: "EPIC-1", LeadName: "ghost", Mutate: true})
	if ErrorKindOf(err) != ErrorKindNotFound || !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Start() error = %v, want not found wrapping ErrNotFound", err)
	}
}

func TestStartWithoutLeadAllowsUnassignedRunner(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	res, err := Start(ctx, st, StartInput{WorkspaceKey: "ws", EpicID: "EPIC-1", OrchestratorSessionID: "manual"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if res.State != StartStateUnassigned || res.OrchestratorSessionID != "manual" {
		t.Fatalf("result = %+v, want unassigned manual runner", res)
	}
}

func TestStartSerializesConcurrentParentClaims(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	createLead(t, st, "ws", "nova", "", "")

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	parents := []string{"EPIC-1", "EPIC-2"}
	for _, parent := range parents {
		parent := parent
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := Start(ctx, st, StartInput{WorkspaceKey: "ws", EpicID: parent, LeadName: "nova", Mutate: true})
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
		case ErrorKindOf(err) == ErrorKindConflict:
			conflicts++
		default:
			t.Fatalf("unexpected start error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes/conflicts = %d/%d, want 1/1", successes, conflicts)
	}
}

func TestAcquireBindLockTimesOutWhenHeld(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	unlock, err := AcquireBindLockWithTimeout("ws", "nova", time.Second, time.Millisecond)
	if err != nil {
		t.Fatalf("initial AcquireBindLockWithTimeout() error = %v", err)
	}
	defer unlock()

	_, err = AcquireBindLockWithTimeout("ws", "nova", 10*time.Millisecond, time.Millisecond)
	if ErrorKindOf(err) != ErrorKindConflict {
		t.Fatalf("second AcquireBindLockWithTimeout() error = %v, want conflict timeout", err)
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error = %v, want timeout message", err)
	}
}

func newTestStore(t *testing.T) store.Store {
	t.Helper()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	return memstore.New()
}

func createLead(t *testing.T, st store.Store, workspace, name, parent, orchestrator string) {
	t.Helper()
	ctx := context.Background()
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: workspace,
		Name:         name,
		RoleName:     "lead",
		Parent:       parent,
	}); err != nil {
		t.Fatalf("create lead: %v", err)
	}
	if orchestrator != "" {
		if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
			WorkspaceKey: workspace,
			SessionID:    orchestrator,
			AgentID:      name,
			Kind:         domain.AgentSessionKindOrchestration,
			Status:       domain.AgentSessionRunning,
		}); err != nil {
			t.Fatalf("create orchestrator session: %v", err)
		}
	}
}
