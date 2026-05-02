package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

// hooksCmd is the hidden parent command for all hook-related subcommands.
// Its PersistentPreRunE intentionally does NOT call cli.ResolveAndSetBackend()
// or DefaultDeps() — this is critical for performance because hooks fire
// on every Claude turn and must complete as fast as possible.
var hooksCmd = &cobra.Command{
	Use:    "hooks",
	Short:  "Manage agent lifecycle hooks",
	Hidden: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Intentionally empty — replaces root's PersistentPreRunE.
		// Hook commands must be fast; they skip backend resolution and
		// dependency injection entirely.
		return nil
	},
}

// hooksClaudeCodeCmd groups the four Claude Code hook handlers.
var hooksClaudeCodeCmd = &cobra.Command{
	Use:    "claude-code",
	Short:  "Claude Code hook handlers",
	Hidden: true,
}

// hookSessionStartCmd handles the Claude Code SessionStart hook.
var hookSessionStartCmd = &cobra.Command{
	Use:    "session-start",
	Short:  "Handle Claude Code SessionStart hook",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runClaudeHook(cmd, "session-start")
	},
}

// hookUserPromptSubmitCmd handles the Claude Code UserPromptSubmit hook.
var hookUserPromptSubmitCmd = &cobra.Command{
	Use:    "user-prompt-submit",
	Short:  "Handle Claude Code UserPromptSubmit hook",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runClaudeHook(cmd, "user-prompt-submit")
	},
}

// hookStopCmd handles the Claude Code Stop hook.
var hookStopCmd = &cobra.Command{
	Use:    "stop",
	Short:  "Handle Claude Code Stop hook",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runClaudeHook(cmd, "stop")
	},
}

// hookPreTaskCmd handles the Claude Code PreToolUse[Task] hook.
var hookPreTaskCmd = &cobra.Command{
	Use:    "pre-task",
	Short:  "Handle Claude Code PreToolUse[Task] hook",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runClaudeHook(cmd, "pre-task")
	},
}

// hookPostTaskCmd handles the Claude Code PostToolUse[Task] hook.
var hookPostTaskCmd = &cobra.Command{
	Use:    "post-task",
	Short:  "Handle Claude Code PostToolUse[Task] hook",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runClaudeHook(cmd, "post-task")
	},
}

// hookYieldGuardCmd checks the yield file and blocks all tools if yield is requested.
// This is a PreToolUse hook with an empty matcher (fires on ALL tools), providing
// sub-minute cooperative preemption during active Claude invocations.
var hookYieldGuardCmd = &cobra.Command{
	Use:    "yield-guard",
	Short:  "Check yield file and block tools if yield requested",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runYieldGuard(cmd)
	},
}

// hookSessionEndCmd handles the Claude Code SessionEnd hook.
var hookSessionEndCmd = &cobra.Command{
	Use:    "session-end",
	Short:  "Handle Claude Code SessionEnd hook",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runClaudeHook(cmd, "session-end")
	},
}

// runClaudeHook is the shared logic for all four Claude Code hook handlers.
// It reads LOOM_SESSION_ID and the workspace runtime dir from env, reads JSON from
// stdin, parses it, and dispatches the event. Always returns nil so the
// hook process exits 0.
func runClaudeHook(cmd *cobra.Command, hookName string) error {
	sessionID := os.Getenv("LOOM_SESSION_ID")
	runtimeDir := os.Getenv("LOOM_WORKSPACE_RUNTIME_DIR")
	if runtimeDir == "" {
		runtimeDir = os.Getenv("LOOM_BEADS_DIR") // legacy hook compatibility
	}

	event, err := ParseClaudeHookInput(hookName, cmd.InOrStdin())
	if err != nil {
		fmt.Fprintf(os.Stderr, "loom hook %s: parse error: %v\n", hookName, err)
		return nil // Always exit 0
	}

	_ = dispatchHookEvent(event, runtimeDir, sessionID)

	// Yield check for stop hook (defense-in-depth)
	if hookName == "stop" {
		if yieldFile := os.Getenv("LOOM_YIELD_FILE"); yieldFile != "" {
			if _, statErr := os.Stat(yieldFile); statErr == nil {
				fmt.Fprintf(os.Stderr, "loom hook stop: yield file detected, ensuring stop\n")
			}
		}
	}

	return nil // Always exit 0
}

