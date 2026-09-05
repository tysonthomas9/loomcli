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
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/agent"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/epicrunner"
	"github.com/tysonthomas9/loomcli/internal/hookcfg"
	"github.com/tysonthomas9/loomcli/internal/leadcontrol"
	"github.com/tysonthomas9/loomcli/internal/skillmat"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// envOrchestratorSessionID is the env var lead injects so descendants
// (e.g. agents created by `loom agentdef add` from within this lead's tmux
// session) auto-attribute back to this lead session via OrchestratorSessionID.
const envOrchestratorSessionID = "LOOM_ORCHESTRATOR_SESSION_ID"
const envAgentName = "LOOM_AGENT_NAME"
const envAgentTerminalID = "LOOM_AGENT_TERMINAL_ID"

const leadHeartbeatInterval = 30 * time.Second
const leadStoreOpTimeout = 10 * time.Second

// leadMessage is an optional initial user request appended to the lead system
// prompt so the agent starts with a concrete task to address. Populated by the
// --message flag.
var leadMessage string
var leadPromptFile string
var leadResume string
var leadContinue bool
var leadListSessions bool
var leadListOutput = leadListOutputText
var materializeLeadSkillsAtStart = materializeLeadSkills

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

Use --list-sessions to see this agent's previous lead sessions (with the codex
thread name where codex recorded one) and exit without starting a new one.

Use --continue to reopen this agent's most recent lead conversation, or
--resume <id> to reopen a specific one (a loom session id, or the harness
session id / codex thread id recorded for it). A bare --resume is the same as
--continue. Resume is supported on the claude and codex backends only, and it
refuses rather than silently starting a fresh conversation.

This command does not require a worktree - it can run from the main
repository or any worktree.

