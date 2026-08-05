package agentscompatstore

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestEnsureRoleReplaysExactDefinitionAndRejectsDivergence(t *testing.T) {
	st := memstore.New()
	adapter, err := New(st.Roles(), st.AgentServices(), st.Agents())
	if err != nil {
		t.Fatal(err)
	}
	command := agents.EnsureRoleCommand{
		RequestID:    "workspace-bootstrap:WS:docs",
		WorkspaceKey: "WS",
		Role: agents.RoleDefinition{
			Name: "docs", Description: "Documentation review", Backend: "codex",
		},
	}

	created, changed, err := adapter.EnsureRole(t.Context(), command)
	if err != nil || !changed {
		t.Fatalf("first ensure = %+v, changed=%v, err=%v", created, changed, err)
	}
	replayed, changed, err := adapter.EnsureRole(t.Context(), command)
	if err != nil || changed || replayed.Name != "docs" {
		t.Fatalf("replay = %+v, changed=%v, err=%v", replayed, changed, err)
	}
	command.Role.Description = "Different authority-bearing definition"
	if _, _, err := adapter.EnsureRole(t.Context(), command); !errors.Is(err, agents.ErrConflict) {
		t.Fatalf("divergent ensure = %v, want conflict", err)
	}
}

func TestRepairManagedRolePromptFileIsAtomicAcrossConcurrentWritersAndReplayable(t *testing.T) {
	st := memstore.New()
	if _, err := st.Roles().Create(t.Context(), store.RoleCreate{
		WorkspaceKey: "WS",
		Name:         "docs",
	}); err != nil {
		t.Fatal(err)
	}
	adapter, err := New(st.Roles(), st.AgentServices(), st.Agents())
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		path    string
		changed bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, path := range []string{"/workspace/prompts/docs-a.md", "/workspace/prompts/docs-b.md"} {
		go func() {
			ready.Done()
			<-start
			_, changed, ensureErr := adapter.RepairManagedRolePromptFile(
				t.Context(),
				agents.RepairManagedRolePromptFileCommand{
					RequestID:    "builtin-role-prompt-backfill:WS:docs:" + path,
					WorkspaceKey: "WS",
					RoleName:     "docs",
					PromptFile:   path,
				},
			)
			results <- result{path: path, changed: changed, err: ensureErr}
		}()
	}
	ready.Wait()
	close(start)

	var winner result
	conflicts := 0
	for range 2 {
		got := <-results
		switch {
		case got.err == nil && got.changed:
			winner = got
		case errors.Is(got.err, agents.ErrConflict) && !got.changed:
			conflicts++
		default:
			t.Fatalf("unexpected concurrent result = %+v", got)
		}
	}
	if winner.path == "" || conflicts != 1 {
		t.Fatalf("winner=%+v conflicts=%d", winner, conflicts)
	}
	persisted, err := st.Roles().Get(t.Context(), "WS", "docs")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.PromptFile != winner.path {
		t.Fatalf("persisted prompt = %q, winner = %q", persisted.PromptFile, winner.path)
	}
	_, changed, err := adapter.RepairManagedRolePromptFile(t.Context(), agents.RepairManagedRolePromptFileCommand{
		RequestID:    "builtin-role-prompt-backfill:WS:docs:replay",
		WorkspaceKey: "WS",
		RoleName:     "docs",
		PromptFile:   winner.path,
	})
	if err != nil || changed {
		t.Fatalf("winner replay changed=%v err=%v", changed, err)
	}
}

func TestEnsureCommandsRejectNonCanonicalReplayIdentityBeforePersistence(t *testing.T) {
	st := memstore.New()
	adapter, err := New(st.Roles(), st.AgentServices(), st.Agents())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := adapter.EnsureRole(t.Context(), agents.EnsureRoleCommand{
		RequestID: " request", WorkspaceKey: "WS", Role: agents.RoleDefinition{Name: "docs"},
	}); !errors.Is(err, agents.ErrInvalid) {
		t.Fatalf("EnsureRole invalid request id = %v", err)
	}
	if _, _, err := adapter.EnsureAgent(t.Context(), agents.EnsureAgentCommand{
		RequestID: "request", CreateAgentCommand: agents.CreateAgentCommand{
			WorkspaceKey: " WS", AgentID: "docs",
		},
	}); !errors.Is(err, agents.ErrInvalid) {
		t.Fatalf("EnsureAgent invalid workspace = %v", err)
	}
}

