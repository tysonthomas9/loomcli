package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// envOrchestratorSessionID is the env var lead injects so descendants
// (e.g. agents created by `loom agentdef add` from within this lead's tmux
// session) auto-attribute back to this lead session via OrchestratorSessionID.
const envOrchestratorSessionID = "LOOM_ORCHESTRATOR_SESSION_ID"

const leadHeartbeatInterval = 30 * time.Second
const leadStoreOpTimeout = 10 * time.Second

// leadMessage is an optional initial user request appended to the lead system
// prompt so the agent starts with a concrete task to address. Populated by the
// --message flag.
var leadMessage string

var leadCmd = &cobra.Command{
	Use:     "lead",
	Short:   "Interactive project management with AI agent",
	GroupID: "agents",
	Long: `Launch an interactive AI agent session for project management.

Unlike 'plan' and 'task' (which are autonomous agents), 'lead' is a
human-collaborative mode where the AI agent helps you:
  - Review and approve/reject plans from planning agents
  - Create new tickets (tasks, bugs, features, epics)
  - Triage and prioritize the backlog
  - Manage dependencies between tickets

This command does not require a worktree - it can run from the main
repository or any worktree.

Use --message to seed the session with an initial user request. The message
is appended to the lead system prompt, so the agent performs its normal
lead-mode startup and then addresses the request using lead-mode conventions.`,
	Args: cobra.NoArgs,
	Run:  runLead,
}

func init() {
	cli.RegisterCommand(leadCmd)
	leadCmd.Flags().StringVar(&leadMessage, "message", "", "Initial user request to address in lead mode")
}

func runLead(cmd *cobra.Command, args []string) {
	// Get current working directory
	workDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting working directory: %v\n", err)
		os.Exit(1)
	}

	// Check backend health before invoking. If the binary isn't installed,
	// show a helpful error and drop into a shell so the user can fix it.
	backendName := cli.GetBackendName()
	if hs, ok := backends.CheckBackendHealth(backendName); ok && !hs.Installed {
		fmt.Fprintf(os.Stderr, "Error: %s backend is not installed (%s)\n\n", backendName, hs.Message)
		fmt.Fprintf(os.Stderr, "Install it and try again. Dropping into a shell so you can fix this.\n\n")
		execShell(workDir)
		return
	}

	fmt.Println("=========================================")
	fmt.Println("Starting LEAD mode (Interactive)")
	fmt.Println("=========================================")
	fmt.Println()

	// Best-effort: register this lead as an orchestrator session so workers
	// the AI spawns via `loom agentdef add` are attributed back to it. Skips
	// silently if there is no active workspace or fleet-db is unreachable.
	finalize := registerLeadOrchestratorSession(context.Background(), workDir)
	defer finalize()

	// Generate the lead prompt and append the user's initial request if provided.
	prompt := GenerateLeadPrompt()
	if leadMessage != "" {
		prompt += "\n\n## User's Initial Request\n\n" + leadMessage +
			"\n\nAddress this request using the lead mode conventions above."
	}

	// Invoke agent interactively (no agent name needed - lead mode doesn't claim tasks)
	if err := cli.InvokeAgent(workDir, prompt, ""); err != nil {
		fmt.Fprintf(os.Stderr, "Error running agent: %v\n", err)
		fmt.Fprintf(os.Stderr, "\nDropping into a shell. Fix the issue and run 'loom lead' to retry.\n\n")
		execShell(workDir)
	}
}

// registerLeadOrchestratorSession opens fleet-db, creates an
// AgentSession{Kind:orchestration}, and starts a heartbeat goroutine. Returns a
// finalize fn the caller defers to mark the session completed and stop the
// heartbeat. Best-effort: any error returns a no-op finalize so lead always runs.
func registerLeadOrchestratorSession(ctx context.Context, workDir string) func() {
	noop := func() {}

	handle, err := cmdstore.OpenStore(ctx)
	if err != nil {
		slog.Debug("lead orchestrator session: store unavailable, continuing without registration", "err", err)
		return noop
	}

	ws, err := bootstrap.ResolveActiveWorkspaceKey(ctx, handle.Store.Workspaces())
	if err != nil {
		_ = handle.Close()
		slog.Debug("lead orchestrator session: no active workspace, continuing without registration", "err", err)
		return noop
	}

	sid := "lead-" + uuid.New().String()
	actor := os.Getenv("USER")
	if actor == "" {
		actor = "unknown"
	}

	createCtx, createCancel := context.WithTimeout(ctx, leadStoreOpTimeout)
	_, err = handle.Store.AgentSessions().Create(createCtx, store.AgentSessionCreate{
		WorkspaceKey: ws,
		SessionID:    sid,
		Kind:         domain.AgentSessionKindOrchestration,
		Status:       domain.AgentSessionRunning,
		Metadata: map[string]string{
			"actor":        actor,
			"lead_workdir": workDir,
		},
	})
	createCancel()
	if err != nil {
		_ = handle.Close()
		slog.Warn("lead orchestrator session: create failed, continuing without registration", "err", err)
		return noop
	}

	// Inject env so child agents (`loom agentdef add` from within this tmux
	// session) auto-stamp OrchestratorSessionID = this session's ID.
	if err := os.Setenv(envOrchestratorSessionID, sid); err != nil {
		slog.Warn("lead orchestrator session: setenv failed", "err", err)
	}

	fmt.Printf("Lead session: %s (orchestrator linkage active)\n\n", sid)

	stopHB := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go heartbeatLeadSession(handle, ws, sid, stopHB, &wg)

	return func() {
		close(stopHB)
		wg.Wait()
		finCtx, finCancel := context.WithTimeout(context.Background(), leadStoreOpTimeout)
		defer finCancel()
		status := domain.AgentSessionCompleted
		now := time.Now().UTC()
		finishedAt := &now
		if _, err := handle.Store.AgentSessions().Update(finCtx, ws, sid, store.AgentSessionUpdate{
			Status:     &status,
			FinishedAt: &finishedAt,
		}); err != nil {
			slog.Debug("lead orchestrator session: finalize failed", "err", err)
		}
		_ = handle.Close()
	}
}

// heartbeatLeadSession periodically refreshes the lead session's last_heartbeat
// so observers can detect a stale lead (e.g. tmux force-killed). Stops on stopHB
// close. Best-effort — heartbeat failures are logged at debug only.
func heartbeatLeadSession(handle *bootstrap.StoreHandle, ws, sid string, stopHB <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	ticker := time.NewTicker(leadHeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stopHB:
			return
		case <-ticker.C:
			hbCtx, cancel := context.WithTimeout(context.Background(), leadStoreOpTimeout)
			if _, err := handle.Store.AgentSessions().Heartbeat(hbCtx, ws, sid); err != nil {
				slog.Debug("lead orchestrator session: heartbeat failed", "err", err)
			}
			cancel()
		}
	}
}

// execShell replaces the current process with an interactive shell.
// Falls back to running the shell as a subprocess if exec fails.
func execShell(workDir string) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	// Try to replace the process entirely so the terminal stays alive.
	// The shell path is read from the user's own $SHELL env var (trusted),
	// with a safe fallback to /bin/bash. This is an interactive drop-in,
	// not a user-supplied command string.
	// #nosec G204 -- shell path is from $SHELL/static fallback, not user input
	if err := syscall.Exec(shell, []string{shell}, os.Environ()); err != nil {
		// Fallback: run as a child process.
		// #nosec G204 -- shell path is from $SHELL/static fallback, not user input
		cmd := exec.Command(shell)
		cmd.Dir = workDir
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		_ = cmd.Run()
	}
}
