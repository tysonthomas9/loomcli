package automode

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/workspace"
)

// IsTmuxAvailable checks if tmux is installed and available.
// It is a variable so tests can override it.
var IsTmuxAvailable = func() bool {
	return exec.Command("tmux", "-V").Run() == nil
}

// RunAutoModeTmux runs auto mode with tmux session management and live streaming
// tmuxLoopCtx holds state for the tmux auto mode loop.
type tmuxLoopCtx struct {
	opts                  AutoModeOptions
	sessionName           string
	logFile               string
	yieldFile             string
	hasAvailableTasks     func() (bool, error)
	taskCount             int
	consecutiveNoProgress int
	idleStart             time.Time
}

func RunAutoModeTmux(opts AutoModeOptions, shutdown chan struct{}) {
	opts.WorktreePath = canonicalizePath(opts.WorktreePath)
	applyAutoModeDefaults(&opts)
	ctx := initTmuxLoop(opts)
	printTmuxHeader(ctx)

	fmt.Print("\x1b[?1004l")
	defer fmt.Print("\x1b[?1004h")

	attachChan := make(chan struct{}, 1)
	go listenForAttachKey(attachChan, shutdown)

	for {
		if exitReason := tmuxCheckExit(ctx, shutdown); exitReason != "" {
			cleanupTmuxSession(ctx.sessionName)
			printTmuxSummary(ctx.taskCount)
			return
		}

		if !tmuxWaitForTasks(ctx, shutdown) {
			return
		}

		ctx.idleStart = time.Now()
		_ = os.Remove(filepath.Join(opts.WorktreePath, cli.LockFileName))

		if !tmuxRunSession(ctx, attachChan, shutdown) {
			return
		}

		if !tmuxHandlePostSession(ctx, shutdown) {
			return
		}
		time.Sleep(2 * time.Second)
	}
}

func canonicalizePath(path string) string {
	if absPath, err := filepath.Abs(path); err == nil {
		if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
			return resolved
		}
		return absPath
	}
	return path
}

func initTmuxLoop(opts AutoModeOptions) *tmuxLoopCtx {
	wsID := workspace.ResolveWorkspaceID(opts.WorkspaceID)
	wsPrefix := workspace.ShortWorkspaceID(wsID)
	sessionName := fmt.Sprintf("loom-%s-%s-%s-%d", wsPrefix, opts.AgentType, opts.AgentName, os.Getpid())
	logFile := resolveTmuxLogFile(wsID, opts.AgentName)

	return &tmuxLoopCtx{
		opts:              opts,
		sessionName:       sessionName,
		logFile:           logFile,
		yieldFile:         os.Getenv("LOOM_YIELD_FILE"),
		hasAvailableTasks: resolveTaskChecker(opts),
		idleStart:         time.Now(),
	}
}

func resolveTmuxLogFile(wsID, agentName string) string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = os.TempDir()
	}
	workspaceID := wsID
	if workspaceID == "" {
		workspaceID = "_default"
	}
	agentLogDir := filepath.Join(homeDir, ".loom", "logs", workspaceID, "agents")
	if err := os.MkdirAll(agentLogDir, 0700); err != nil {
		fmt.Printf("[auto] Warning: could not create log directory: %v\n", err)
	}
	return filepath.Join(agentLogDir, fmt.Sprintf("%s.log", agentName))
}

func printTmuxHeader(ctx *tmuxLoopCtx) {
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Printf("Running %s agent in AUTO MODE (tmux)\n", strings.ToUpper(ctx.opts.AgentType))
	fmt.Printf("Worktree: %s\n", ctx.opts.WorktreePath)
	fmt.Printf("Session: %s\n", ctx.sessionName)
	fmt.Println("")
	fmt.Println("Press ENTER to attach (Ctrl+B D to detach)")
	fmt.Println("Press Ctrl+C to stop")
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println("")
}

// tmuxCheckExit returns a non-empty reason string if the loop should exit.
func tmuxCheckExit(ctx *tmuxLoopCtx, shutdown chan struct{}) string {
	select {
	case <-shutdown:
		return "shutdown"
	default:
	}
	if reason, yielded := checkYieldFile(ctx.yieldFile); yielded {
		fmt.Printf("[auto] Yield requested (reason: %s), exiting gracefully...\n", reason)
		return reason
	}
	if ctx.opts.MaxTasks > 0 && ctx.taskCount >= ctx.opts.MaxTasks {
		fmt.Printf("[auto] Reached max tasks (%d)\n", ctx.opts.MaxTasks)
		return "max tasks"
	}
	return ""
}

