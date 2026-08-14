package interaction

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"
)

type agentTerminalDirectoryFake struct {
	agent    *agents.Agent
	role     *agents.Role
	agentErr error
	roleErr  error
}

func (fake *agentTerminalDirectoryFake) GetAgent(context.Context, string, string) (*agents.Agent, error) {
	return fake.agent, fake.agentErr
}
func (fake *agentTerminalDirectoryFake) ListAgents(context.Context, string, agents.AgentFilter) ([]*agents.Agent, error) {
	if fake.agent == nil {
		return nil, fake.agentErr
	}
	return []*agents.Agent{fake.agent}, fake.agentErr
}
func (fake *agentTerminalDirectoryFake) GetRole(context.Context, string, string) (*agents.Role, error) {
	return fake.role, fake.roleErr
}
func (fake *agentTerminalDirectoryFake) ListRoles(context.Context, string) ([]*agents.Role, error) {
	if fake.role == nil {
		return nil, fake.roleErr
	}
	return []*agents.Role{fake.role}, fake.roleErr
}

type agentTerminalPlacementFake struct {
	orchestrationID string
	worktree        string
	backend         string
	configDir       string
}

func (fake agentTerminalPlacementFake) FindActiveOrchestrationSession(context.Context, string, string) (string, error) {
	return fake.orchestrationID, nil
}
func (fake agentTerminalPlacementFake) AgentWorktree(context.Context, string, string) string {
	return fake.worktree
}
func (fake agentTerminalPlacementFake) WorkspacePath(context.Context, string) string {
	return fake.worktree
}
func (fake agentTerminalPlacementFake) DefaultBackend(context.Context, string) string {
	return fake.backend
}
func (fake agentTerminalPlacementFake) ConfigDir() string { return fake.configDir }

func newAgentTerminalService(t *testing.T, desired agents.DesiredState, roleKind string) (*TerminalTabService, *terminalStoreFake, *terminalRuntimeFake, *agentTerminalDirectoryFake) {
	t.Helper()
	now := time.Now().UTC()
	directory := &agentTerminalDirectoryFake{
		agent: &agents.Agent{
			WorkspaceKey: "WS", AgentID: "reviewer", Name: "Reviewer",
			Behavior:     agents.BehaviorReference{RoleName: "lead"},
			DesiredState: desired, MaxInstances: 1,
			Metadata:  map[string]string{agents.MetadataBackend: "codex"},
			CreatedAt: now, UpdatedAt: now,
		},
		role: &agents.Role{
			WorkspaceKey: "WS", Name: "lead", Kind: roleKind,
			PromptFile: "prompts/lead.md", Backend: "codex",
		},
	}
	store := newTerminalStoreFake()
	runtime := newTerminalRuntimeFake()
	service := NewTerminalTabs(store, runtime, now, TerminalDependencies{
		Agents: directory, Roles: directory,
		Placement: agentTerminalPlacementFake{
			orchestrationID: "lead-existing", worktree: "/worktrees/reviewer",
			backend: "claude", configDir: "/loom-data",
		},
	}).(*TerminalTabService)
	return service, store, runtime, directory
}

