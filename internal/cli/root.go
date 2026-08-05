package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/observability/tracing"
)

// Version information (set at build time via ldflags)
var (
	Version = "dev"
	Build   = "unknown"
)

// serverFlag stores the --server flag value (remote loom server base URL).
// When non-empty, the CLI routes issue operations through the api backend
// instead of the local fleet-db backend. Mirrored into LOOM_SERVER_URL in
// PersistentPreRun so env-based resolution sees the value consistently.
var serverFlag string

// workspaceFlag stores the --workspace flag value (target workspace ID for
// --server mode).
var workspaceFlag string

var rootCmd = &cobra.Command{
	Use:   "loom",
	Short: "Agent management CLI for parallel Claude Code workflows",
	Long: `loom - Agent Management CLI

Manage Claude Code agents working in parallel across workspace repos.

GETTING STARTED
  1. Select a FleetDB-backed workspace with registered repos.

  2. Create tasks for agents to work on:
     loom task create --title="Add login feature" --type=feature --priority=2

  3. Run agents:
     loom plan falcon    # Creates design, sets status=review
     loom lead           # Review and approve plans
     loom task falcon    # Implements approved design

KEY CONCEPTS
  Workspaces   FleetDB-backed repo groups where agents work independently.

  Agents       Claude processes that work on tasks:
               - 'plan' agent: researches and creates designs
               - 'task' agent: implements approved designs

  FleetDB      Issue tracker. Tasks flow through states:
               open → in_progress → review → open → closed

  Auto Mode    --auto flag runs agents continuously, processing multiple
               tasks until stopped (Ctrl+C) or idle timeout.

COMMANDS
  plan         Run a planning agent (creates designs, marks for review)
  task         Run an implementation agent (implements approved tasks)
  lead         Interactive mode for reviewing plans and managing backlog
  monitor      Dashboard showing agent status and task progress
  recover      Recover agent from error state (clear stale locks, reset tasks)
  push         Push worktree branches to target with AI conflict resolution
  pull         Pull integration branch into worktrees with AI conflict resolution
  sync         Full sync: push all completed work, then pull into all worktrees
  reset        Hard reset worktrees to a specific branch
  list         List all agents and their status

GLOBAL FLAGS
      --backend          AI backend CLI (codex, claude, opencode). Env: LOOM_BACKEND

ENVIRONMENT VARIABLES
  LOOM_DEFAULT_BRANCH    Default integration branch (default: main)
  LOOM_BACKEND           AI backend CLI to use (default: codex)

EXAMPLES
  loom plan falcon              # Run planning agent in falcon worktree
  loom task falcon --auto       # Continuous implementation mode
  loom lead                     # Interactive backlog management
  loom monitor                  # Watch agent progress
  loom push --all               # Push all worktrees to main
  loom pull --all               # Pull main into all worktrees
  loom sync                     # Full sync: push all + pull all`,
	Run: func(cmd *cobra.Command, args []string) {
		if v, _ := cmd.Flags().GetBool("version"); v {
			fmt.Printf("loom version %s (%s)\n", Version, Build)
			return
		}
		_ = cmd.Help()
	},
}

