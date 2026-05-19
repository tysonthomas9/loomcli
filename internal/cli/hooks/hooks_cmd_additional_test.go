package hooks

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestHookEventTypeStringAndTaskParsers(t *testing.T) {
	tests := map[HookEventType]string{
		HookSessionStart:  "SessionStart",
		HookTurnStart:     "TurnStart",
		HookTurnEnd:       "TurnEnd",
		HookSessionEnd:    "SessionEnd",
		HookSubagentStart: "SubagentStart",
		HookSubagentEnd:   "SubagentEnd",
		0:                 "Unknown",
	}
	for eventType, want := range tests {
		if got := eventType.String(); got != want {
			t.Fatalf("%v.String() = %q, want %q", int(eventType), got, want)
		}
	}

	pre, err := ParseClaudeHookInput("pre-task", strings.NewReader(`{"session_id":"s","transcript_path":"/tmp/t","tool_use_id":"tool-1","tool_input":{"description":"work"}}`))
	if err != nil {
		t.Fatalf("pre-task parse: %v", err)
	}
	if pre.Type != HookSubagentStart || pre.ToolUseID != "tool-1" || !json.Valid(pre.ToolInput) {
		t.Fatalf("pre-task event = %#v", pre)
	}

	post, err := ParseClaudeHookInput("post-task", strings.NewReader(`{"session_id":"s","transcript_path":"/tmp/t","tool_use_id":"tool-2","tool_input":{},"tool_response":{"agentId":"agent-1"}}`))
	if err != nil {
		t.Fatalf("post-task parse: %v", err)
	}
	if post.Type != HookSubagentEnd || post.SubagentID != "agent-1" {
		t.Fatalf("post-task event = %#v", post)
	}
}

func TestHooksInstallStatusUninstallCommandRunE(t *testing.T) {
	dir := t.TempDir()
	oldForce := hooksInstallForce
	t.Cleanup(func() { hooksInstallForce = oldForce })

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := hooksInstallCmd.RunE(cmd, []string{dir}); err != nil {
		t.Fatalf("install RunE: %v", err)
	}
	if !strings.Contains(out.String(), "Hooks installed") {
		t.Fatalf("install output = %q", out.String())
	}

	out.Reset()
	cmd.SetOut(&out)
	if err := hooksStatusCmd.RunE(cmd, []string{dir}); err != nil {
		t.Fatalf("status RunE: %v", err)
	}
	if !strings.Contains(out.String(), "Hooks installed") {
		t.Fatalf("status output = %q", out.String())
	}

	out.Reset()
	cmd.SetOut(&out)
	if err := hooksUninstallCmd.RunE(cmd, []string{dir}); err != nil {
		t.Fatalf("uninstall RunE: %v", err)
	}
	if !strings.Contains(out.String(), "Hooks uninstalled") {
		t.Fatalf("uninstall output = %q", out.String())
	}
}

func TestHooksInstallRunEFleetModeWarningAndDefaultPath(t *testing.T) {
	dir := t.TempDir()
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCwd) })

	t.Setenv("LOOM_ISSUE_BACKEND", "fleet")
	oldForce := hooksInstallForce
	t.Cleanup(func() { hooksInstallForce = oldForce })
	hooksInstallForce = false

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := hooksInstallCmd.RunE(cmd, nil); err != nil {
		t.Fatalf("fleet install RunE: %v", err)
	}
	if !strings.Contains(out.String(), "fleet mode is active") {
		t.Fatalf("fleet output = %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("settings should not be created without force: %v", err)
	}
}