func TestSupervisedAssignmentCommandsSeparateIntentAndParent(t *testing.T) {
	base := memstore.New()
	counting := &countingSupervisedAssignmentStore{AgentStore: base.Agents()}
	adapter, err := New(base.Roles(), base.AgentServices(), counting)
	if err != nil {
		t.Fatal(err)
	}

	created, err := adapter.CreateSupervisedAssignment(
		t.Context(),
		agents.CreateSupervisedAssignmentCommand{
			WorkspaceKey: "WS",
			AgentName:    "docs",
			RoleName:     "task",
			Backend:      "codex",
			DesiredState: agents.SupervisedAssignmentDesiredStopped,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "docs" || created.RoleName != "task" {
		t.Fatalf("created assignment = %+v", created)
	}

	auto := true
	backend := "claude"
	desiredRunning := agents.SupervisedAssignmentDesiredRunning
	intent, err := adapter.UpdateSupervisedAssignmentIntent(
		t.Context(),
		agents.UpdateSupervisedAssignmentIntentCommand{
			WorkspaceKey: "WS",
			AgentName:    "docs",
			Patch: agents.SupervisedAssignmentIntentPatch{
				Auto:         &auto,
				Backend:      &backend,
				DesiredState: &desiredRunning,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !intent.Auto || intent.Backend != "claude" ||
		intent.DesiredState != agents.SupervisedAssignmentDesiredRunning ||
		intent.State != agents.SupervisedAssignmentStateIdle {
		t.Fatalf("intent update crossed field families: %+v", intent)
	}

	expectedEmpty := ""
	bound, err := adapter.BindSupervisedAssignmentParent(
		t.Context(),
		agents.BindSupervisedAssignmentParentCommand{
			WorkspaceKey:   "WS",
			AgentName:      "docs",
			ExpectedParent: &expectedEmpty,
			Parent:         "EPIC-1",
			Proof: agents.ParentBindingProof{
				DriverRunID: "run-1", NodeID: "node-1", LeaseID: "lease-1", FencingToken: 1,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if bound.Parent != "EPIC-1" {
		t.Fatalf("parent = %q", bound.Parent)
	}
	if _, err := adapter.BindSupervisedAssignmentParent(
		t.Context(),
		agents.BindSupervisedAssignmentParentCommand{
			WorkspaceKey:   "WS",
			AgentName:      "docs",
			ExpectedParent: &expectedEmpty,
			Parent:         "EPIC-2",
			Proof: agents.ParentBindingProof{
				DriverRunID: "run-1", NodeID: "node-1", LeaseID: "lease-1", FencingToken: 1,
			},
		},
	); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale parent bind = %v, want conflict", err)
	}
	if counting.updates != 2 {
		t.Fatalf("intent plus parent updates = %d, want 2 isolated writes", counting.updates)
	}
}

func TestRetireSupervisedAssignmentIsIdempotent(t *testing.T) {
	base := memstore.New()
	adapter, err := New(base.Roles(), base.AgentServices(), base.Agents())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.CreateSupervisedAssignment(
		t.Context(),
		agents.CreateSupervisedAssignmentCommand{
			WorkspaceKey: "WS",
			AgentName:    "reviewer",
			RoleName:     "pr-review",
		},
	); err != nil {
		t.Fatal(err)
	}
	command := agents.RetireSupervisedAssignmentCommand{
		WorkspaceKey: "WS",
		AgentName:    "reviewer",
	}
	if err := adapter.RetireSupervisedAssignment(t.Context(), command); err != nil {
		t.Fatalf("first retirement = %v", err)
	}
	if err := adapter.RetireSupervisedAssignment(t.Context(), command); err != nil {
		t.Fatalf("retirement replay = %v", err)
	}
	if _, err := base.Agents().Get(t.Context(), "WS", "reviewer"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("retired assignment get = %v, want not found", err)
	}
}

func TestSupervisedAssignmentCommandsRejectNonCanonicalCoordinates(t *testing.T) {
	base := memstore.New()
	adapter, err := New(base.Roles(), base.AgentServices(), base.Agents())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.CreateSupervisedAssignment(
		t.Context(),
		agents.CreateSupervisedAssignmentCommand{
			WorkspaceKey: " WS",
			AgentName:    "docs",
			RoleName:     "task",
		},
	); !errors.Is(err, agents.ErrInvalid) {
		t.Fatalf("non-canonical workspace = %v, want invalid", err)
	}
	if _, err := adapter.UpdateSupervisedAssignmentIntent(
		t.Context(),
		agents.UpdateSupervisedAssignmentIntentCommand{
			WorkspaceKey: "WS",
			AgentName:    "docs ",
			Patch: agents.SupervisedAssignmentIntentPatch{
				Auto: pointer(true),
			},
		},
	); !errors.Is(err, agents.ErrInvalid) {
		t.Fatalf("non-canonical agent = %v, want invalid", err)
	}
	if _, err := base.Agents().Get(t.Context(), "WS", "docs"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("invalid command reached persistence: %v", err)
	}
}

func pointer[T any](value T) *T {
	return &value
}

type countingSupervisedAssignmentStore struct {
	store.AgentStore
	updates int
}

func (counting *countingSupervisedAssignmentStore) Update(
	ctx context.Context,
	workspace, name string,
	patch store.AgentUpdate,
) (*domain.Agent, error) {
	counting.updates++
	return counting.AgentStore.Update(ctx, workspace, name, patch)
}
