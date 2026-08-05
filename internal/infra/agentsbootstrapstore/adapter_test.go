package agentsbootstrapstore

import (
	"errors"
	"sync"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestEnsureRoleReplaysExactDefinitionAndRejectsDivergence(t *testing.T) {
	st := memstore.New()
	adapter, err := New(st.Roles(), st.AgentServices())
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
	adapter, err := New(st.Roles(), st.AgentServices())
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
	adapter, err := New(st.Roles(), st.AgentServices())
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