func init() {
	rootCmd.Flags().BoolP("version", "v", false, "Print version information")
	rootCmd.PersistentFlags().StringVar(&backendFlag, "backend", "", "AI backend CLI to use (codex, claude, opencode). Env: LOOM_BACKEND")
	rootCmd.PersistentFlags().StringVar(&logFormat, "log-format", "text", "Log format (text|json)")
	rootCmd.PersistentFlags().StringVar(&logOutput, "log-output", "stderr", "Log output destination (stderr|<filepath>)")
	rootCmd.PersistentFlags().StringVar(&serverFlag, "server", "", "Remote loom server base URL. When set, CLI uses HTTP API backend instead of local FleetDB. Env: LOOM_SERVER_URL")
	rootCmd.PersistentFlags().StringVar(&workspaceFlag, "workspace", "", "Workspace ID. Env: LOOM_WORKSPACE")

	// Resolve and set active backend before any subcommand runs,
	// then inject the Deps container into the command context.
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if err := prepareCommandEnvironment(); err != nil {
			return err
		}
		if err := runPreBackendCommandGuards(cmd); err != nil {
			return err
		}
		if err := ResolveAndSetBackend(); err != nil {
			return err
		}
		// Rebuild the package-level defaultDeps now that --workspace /
		// --server have been mirrored into the env. The eager
		// `var defaultDeps = DefaultDeps()` in deps.go runs at process
		// load time, before Cobra has parsed any flags, so its cached
		// IssueBackend would otherwise be locked to whatever env was
		// inherited from the shell. resolveDirectIssueBackend() reads
		// defaultDeps.IssueBackend, so refresh it here before any
		// subcommand runs.
		deps := DefaultDeps()
		defaultDeps = deps
		cmd.SetContext(WithDeps(cmd.Context(), deps))
		return nil
	}

	// Add command groups for organized help
	rootCmd.AddGroup(&cobra.Group{ID: "agents", Title: "Agent Commands:"})
	rootCmd.AddGroup(&cobra.Group{ID: "git", Title: "Git Operations:"})
	rootCmd.AddGroup(&cobra.Group{ID: "config", Title: "Configuration:"})
	rootCmd.AddGroup(&cobra.Group{ID: "workspace", Title: "Workspace Commands:"})
}

// PrepareStandaloneHTTPCommand is a PersistentPreRunE hook for commands that
// use an explicitly configured Loom HTTP endpoint and do not consume the
// process-wide IssueBackend or Deps container. It preserves logging and global
// flag-to-environment behavior while deliberately skipping eager issue-backend
// construction (and its separate /api/config auth discovery).
func PrepareStandaloneHTTPCommand(_ *cobra.Command, _ []string) error {
	return prepareCommandEnvironment()
}

func prepareCommandEnvironment() error {
	if err := InitLogger(logFormat, logOutput); err != nil {
		return err
	}
	// Mirror --server / --workspace flags into env vars so that HTTP clients
	// and backend factory helpers see the same value whether the caller used
	// the flag or the env var directly.
	if serverFlag != "" {
		if err := os.Setenv("LOOM_SERVER_URL", serverFlag); err != nil {
			return err
		}
	}
	if workspaceFlag != "" {
		if err := os.Setenv("LOOM_WORKSPACE", workspaceFlag); err != nil {
			return err
		}
	}
	return nil
}

// Execute runs the root command
func Execute() error {
	registerPendingCommands()

	traceShutdown := initCLITracing()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = traceShutdown(ctx)
	}()

	// Wrap the entire CLI invocation in a root span so HTTP calls to fleet-db
	// hang off it as children rather than fragmenting into per-call traces.
	// Cobra subcommands ignore cmd.Context() in many helpers (cmdstore.WithStore
	// builds its own SignalContext), so we publish the trace-bearing context
	// as a process-wide root via cmdstore.SetRootContext for any helper that
	// derives its context from there.
	tracer := tracing.Tracer("github.com/tysonthomas9/loomcli/internal/cli")
	// BootstrapContext consumes LOOM_TRACE_PARENT (set by a parent loom
	// process — daemon spawning agent, or loom spawning embedded fleet-db)
	// so this process's root span inherits the spawner's trace ID. Returns
	// context.Background unchanged when no env var is set.
	bootstrapCtx := tracing.BootstrapContext(context.Background())
	ctx, span := tracer.Start(bootstrapCtx, "loom.cli")
	defer span.End()
	rootCmd.SetContext(ctx)
	cmdstore.SetRootContext(ctx)
	// Publish the active span + shutdown so any handler that calls os.Exit
	// can route through ExitWithFlush() and still flush traces.
	RegisterActiveTraceState(span, traceShutdown)
	// Bus.Emit uses this provider to capture the active trace context into
	// every emitted event (Event.TraceParent). Without it, loom.task /
	// loom.agent.lifecycle spans would land in a separate trace.
	events.SetContextProvider(cmdstore.RootContext)
	if len(os.Args) > 1 {
		span.SetName("loom.cli." + os.Args[1])
	}

	// Trace-flush on signal: SIGINT/SIGTERM (sent by `timeout`, Ctrl-C,
	// supervisor kill) bypass deferred span.End. End the root span here
	// so it exports under sync mode. Do NOT shut down the TracerProvider
	// in the signal handler — that would prevent in-flight spans (e.g.,
	// a backend.invoke span deeper in the call stack) from flushing as
	// they unwind. Sync mode means each ended span exports immediately,
	// so leaving the provider alive until the process actually exits is
	// the right call.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		s, ok := <-sigCh
		if !ok {
			return
		}
		span.End()
		// Reset to default handler so the second signal terminates immediately,
		// and re-raise so unwinding code paths run before exit.
		signal.Reset(s.(syscall.Signal))
		_ = syscall.Kill(syscall.Getpid(), s.(syscall.Signal))
	}()
	defer signal.Stop(sigCh)

	return rootCmd.Execute()
}

