package automode

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// AutoModeOptions holds configuration for auto mode
type AutoModeOptions struct {
	Interval        int    // Polling interval in seconds when no tasks available
	MaxTasks        int    // Maximum tasks to process before exiting (0 = unlimited)
	IdleTimeout     int    // Exit after N minutes with no available tasks (0 = no timeout)
	AgentType       string // "plan" or "task"
	AgentName       string
	WorktreePath    string
	WorkspaceID     string                                       // Stable workspace UUID (falls back to LOOM_WORKSPACE_ID env)
	ParentID        string                                       // Epic ID to scope task discovery to (empty = all tasks)
	CustomPromptGen func(string, *config.WorkspaceConfig) string // Custom prompt generator (overrides AgentType selection)
	CustomTaskCheck func() (bool, error)                         // Custom task availability check (overrides AgentType selection)
	BackoffBase     time.Duration                                // Base backoff duration for no-progress retries (default 30s)
	TaskPause       time.Duration                                // Pause after task completion before checking for next (default 2s)
	EventBus        events.Emitter                               // Event emission for observability (nil = no events)

	// Rate-limit circuit breaker configuration. If any is 0, defaults are applied
	// (10m window, 5 threshold, 5m cooldown). Set RateLimitThreshold to a
	// negative value to disable the breaker entirely.
	RateLimitWindow    time.Duration // Sliding window for rate-limit tracking (default 10m)
	RateLimitThreshold int           // Rate limits within window to trip breaker (default 5)
	RateLimitCooldown  time.Duration // Pause duration when breaker trips (default 5m)

	// Interface abstractions for remote agent support (nil = local filesystem).
	LockBridge   cli.LockBridge
	EventEmitter EventEmitter
	Deps         *cli.Deps // Optional deps for testability (nil = cli.GetDeps(nil)).
}

// AutoModeState tracks the current state of auto mode execution
type AutoModeState struct {
	TasksCompleted        int
	ConsecutiveErrors     int
	ConsecutiveRateLimits int
	ConsecutiveNoProgress int // sessions that completed without claiming a task
	CircuitBreakerTrips   int // lifetime count of rate-limit circuit breaker trips
	LastTaskTime          time.Time
	IdleStartTime         time.Time
	ShouldExit            bool
	ExitReason            string
}

// SetupSignalHandler sets up graceful shutdown on SIGINT/SIGTERM
// Returns a channel that will be CLOSED when shutdown is requested
// (closed channel pattern allows multiple goroutines to detect shutdown)
func SetupSignalHandler() chan struct{} {
	shutdown := make(chan struct{})
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	go func() {
		sig := <-sigChan
		signal.Stop(sigChan) // Stop delivering signals to this channel
		log.Printf("[auto] Shutdown signal received: %v (PID=%d, PGID=%d)", sig, os.Getpid(), syscall.Getpgrp())
		fmt.Printf("\n[auto] Shutdown signal received (%v), stopping gracefully...\n", sig)
		close(shutdown) // Closing unblocks ALL receivers
	}()

	return shutdown
}

// interruptibleSleep waits for the specified duration or until shutdown is signaled
// Returns true if interrupted by shutdown, false if duration elapsed
func interruptibleSleep(d time.Duration, shutdown <-chan struct{}) bool {
	select {
	case <-time.After(d):
		return false
	case <-shutdown:
		return true
	}
}

