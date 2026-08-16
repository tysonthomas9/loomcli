package prreview

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"
)

func TestEnsureReviewerAgentUsesCanonicalAgents(t *testing.T) {
	registry := newReviewerAgentRegistry()
	module := &Module{reviewerIdentities: registry, reviewerAgents: registry}
	const agentID = "review-octocat-hello-abc12345-pr-7"

	if err := module.ensureReviewerAgent(t.Context(), prReviewTestWorkspace, agentID); err != nil {
		t.Fatalf("ensureReviewerAgent: %v", err)
	}
	agent, err := registry.GetAgent(t.Context(), prReviewTestWorkspace, agentID)
	if err != nil {
		t.Fatal(err)
	}
	if agent.Kind != agents.AgentKindSupport ||
		agent.Behavior != (agents.BehaviorReference{RoleName: reviewerRoleName}) ||
		agent.DesiredState != agents.DesiredRunning {
		t.Fatalf("canonical Agent = %#v", agent)
	}
}

func TestReviewerIdentityCreateArchiveAndReplayUseOneVersionedPreset(t *testing.T) {
	h := newPRReviewHarness(t, false)
	agentID := reviewerAgentName("octocat", "hello", 7)
	if err := h.module.ensureReviewerAgent(t.Context(), prReviewTestWorkspace, agentID); err != nil {
		t.Fatal(err)
	}

	for call := 0; call < 2; call++ {
		status, raw := h.delete(t, "/api/workspaces/WS/pull-requests/octocat/hello/7/reviewer")
		if status != http.StatusOK {
			t.Fatalf("archive call %d status/body = %d/%s", call+1, status, raw)
		}
		var response struct {
			Data struct {
				AgentName string `json:"agent_name"`
				Archived  bool   `json:"archived"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &response); err != nil {
			t.Fatal(err)
		}
		if response.Data.AgentName != agentID || !response.Data.Archived {
			t.Fatalf("archive response = %+v", response.Data)
		}
	}

	agent, err := h.reviewers.GetAgent(t.Context(), prReviewTestWorkspace, agentID)
	if err != nil {
		t.Fatal(err)
	}
	if agent.DeletedAt == nil {
		t.Fatalf("reviewer Agent remained active: %#v", agent)
	}
	h.reviewers.mu.Lock()
	commands := append([]agents.ManagedReviewerCommand(nil), h.reviewers.commands...)
	h.reviewers.mu.Unlock()
	if len(commands) != 3 {
		t.Fatalf("convergence commands = %d, want active plus two archived replays", len(commands))
	}
	for index, command := range commands {
		if command.Preset.PresetID != reviewerPresetID ||
			command.Preset.Revision != reviewerPresetRevision ||
			command.Preset.Role.Name != reviewerRoleName ||
			command.Preset.Agent.RoleName != reviewerRoleName {
			t.Fatalf("command %d preset = %+v", index, command.Preset)
		}
	}
	if commands[0].DesiredState != agents.ManagedReviewerActive ||
		commands[1].DesiredState != agents.ManagedReviewerArchived ||
		commands[2].DesiredState != agents.ManagedReviewerArchived {
		t.Fatalf("reviewer desired states = %q, %q, %q", commands[0].DesiredState, commands[1].DesiredState, commands[2].DesiredState)
	}
}

func TestArchiveReviewerReportsIdentityConflict(t *testing.T) {
	h := newPRReviewHarness(t, false)
	h.reviewers.err = agents.ErrConflict
	status, raw := h.delete(t, "/api/workspaces/WS/pull-requests/octocat/hello/7/reviewer")
	if status != http.StatusConflict || !strings.Contains(string(raw), `"code":"reviewer_identity_conflict"`) {
		t.Fatalf("status/body = %d/%s", status, raw)
	}
}

func TestEnsureReviewerAgentFailsClosedWithoutCanonicalCapability(t *testing.T) {
	module := &Module{}
	if err := module.ensureReviewerAgent(
		t.Context(),
		prReviewTestWorkspace,
		"reviewer",
	); !errors.Is(err, agents.ErrUnavailable) {
		t.Fatalf("ensureReviewerAgent error = %v, want unavailable", err)
	}
}
