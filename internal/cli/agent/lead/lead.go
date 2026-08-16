package lead

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/agent"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/epicrunner"
	"github.com/tysonthomas9/loomcli/internal/infra/interactionclient"
	leadcontrol "github.com/tysonthomas9/loomcli/internal/infra/interactionlead"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

// envOrchestratorSessionID is the env var lead injects so descendants
// (e.g. agents created by `loom agentdef add` from within this lead's tmux
// session) auto-attribute back to this lead session via OrchestratorSessionID.
const envOrchestratorSessionID = "LOOM_ORCHESTRATOR_SESSION_ID"
const envAgentName = "LOOM_AGENT_NAME"

const leadHeartbeatInterval = 30 * time.Second
const leadStoreOpTimeout = 10 * time.Second

// leadMessage is an optional initial user request appended to the lead system
// prompt so the agent starts with a concrete task to address. Populated by the
// --message flag.
var leadMessage string
var leadPromptFile string

var registerLeadSession = registerLeadOrchestratorSession
var runLeadShellCommand = func(cmd *exec.Cmd) error { return cmd.Run() }

var leadCmd = &cobra.Command{
	Use:     "lead",
	Short:   "Run the interactive terminal-agent runtime",
	GroupID: "agents",
	Long: `Launch an interactive terminal-agent session.

Unlike 'plan' and 'task' (which are autonomous worker agents), 'lead' is the
interactive terminal-agent runtime. The default persona is lead/project
management mode, where the AI agent helps you:
  - Review and approve/reject plans from planning agents
  - Create new tickets (tasks, bugs, features, epics)
  - Triage and prioritize the backlog
  - Manage dependencies between tickets

Pass --prompt to replace the default lead prompt with a role prompt_file while
keeping terminal-agent guardrails and orchestration behavior.

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
	leadCmd.Flags().StringVar(&leadPromptFile, "prompt", "", "Path to terminal-agent prompt template")
}

// leadStartupPrompt picks the lead runtime's boot prompt. A role prompt_file
// supplied via --prompt wins, otherwise inline role prompt and default lead
// prompt resolution happen in that order.
func leadStartupPrompt(ctx context.Context, registration leadSessionRegistration) (string, error) {
	prompt, err := generateLeadTerminalPrompt(ctx, registration)
	if err != nil {
		return "", err
	}
	return applyLeadPromptContextForSession(prompt, registration), nil
}

func runLead(cmd *cobra.Command, args []string) { //nolint:funlen // The foreground CLI keeps lead startup, cleanup, and exit reporting in one lifecycle boundary.
	// Get current working directory
	workDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting working directory: %v\n", err)
		os.Exit(1)
	}

	// Bind to the server-created owner-fenced Interaction session before any
	// recovery shell or backend child can inherit the process environment. A
	// wholly absent envelope is explicit standalone mode; a partial envelope
	// stops before any backend is launched.
	registration := registerLeadSession(context.Background(), workDir)
	if err := registration.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: invalid interactive session authority: %v\n", err)
		return
	}
	defer registration.Finalize()

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

	// Generate the terminal-agent prompt and append the user's initial request if provided.
	prompt, err := leadStartupPrompt(context.Background(), registration)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading terminal prompt: %v\n", err)
		fmt.Fprintf(os.Stderr, "\nDropping into a shell. Fix the prompt file and run 'loom lead' to retry.\n\n")
		execShell(workDir)
		return
	}

	// Invoke agent interactively (no agent name needed - lead mode doesn't claim tasks).
	// Backends with a controlled runtime (codex app-server, harness-wrapper PTY
	// supervision for claude and others) get queued message delivery; anything
	// else falls back to a plain interactive launch.
	handled, invokeErr := backends.RunControlledLeadRuntime(
		context.Background(),
		registration.Store(),
		registration.Runtime(),
		registration.Workspace,
		registration.AgentID,
		registration.SessionID,
		workDir,
		prompt,
		backendName,
	)
	if !handled {
		invokeErr = cli.InvokeAgent(workDir, prompt, "")
	}
	if invokeErr != nil {
		fmt.Fprintf(os.Stderr, "Error running agent: %v\n", invokeErr)
		fmt.Fprintf(os.Stderr, "\nDropping into a shell. Fix the issue and run 'loom lead' to retry.\n\n")
		execShell(workDir)
	}
}

func generateLeadTerminalPrompt(ctx context.Context, registration leadSessionRegistration) (string, error) {
	if strings.TrimSpace(leadPromptFile) != "" {
		return agent.GenerateTerminalPrompt(ctx, leadPromptFile)
	}
	if prompt := loadLeadRolePrompt(ctx, registration); strings.TrimSpace(prompt) != "" {
		return agent.GenerateTerminalPromptText(prompt)
	}
	return agent.GenerateTerminalPrompt(ctx, "")
}

func loadLeadRolePrompt(ctx context.Context, registration leadSessionRegistration) string {
	roleName := strings.TrimSpace(os.Getenv("LOOM_AGENT_ROLE"))
	if roleName == "" {
		roleName = "lead"
	}

	st := registration.Store()
	ws := strings.TrimSpace(registration.Workspace)
	var handle *bootstrap.StoreHandle
	if st == nil || ws == "" {
		openCtx, cancel := context.WithTimeout(ctx, leadStoreOpTimeout)
		defer cancel()
		var ok bool
		handle, ws, ok = openLeadSessionStore(openCtx)
		if !ok {
			return ""
		}
		defer func() { _ = handle.Close() }()
		st = handle.Store
	}
	if st == nil || st.Roles() == nil || ws == "" {
		return ""
	}

	loadCtx, cancel := context.WithTimeout(ctx, leadStoreOpTimeout)
	defer cancel()
	role, err := st.Roles().Get(loadCtx, ws, roleName)
	if errors.Is(err, persistence.ErrNotFound) {
		return ""
	}
	if err != nil {
		slog.Warn("lead inline prompt lookup failed, using default prompt", "workspace", ws, "role", roleName, "err", err)
		return ""
	}
	if role == nil {
		return ""
	}
	return role.Prompt
}

// applyLeadPromptContext appends the backend assignment context and the
// optional --message initial request onto the base terminal-agent prompt.
func applyLeadPromptContext(prompt string) string {
	return applyLeadPromptContextForSession(prompt, leadSessionRegistration{})
}

func applyLeadPromptContextForSession(prompt string, registration leadSessionRegistration) string {
	if assignment := currentLeadAssignmentPrompt(context.Background(), registration); assignment != "" {
		prompt += "\n\n## Loom Backend Assignment\n\n" + assignment
	}
	if leadMessage != "" {
		prompt += "\n\n## User's Initial Request\n\n" + leadMessage +
			"\n\nAddress this request using the lead mode conventions above."
	}
	return prompt
}

func currentLeadAssignmentPrompt(ctx context.Context, registration leadSessionRegistration) string {
	handle := registration.handle
	ws := strings.TrimSpace(registration.Workspace)
	closeHandle := false
	if handle == nil || handle.Store == nil || ws == "" {
		var ok bool
		handle, ws, ok = openLeadSessionStore(ctx)
		if !ok {
			return ""
		}
		closeHandle = true
	}
	if closeHandle {
		defer func() { _ = handle.Close() }()
	}

	loadCtx, cancel := context.WithTimeout(ctx, leadStoreOpTimeout)
	defer cancel()
	assignment, err := epicrunner.LoadLeadAssignmentContext(
		loadCtx,
		epicrunner.NewStoreLeadAssignmentSource(handle.Store),
		ws,
		resolveLeadAgentID(),
	)
	if err != nil || assignment == nil {
		return ""
	}
	if err := markLeadAssignmentDelivered(loadCtx, registration.Runtime(), ws, assignment); err != nil {
		slog.Debug("lead assignment delivery marker failed", "err", err)
	}
	return epicrunner.FormatLeadAssignmentContext(assignment)
}

func markLeadAssignmentDelivered(
	ctx context.Context,
	runtime leadcontrol.SessionRuntime,
	ws string,
	assignment *epicrunner.LeadAssignmentContext,
) error {
	if runtime == nil || assignment == nil {
		return nil
	}
	sessionID := strings.TrimSpace(assignment.OrchestratorSessionID)
	version := strings.TrimSpace(assignment.AssignmentVersion)
	if sessionID == "" || version == "" {
		return nil
	}

	return runtime.PatchSessionRuntimeContext(ctx, interaction.PatchSessionCommand{
		WorkspaceKey: ws,
		SessionID:    sessionID,
		MetadataUpserts: map[string]string{
			"lead_assignment_delivered_version": version,
			"lead_assignment_delivered_epic":    strings.TrimSpace(assignment.EpicID),
		},
	})
}

type leadSessionRegistration struct {
	handle    *bootstrap.StoreHandle
	store     leadcontrol.RuntimeStore
	runtime   leadcontrol.SessionRuntime
	err       error
	Workspace string
	SessionID string
	AgentID   string
	finalize  func()
}

func (r leadSessionRegistration) Finalize() {
	if r.finalize != nil {
		r.finalize()
	}
}

func (r leadSessionRegistration) Store() leadcontrol.RuntimeStore {
	if r.store != nil {
		return r.store
	}
	if r.handle == nil {
		return nil
	}
	return r.handle.Store
}

func (r leadSessionRegistration) Runtime() leadcontrol.SessionRuntime {
	return r.runtime
}

func (r leadSessionRegistration) Err() error {
	return r.err
}

// registerLeadOrchestratorSession accepts only a complete server-issued
// SessionEnvelope. The server has already created the AgentSession and lease;
// the child can only heartbeat, patch its runtime context, finish, and consume
// its inbox through the owner-fenced Interaction API. No envelope is explicit
// standalone mode and remains unregistered.
func registerLeadOrchestratorSession(ctx context.Context, workDir string) leadSessionRegistration {
	noop := func() {}
	empty := leadSessionRegistration{finalize: noop}
	client, registered, err := interactionclient.NewFromEnv()
	if err != nil {
		slog.Warn("lead session envelope rejected", "err", err)
		empty.err = err
		return empty
	}
	if !registered {
		// A generic workspace terminal is intentionally not registered as a
		// durable Interaction session, but it still belongs to the explicit
		// workspace selected by the server-owned PTY launch environment. Preserve
		// that scope for the controlled backend child so its `loom data` commands
		// do not fall back to an unrelated config directory or fail with "no
		// active workspace" after the child environment filters ambient LOOM_*.
		empty.Workspace = strings.TrimSpace(os.Getenv("LOOM_WORKSPACE"))
		empty.AgentID = resolveLeadAgentID()
		return empty
	}
	proof := client.Proof()
	handle, _, storeOK := openLeadSessionStore(ctx)
	if !storeOK {
		handle = nil
	}

	activateLeadSessionEnv(proof.SessionID)
	fmt.Printf("Lead session: %s (Interaction authority active)\n\n", proof.SessionID)
	stopHB, wg := startLeadSessionHeartbeat(client, proof.WorkspaceKey, proof.SessionID)
	return leadSessionRegistration{
		handle:    handle,
		runtime:   client,
		Workspace: proof.WorkspaceKey,
		SessionID: proof.SessionID,
		AgentID:   proof.AgentID,
		finalize:  leadSessionFinalizer(handle, client, proof.WorkspaceKey, proof.SessionID, stopHB, wg),
	}
}

func openLeadSessionStore(ctx context.Context) (*bootstrap.StoreHandle, string, bool) {
	handle, err := cmdstore.OpenStore(ctx)
	if err != nil {
		slog.Debug("lead orchestrator session: store unavailable, continuing without registration", "err", err)
		return nil, "", false
	}
	ws, err := bootstrap.ResolveActiveWorkspaceKey(ctx, handle.Store.Workspaces())
	if err != nil {
		_ = handle.Close()
		slog.Debug("lead orchestrator session: no active workspace, continuing without registration", "err", err)
		return nil, "", false
	}
	return handle, ws, true
}

func activateLeadSessionEnv(sid string) {
	// Child agents spawned from this session inherit the orchestrator ID.
	if err := os.Setenv(envOrchestratorSessionID, sid); err != nil {
		slog.Warn("lead orchestrator session: setenv failed", "err", err)
	}
}

func startLeadSessionHeartbeat(
	runtime leadcontrol.SessionRuntime,
	ws, sid string,
) (chan struct{}, *sync.WaitGroup) {
	stopHB := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go heartbeatLeadSession(runtime, ws, sid, stopHB, &wg)
	return stopHB, &wg
}

func leadSessionFinalizer(
	handle *bootstrap.StoreHandle,
	runtime leadcontrol.SessionRuntime,
	ws, sid string,
	stopHB chan struct{},
	wg *sync.WaitGroup,
) func() {
	return func() {
		close(stopHB)
		wg.Wait()
		finCtx, finCancel := context.WithTimeout(context.Background(), leadStoreOpTimeout)
		defer finCancel()
		if err := runtime.FinishSession(finCtx, interaction.FinishSessionCommand{
			WorkspaceKey: ws,
			SessionID:    sid,
			Status:       interaction.SessionCompleted,
		}); err != nil {
			slog.Debug("lead orchestrator session: finalize failed", "err", err)
		}
		if err := runtime.Close(); err != nil {
			slog.Debug("lead orchestrator session: credential close failed", "err", err)
		}
		if handle != nil {
			_ = handle.Close()
		}
	}
}

func resolveLeadAgentID() string {
	if agentID := strings.TrimSpace(os.Getenv(envAgentName)); agentID != "" {
		return agentID
	}
	return "lead"
}

// heartbeatLeadSession periodically refreshes the lead session's last_heartbeat
// so observers can detect a stale lead (e.g. tmux force-killed). Stops on stopHB
// close. Best-effort — heartbeat failures are logged at debug only.
func heartbeatLeadSession(
	runtime leadcontrol.SessionRuntime,
	ws, sid string,
	stopHB <-chan struct{},
	wg *sync.WaitGroup,
) {
	heartbeatLeadSessionEvery(runtime, ws, sid, stopHB, wg, leadHeartbeatInterval)
}

func heartbeatLeadSessionEvery(
	runtime leadcontrol.SessionRuntime,
	ws, sid string,
	stopHB <-chan struct{},
	wg *sync.WaitGroup,
	interval time.Duration,
) {
	defer wg.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stopHB:
			return
		case <-ticker.C:
			hbCtx, cancel := context.WithTimeout(context.Background(), leadStoreOpTimeout)
			if err := runtime.HeartbeatSession(hbCtx, interaction.HeartbeatSessionCommand{
				WorkspaceKey: ws,
				SessionID:    sid,
				Phase:        "interactive",
				LeaseTTL:     2 * leadHeartbeatInterval,
			}); err != nil {
				slog.Debug("lead orchestrator session: heartbeat failed", "err", err)
			}
			cancel()
		}
	}
}

// execShell runs an interactive shell as a child process. Keeping the lead
// process alive preserves its heartbeat while the recovery shell is open and
// lets runLead's deferred session finalizer run when the shell exits.
func execShell(workDir string) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	// The shell path is read from the user's own $SHELL env var (trusted), with
	// a safe fallback to /bin/bash. This is an interactive drop-in, not a
	// user-supplied command string.
	// #nosec G204 -- shell path is from $SHELL/static fallback, not user input
	cmd := exec.Command(shell)
	cmd.Dir = workDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = runLeadShellCommand(cmd)
}
