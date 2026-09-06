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

// leadPrintPrompt makes `loom lead` print the resolved STATIC prompt and exit
// without starting a session. It is how the lead profile's CLAUDE.md is
// generated, so it must never emit the per-session sections that
// applyLeadPromptContext appends.
var leadPrintPrompt bool

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
lead-mode startup and then addresses the request using lead-mode conventions.

Use --print-prompt to print the resolved static lead prompt and exit without
starting a session. It prints only the static half - no backend assignment and
no --message request - which is exactly what belongs in an agent profile's
CLAUDE.md. Generate one with:

  loom lead --print-prompt > "$WORKSPACE/profiles/lead/claude/CLAUDE.md"

A session whose profile carries that CLAUDE.md should then be launched with
--prompt builtin:lead-profile, a minimal pointer prompt that leaves the role
instructions to the profile instead of repeating them every session.

--prompt builtin:none goes one step further and suppresses the argv persona
entirely: the prompt is empty and no positional prompt argument is passed to
the backend at all, so the role instructions must already reach the model as
ambient context. Suppression is absolute - it ignores a ./loom-prompts/none.md
override and drops the LOOM_READ_ONLY preamble (a warning is logged; hard
read-only enforcement stays on the backend flags). With --print-prompt it
prints nothing and exits 0.`,
	Args: cobra.NoArgs,
	Run:  runLead,
}

func init() {
	cli.RegisterCommand(leadCmd)
	leadCmd.Flags().StringVar(&leadMessage, "message", "", "Initial user request to address in lead mode")
	leadCmd.Flags().StringVar(&leadPromptFile, "prompt", "", "Path to terminal-agent prompt template")
	leadCmd.Flags().BoolVar(&leadPrintPrompt, "print-prompt", false, "Print the resolved static lead prompt and exit (no session, no dynamic sections)")
}

// leadStartupPrompt picks the lead runtime's boot prompt. A role prompt_file
// supplied via --prompt wins, otherwise inline role prompt and default lead
// prompt resolution happen in that order.
//
// The second return value is the seed-and-shrink predicate: true only when the
// workdir is dedicated to lead AND the built-in lead prompt is the one in play.
// It gates BOTH halves of this feature, so they can never disagree - see
// generateLeadTerminalPrompt.
func leadStartupPrompt(ctx context.Context, registration leadSessionRegistration, dedicated bool) (string, bool, error) {
	prompt, seedAndShrink, err := generateLeadTerminalPrompt(ctx, registration, dedicated)
	if err != nil {
		return "", false, err
	}
	return applyLeadPromptContext(prompt), seedAndShrink, nil
}

func runLead(cmd *cobra.Command, args []string) {
	// Print-and-exit runs before the preflight and before session
	// registration: generating a profile file must not touch the backend, write
	// an orchestrator session row, or mark an epic assignment delivered.
	if leadPrintPrompt {
		printLeadPrompt()
		return
	}

	workDir, backendName, dedicated, ok := leadRuntimePreflight()
	if !ok {
		return
	}

	// A suppressed persona moves the safety block off argv and onto a static
	// file. Refuse - do not degrade - when that file does not carry the block
	// this run would have rendered. This deliberately sits AFTER the
	// --print-prompt early return above: --print-prompt is how the file is
	// generated in the first place, so gating it would make the ambient file
	// impossible to create.
	if reason, suppressed := leadRunPersonaSuppression(context.Background()); suppressed {
		if err := CheckAmbientSafetyBlock(backendName, workDir, dedicated, reason); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}

	// Best-effort: register this lead as an orchestrator session so workers
	// the AI spawns via `loom agentdef add` are attributed back to it. Skips
	// silently if there is no active workspace or fleet-db is unreachable.
	registration := registerLeadOrchestratorSession(context.Background(), workDir)
	defer registration.Finalize()

	// Generate the terminal-agent prompt and append the user's initial request if provided.
	prompt, seedAndShrink, err := leadStartupPrompt(context.Background(), registration, dedicated)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading terminal prompt: %v\n", err)
		fmt.Fprintf(os.Stderr, "\nDropping into a shell. Fix the prompt file and run 'loom lead' to retry.\n\n")
		execShell(workDir)
		return
	}

	// The persona left argv, so it has to be on disk before the harness starts.
	if seedAndShrink {
		seedLeadWorkdirFiles(workDir)
	}

	// Invoke agent interactively (no agent name needed - lead mode doesn't claim tasks).
	// Backends with a controlled runtime (codex app-server, harness-wrapper PTY
	// supervision for claude and others) get queued message delivery; anything
	// else falls back to a plain interactive launch.
	handled, invokeErr := backends.RunControlledLeadRuntime(
		context.Background(),
		registration.Store(),
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

// leadRuntimePreflight resolves lead's own working directory (<ws>/lead, or
// LOOM_LEAD_WORKDIR, falling back to the current directory outside a
// workspace), resolves the backend and prints the mode banner. An uninstalled
// backend is not fatal: the operator is dropped into a shell in the lead
// workdir (and ok is false) so they can fix it in place.
//
// dedicated reports whether workDir is lead's own directory. It is the first
// half of the seed-and-shrink predicate - see generateLeadTerminalPrompt.
func leadRuntimePreflight() (workDir, backendName string, dedicated, ok bool) {
	workDir, dedicated, err := resolveLeadWorkdir(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting working directory: %v\n", err)
		os.Exit(1)
	}

	backendName = cli.GetBackendName()
	if hs, healthy := backends.CheckBackendHealth(backendName); healthy && !hs.Installed {
		fmt.Fprintf(os.Stderr, "Error: %s backend is not installed (%s)\n\n", backendName, hs.Message)
		fmt.Fprintf(os.Stderr, "Install it and try again. Dropping into a shell so you can fix this.\n\n")
		execShell(workDir)
		return "", "", false, false
	}

	fmt.Println("=========================================")
	fmt.Println("Starting LEAD mode (Interactive)")
	fmt.Println("=========================================")
	fmt.Println()
	return workDir, backendName, dedicated, true
}

// printLeadPrompt writes the static lead prompt to stdout. The zero
// registration is deliberate: loadLeadRolePrompt then opens its own short-lived
// read-only store handle, or returns "" when there is no workspace, so this
// works outside a workspace and with fleet-db down.
//
// dedicated is false on purpose: this prints the FULL static prompt, which is
// exactly what belongs in the profile's CLAUDE.md. Shrinking it to the safety
// block here would write a persona-less file.
func printLeadPrompt() {
	prompt, _, err := generateLeadTerminalPrompt(context.Background(), leadSessionRegistration{}, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading terminal prompt: %v\n", err)
		os.Exit(1)
	}
	// A suppressed persona (--prompt builtin:none) must print 0 bytes, not the
	// bare newline fmt.Println would add: the output is redirected straight
	// into a profile's CLAUDE.md.
	if prompt == "" {
		return
	}
	fmt.Println(prompt)
}

// generateLeadTerminalPrompt resolves the argv prompt and reports whether this
// launch seeds ambient instruction files and shrinks argv to the safety block.
//
// Both an explicit --prompt file and an inline role prompt keep today's
// behavior verbatim and clear the predicate: they are the operator asking for a
// specific persona on argv, and neither belongs in a seeded AGENTS.md. That is
// also the path `--prompt builtin:lead-profile` takes, which is how a claude
// session under its own CLAUDE_CONFIG_DIR gets its persona: from the profile's
// CLAUDE.md, not from a file in the workdir.
//
// The built-in lead prompt shrinks to the safety guardrails ONLY in a dedicated
// workdir. Shrinking in the os.Getwd fallback would boot a lead with no persona
// at all, or - worse, since seeding never overwrites - let it silently adopt an
// unrelated AGENTS.md that happened to be sitting in that directory.
func generateLeadTerminalPrompt(ctx context.Context, registration leadSessionRegistration, dedicated bool) (string, bool, error) {
	if strings.TrimSpace(leadPromptFile) != "" {
		prompt, err := agent.GenerateTerminalPrompt(leadPromptFile)
		return prompt, false, err
	}
	if prompt := loadLeadRolePrompt(ctx, registration); strings.TrimSpace(prompt) != "" {
		prompt, err := agent.GenerateTerminalPromptText(prompt)
		return prompt, false, err
	}
	if dedicated {
		return agent.LeadSafetyPrompt(), true, nil
	}
	prompt, err := agent.GenerateTerminalPrompt("")
	return prompt, false, err
}

// leadRoleName is the role this lead runs as: LOOM_AGENT_ROLE, or "lead".
// Shared with the safety-drift probe so both look up the same row.
func leadRoleName() string {
	if roleName := strings.TrimSpace(os.Getenv("LOOM_AGENT_ROLE")); roleName != "" {
		return roleName
	}
	return "lead"
}

func loadLeadRolePrompt(ctx context.Context, registration leadSessionRegistration) string {
	roleName := leadRoleName()

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
	return composeLeadPrompt(prompt, currentLeadAssignmentPrompt(context.Background()), leadMessage)
}

// composeLeadPrompt joins the base prompt with the per-session sections. It is
// the pure half of applyLeadPromptContext, so the exact bytes it produces are
// pinned by the argv_golden testdata.
func composeLeadPrompt(base, assignment, message string) string {
	sections := make([]string, 0, 3)
	if base != "" {
		sections = append(sections, base)
	}
	if assignment != "" {
		sections = append(sections, "## Loom Backend Assignment\n\n"+assignment)
	}
	if message != "" {
		sections = append(sections, "## User's Initial Request\n\n"+message+
			"\n\nAddress this request using the lead mode conventions above.")
	}
	return strings.Join(sections, "\n\n")
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

// registerLeadOrchestratorSession opens fleet-db, creates an
// AgentSession{Kind:orchestration}, and starts a heartbeat goroutine. Returns a
// registration whose Finalize method marks the session completed and stops the
// heartbeat. Best-effort: any error returns a no-op registration so lead always
// runs.
func registerLeadOrchestratorSession(ctx context.Context, workDir string) leadSessionRegistration {
	noop := func() {}
	empty := leadSessionRegistration{finalize: noop}
	handle, ws, ok := openLeadSessionStore(ctx)
	if !ok {
		return empty
	}

	sid := resolveLeadOrchestratorSessionID()
	agentID := resolveLeadAgentID()
	if err := createLeadSession(ctx, handle, ws, sid, agentID, workDir); err != nil {
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

func createLeadSession(ctx context.Context, handle *bootstrap.StoreHandle, ws, sid, agentID, workDir string) error {
	createCtx, createCancel := context.WithTimeout(ctx, leadStoreOpTimeout)
	defer createCancel()
	_, err := handle.Store.AgentSessions().Create(createCtx, store.AgentSessionCreate{
		WorkspaceKey: ws,
		SessionID:    sid,
		AgentID:      agentID,
		Kind:         domain.AgentSessionKindOrchestration,
		TerminalID:   strings.TrimSpace(os.Getenv(envAgentTerminalID)),
		Status:       domain.AgentSessionRunning,
		Metadata: map[string]string{
			"actor":        leadSessionActor(),
			"lead_workdir": workDir,
		},
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