// initCLITracing installs the OTel tracer for the current CLI invocation
// and returns the shutdown closure. Off unless OTEL_EXPORTER_OTLP_ENDPOINT
// is set or LOOM_TRACE=1. CLI runs are short-lived; we init + shutdown per
// invocation. See docs/observability/tracing-contract.md §8.
func initCLITracing() tracing.Shutdown {
	traceEnabled := os.Getenv("LOOM_TRACE") == "1" || os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != ""
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if traceEnabled && endpoint == "" {
		endpoint = "localhost:4318"
	}
	serviceName := resolveCLIServiceName()
	// Sync mode for short-lived CLI/agent runs: every span.End() blocks until
	// export completes, so spans land in Jaeger even when the process exits
	// via os.Exit. Long-running serve/daemon use the batcher.
	syncExport := serviceName == "loom-cli" || serviceName == "loom-agent"
	shutdown, _, err := tracing.Init(context.Background(), tracing.Config{
		ServiceName:    serviceName,
		ServiceVersion: Build,
		Environment:    os.Getenv("LOOM_ENV"),
		Endpoint:       endpoint,
		AlwaysOn:       true,
		Sync:           syncExport,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: tracing init failed: %v\n", err)
	}
	// On the error path tracing.Init returns a nil shutdown; the caller
	// in Execute then defers `_ = traceShutdown(ctx)` which would panic
	// (nil function call) for any misconfigured OTLP setting. Always
	// return a callable closure so the contract is "you can defer this
	// unconditionally". The same closure is fed to RegisterActiveTraceState
	// so ExitWithFlush stays nil-safe.
	if shutdown == nil {
		shutdown = func(context.Context) error { return nil }
	}
	return shutdown
}

// resolveCLIServiceName maps the top-level subcommand to a service name so
// dashboards can filter long-running daemons separately from agent runs and
// short-lived CLI invocations. Trace contract §1.
func resolveCLIServiceName() string {
	if len(os.Args) <= 1 {
		return "loom-cli"
	}
	switch os.Args[1] {
	case "serve":
		return "loom-serve"
	case "daemon":
		return "loom-daemon"
	case "plan", "task", "agent", "lead":
		return "loom-agent"
	}
	return "loom-cli"
}

// --- Command registration (merged from register.go) ---

// pendingCmds accumulates commands registered by sub-packages via init().
var pendingCmds []*cobra.Command

// PreBackendCommandGuard validates a parsed command after global flags have
// been mirrored into the environment but before any issue backend or default
// dependency container is resolved.
type PreBackendCommandGuard func(*cobra.Command) error

type registeredPreBackendCommandGuard struct {
	id    uint64
	guard PreBackendCommandGuard
}

var (
	preBackendCommandGuardMu     sync.RWMutex
	nextPreBackendCommandGuardID uint64
	preBackendCommandGuards      []registeredPreBackendCommandGuard
)

// RegisterPreBackendCommandGuard installs a composition-root policy check and
// returns an idempotent restore function. Production callers may discard the
// restore function; package tests should defer it to avoid global leakage.
func RegisterPreBackendCommandGuard(guard PreBackendCommandGuard) func() {
	if guard == nil {
		return func() {}
	}

	preBackendCommandGuardMu.Lock()
	nextPreBackendCommandGuardID++
	registration := registeredPreBackendCommandGuard{id: nextPreBackendCommandGuardID, guard: guard}
	preBackendCommandGuards = append(preBackendCommandGuards, registration)
	preBackendCommandGuardMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			unregisterPreBackendCommandGuard(registration.id)
		})
	}
}

