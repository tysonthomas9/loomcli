package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
)

func TestResolveRoleConfigStatic_BuiltinPlan(t *testing.T) {
	config := &DaemonConfig{
		Roles: map[string]RoleConfig{},
	}

	rc, err := ResolveRoleConfigStatic("plan", config, "/tmp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rc.Description == "" {
		t.Error("Description should not be empty for built-in role")
	}
}

func TestResolveRoleConfigStatic_BuiltinWithOverlay(t *testing.T) {
	maxP := 2
	config := &DaemonConfig{
		Roles: map[string]RoleConfig{
			"plan": {
				Skills:      []string{"planning", "architecture"},
				MaxPriority: &maxP,
			},
		},
	}

	rc, err := ResolveRoleConfigStatic("plan", config, "/tmp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rc.Skills) != 2 || rc.Skills[0] != "planning" {
		t.Errorf("Skills = %v, want [planning architecture]", rc.Skills)
	}
	if rc.MaxPriority == nil || *rc.MaxPriority != 2 {
		t.Errorf("MaxPriority = %v, want 2", rc.MaxPriority)
	}
}

func TestResolveRoleConfigStatic_CustomRole(t *testing.T) {
	tmpDir := t.TempDir()
	promptPath := filepath.Join(tmpDir, "custom.md")
	if err := os.WriteFile(promptPath, []byte("prompt"), 0644); err != nil {
		t.Fatal(err)
	}

	config := &DaemonConfig{
		Roles: map[string]RoleConfig{
			"reviewer": {
				Description: "Code reviewer",
				PromptFile:  "custom.md",
				TaskFilter:  "has_design",
			},
		},
	}

	rc, err := ResolveRoleConfigStatic("reviewer", config, tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rc.Description != "Code reviewer" {
		t.Errorf("Description = %q, want %q", rc.Description, "Code reviewer")
	}
	if rc.PromptFile != promptPath {
		t.Errorf("PromptFile = %q, want %q", rc.PromptFile, promptPath)
	}
}

func TestResolveRoleConfigStatic_UnknownRole(t *testing.T) {
	config := &DaemonConfig{
		Roles: map[string]RoleConfig{},
	}

	_, err := ResolveRoleConfigStatic("nonexistent", config, "/tmp")
	if err == nil {
		t.Fatal("expected error for unknown role")
	}
}

func TestResolveRoleConfigStatic_CustomMissingPrompt(t *testing.T) {
	config := &DaemonConfig{
		Roles: map[string]RoleConfig{
			"reviewer": {
				Description: "Code reviewer",
			},
		},
	}

	_, err := ResolveRoleConfigStatic("reviewer", config, "/tmp")
	if err == nil {
		t.Fatal("expected error for custom role without prompt_file")
	}
}

func TestFindAgentEntry_Found(t *testing.T) {
	config := &DaemonConfig{
		Agents: []AgentEntry{
			{Worktree: "falcon", Role: "plan"},
			{Worktree: "nova", Role: "task"},
		},
	}

	agent, err := findAgentEntryStatic(config, "nova")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if agent.Worktree != "nova" {
		t.Errorf("Worktree = %q, want %q", agent.Worktree, "nova")
	}
	if agent.Role != "task" {
		t.Errorf("Role = %q, want %q", agent.Role, "task")
	}
}

func TestFindAgentEntry_NotFound(t *testing.T) {
	config := &DaemonConfig{
		Agents: []AgentEntry{
			{Worktree: "falcon", Role: "plan"},
			{Worktree: "nova", Role: "task"},
		},
	}

	_, err := findAgentEntryStatic(config, "spark")
	if err == nil {
		t.Fatal("expected error for unknown agent")
	}
}

func TestDaemonQueueCmd_Registration(t *testing.T) {
	found := false
	for _, sub := range daemonCmd.Commands() {
		if sub.Name() == "queue" {
			found = true
			break
		}
	}
	if !found {
		t.Error("daemonQueueCmd not registered as subcommand of daemonCmd")
	}
}

func TestResolveRoleConfigStatic_MatchesDaemonMethod(t *testing.T) {
	maxP := 3
	config := &DaemonConfig{
		Roles: map[string]RoleConfig{
			"plan": {
				Skills:      []string{"go"},
				MaxPriority: &maxP,
				TaskFilter:  "needs_plan",
			},
		},
	}

	staticRC, staticErr := ResolveRoleConfigStatic("plan", config, "/tmp")
	// resolveRoleConfig moved to supervisor (unexported); use static version for comparison
	daemonRC, daemonErr := supervisor.ResolveRoleConfigStatic("plan", config, "/tmp")

	if staticErr != nil {
		t.Fatalf("static error: %v", staticErr)
	}
	if daemonErr != nil {
		t.Fatalf("daemon error: %v", daemonErr)
	}

	if staticRC.Description != daemonRC.Description {
		t.Errorf("Description: static=%q daemon=%q", staticRC.Description, daemonRC.Description)
	}
	if len(staticRC.Skills) != len(daemonRC.Skills) {
		t.Errorf("Skills: static=%v daemon=%v", staticRC.Skills, daemonRC.Skills)
	}
	if staticRC.TaskFilter != daemonRC.TaskFilter {
		t.Errorf("TaskFilter: static=%q daemon=%q", staticRC.TaskFilter, daemonRC.TaskFilter)
	}
	if (staticRC.MaxPriority == nil) != (daemonRC.MaxPriority == nil) {
		t.Errorf("MaxPriority: static=%v daemon=%v", staticRC.MaxPriority, daemonRC.MaxPriority)
	} else if staticRC.MaxPriority != nil && *staticRC.MaxPriority != *daemonRC.MaxPriority {
		t.Errorf("MaxPriority: static=%d daemon=%d", *staticRC.MaxPriority, *daemonRC.MaxPriority)
	}
}

func TestDaemonQueueScoringAndOutputHelpers(t *testing.T) {
	maxPriority := 3
	issues := []backend.IssueData{
		{ID: "TASK-2", Title: "best", Status: "open", IssueType: "task", Priority: 1, Labels: []string{"go"}, SourceRepo: "api", Design: "ready"},
		{ID: "TASK-1", Title: "also good", Status: "open", IssueType: "task", Priority: 2, Labels: []string{"go"}, SourceRepo: "api", Design: "ready"},
		{ID: "TASK-3", Title: "repo mismatch", Status: "open", IssueType: "task", Priority: 1, Labels: []string{"go"}, SourceRepo: "web", Design: "ready"},
		{ID: "TASK-4", Title: "too much", Status: "open", IssueType: "task", Priority: 5, Labels: []string{"go"}, SourceRepo: "api", Design: "ready"},
		{ID: "TASK-5", Title: "no design", Status: "open", IssueType: "task", Priority: 1, Labels: []string{"go"}, SourceRepo: "api"},
	}

	matched, rejected := scoreQueueCandidates(issues, cli.RoleConstraints{
		TaskFilter:  "has_design",
		Skills:      []string{"go"},
		MaxPriority: &maxPriority,
		SourceRepos: []string{"api"},
	})
	if len(matched) != 3 {
		t.Fatalf("matched len = %d, want 3: %+v", len(matched), matched)
	}
	if matched[0].Issue.ID != "TASK-2" || matched[1].Issue.ID != "TASK-1" {
		t.Fatalf("matched order = %+v, want TASK-2 then TASK-1", matched)
	}
	if matched[2].Issue.ID != "TASK-3" || matched[2].Reason != "repo mismatch" {
		t.Fatalf("repo mismatch fallback match = %+v", matched[2])
	}
	if rejected["priority 5 exceeds max 3"] != 1 || rejected["filter: not ready to implement"] != 1 {
		t.Fatalf("rejections = %+v", rejected)
	}

	out := captureDaemonStdout(t, func() {
		printQueueResults(append(matched, matched...), map[string]int{"z reason": 2, "a reason": 1})
	})
	for _, want := range []string{"6 tasks match agent constraints (showing top 5)", "TASK-2", "3 filtered:", "1 a reason", "2 z reason"} {
		if !strings.Contains(out, want) {
			t.Fatalf("queue output missing %q:\n%s", want, out)
		}
	}

	out = captureDaemonStdout(t, func() {
		printQueueResults(nil, nil)
	})
	if !strings.Contains(out, "0 tasks match agent constraints") {
		t.Fatalf("empty queue output = %q", out)
	}
}

func TestResolveQueueSourceReposWarnsWithoutWorkspace(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv("LOOM_WORKSPACE", "")
	agent := &AgentEntry{Worktree: "worker", Role: "task", Repos: []string{"api"}}

	resolveQueueSourceRepos(agent)

	if len(agent.SourceRepos) != 0 {
		t.Fatalf("SourceRepos = %+v, want empty without workspace", agent.SourceRepos)
	}
}

func TestRunDaemonQueueUsesHookedDependencies(t *testing.T) {
	oldGetwd := daemonGetwdFn
	oldLoad := loadDaemonConfigFn
	oldResolveRole := resolveQueueRoleConfigFn
	oldResolveRepos := resolveQueueSourceReposFn
	oldFetch := fetchQueueReadyIssuesFn
	t.Cleanup(func() {
		daemonGetwdFn = oldGetwd
		loadDaemonConfigFn = oldLoad
		resolveQueueRoleConfigFn = oldResolveRole
		resolveQueueSourceReposFn = oldResolveRepos
		fetchQueueReadyIssuesFn = oldFetch
	})

	cfg := &DaemonConfig{
		Agents: []AgentEntry{{Worktree: "spark", Role: "task", Parent: "EPIC-1", Repo: "api"}},
	}
	daemonGetwdFn = func() (string, error) { return "/repo", nil }
	loadDaemonConfigFn = func(projectDir string) (*DaemonConfig, error) {
		if projectDir != "/repo" {
			t.Fatalf("projectDir = %q", projectDir)
		}
		return cfg, nil
	}
	resolveQueueRoleConfigFn = func(roleName string, got *DaemonConfig, projectDir string) (RoleConfig, error) {
		if roleName != "task" || got != cfg || projectDir != "/repo" {
			t.Fatalf("role hook args role=%q cfg=%p projectDir=%q", roleName, got, projectDir)
		}
		return RoleConfig{TaskFilter: "has_design", Skills: []string{"go"}}, nil
	}
	resolveQueueSourceReposFn = func(agent *AgentEntry) {
		agent.SourceRepos = []string{"api"}
	}
	fetchQueueReadyIssuesFn = func(parentID, repoLabel string) ([]backend.IssueData, error) {
		if parentID != "EPIC-1" || repoLabel != "api" {
			t.Fatalf("fetch args parent=%q repo=%q", parentID, repoLabel)
		}
		return []backend.IssueData{
			{ID: "TASK-1", Title: "ship it", Status: "open", IssueType: "task", Priority: 1, Labels: []string{"go"}, SourceRepo: "api", Design: "ready"},
			{ID: "TASK-2", Title: "needs plan", Status: "open", IssueType: "task", Priority: 1, SourceRepo: "api"},
		}, nil
	}

	out := captureDaemonStdout(t, func() { runDaemonQueue(nil, []string{"spark"}) })
	for _, want := range []string{"Agent: spark", "Role:  task", "Skills: go", "Source repos: api", "TASK-1", "1 filtered"} {
		if !strings.Contains(out, want) {
			t.Fatalf("queue output missing %q:\n%s", want, out)
		}
	}

	fetchQueueReadyIssuesFn = func(string, string) ([]backend.IssueData, error) { return nil, nil }
	out = captureDaemonStdout(t, func() { runDaemonQueue(nil, []string{"spark"}) })
	if !strings.Contains(out, "No tasks in the ready queue") {
		t.Fatalf("empty queue output = %q", out)
	}
}
