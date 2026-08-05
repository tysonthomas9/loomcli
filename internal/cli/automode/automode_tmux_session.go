package automode

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

// startTmuxSession creates a detached tmux session running one ordinary
// single-task compatibility command. The child selects and claims its own task;
// there is no supervisor-assigned leaf mode.
func startTmuxSession(sessionName string, opts AutoModeOptions, logFile string) error {
	// Kill any existing session with this name (error expected if session doesn't exist)
	_ = exec.Command("tmux", "kill-session", "-t", sessionName).Run() //nolint:gosec // args are constant tmux subcommands + validated session name

	// Build the loom command to run inside tmux
	// TERM=dumb disables alternate screen buffer for Claude, enabling output streaming via capture-pane.
	// Other backends (codex, opencode) need a real TERM value to detect TTY properly.
	termPrefix := "TERM=dumb "
	if resolved := cli.GetBackendName(); resolved != "claude" {
		termPrefix = ""
	}
	loomCmd := fmt.Sprintf("%sloom %s %s", termPrefix, shellQuote(opts.AgentType), shellQuote(opts.WorktreePath))

	// Always propagate backend to subprocess so the tmux-spawned process
	// (which runs the installed binary) uses the same backend as the parent.
	loomCmd += fmt.Sprintf(" --backend %s", shellQuote(cli.GetBackendName()))

	// Propagate parent ID filter to subprocess
	if opts.ParentID != "" {
		loomCmd += fmt.Sprintf(" --parent %s", shellQuote(opts.ParentID))
	}

	// Create detached session with current terminal dimensions
	args := []string{"new-session", "-d", "-s", sessionName}

	// Get terminal size and pass to tmux so output uses full width
	if width, height, err := getTerminalSize(); err == nil && width > 0 && height > 0 {
		args = append(args, "-x", fmt.Sprintf("%d", width), "-y", fmt.Sprintf("%d", height))
	}

	args = append(args, loomCmd)
	if err := exec.Command("tmux", args...).Run(); err != nil { //nolint:gosec // args built from controlled tmux flags + shell-quoted values
		return fmt.Errorf("tmux new-session failed: %w", err)
	}

	// Disable tmux focus-events to prevent ^[[I and ^[[O in output
	_ = exec.Command("tmux", "set", "-t", sessionName, "focus-events", "off").Run() //nolint:gosec // constant tmux subcommands + validated session name

	// Setup logging via loom log-router for intelligent log routing
	// log-router writes to agent log always, and task log when a task is claimed
	// logFile is ~/.loom/logs/{workspaceID}/agents/{agentName}.log, so
	// filepath.Dir twice gives ~/.loom/logs/{workspaceID} which is already
	// workspace-scoped. We pass this directly as --base-dir.
	logDir := filepath.Dir(filepath.Dir(logFile))
	lockPath := filepath.Join(cli.ResolveLockDir(opts.WorktreePath), cli.LockFileName)
	routerCmd := fmt.Sprintf("loom log-router --agent %s --base-dir %s --lock-path %s --max-log-size 50",
		shellQuote(opts.AgentName),
		shellQuote(logDir),
		shellQuote(lockPath))
	if err := exec.Command("tmux", "pipe-pane", "-t", sessionName, "-o", routerCmd).Run(); err != nil { //nolint:gosec // args are shell-quoted
		fmt.Printf("[auto] Warning: logging setup failed: %v\n", err)
	}

	return nil
}

// streamUntilExit streams tmux output until the session exits naturally
// Reads from the pipe-pane log file instead of capture-pane to avoid
// visual artifacts from cursor positioning and UI redraws
// streamCtx holds state for the streamUntilExit loop.
type streamCtx struct {
	sessionName string
	logFile     string
	signalFile  string
	lastOffset  int64
	poller      *adaptivePoller
	ticker      *time.Ticker
}

func (s *streamCtx) getTickChan() <-chan time.Time {
	if useFixedPolling {
		return s.ticker.C
	}
	return s.poller.tick()
}

func (s *streamCtx) markActivity() {
	if s.poller != nil {
		s.poller.hadActivity()
	}
}

func (s *streamCtx) markNoActivity() {
	if s.poller != nil {
		s.poller.hadNoActivity()
	}
}

func streamUntilExit(sessionName, logFile, worktreePath string, attachChan, shutdown chan struct{}) {
	worktreePath = canonicalizePath(worktreePath)

	sc := &streamCtx{
		sessionName: sessionName,
		logFile:     logFile,
		signalFile:  cli.GetSignalFilePath(worktreePath),
	}
	if info, err := os.Stat(logFile); err == nil {
		sc.lastOffset = info.Size()
	}

	if useFixedPolling {
		sc.ticker = time.NewTicker(200 * time.Millisecond)
		defer sc.ticker.Stop()
	} else {
		sc.poller = newAdaptivePoller()
	}

	for {
		select {
		case <-shutdown:
			return
		case <-attachChan:
			handleTmuxAttach(sc, shutdown)
		case <-sc.getTickChan():
			if handleStreamTick(sc, shutdown) {
				return
			}
		}
	}
}

// handleTmuxAttach attaches to the tmux session and handles detach.
func handleTmuxAttach(sc *streamCtx, _ <-chan struct{}) {
	fmt.Print("\n─── ATTACHED (Ctrl+B D to detach) ───\n\n")

	cmd := exec.Command("tmux", "attach", "-t", sc.sessionName) //nolint:gosec // constant tmux subcommands + validated session name
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if !tmuxSessionExists(sc.sessionName) || tmuxPaneDead(sc.sessionName) {
			cleanupTmuxSession(sc.sessionName)
			return
		}
		fmt.Printf("[auto] Attach error: %v\n", err)
	}

	fmt.Print("\n─── DETACHED, resuming stream ───\n\n")
	sc.markActivity()
}

