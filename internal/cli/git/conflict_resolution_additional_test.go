package git

import (
	"errors"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
)

func TestConflictResolutionPromptBranches(t *testing.T) {
	deps, _, _, _, _ := NewTestDeps(t)
	agent := &clitest.MockAgentInvoker{}
	deps.Agent = agent

	oldGen, oldPushGen := ConflictPromptGen, ConflictPromptGenWithPush
	t.Cleanup(func() {
		ConflictPromptGen, ConflictPromptGenWithPush = oldGen, oldPushGen
	})
	ConflictPromptGen = nil
	ConflictPromptGenWithPush = nil

	if err := resolveConflictsWithAgentDeps(deps, "/repo", "feature", "main", []string{"a.go", "b.go"}); err != nil {
		t.Fatalf("resolveConflictsWithAgentDeps: %v", err)
	}
	if len(agent.InteractiveCalls) != 1 || !strings.Contains(agent.InteractiveCalls[0].Prompt, "a.go") {
		t.Fatalf("fallback prompt call = %#v", agent.InteractiveCalls)
	}

	if err := resolveLocalConflictsWithAgentDeps(deps, "/repo", "feature", "main", []string{"local.go"}); err != nil {
		t.Fatalf("resolveLocalConflictsWithAgentDeps: %v", err)
	}
	if len(agent.InteractiveCalls) != 2 || !strings.Contains(agent.InteractiveCalls[1].Prompt, "local-only repo") {
		t.Fatalf("local prompt call = %#v", agent.InteractiveCalls)
	}

	ConflictPromptGenWithPush = func(sourceBranch, targetBranch string, conflicts []string, pushRef string) string {
		return "custom-push:" + sourceBranch + ":" + targetBranch + ":" + pushRef + ":" + strings.Join(conflicts, ",")
	}
	if err := resolveConflictsDetachedDeps(deps, "/repo", "feature", "main", []string{"detached.go"}, "refs/heads/out"); err != nil {
		t.Fatalf("resolveConflictsDetachedDeps custom: %v", err)
	}
	if len(agent.InteractiveCalls) != 3 || !strings.Contains(agent.InteractiveCalls[2].Prompt, "custom-push:feature:main:refs/heads/out") {
		t.Fatalf("custom push prompt call = %#v", agent.InteractiveCalls)
	}

	ConflictPromptGen = func(sourceBranch, targetBranch string, conflicts []string) string {
		return "custom:" + sourceBranch + ":" + targetBranch + ":" + strings.Join(conflicts, ",")
	}
	if err := invokeAgentForConflictsDeps(deps, "/repo", "feature", "main", []string{"custom.go"}); err != nil {
		t.Fatalf("invokeAgentForConflictsDeps custom: %v", err)
	}
	if len(agent.InteractiveCalls) != 4 || !strings.Contains(agent.InteractiveCalls[3].Prompt, "custom:feature:main:custom.go") {
		t.Fatalf("custom prompt call = %#v", agent.InteractiveCalls)
	}

	agent.InteractiveErr = errors.New("agent failed")
	if err := invokeAgentDeps(deps, "/repo", "prompt", "agent"); !errors.Is(err, agent.InteractiveErr) {
		t.Fatalf("invokeAgentDeps err = %v", err)
	}
}

func TestConflictResolutionDefaultWrappersUseDefaultDeps(t *testing.T) {
	agent := &clitest.MockAgentInvoker{}

	rootDeps := cli.TestingGetDefaultDeps()
	oldAgent := rootDeps.Agent
	oldGen, oldPushGen := ConflictPromptGen, ConflictPromptGenWithPush
	rootDeps.Agent = agent
	ConflictPromptGen = nil
	ConflictPromptGenWithPush = nil
	t.Cleanup(func() {
		rootDeps.Agent = oldAgent
		ConflictPromptGen = oldGen
		ConflictPromptGenWithPush = oldPushGen
	})

	if err := resolveConflictsWithAgent("/repo", "feature", "main", []string{"a.go"}); err != nil {
		t.Fatalf("resolveConflictsWithAgent: %v", err)
	}
	if err := resolveConflictsDetached("/repo", "feature", "main", []string{"b.go"}, "refs/heads/out"); err != nil {
		t.Fatalf("resolveConflictsDetached: %v", err)
	}
	if len(agent.InteractiveCalls) != 2 {
		t.Fatalf("interactive calls = %#v, want 2", agent.InteractiveCalls)
	}
	if !strings.Contains(agent.InteractiveCalls[0].Prompt, "a.go") || !strings.Contains(agent.InteractiveCalls[1].Prompt, "refs/heads/out") {
		t.Fatalf("prompts = %#v", agent.InteractiveCalls)
	}
}