func TestEnsureAgentTerminalBuildsCanonicalInteractiveLaunch(t *testing.T) {
	service, _, _, _ := newAgentTerminalService(t, agents.DesiredRunning, agents.RoleKindInteractive)

	meta, err := service.EnsureAgentTerminal(t.Context(), EnsureAgentTerminalCommand{
		WorkspaceKey: "WS", AgentID: "reviewer",
	})
	if err != nil {
		t.Fatalf("EnsureAgentTerminal: %v", err)
	}
	if meta.Kind != "agent" || meta.AgentID != "reviewer" || meta.Role != "lead" || meta.Backend != "codex" {
		t.Fatalf("metadata = %#v", meta)
	}
	if meta.Launch == nil || meta.Launch.Cwd != "/worktrees/reviewer" || len(meta.Launch.Argv) != 2 {
		t.Fatalf("launch = %#v", meta.Launch)
	}
	command := meta.Launch.Argv[1]
	for _, want := range []string{"'loom'", "'--workspace'", "'WS'", "'--backend'", "'codex'", "'lead'", "'--prompt'", "'prompts/lead.md'"} {
		if !strings.Contains(command, want) {
			t.Fatalf("launch command %q missing %q", command, want)
		}
	}
	wantEnv := map[string]string{
		"LOOM_AGENT_NAME": "reviewer", "LOOM_AGENT_ROLE": "lead",
		"LOOM_WORKSPACE": "WS", "LOOM_BACKEND": "codex",
		"LOOM_CONFIG_DIR": "/loom-data", "LOOM_ORCHESTRATOR_SESSION_ID": "lead-existing",
	}
	for name, want := range wantEnv {
		if got := meta.Launch.Env[name]; got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestEnsureAgentTerminalRejectsWorkerAndStoppedAgent(t *testing.T) {
	worker, _, _, _ := newAgentTerminalService(t, agents.DesiredRunning, agents.RoleKindWorker)
	if _, err := worker.EnsureAgentTerminal(t.Context(), EnsureAgentTerminalCommand{
		WorkspaceKey: "WS", AgentID: "reviewer",
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("worker error = %v, want ErrInvalid", err)
	}

	stopped, _, _, _ := newAgentTerminalService(t, agents.DesiredStopped, agents.RoleKindInteractive)
	if _, err := stopped.EnsureAgentTerminal(t.Context(), EnsureAgentTerminalCommand{
		WorkspaceKey: "WS", AgentID: "reviewer",
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("stopped error = %v, want ErrInvalid", err)
	}
}

func TestResolveTerminalLaunchRechecksDesiredStateAndRole(t *testing.T) {
	service, _, _, directory := newAgentTerminalService(t, agents.DesiredRunning, agents.RoleKindInteractive)
	meta, err := service.EnsureAgentTerminal(t.Context(), EnsureAgentTerminalCommand{
		WorkspaceKey: "WS", AgentID: "reviewer",
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := service.resolveTerminalLaunch(t.Context(), TerminalKey{
		WorkspaceKey: "WS", TerminalID: meta.SessionName,
	})
	if err != nil || resolved.AgentID != "reviewer" || resolved.Launch == nil {
		t.Fatalf("running resolve = %#v, %v", resolved, err)
	}

	directory.agent.DesiredState = agents.DesiredStopped
	if _, err := service.resolveTerminalLaunch(t.Context(), TerminalKey{
		WorkspaceKey: "WS", TerminalID: meta.SessionName,
	}); !errors.Is(err, ErrAgentTerminalStopped) {
		t.Fatalf("stopped resolve error = %v", err)
	}
	directory.agent.DesiredState = agents.DesiredRunning
	directory.role.Kind = agents.RoleKindWorker
	if _, err := service.resolveTerminalLaunch(t.Context(), TerminalKey{
		WorkspaceKey: "WS", TerminalID: meta.SessionName,
	}); !errors.Is(err, ErrAgentTerminalWorker) {
		t.Fatalf("worker resolve error = %v", err)
	}
}

func TestEnsureAgentTerminalConvergesStaleLiveLaunchBeforeReplacement(t *testing.T) {
	service, store, runtime, directory := newAgentTerminalService(t, agents.DesiredRunning, agents.RoleKindInteractive)
	first, err := service.EnsureAgentTerminal(t.Context(), EnsureAgentTerminalCommand{
		WorkspaceKey: "WS", AgentID: "reviewer",
	})
	if err != nil {
		t.Fatal(err)
	}
	oldKey := TerminalKey{WorkspaceKey: "WS", TerminalID: first.SessionName}
	runtime.live[oldKey] = true
	directory.agent.Metadata[agents.MetadataBackend] = "claude"

	replacement, err := service.EnsureAgentTerminal(t.Context(), EnsureAgentTerminalCommand{
		WorkspaceKey: "WS", AgentID: "reviewer",
	})
	if err != nil {
		t.Fatalf("replace stale terminal: %v", err)
	}
	if replacement.SessionName == first.SessionName || replacement.Backend != "claude" {
		t.Fatalf("replacement = %#v, first = %#v", replacement, first)
	}
	if len(runtime.killed) != 1 || runtime.killed[0] != oldKey {
		t.Fatalf("killed = %#v, want %#v", runtime.killed, oldKey)
	}
	if old, _ := store.Get(t.Context(), "WS", first.SessionName); old != nil {
		t.Fatalf("stale placement survived: %#v", old)
	}
}

func TestEnsureAgentTerminalFailsClosedWhenIdentityLookupFails(t *testing.T) {
	service, _, _, directory := newAgentTerminalService(t, agents.DesiredRunning, agents.RoleKindInteractive)
	directory.agentErr = agents.ErrUnavailable
	_, err := service.EnsureAgentTerminal(t.Context(), EnsureAgentTerminalCommand{
		WorkspaceKey: "WS", AgentID: "reviewer",
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error = %v, want ErrUnavailable", err)
	}
}

func TestConcurrentEnsureAgentTerminalCreatesOnePlacement(t *testing.T) {
	service, store, _, _ := newAgentTerminalService(t, agents.DesiredRunning, agents.RoleKindInteractive)
	const callers = 8
	results := make(chan *TabMetadata, callers)
	errorsCh := make(chan error, callers)
	var group sync.WaitGroup
	for range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			meta, err := service.EnsureAgentTerminal(t.Context(), EnsureAgentTerminalCommand{
				WorkspaceKey: "WS", AgentID: "reviewer",
			})
			results <- meta
			errorsCh <- err
		}()
	}
	group.Wait()
	close(results)
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatalf("concurrent ensure: %v", err)
		}
	}
	var session string
	for meta := range results {
		if meta == nil {
			t.Fatal("concurrent ensure returned nil metadata")
		}
		if session == "" {
			session = meta.SessionName
		}
		if meta.SessionName != session {
			t.Fatalf("placements diverged: %q and %q", session, meta.SessionName)
		}
	}
	tabs, err := store.List(t.Context(), "WS")
	if err != nil || len(tabs) != 1 {
		t.Fatalf("stored tabs = %#v, err = %v", tabs, err)
	}
}