// runYieldGuard checks the yield file and blocks tools if yield is requested.
// It does NOT read stdin — only checks the LOOM_YIELD_FILE env var.
func runYieldGuard(cmd *cobra.Command) error {
	yieldFile := os.Getenv("LOOM_YIELD_FILE")
	blockJSON, shouldBlock := checkYieldForGuard(yieldFile)
	if !shouldBlock {
		return nil // exit 0 — tool proceeds
	}
	_, _ = fmt.Fprint(cmd.OutOrStdout(), blockJSON)
	os.Exit(2) // signal tool block to Claude Code
	return nil // unreachable
}

// checkYieldForGuard checks if a yield file exists at the given path and returns
// the JSON to print to stdout and whether to block. Extracted for testability.
func checkYieldForGuard(yieldFile string) (blockJSON string, shouldBlock bool) {
	if yieldFile == "" {
		return "", false
	}
	if _, err := os.Stat(yieldFile); err != nil {
		return "", false
	}
	// File exists — read reason (best-effort)
	reason := "unknown"
	data, err := os.ReadFile(yieldFile) //nolint:gosec // path from trusted env var
	if err == nil {
		var req struct {
			Reason string `json:"reason"`
		}
		if json.Unmarshal(data, &req) == nil && req.Reason != "" {
			reason = req.Reason
		}
	}
	resp := map[string]string{
		"decision": "block",
		"reason":   fmt.Sprintf("Yield requested (%s) — please stop and exit immediately.", reason),
	}
	out, _ := json.Marshal(resp)
	return string(out), true
}

// hooksInstallForce overrides the fleet mode warning.
var hooksInstallForce bool

// hooksInstallCmd installs loom hooks into .claude/settings.json.
var hooksInstallCmd = &cobra.Command{
	Use:   "install [worktree-path]",
	Short: "Install loom hooks into Claude Code settings",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := "."
		if len(args) > 0 {
			path = args[0]
		}
		absPath, err := filepath.Abs(path)
		if err != nil {
			return fmt.Errorf("failed to resolve path: %w", err)
		}
		if cli.IsFleetModeFromEnv() && !hooksInstallForce {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Warning: fleet mode is active — hooks may not be needed (install anyway with --force)\n")
			return nil
		}
		if err := InstallClaudeHooks(absPath); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Hooks installed in %s\n", absPath)
		return nil
	},
}

// hooksUninstallCmd removes loom hooks from .claude/settings.json.
var hooksUninstallCmd = &cobra.Command{
	Use:   "uninstall [worktree-path]",
	Short: "Remove loom hooks from Claude Code settings",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := "."
		if len(args) > 0 {
			path = args[0]
		}
		absPath, err := filepath.Abs(path)
		if err != nil {
			return fmt.Errorf("failed to resolve path: %w", err)
		}
		if err := UninstallClaudeHooks(absPath); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Hooks uninstalled from %s\n", absPath)
		return nil
	},
}

// hooksStatusCmd shows whether loom hooks are installed.
var hooksStatusCmd = &cobra.Command{
	Use:   "status [worktree-path]",
	Short: "Show loom hooks installation status",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := "."
		if len(args) > 0 {
			path = args[0]
		}
		absPath, err := filepath.Abs(path)
		if err != nil {
			return fmt.Errorf("failed to resolve path: %w", err)
		}
		installed, hooks, err := ClaudeHooksStatus(absPath)
		if err != nil {
			return err
		}
		if !installed {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Hooks not installed in %s\n", absPath)
			return nil
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Hooks installed in %s\n", absPath)
		for _, h := range hooks {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", h)
		}
		return nil
	},
}

func init() {
	// Wire up the Claude Code hook handlers
	hooksClaudeCodeCmd.AddCommand(hookSessionStartCmd)
	hooksClaudeCodeCmd.AddCommand(hookUserPromptSubmitCmd)
	hooksClaudeCodeCmd.AddCommand(hookStopCmd)
	hooksClaudeCodeCmd.AddCommand(hookSessionEndCmd)
	hooksClaudeCodeCmd.AddCommand(hookPreTaskCmd)
	hooksClaudeCodeCmd.AddCommand(hookPostTaskCmd)
	hooksClaudeCodeCmd.AddCommand(hookYieldGuardCmd)

	// Wire up user-facing commands and claude-code subgroup
	hooksCmd.AddCommand(hooksClaudeCodeCmd)
	hooksInstallCmd.Flags().BoolVar(&hooksInstallForce, "force", false,
		"Install hooks even when fleet mode is active")
	hooksCmd.AddCommand(hooksInstallCmd)
	hooksCmd.AddCommand(hooksUninstallCmd)
	hooksCmd.AddCommand(hooksStatusCmd)

	// Register hooksCmd under cli.GetRootCmd()
	cli.RegisterCommand(hooksCmd)
}
