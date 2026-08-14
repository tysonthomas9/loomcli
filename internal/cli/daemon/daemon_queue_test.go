package daemon

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
)

func captureQueueHeaderOutput(t *testing.T, fn func()) string {
	t.Helper()
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = oldStdout })

	fn()
	if err := w.Close(); err != nil {
		t.Fatalf("close stdout writer: %v", err)
	}
	os.Stdout = oldStdout

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close stdout reader: %v", err)
	}
	return buf.String()
}

func TestPrintQueueHeader_ShowsAgentRoleLabelConstraints(t *testing.T) {
	agent := &AgentEntry{Worktree: "atlas", Role: "architect"}
	constraints := cli.RoleConstraints{
		TaskFilter:    "any",
		Labels:        []string{"architect", "area:api"},
		ExcludeLabels: []string{"needs-revision", "blocked"},
	}

	output := captureQueueHeaderOutput(t, func() {
		printQueueHeader(agent.Worktree, agent, constraints)
	})
	for _, want := range []string{
		"Agent role: architect\n",
		"Labels: architect, area:api\n",
		"Exclude labels: needs-revision, blocked\n",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("queue header missing %q:\n%s", want, output)
		}
	}
}

func TestPrintQueueHeader_OmitsEmptyLabelConstraints(t *testing.T) {
	agent := &AgentEntry{Worktree: "ember", Role: "task"}
	output := captureQueueHeaderOutput(t, func() {
		printQueueHeader(agent.Worktree, agent, cli.RoleConstraints{TaskFilter: "has_design"})
	})

	if strings.Contains(output, "Labels:") || strings.Contains(output, "Exclude labels:") {
		t.Fatalf("queue header rendered empty label constraints:\n%s", output)
	}
}

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