Use --message to seed the session with an initial user request. The message
is appended to the lead system prompt, so the agent performs its normal
lead-mode startup and then addresses the request using lead-mode conventions.`,
	Args: leadArgs,
	Run:  runLead,
}

func init() {
	cli.RegisterCommand(leadCmd)
	leadCmd.Flags().StringVar(&leadMessage, "message", "", "Initial user request to address in lead mode")
	leadCmd.Flags().StringVar(&leadPromptFile, "prompt", "", "Path to terminal-agent prompt template")
	leadCmd.Flags().StringVar(&leadResume, "resume", "",
		"Resume a previous lead session by loom session id or provider session id (bare --resume resumes the latest)")
	// A bare --resume takes the sentinel, so it means exactly what --continue
	// means instead of erroring on a missing value.
	leadCmd.Flags().Lookup("resume").NoOptDefVal = leadcontrol.ResumeLatestSentinel
	leadCmd.Flags().BoolVar(&leadContinue, "continue", false,
		"Resume this agent's most recent lead session")
	leadCmd.Flags().BoolVar(&leadListSessions, "list-sessions", false,
		"List this agent's previous lead sessions and exit without starting one")
	leadCmd.Flags().StringVarP(&leadListOutput, "output", "o", leadListOutputText,
		"Output format for --list-sessions: text|json")
}

// leadStartupPrompt picks the lead runtime's boot prompt. A role prompt_file
// supplied via --prompt wins, otherwise inline role prompt and default lead
// prompt resolution happen in that order.
func leadStartupPrompt(ctx context.Context, registration leadSessionRegistration) (string, error) {
	prompt, err := generateLeadTerminalPrompt(ctx, registration)
	if err != nil {
		return "", err
	}
	return applyLeadPromptContext(prompt), nil
}

//nolint:funlen // The lead startup sequence stays in launch order.
func runLead(cmd *cobra.Command, args []string) {
	enforceLeadProfile()
	// Non-fatal, and it belongs here: the profile's config root is only
	// settled once enforceLeadProfile has injected or verified it.
	warnClaudeTranscriptCleanup(os.Stderr)

	// Get current working directory
	workDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting working directory: %v\n", err)
		os.Exit(1)
	}

	// --list-sessions is a query, not a launch: it answers and returns before
	// anything is registered, generated or materialized. It sits ahead of the
	// resume resolution below so its usage errors are raised from the flags
	// alone, without touching the store.
	if leadListSessions {
		if err := runLeadListSessions(context.Background(), os.Stdout, workDir); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	backendName := cli.GetBackendName()
	// Resume is resolved BEFORE the orchestration session is registered: a
	// resumed lead seeds its brand-new row with the ancestry and the provider
	// handle, so the row is resumable itself even if this process dies before
	// the runtime watcher persists anything. Every failure here exits non-zero
	// -- never execShell, never a quiet fresh session.
	resumeTarget, err := resolveLeadResume(context.Background(), workDir, backendName, args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Check backend health before invoking. If the binary isn't installed,
	// show a helpful error and drop into a shell so the user can fix it.
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
	registration := registerLeadOrchestratorSession(context.Background(), workDir, resumeTarget)
	defer registration.Finalize()
	if err := materializeLeadSkillsAtStart(context.Background(), registration, workDir); err != nil {
		fmt.Fprintf(os.Stderr, "Error materializing lead skills: %v\n", err)
		fmt.Fprintf(os.Stderr, "\nDropping into a shell. Resolve the skill materialization error and run 'loom lead' to retry.\n\n")
		execShell(workDir)
		return
	}
	ensureLeadHookConfig(workDir, backendName)

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
	handled, invokeErr := backends.RunControlledLeadRuntime(context.Background(), leadRuntimeOptions(
		registration, workDir, prompt, backendName, resumeTarget))
	if !handled {
		invokeErr = cli.InvokeAgent(workDir, prompt, "")
	}
	if invokeErr != nil {
		fmt.Fprintf(os.Stderr, "Error running agent: %v\n", invokeErr)
		fmt.Fprintf(os.Stderr, "\nDropping into a shell. Fix the issue and run 'loom lead' to retry.\n\n")
		execShell(workDir)
	}
}

func ensureLeadHookConfig(workDir, backend string) {
	if err := hookcfg.EnsureSkillMaterializeHook(workDir, backend); err != nil {
		slog.Warn("lead hook configuration failed; continuing without raw-PTY pre-turn hook",
			"target", workDir, "backend", backend, "err", err)
	}
}

func generateLeadTerminalPrompt(ctx context.Context, registration leadSessionRegistration) (string, error) {
	if strings.TrimSpace(leadPromptFile) != "" {
		return agent.GenerateTerminalPrompt(leadPromptFile)
	}
	if prompt := loadLeadRolePrompt(ctx, registration); strings.TrimSpace(prompt) != "" {
		return agent.GenerateTerminalPromptText(prompt)
	}
	return agent.GenerateTerminalPrompt("")
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
	if errors.Is(err, domain.ErrNotFound) {
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
	if assignment := currentLeadAssignmentPrompt(context.Background()); assignment != "" {
		prompt += "\n\n## Loom Backend Assignment\n\n" + assignment
	}
	if leadMessage != "" {
		prompt += "\n\n## User's Initial Request\n\n" + leadMessage +
			"\n\nAddress this request using the lead mode conventions above."
	}
	return prompt
}

func currentLeadAssignmentPrompt(ctx context.Context) string {
	handle, ws, ok := openLeadSessionStore(ctx)
	if !ok {
		return ""
	}
	defer func() { _ = handle.Close() }()

	loadCtx, cancel := context.WithTimeout(ctx, leadStoreOpTimeout)
	defer cancel()
	assignment, err := epicrunner.LoadLeadAssignmentContext(loadCtx, handle.Store, ws, resolveLeadAgentID())
	if err != nil || assignment == nil {
		return ""
	}
	if err := markLeadAssignmentDelivered(loadCtx, handle.Store, ws, assignment); err != nil {
		slog.Debug("lead assignment delivery marker failed", "err", err)
	}
	return epicrunner.FormatLeadAssignmentContext(assignment)
}

func markLeadAssignmentDelivered(ctx context.Context, st store.Store, ws string, assignment *epicrunner.LeadAssignmentContext) error {
	if st == nil || st.AgentSessions() == nil || assignment == nil {
		return nil
	}
	sessionID := strings.TrimSpace(assignment.OrchestratorSessionID)
	version := strings.TrimSpace(assignment.AssignmentVersion)
	if sessionID == "" || version == "" {
		return nil
	}

	session, err := st.AgentSessions().Get(ctx, ws, sessionID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	metadata := make(map[string]string, len(session.Metadata)+2)
	for k, v := range session.Metadata {
		metadata[k] = v
	}
	metadata["lead_assignment_delivered_version"] = version
	metadata["lead_assignment_delivered_epic"] = strings.TrimSpace(assignment.EpicID)
	_, err = st.AgentSessions().Update(ctx, ws, sessionID, store.AgentSessionUpdate{Metadata: &metadata})
	return err
}

type leadSessionRegistration struct {
	handle    *bootstrap.StoreHandle
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

func (r leadSessionRegistration) Store() store.Store {
	if r.handle == nil {
		return nil
	}
	return r.handle.Store
}

type leadSkillMaterializer func(context.Context, store.Store, string, string, string) error

func materializeLeadSkills(ctx context.Context, registration leadSessionRegistration, workDir string) error {
	return materializeLeadSkillsWith(ctx, registration, workDir, skillmat.MaterializeLeased)
}

func materializeLeadSkillsWith(ctx context.Context, registration leadSessionRegistration, workDir string, materialize leadSkillMaterializer) error {
	st := registration.Store()
	workspace := strings.TrimSpace(registration.Workspace)
	if st == nil || workspace == "" {
		return nil
	}
	opCtx, cancel := context.WithTimeout(ctx, leadStoreOpTimeout)
	defer cancel()

	roleName := leadcontrol.SessionRoleName(opCtx, st, workspace, registration.AgentID)

	if err := materialize(opCtx, st, workspace, roleName, workDir); err != nil {
		if skillmat.IsStoreUnavailable(err) {
			slog.Warn("skill store unavailable; continuing with existing lead materialization",
				"workspace", workspace, "role", roleName, "target", workDir, "err", err)
			return nil
		}
		return fmt.Errorf("materialize lead skills: %w", err)
	}
	return nil
}

// registerLeadOrchestratorSession opens fleet-db, creates an
// AgentSession{Kind:orchestration}, and starts a heartbeat goroutine. Returns a
// registration whose Finalize method marks the session completed and stops the
// heartbeat. Best-effort: any error returns a no-op registration so lead always
// runs.
func registerLeadOrchestratorSession(ctx context.Context, workDir string, resume *leadcontrol.ResumeTarget) leadSessionRegistration {
	noop := func() {}
	empty := leadSessionRegistration{finalize: noop}
	handle, ws, ok := openLeadSessionStore(ctx)
	if !ok {
		return empty
	}

	sid := resolveLeadOrchestratorSessionID()
	agentID := resolveLeadAgentID()
	if err := createLeadSession(ctx, handle, ws, sid, agentID, workDir, resume); err != nil {
		_ = handle.Close()
		slog.Warn("lead orchestrator session: create failed, continuing without registration", "err", err)
		return empty
	}

	activateLeadSessionEnv(sid)
	fmt.Printf("Lead session: %s (orchestrator linkage active)\n\n", sid)
	stopHB, wg := startLeadSessionHeartbeat(handle, ws, sid)
	return leadSessionRegistration{
		handle:    handle,
		Workspace: ws,
		SessionID: sid,
		AgentID:   agentID,
		finalize:  leadSessionFinalizer(handle, ws, sid, stopHB, wg),
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

func createLeadSession(ctx context.Context, handle *bootstrap.StoreHandle, ws, sid, agentID, workDir string, resume *leadcontrol.ResumeTarget) error {
	createCtx, createCancel := context.WithTimeout(ctx, leadStoreOpTimeout)
	defer createCancel()
	metadata := map[string]string{
		"actor":                         leadSessionActor(),
		leadcontrol.MetadataLeadWorkDir: workDir,
	}
	roleName := strings.TrimSpace(os.Getenv("LOOM_AGENT_ROLE"))
	if roleName == "" && strings.TrimSpace(agentID) == "lead" {
		roleName = "lead"
	}
	if roleName != "" {
		metadata[leadcontrol.MetadataLeadRole] = roleName
	}
	seedResumeMetadata(metadata, resume)
	_, err := handle.Store.AgentSessions().Create(createCtx, store.AgentSessionCreate{
		WorkspaceKey: ws,
		SessionID:    sid,
		AgentID:      agentID,
		Kind:         domain.AgentSessionKindOrchestration,
		TerminalID:   strings.TrimSpace(os.Getenv(envAgentTerminalID)),
		Status:       domain.AgentSessionRunning,
		Metadata:     metadata,
	})
	if errors.Is(err, domain.ErrAlreadyExists) {
		return nil
	}
	return err
}

func leadSessionActor() string {
	if actor := os.Getenv("USER"); actor != "" {
		return actor
	}
	return "unknown"
}

func activateLeadSessionEnv(sid string) {
	// Child agents spawned from this session inherit the orchestrator ID.
	if err := os.Setenv(envOrchestratorSessionID, sid); err != nil {
		slog.Warn("lead orchestrator session: setenv failed", "err", err)
	}
}

func startLeadSessionHeartbeat(handle *bootstrap.StoreHandle, ws, sid string) (chan struct{}, *sync.WaitGroup) {
	stopHB := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go heartbeatLeadSession(handle, ws, sid, stopHB, &wg)
	return stopHB, &wg
}

func leadSessionFinalizer(handle *bootstrap.StoreHandle, ws, sid string, stopHB chan struct{}, wg *sync.WaitGroup) func() {
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

func resolveLeadOrchestratorSessionID() string {
	if sid := strings.TrimSpace(os.Getenv(envOrchestratorSessionID)); sid != "" {
		return sid
	}
	return "lead-" + uuid.New().String()
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