func unregisterPreBackendCommandGuard(id uint64) {
	preBackendCommandGuardMu.Lock()
	defer preBackendCommandGuardMu.Unlock()

	for index, registration := range preBackendCommandGuards {
		if registration.id != id {
			continue
		}
		copy(preBackendCommandGuards[index:], preBackendCommandGuards[index+1:])
		preBackendCommandGuards[len(preBackendCommandGuards)-1] = registeredPreBackendCommandGuard{}
		preBackendCommandGuards = preBackendCommandGuards[:len(preBackendCommandGuards)-1]
		return
	}
}

func runPreBackendCommandGuards(cmd *cobra.Command) error {
	preBackendCommandGuardMu.RLock()
	guards := make([]PreBackendCommandGuard, 0, len(preBackendCommandGuards))
	for _, registration := range preBackendCommandGuards {
		guards = append(guards, registration.guard)
	}
	preBackendCommandGuardMu.RUnlock()

	for _, guard := range guards {
		if err := guard(cmd); err != nil {
			return err
		}
	}
	return nil
}

// RegisterCommand adds a command to the pending list.
// Sub-packages call this in their init() functions.
func RegisterCommand(cmd *cobra.Command) {
	pendingCmds = append(pendingCmds, cmd)
}

// registerPendingCommands adds all pending commands to rootCmd.
func registerPendingCommands() {
	rootCmd.AddCommand(pendingCmds...)
}

// GetRootCmd returns the root cobra command for sub-packages that need
// to add command groups or other root-level configuration.
func GetRootCmd() *cobra.Command {
	return rootCmd
}

// worktreeCompletion provides completion for worktree and workspace names
func WorktreeCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	// Only complete the first argument (worktree/workspace name)
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	seen := make(map[string]bool)
	var completions []string

	resolver, err := NewResolver()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	for _, name := range resolver.WorkspaceNames() {
		completions = append(completions, name+"\tworkspace")
		seen[name] = true
	}

	worktrees, err := resolver.DiscoverWorktrees()
	if err != nil {
		if len(completions) > 0 {
			return completions, cobra.ShellCompDirectiveNoFileComp
		}
		return nil, cobra.ShellCompDirectiveError
	}

	for _, wt := range worktrees {
		if seen[wt.Name] {
			continue // Skip if already added as workspace name
		}
		// Format: "name\tdescription" for shell completion
		completions = append(completions, wt.Name+"\t"+wt.Branch)
	}
	return completions, cobra.ShellCompDirectiveNoFileComp
}

// branchCompletion provides completion for git branch names
func BranchCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	branches, err := GetGitBranches()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	return branches, cobra.ShellCompDirectiveNoFileComp
}

// worktreeThenBranchCompletion provides worktree names for first arg, branches for second
func WorktreeThenBranchCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return WorktreeCompletion(cmd, args, toComplete)
	}
	if len(args) == 1 {
		return BranchCompletion(cmd, args, toComplete)
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

// getGitBranchesDeps is the deps-aware implementation of GetGitBranches.
func getGitBranchesDeps(deps *Deps) ([]string, error) {
	output, err := RunGit(deps, ".", "branch", "-a", "--format=%(refname:short)")
	if err != nil {
		return nil, err
	}

	return parseGitBranches(output), nil
}

// GetGitBranches returns all local and remote branch names
func GetGitBranches() ([]string, error) {
	return getGitBranchesDeps(ensureDefaultDeps())
}

// parseGitBranches parses the output of git branch -a into unique branch names.
func parseGitBranches(output string) []string {

	var branches []string
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		// Clean up remote branch names (origin/branch -> branch for display)
		branch := strings.TrimPrefix(line, "origin/")
		// Skip HEAD references
		if strings.Contains(branch, "HEAD") {
			continue
		}
		branches = append(branches, branch)
	}

	// Remove duplicates (local and remote versions of same branch)
	seen := make(map[string]bool)
	var unique []string
	for _, b := range branches {
		if !seen[b] {
			seen[b] = true
			unique = append(unique, b)
		}
	}

	return unique
}