// tmuxWaitForTasks waits until tasks are available. Returns false if loop should exit.
func tmuxWaitForTasks(ctx *tmuxLoopCtx, shutdown chan struct{}) bool {
	available, err := ctx.hasAvailableTasks()
	if err != nil {
		fmt.Printf("[auto] Error checking tasks: %v\n", err)
		if interruptibleSleep(5*time.Second, shutdown) {
			cleanupTmuxSession(ctx.sessionName)
			printTmuxSummary(ctx.taskCount)
			return false
		}
		return true
	}
	if available {
		return true
	}

	if ctx.opts.IdleTimeout > 0 && time.Since(ctx.idleStart) >= time.Duration(ctx.opts.IdleTimeout)*time.Minute {
		fmt.Printf("[auto] Idle timeout exceeded (%d minutes)\n", ctx.opts.IdleTimeout)
		cleanupTmuxSession(ctx.sessionName)
		printTmuxSummary(ctx.taskCount)
		return false
	}
	fmt.Printf("[auto] No tasks available, waiting %ds...\n", ctx.opts.Interval)
	if interruptibleSleep(time.Duration(ctx.opts.Interval)*time.Second, shutdown) {
		cleanupTmuxSession(ctx.sessionName)
		printTmuxSummary(ctx.taskCount)
		return false
	}
	return true
}

// tmuxRunSession starts and streams a single tmux session. Returns false if loop should exit.
func tmuxRunSession(ctx *tmuxLoopCtx, attachChan chan struct{}, shutdown chan struct{}) bool {
	fmt.Printf("═══════════════════════════════════════════════════════════════\n")
	fmt.Printf("[Session] Starting...\n")
	fmt.Printf("═══════════════════════════════════════════════════════════════\n")

	if err := startTmuxSession(ctx.sessionName, ctx.opts, ctx.logFile); err != nil {
		fmt.Printf("[auto] Failed to start session: %v\n", err)
		if interruptibleSleep(5*time.Second, shutdown) {
			cleanupTmuxSession(ctx.sessionName)
			printTmuxSummary(ctx.taskCount)
			return false
		}
		return true
	}

	streamUntilExit(ctx.sessionName, ctx.logFile, ctx.opts.WorktreePath, attachChan, shutdown)

	select {
	case <-shutdown:
		cleanupTmuxSession(ctx.sessionName)
		printTmuxSummary(ctx.taskCount)
		return false
	default:
	}
	return true
}

// tmuxHandlePostSession processes results after session exit. Returns false if loop should exit.
func tmuxHandlePostSession(ctx *tmuxLoopCtx, shutdown chan struct{}) bool {
	if reason, yielded := checkYieldFile(ctx.yieldFile); yielded {
		fmt.Printf("[auto] Yield requested after task (reason: %s), exiting gracefully...\n", reason)
		cleanupTmuxSession(ctx.sessionName)
		printTmuxSummary(ctx.taskCount)
		return false
	}

	if agentClaimedTask(ctx.opts.WorktreePath, ctx.opts.AgentName, ctx.opts.LockBridge) {
		ctx.taskCount++
		ctx.consecutiveNoProgress = 0
		fmt.Printf("[Session #%d] Completed, cycling...\n", ctx.taskCount)
		return true
	}

	ctx.consecutiveNoProgress++
	fmt.Printf("[auto] Agent exited without claiming a task (%d consecutive)\n", ctx.consecutiveNoProgress)
	if ctx.consecutiveNoProgress >= 3 {
		fmt.Printf("[auto] No tasks claimed in %d consecutive sessions, exiting\n", ctx.consecutiveNoProgress)
		cleanupTmuxSession(ctx.sessionName)
		printTmuxSummary(ctx.taskCount)
		return false
	}
	backoff := time.Duration(30<<(ctx.consecutiveNoProgress-1)) * time.Second
	if backoff > 120*time.Second {
		backoff = 120 * time.Second
	}
	fmt.Printf("[auto] Backing off for %s before retry...\n", backoff)
	if interruptibleSleep(backoff, shutdown) {
		cleanupTmuxSession(ctx.sessionName)
		printTmuxSummary(ctx.taskCount)
		return false
	}
	return true
}

// printTmuxSummary prints the auto mode completion summary
func printTmuxSummary(taskCount int) {
	fmt.Println("")
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Printf("AUTO MODE COMPLETE - %d task(s) processed\n", taskCount)
	fmt.Println("═══════════════════════════════════════════════════════════════")
}
