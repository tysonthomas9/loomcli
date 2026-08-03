package terminal

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"
)

type terminalAgentIdentityStub struct {
	record *agents.Agent
	err    error
	calls  int
}

func (stub *terminalAgentIdentityStub) GetAgent(
	context.Context,
	string,
	string,
) (*agents.Agent, error) {
	stub.calls++
	return stub.record, stub.err
}

func TestLoadTerminalAgentPrefersCanonicalAgentsProjection(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	metadata, err := agents.WithRuntimeMetadata(nil, agents.RuntimeMetadata{
		RoleKind: "interactive", Backend: "codex", FallbackBackends: []string{"claude"},
		Repos: []string{"loom"}, RepoGroups: []string{"core"}, CrossRepo: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	identity := &terminalAgentIdentityStub{record: &agents.Agent{
		WorkspaceKey: "WS",
		AgentID:      "reviewer",
		Name:         "Reviewer",
		Kind:         agents.AgentKindSupport,
		Behavior:     agents.BehaviorReference{RoleName: "pr-reviewer"},
		DesiredState: agents.DesiredRunning,
		MaxInstances: 1,
		BudgetPolicy: "bounded",
		Metadata:     metadata,
		CreatedAt:    now,
		UpdatedAt:    now,
	}}

	got, err := loadTerminalAgent(t.Context(), "WS", "reviewer", identity)
	if err != nil {
		t.Fatalf("loadTerminalAgent: %v", err)
	}
	if got.AgentID != "reviewer" ||
		got.RoleName != "pr-reviewer" ||
		got.DesiredState != agents.DesiredRunning ||
		got.MaxInstances != 1 ||
		got.BudgetPolicy != "bounded" || got.Backend != "codex" ||
		len(got.FallbackBackends) != 1 || got.FallbackBackends[0] != "claude" ||
		len(got.Repos) != 1 || got.Repos[0] != "loom" ||
		len(got.RepoGroups) != 1 || got.RepoGroups[0] != "core" || !got.CrossRepo {
		t.Fatalf("canonical projection = %+v", got)
	}
}

func TestLoadTerminalAgentDoesNotFallbackWhenCanonicalIdentityIsMissing(t *testing.T) {
	identity := &terminalAgentIdentityStub{err: agents.ErrNotFound}

	_, err := loadTerminalAgent(t.Context(), "WS", "legacy", identity)
	if err == nil || !errors.Is(err, agents.ErrNotFound) || identity.calls != 1 {
		t.Fatalf("loadTerminalAgent error = %v, calls = %d", err, identity.calls)
	}
}

func TestLoadTerminalAgentDoesNotBypassCanonicalFailure(t *testing.T) {
	identity := &terminalAgentIdentityStub{err: agents.ErrUnavailable}

	_, err := loadTerminalAgent(t.Context(), "WS", "legacy", identity)
	if err == nil || !errors.Is(err, agents.ErrUnavailable) {
		t.Fatalf("loadTerminalAgent error = %v, want canonical unavailable", err)
	}
}

func TestLoadTerminalAgentRejectsMalformedCanonicalRuntimeMetadata(t *testing.T) {
	identity := &terminalAgentIdentityStub{record: &agents.Agent{
		WorkspaceKey: "WS", AgentID: "reviewer", Name: "Reviewer",
		Kind: agents.AgentKindLead, Behavior: agents.BehaviorReference{RoleName: "lead"},
		DesiredState: agents.DesiredRunning, Metadata: map[string]string{
			agents.MetadataRoleKind:         "interactive",
			agents.MetadataRepos:            "not-json",
			agents.MetadataRepoGroups:       "[]",
			agents.MetadataFallbackBackends: "[]",
			agents.MetadataCrossRepo:        "false", agents.MetadataAuto: "false",
		},
	}}
	_, err := loadTerminalAgent(t.Context(), "WS", "reviewer", identity)
	if err == nil || !errors.Is(err, agents.ErrInvalidPersistedState) {
		t.Fatalf("loadTerminalAgent error = %v, want invalid persisted state", err)
	}
}
