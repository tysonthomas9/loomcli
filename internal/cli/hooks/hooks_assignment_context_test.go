package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/epicrunner"
)

func TestMaybeWriteClaudeAssignmentContext_UserPromptSubmit(t *testing.T) {
	restore := stubClaudeAssignmentContext(&epicrunner.LeadAssignmentContext{
		WorkspaceKey:          "WS",
		LeadName:              "nova",
		EpicID:                "EPIC-1",
		AssignmentVersion:     "v1",
		OrchestratorSessionID: "lead-session",
	})
	defer restore()

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)

	maybeWriteClaudeAssignmentContext(cmd, "user-prompt-submit")

	var got claudeHookAssignmentOutput
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &got); err != nil {
		t.Fatalf("unmarshal hook output %q: %v", out.String(), err)
	}
	if got.HookSpecificOutput.HookEventName != "UserPromptSubmit" {
		t.Fatalf("hookEventName = %q", got.HookSpecificOutput.HookEventName)
	}
	if got.HookSpecificOutput.SessionTitle != "nova - EPIC-1" {
		t.Fatalf("sessionTitle = %q", got.HookSpecificOutput.SessionTitle)
	}
	if !strings.Contains(got.HookSpecificOutput.AdditionalContext, "assigned_epic: EPIC-1") ||
		!strings.Contains(got.HookSpecificOutput.AdditionalContext, "authoritative backend state") {
		t.Fatalf("additionalContext = %q", got.HookSpecificOutput.AdditionalContext)
	}
}

func TestMaybeWriteClaudeAssignmentContext_SkipsUnsupportedHookAndEmptyAssignment(t *testing.T) {
	for _, tt := range []struct {
		name       string
		hook       string
		assignment *epicrunner.LeadAssignmentContext
	}{
		{name: "unsupported", hook: "post-task", assignment: &epicrunner.LeadAssignmentContext{LeadName: "nova", EpicID: "EPIC-1"}},
		{name: "empty", hook: "user-prompt-submit", assignment: nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			restore := stubClaudeAssignmentContext(tt.assignment)
			defer restore()

			var out bytes.Buffer
			cmd := &cobra.Command{}
			cmd.SetOut(&out)

			maybeWriteClaudeAssignmentContext(cmd, tt.hook)
			if out.Len() != 0 {
				t.Fatalf("output = %q, want empty", out.String())
			}
		})
	}
}

func stubClaudeAssignmentContext(assignment *epicrunner.LeadAssignmentContext) func() {
	orig := loadClaudeHookAssignmentContext
	loadClaudeHookAssignmentContext = func(context.Context) (*epicrunner.LeadAssignmentContext, error) {
		return assignment, nil
	}
	return func() {
		loadClaudeHookAssignmentContext = orig
	}
}