// checkYieldFile returns (reason, true) if a yield file exists at the given path, or ("", false) otherwise.
func checkYieldFile(yieldFile string) (string, bool) {
	if yieldFile == "" {
		return "", false
	}
	if _, err := os.Stat(yieldFile); err != nil {
		return "", false
	}
	data, err := os.ReadFile(yieldFile)
	if err != nil {
		return "unknown", true
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if json.Unmarshal(data, &req) != nil || req.Reason == "" {
		return "unknown", true
	}
	return req.Reason, true
}

// agentClaimedTask checks the lock file to determine if the agent claimed a task
// during its session. Returns true if TaskID is non-empty.
// When bridge is non-nil, reads via the bridge; otherwise uses the local filesystem.
func agentClaimedTask(worktreePath, agentName string, bridge cli.LockBridge) bool {
	var info *cli.LockInfo
	var err error
	if bridge != nil {
		info, err = bridge.ReadLock(agentName)
	} else {
		info, err = cli.ReadLockFile(worktreePath)
	}
	if err != nil {
		// Can't read lock file — daemon never ran or failed before writing lock.
		// No task was claimed (no progress).
		return false
	}
	return info.TaskID != ""
}

// autoLoopCtx holds runtime state for the auto mode loop.
type autoLoopCtx struct {
	opts              AutoModeOptions
	state             *AutoModeState
	deps              *cli.Deps
	yieldFile         string
	hasAvailableTasks func() (bool, error)
	generatePrompt    func(string, *config.WorkspaceConfig) string
	usageStore        *usage.Store
	sessStore         *sessions.Store
	updateState       func(string) error
	clearTaskID       func() error
	readLock          func() (*cli.LockInfo, error)

	// lastClaudeSessionID holds the Claude session UUID from the previous
	// invocation. Used to resume the session on error-retry instead of
	// cold-starting with a fresh prompt.
	lastClaudeSessionID string
	// resumeFailures counts consecutive resume failures on the same session ID.
	// After 2 failures, we clear the session ID and fall back to cold-start.
	resumeFailures int
	// resumeAttempted is set for a single invocation when we actually asked the
	// backend to resume a previous Claude session.
	resumeAttempted bool

	// lastFailedTaskID is the task ID from the previous failed invocation.
	// Used to detect when the same task is failing repeatedly.
	lastFailedTaskID string
	// sameTaskFailures counts consecutive failures on lastFailedTaskID.
	// Reset to 1 when a different task ID fails, reset to 0 on success.
	// When this reaches maxSameTaskFailures, the task is added to stuckTaskIDs.
	sameTaskFailures int
	// stuckTaskIDs is the set of task IDs that have been declared stuck and
	// should be skipped if the agent re-claims them. Persists for the lifetime
	// of the auto-mode loop. Bounded at maxStuckTasks; when full, the
	// oldest-inserted entry (tracked via stuckTaskOrder) is evicted.
	stuckTaskIDs   map[string]bool
	stuckTaskOrder []string

	// rateLimitBreaker is a sliding-window circuit breaker that trips when
	// rate-limit errors accumulate within a time window. It pauses auto-mode
	// for a cooldown period before allowing a single probe invocation.
	rateLimitBreaker *rateLimitBreaker
}

// RunAutoModeLoop runs the auto mode loop for either plan or task agents.
func RunAutoModeLoop(opts AutoModeOptions, shutdown chan struct{}) {
	ctx := initAutoLoop(opts)
	printAutoModeHeader(opts)

	for {
		if err := ctx.updateState(cli.StateIdle); err != nil {
			fmt.Printf("[auto] Warning: failed to update state: %v\n", err)
		}

		if exitReason := checkAutoExitConditions(ctx, shutdown); exitReason != "" {
			ctx.state.ShouldExit = true
			ctx.state.ExitReason = exitReason
			break
		}

		if !waitForAvailableTasks(ctx, shutdown) {
			break
		}

		ctx.state.IdleStartTime = time.Now()
		if err := ctx.updateState(cli.StateActive); err != nil {
			fmt.Printf("[auto] Warning: failed to update state: %v\n", err)
		}
		if clearErr := ctx.clearTaskID(); clearErr != nil {
			fmt.Printf("[auto] Warning: failed to clear task ID: %v\n", clearErr)
		}

		if !runAutoTask(ctx, shutdown) {
			break
		}
	}

	if ctx.rateLimitBreaker != nil {
		ctx.state.CircuitBreakerTrips = ctx.rateLimitBreaker.totalTrips
	}
	printAutoModeSummary(ctx.state)
}

// applyAutoModeDefaults fills zero-value fields in opts with sensible defaults.
func applyAutoModeDefaults(opts *AutoModeOptions) {
	if opts.Deps == nil {
		opts.Deps = cli.GetDeps(nil)
	}
	if opts.BackoffBase == 0 {
		opts.BackoffBase = 30 * time.Second
	}
	if opts.TaskPause == 0 {
		opts.TaskPause = 2 * time.Second
	}
	if opts.EventBus == nil {
		opts.EventBus = events.NopBus{}
	}
	if opts.RateLimitWindow == 0 {
		opts.RateLimitWindow = 10 * time.Minute
	}
	if opts.RateLimitThreshold == 0 {
		opts.RateLimitThreshold = 5
	}
	if opts.RateLimitCooldown == 0 {
		opts.RateLimitCooldown = 5 * time.Minute
	}
}

func initAutoLoop(opts AutoModeOptions) *autoLoopCtx {
	applyAutoModeDefaults(&opts)
	d := opts.Deps

	ctx := &autoLoopCtx{
		opts:      opts,
		deps:      d,
		yieldFile: os.Getenv("LOOM_YIELD_FILE"),
		state: &AutoModeState{
			LastTaskTime:  time.Now(),
			IdleStartTime: time.Now(),
		},
		stuckTaskIDs:     make(map[string]bool),
		rateLimitBreaker: newRateLimitBreaker(opts.RateLimitWindow, opts.RateLimitCooldown, opts.RateLimitThreshold),
	}

	ctx.hasAvailableTasks = resolveTaskChecker(opts)
	if opts.CustomPromptGen != nil {
		ctx.generatePrompt = opts.CustomPromptGen
	} else {
		log.Fatal("CustomPromptGen must be set on AutoModeOptions")
	}

	usageStore, usageErr := usage.NewStore(cli.GetBeadsDir())
	if usageErr != nil {
		fmt.Printf("[auto] Warning: usage tracking disabled: %v\n", usageErr)
	}
	ctx.usageStore = usageStore

	sessStore, sessErr := sessions.NewStore(cli.GetBeadsDir())
	if sessErr != nil {
		log.Printf("[auto] Warning: session store unavailable: %v", sessErr)
	}
	ctx.sessStore = sessStore

	ctx.updateState = buildStateUpdater(opts)
	ctx.clearTaskID = buildTaskIDClearer(opts)
	ctx.readLock = buildLockReader(opts)

	return ctx
}

func resolveTaskChecker(opts AutoModeOptions) func() (bool, error) {
	if opts.CustomTaskCheck != nil {
		return opts.CustomTaskCheck
	}
	if opts.AgentType == "plan" {
		return func() (bool, error) { return HasAvailablePlanningTasks(opts.ParentID, os.Getenv("LOOM_AGENT_REPO")) }
	}
	return func() (bool, error) {
		return HasAvailableImplementationTasks(opts.ParentID, os.Getenv("LOOM_AGENT_REPO"))
	}
}

func buildStateUpdater(opts AutoModeOptions) func(string) error {
	if opts.LockBridge != nil {
		return func(state string) error { return opts.LockBridge.UpdateState(opts.AgentName, state) }
	}
	return func(state string) error { return cli.UpdateLockState(opts.WorktreePath, state) }
}

func buildTaskIDClearer(opts AutoModeOptions) func() error {
	if opts.LockBridge != nil {
		return func() error { return opts.LockBridge.ClearTaskID(opts.AgentName) }
	}
	return func() error { return cli.ClearLockTaskID(opts.WorktreePath) }
}

func buildLockReader(opts AutoModeOptions) func() (*cli.LockInfo, error) {
	if opts.LockBridge != nil {
		return func() (*cli.LockInfo, error) { return opts.LockBridge.ReadLock(opts.AgentName) }
	}
	return func() (*cli.LockInfo, error) { return cli.ReadLockFile(opts.WorktreePath) }
}

func printAutoModeHeader(opts AutoModeOptions) {
	fmt.Println("=========================================")
	fmt.Printf("Running %s agent in AUTO MODE\n", strings.ToUpper(opts.AgentType))
	fmt.Printf("Worktree: %s\n", opts.WorktreePath)
	fmt.Printf("Agent name: %s\n", opts.AgentName)
	fmt.Printf("Interval: %ds | Max tasks: %s | Idle timeout: %s\n",
		opts.Interval, formatLimit(opts.MaxTasks), formatTimeout(opts.IdleTimeout))
	fmt.Println("Press Ctrl+C to stop gracefully")
	fmt.Println("=========================================")
	fmt.Println("")
}

// checkAutoExitConditions checks shutdown, yield, and max-tasks limits.
// Returns non-empty exit reason if the loop should stop.
func checkAutoExitConditions(ctx *autoLoopCtx, shutdown chan struct{}) string {
	select {
	case <-shutdown:
		return "shutdown signal received"
	default:
	}
	if reason, yielded := checkYieldFile(ctx.yieldFile); yielded {
		fmt.Printf("[auto] Yield requested (reason: %s), exiting gracefully...\n", reason)
		return fmt.Sprintf("yield requested: %s", reason)
	}
	if ctx.opts.MaxTasks > 0 && ctx.state.TasksCompleted >= ctx.opts.MaxTasks {
		return fmt.Sprintf("reached max tasks limit (%d)", ctx.opts.MaxTasks)
	}
	return ""
}

// waitForAvailableTasks waits until tasks are available. Returns false if loop should exit.
func waitForAvailableTasks(ctx *autoLoopCtx, shutdown chan struct{}) bool {
	available, err := ctx.hasAvailableTasks()
	if err != nil {
		fmt.Printf("[auto] Error checking tasks: %v\n", err)
		if interruptibleSleep(time.Duration(ctx.opts.Interval)*time.Second, shutdown) {
			ctx.state.ShouldExit = true
			ctx.state.ExitReason = "shutdown signal received"
			return false
		}
		return true // retry
	}
	if available {
		return true
	}

	if ctx.opts.IdleTimeout > 0 && time.Since(ctx.state.IdleStartTime) >= time.Duration(ctx.opts.IdleTimeout)*time.Minute {
		ctx.state.ShouldExit = true
		ctx.state.ExitReason = fmt.Sprintf("idle timeout exceeded (%d minutes)", ctx.opts.IdleTimeout)
		return false
	}

	fmt.Printf("[auto] No tasks available, waiting %ds...\n", ctx.opts.Interval)
	if interruptibleSleep(time.Duration(ctx.opts.Interval)*time.Second, shutdown) {
		ctx.state.ShouldExit = true
		ctx.state.ExitReason = "shutdown signal received"
		return false
	}
	return true // retry
}

// runAutoTask invokes the agent for one task and handles the result. Returns false if loop should exit.
func runAutoTask(ctx *autoLoopCtx, shutdown chan struct{}) bool {
	if !waitForCircuitBreaker(ctx, shutdown) {
		return false
	}

	fmt.Printf("\n[auto] === Starting task %d ===\n\n", ctx.state.TasksCompleted+1)

	beforeRef := CaptureHEADRef(ctx.opts.WorktreePath)
	// Phase 4 of fleet-db migration: resolve the active workspace via
	// store first, falling back to the legacy yaml-derived config when
	// the store is unreachable. The prompt generator still consumes
	// *config.WorkspaceConfig, so we synthesize one from store data.
	workspace := resolveActiveWorkspaceForAutomode()
	prompt := ctx.generatePrompt(ctx.opts.AgentName, workspace)
	sess := createAutoSession(ctx, prompt)

	ctx.resumeAttempted = false
	if ctx.lastClaudeSessionID != "" {
		ctx.resumeAttempted = true
		backends.SetResumeSessionID(ctx.lastClaudeSessionID)
	}
	defer backends.ClearResumeSessionID()

	backendName := cli.GetBackendName()
	collector := usage.NewCollector(backendName, ctx.opts.AgentName)
	startedAt := time.Now()

	err := ctx.deps.Agent.InvokeNonInteractive(ctx.opts.WorktreePath, prompt, ctx.opts.AgentName, shutdown, collector)
	captureSessionID(ctx)
	endedAt := time.Now()

	recordAndFinalize(ctx, collector, sess, beforeRef, backendName, startedAt, endedAt, err)

	if updateErr := ctx.updateState(cli.StateIdle); updateErr != nil {
		fmt.Printf("[auto] Warning: failed to update state: %v\n", updateErr)
	}
	if reason, yielded := checkYieldFile(ctx.yieldFile); yielded {
		fmt.Printf("[auto] Yield requested after task (reason: %s), exiting gracefully...\n", reason)
		ctx.state.ShouldExit = true
		ctx.state.ExitReason = fmt.Sprintf("yield requested: %s", reason)
		return false
	}

	if err != nil {
		ae := classifyInvokeError(err, backendName)
		return handleAutoTaskError(ctx, ae, err, shutdown)
	}
	return handleAutoTaskSuccess(ctx, beforeRef, startedAt, endedAt, shutdown)
}

// waitForCircuitBreaker blocks while the rate-limit circuit breaker is open,
// returning false if the shutdown signal fires during the cooldown. When the
// breaker transitions Open→HalfOpen, a single probe invocation is allowed to
// proceed. Returns true if the loop should continue.
//
// The loop re-checks ShouldBlock after each sleep: a monotonic clock jump or a
// refactor that adds a second callsite could otherwise leave the breaker Open
// while the caller proceeds, letting the probe run "uncounted".
func waitForCircuitBreaker(ctx *autoLoopCtx, shutdown chan struct{}) bool {
	if ctx.rateLimitBreaker == nil {
		return true
	}
	wasBlocked := false
	for {
		blocked, remaining := ctx.rateLimitBreaker.ShouldBlock()
		if !blocked {
			break
		}
		wasBlocked = true
		fmt.Printf("[auto] Rate-limit circuit breaker OPEN, pausing for %s...\n", remaining.Round(time.Second))
		if interruptibleSleep(remaining, shutdown) {
			ctx.state.ShouldExit = true
			ctx.state.ExitReason = "shutdown signal received"
			return false
		}
	}
	if wasBlocked && ctx.rateLimitBreaker.State() == breakerHalfOpen {
		fmt.Println("[auto] Rate-limit circuit breaker: cooldown elapsed, allowing probe invocation...")
	}
	return true
}

func captureSessionID(ctx *autoLoopCtx) {
	capturedID := backends.GetLastCapturedSessionID()
	if capturedID != "" {
		ctx.lastClaudeSessionID = capturedID
		ctx.resumeFailures = 0
	}
}

func recordAndFinalize(ctx *autoLoopCtx, collector *usage.Collector, sess *sessions.Session, beforeRef, backendName string, startedAt, endedAt time.Time, err error) {
	recordSessionUsage(ctx.usageStore, collector, ctx.opts.WorktreePath, ctx.opts.AgentName, ctx.opts.ParentID, startedAt, endedAt, err, ctx.opts.LockBridge)

	inTok, outTok, cacheRead, cacheWrite := collector.Totals()
	tier := usage.ResolvePricing(backendName)
	costUSD := usage.EstimateCost(tier, usage.SessionUsage{
		InputTokens: inTok, OutputTokens: outTok,
		CacheReadTokens: cacheRead, CacheWriteTokens: cacheWrite,
	})
	finalizeAutoSession(ctx, sess, beforeRef, err, inTok, outTok, cacheRead, cacheWrite, costUSD)
}

func classifyInvokeError(err error, backendName string) *agenterr.AgentError {
	exitCode := 1
	evidence := err.Error()
	var invErr *backends.InvocationError
	if errors.As(err, &invErr) {
		exitCode = invErr.ExitCode
		if strings.TrimSpace(invErr.OutputTail) != "" {
			evidence = invErr.OutputTail
		}
	} else {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}
	return agenterr.ClassifyFromOutput(evidence, exitCode, backendName)
}

func printAutoModeSummary(state *AutoModeState) {
	fmt.Println("")
	fmt.Println("=========================================")
	fmt.Println("AUTO MODE SUMMARY")
	fmt.Println("=========================================")
	fmt.Printf("Exit reason: %s\n", state.ExitReason)
	fmt.Printf("Tasks completed: %d\n", state.TasksCompleted)
	if state.ConsecutiveErrors > 0 {
		fmt.Printf("Errors at exit: %d consecutive\n", state.ConsecutiveErrors)
	}
	if state.ConsecutiveRateLimits > 0 {
		fmt.Printf("Rate limits at exit: %d consecutive\n", state.ConsecutiveRateLimits)
	}
	if state.ConsecutiveNoProgress > 0 {
		fmt.Printf("No-progress sessions at exit: %d consecutive\n", state.ConsecutiveNoProgress)
	}
	if state.CircuitBreakerTrips > 0 {
		fmt.Printf("Rate-limit circuit breaker trips: %d\n", state.CircuitBreakerTrips)
	}
	fmt.Println("=========================================")
}

func formatLimit(limit int) string {
	if limit <= 0 {
		return "unlimited"
	}
	return fmt.Sprintf("%d", limit)
}

func formatTimeout(timeout int) string {
	if timeout <= 0 {
		return "none"
	}
	return fmt.Sprintf("%dm", timeout)
}

// resolveActiveWorkspaceForAutomode returns the active workspace as a
// *config.WorkspaceConfig, preferring the fleet-db store when
// available. Phase 4 of the loom -> fleet-db migration.
//
// Failure to open the store falls back to the legacy yaml path so the
// auto-loop keeps running on dev machines that haven't switched over.
// The synthesized *config.WorkspaceConfig only carries fields used by
// the prompt builders (Repos, Path, ID).
func resolveActiveWorkspaceForAutomode() *config.WorkspaceConfig {
	// Prefer store: avoids parsing yaml + tolerates partial migrations.
	ctx, cancel := cmdstore.SignalContext()
	defer cancel()
	if h, err := cmdstore.OpenStore(ctx); err == nil {
		defer func() { _ = h.Close() }()
		key, keyErr := bootstrap.ResolveActiveWorkspaceKey(ctx, h.Store.Workspaces())
		if keyErr == nil {
			ws, _ := h.Store.Workspaces().Get(ctx, key)
			repos, _ := h.Store.Repos().List(ctx, key)
			if ws != nil {
				out := &config.WorkspaceConfig{
					ID:   ws.Key,
					Path: "", // resolved at call site via state cache; not material for prompts
				}
				for _, r := range repos {
					out.Repos = append(out.Repos, config.RepoConfig{
						Name:          r.Name,
						DefaultBranch: r.DefaultBranch,
						Remote:        r.Remote,
						SourceRepoID:  r.SourceRepoID,
						Groups:        r.Groups,
					})
				}
				return out
			}
		}
	}
	// Legacy fallback.
	ws, _ := config.ResolveActiveWorkspace()
	return ws
}