// handleStreamTick processes one tick of the stream loop. Returns true if the stream should end.
func handleStreamTick(sc *streamCtx, shutdown chan struct{}) bool {
	if handleCompletionSignal(sc, shutdown) {
		return true
	}

	if !tmuxSessionExists(sc.sessionName) {
		streamRemainingLogContent(sc.logFile, &sc.lastOffset)
		return true
	}

	if handleDeadPane(sc) {
		return true
	}

	streamLogOutput(sc)
	return false
}

// handleCompletionSignal checks for and handles the explicit completion signal file.
func handleCompletionSignal(sc *streamCtx, shutdown chan struct{}) bool {
	if _, err := os.Stat(sc.signalFile); err != nil {
		return false
	}
	if valErr := cli.ValidateSignalDir(filepath.Dir(sc.signalFile)); valErr != nil {
		fmt.Fprintf(os.Stderr, "[auto] Warning: signal directory validation failed: %v\n", valErr)
		return false
	}
	fmt.Println("[auto] Task completion signal received, waiting for output to settle...")
	os.Remove(sc.signalFile)

	waitForOutputSilence(sc, shutdown)
	cleanupTmuxSession(sc.sessionName)
	return true
}

// waitForOutputSilence waits for output to settle after a completion signal.
func waitForOutputSilence(sc *streamCtx, shutdown chan struct{}) {
	const silenceTimeout = 10 * time.Second
	const maxWait = 30 * time.Second
	lastActivity := time.Now()
	deadline := time.Now().Add(maxWait)

	for time.Now().Before(deadline) {
		select {
		case <-shutdown:
			streamRemainingLogContent(sc.logFile, &sc.lastOffset)
			cleanupTmuxSession(sc.sessionName)
			return
		default:
		}
		prevOffset := sc.lastOffset
		streamRemainingLogContent(sc.logFile, &sc.lastOffset)
		if sc.lastOffset > prevOffset {
			lastActivity = time.Now()
		} else if time.Since(lastActivity) >= silenceTimeout {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// handleDeadPane checks if the tmux pane is dead and cleans up.
func handleDeadPane(sc *streamCtx) bool {
	state, err := getPaneState(sc.sessionName)
	if err != nil || !state.Dead {
		return false
	}
	if state.ExitStatus != 0 {
		fmt.Printf("[auto] Session exited with status %d", state.ExitStatus)
		if state.ExitSignal != "" {
			fmt.Printf(" (signal: %s)", state.ExitSignal)
		}
		fmt.Println()
	}
	streamRemainingLogContent(sc.logFile, &sc.lastOffset)
	cleanupTmuxSession(sc.sessionName)
	return true
}

// streamLogOutput reads new content from the log file and writes it to stdout.
func streamLogOutput(sc *streamCtx) {
	file, err := os.Open(sc.logFile) //nolint:gosec // logFile from resolveLogFile, rooted under ~/.loom/logs/
	if err != nil {
		sc.markNoActivity()
		return
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		sc.markNoActivity()
		return
	}

	switch {
	case stat.Size() > sc.lastOffset:
		readAndWriteLogChunk(file, sc, sc.lastOffset, stat.Size()-sc.lastOffset)
	case stat.Size() < sc.lastOffset:
		sc.lastOffset = 0
		readAndWriteLogChunk(file, sc, 0, stat.Size())
	default:
		sc.markNoActivity()
	}
}

// readAndWriteLogChunk reads a chunk from the log file and writes it to stdout.
func readAndWriteLogChunk(file *os.File, sc *streamCtx, offset, size int64) {
	if _, err := file.Seek(offset, 0); err != nil {
		return
	}
	buf := make([]byte, size)
	n, _ := file.Read(buf)
	if n > 0 {
		filtered := filterFocusEscapes(buf[:n])
		os.Stdout.Write(filtered)
		sc.lastOffset = offset + int64(n)
		sc.markActivity()
	}
}

// streamRemainingLogContent reads and outputs any remaining content from the log file
func streamRemainingLogContent(logFile string, lastOffset *int64) {
	file, err := os.Open(logFile) //nolint:gosec // logFile is from resolveLogFile, rooted under ~/.loom/logs/
	if err != nil {
		fmt.Fprintf(os.Stderr, "[auto] Warning: failed to read final output: %v\n", err)
		return
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[auto] Warning: failed to stat log file: %v\n", err)
		return
	}

	if stat.Size() > *lastOffset {
		if _, err := file.Seek(*lastOffset, 0); err != nil {
			fmt.Fprintf(os.Stderr, "[auto] Warning: failed to seek log file: %v\n", err)
			return
		}
		newContent := make([]byte, stat.Size()-*lastOffset)
		n, err := file.Read(newContent)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[auto] Warning: failed to read final output: %v\n", err)
		}
		if n > 0 {
			filtered := filterFocusEscapes(newContent[:n])
			os.Stdout.Write(filtered)
			*lastOffset += int64(n)
		}
	} else if stat.Size() < *lastOffset {
		// Log was truncated - read from beginning
		*lastOffset = 0
		if _, err := file.Seek(0, 0); err != nil {
			fmt.Fprintf(os.Stderr, "[auto] Warning: failed to seek log file: %v\n", err)
			return
		}
		newContent := make([]byte, stat.Size())
		n, err := file.Read(newContent)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[auto] Warning: failed to read final output: %v\n", err)
		}
		if n > 0 {
			filtered := filterFocusEscapes(newContent[:n])
			os.Stdout.Write(filtered)
			*lastOffset = int64(n)
		}
	}
}
