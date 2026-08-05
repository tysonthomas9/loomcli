package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/epicrunner"
)

const hookAssignmentTimeout = 2 * time.Second

var loadClaudeHookAssignmentContext = loadClaudeHookAssignmentContextFromStore

type claudeHookAssignmentOutput struct {
	HookSpecificOutput claudeHookSpecificAssignmentOutput `json:"hookSpecificOutput"`
}

type claudeHookSpecificAssignmentOutput struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext,omitempty"`
	SessionTitle      string `json:"sessionTitle,omitempty"`
}

func maybeWriteClaudeAssignmentContext(cmd *cobra.Command, hookName string) {
	hookEventName := claudeNativeHookEventName(hookName)
	if hookEventName == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), hookAssignmentTimeout)
	defer cancel()
	assignment, err := loadClaudeHookAssignmentContext(ctx)
	if err != nil || assignment == nil {
		return
	}

	out := claudeHookAssignmentOutput{
		HookSpecificOutput: claudeHookSpecificAssignmentOutput{
			HookEventName:     hookEventName,
			AdditionalContext: epicrunner.FormatLeadAssignmentContext(assignment),
			SessionTitle:      assignment.LeadName + " - " + assignment.EpicID,
		},
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(raw))
}

func claudeNativeHookEventName(hookName string) string {
	switch hookName {
	case "session-start":
		return "SessionStart"
	case "user-prompt-submit":
		return "UserPromptSubmit"
	case "pre-task":
		return "PreToolUse"
	case "stop":
		return "Stop"
	default:
		return ""
	}
}

func loadClaudeHookAssignmentContextFromStore(ctx context.Context) (*epicrunner.LeadAssignmentContext, error) {
	leadName := strings.TrimSpace(os.Getenv("LOOM_AGENT_NAME"))
	if leadName == "" {
		return nil, nil
	}

	handle, err := cmdstore.OpenStore(ctx)
	if err != nil {
		return nil, nil
	}
	defer func() { _ = handle.Close() }()

	workspace := strings.TrimSpace(os.Getenv("LOOM_WORKSPACE"))
	if workspace == "" {
		workspace, err = cmdstore.ActiveWorkspace(ctx, handle.Store)
		if err != nil {
			return nil, nil
		}
	}
	return epicrunner.LoadLeadAssignmentContext(
		ctx,
		epicrunner.NewStoreLeadAssignmentSource(handle.Store),
		workspace,
		leadName,
	)
}
