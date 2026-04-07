package automode

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
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

	// Interface abstractions for remote agent support (nil = local filesystem).
	LockBridge   cli.LockBridge
	EventEmitter EventEmitter
	Deps         *cli.Deps // Optional deps for testability (nil = cli.GetDeps(nil)).
}

// AutoModeState tracks the current state of auto mode execution
type AutoModeState struct {
	TasksCompleted        int
	ConsecutiveErrors     int
	ConsecutiveNoProgress int // sessions that completed without claiming a task
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

	printAutoModeSummary(ctx.state)
}

func initAutoLoop(opts AutoModeOptions) *autoLoopCtx {
	d := opts.Deps
	if d == nil {
		d = cli.GetDeps(nil)
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

	ctx := &autoLoopCtx{
		opts:      opts,
		deps:      d,
		yieldFile: os.Getenv("LOOM_YIELD_FILE"),
		state: &AutoModeState{
			LastTaskTime:  time.Now(),
			IdleStartTime: time.Now(),
		},
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
	fmt.Printf("\n[auto] === Starting task %d ===\n\n", ctx.state.TasksCompleted+1)

	beforeRef := CaptureHEADRef(ctx.opts.WorktreePath)
	workspace, _ := config.ResolveActiveWorkspace()
	prompt := ctx.generatePrompt(ctx.opts.AgentName, workspace)
	sess := createAutoSession(ctx, prompt)

	backendName := cli.GetBackendName()
	collector := usage.NewCollector(backendName, ctx.opts.AgentName)
	startedAt := time.Now()

	err := ctx.deps.Agent.InvokeNonInteractive(ctx.opts.WorktreePath, prompt, ctx.opts.AgentName, shutdown, collector)

	endedAt := time.Now()
	recordSessionUsage(ctx.usageStore, collector, ctx.opts.WorktreePath, ctx.opts.AgentName, ctx.opts.ParentID, startedAt, endedAt, err, ctx.opts.LockBridge)
	finalizeAutoSession(ctx, sess, beforeRef, err)

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
		return handleAutoTaskError(ctx, err, shutdown)
	}
	return handleAutoTaskSuccess(ctx, beforeRef, startedAt, endedAt, shutdown)
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
	if state.ConsecutiveNoProgress > 0 {
		fmt.Printf("No-progress sessions at exit: %d consecutive\n", state.ConsecutiveNoProgress)
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
