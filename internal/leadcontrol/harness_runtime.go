package leadcontrol

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/olesho/harness-wrapper/pkg/chat"
	"github.com/olesho/harness-wrapper/pkg/chat/memstore"
	"golang.org/x/term"

	"github.com/tysonthomas9/loomcli/internal/store"
)

// Harness adapter names understood by harness-wrapper's pkg/chat.
const (
	HarnessNameClaudeCode = "claude-code"
	HarnessNameCodex      = "codex"
	HarnessNameGemini     = "gemini"
	HarnessNameGeneric    = "generic"
)

const (
	harnessDefaultCols         = 120
	harnessDefaultRows         = 40
	harnessStatusPollInterval  = 2 * time.Second
	harnessSessionIDPollFloor  = 5 * time.Second
	harnessWedgedTurnLogWindow = 5 * time.Minute
)

// HarnessLeadRuntimeConfig configures a controlled lead session supervised by
// harness-wrapper. The human keeps the harness TUI (PTY passthrough); the
// runtime drains the lead's inbox and injects queued messages as turns.
type HarnessLeadRuntimeConfig struct {
	Store     store.Store
	Workspace string
	LeadName  string
	SessionID string
	WorkDir   string
	Prompt    string

	// Backend is the loom backend name ("claude", "gemini", ...) recorded as
	// the runtime provider in session metadata.
	Backend string
	// HarnessName selects the harness-wrapper adapter. Defaults from Backend
	// via HarnessNameForBackend.
	HarnessName string
	// BinaryPath and Args launch the harness; Prompt is appended as the final
	// positional argument.
	BinaryPath string
	Args       []string
	Env        []string

	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Logger *slog.Logger
}

// HarnessNameForBackend maps a loom backend name to the harness-wrapper
// adapter to supervise it with.
func HarnessNameForBackend(backend string) string {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case "claude":
		return HarnessNameClaudeCode
	case "codex":
		return HarnessNameCodex
	case "gemini":
		return HarnessNameGemini
	default:
		return HarnessNameGeneric
	}
}

// RunHarnessLeadRuntime launches the harness TUI under harness-wrapper's PTY
// supervision, mirrors it to the human terminal, persists runtime metadata on
// the lead session, and drains the lead inbox into the visible conversation.
// Blocks until the harness exits.
func RunHarnessLeadRuntime(ctx context.Context, cfg HarnessLeadRuntimeConfig) error {
	cfg = normalizeHarnessLeadRuntimeConfig(cfg)

	cols, rows := harnessTerminalSize(cfg.Stdout)
	conv, err := openHarnessConversation(ctx, chat.Options{
		Harness:    cfg.HarnessName,
		BinaryPath: cfg.BinaryPath,
		Args:       harnessLeadArgs(cfg),
		WorkingDir: cfg.WorkDir,
		Env:        cfg.Env,
		Cols:       cols,
		Rows:       rows,
		// memstore is sufficient: the chat store holds only turn metadata for
		// this process's lifetime. The durable transcript is the harness's
		// own session log, and the resume UUID is persisted onto the loom
		// session metadata below.
		Store: memstore.New(),
	})
	if err != nil {
		return fmt.Errorf("start controlled %s lead session: %w", cfg.Backend, err)
	}

	runtime := HarnessRuntimeMetadata{
		Provider:      cfg.Backend,
		HarnessName:   cfg.HarnessName,
		ChatSessionID: conv.ChatSessionID(),
		PID:           conv.PID(),
		Status:        RuntimeStatusStarting,
		Controlled:    true,
	}
	if err := UpdateHarnessRuntimeMetadata(ctx, cfg.Store, cfg.Workspace, cfg.SessionID, runtime); err != nil {
		cfg.Logger.Warn("failed to persist harness runtime metadata", "err", err)
	}

	handle, unregister := registerLeadConversation(cfg.SessionID, conv)
	defer unregister()

	_, _ = fmt.Fprintf(cfg.Stdout, "Launching controlled %s lead session...\n\n", cfg.Backend)
	detach := conv.AttachOutput(cfg.Stdout)
	defer detach()
	restoreTerminal := forwardHarnessStdin(ctx, cfg, conv)
	defer restoreTerminal()

	watchCtx, cancelWatch := context.WithCancel(ctx)
	defer cancelWatch()
	go watchHarnessLeadRuntime(watchCtx, cfg, conv, handle, runtime)
	drainCtx, cancelDrain := context.WithCancel(ctx)
	defer cancelDrain()
	go drainLeadMessageQueue(drainCtx, cfg.Store, cfg.Workspace, cfg.LeadName, cfg.Logger)

	result, waitErr := conv.Wait()

	cancelWatch()
	cancelDrain()
	unregister()
	restoreTerminal()
	closeCtx, cancelClose := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelClose()
	if err := conv.Close(closeCtx); err != nil {
		cfg.Logger.Debug("harness conversation close failed", "err", err)
	}
	runtime.Status = RuntimeStatusDisconnected
	_ = UpdateHarnessRuntimeMetadata(context.Background(), cfg.Store, cfg.Workspace, cfg.SessionID, runtime)
	if waitErr != nil {
		return waitErr
	}
	if result.ExitCode != 0 && result.ExitCode != -1 {
		return fmt.Errorf("%s exited with status %d", cfg.BinaryPath, result.ExitCode)
	}
	return nil
}

func normalizeHarnessLeadRuntimeConfig(cfg HarnessLeadRuntimeConfig) HarnessLeadRuntimeConfig {
	cfg.Workspace = strings.TrimSpace(cfg.Workspace)
	cfg.LeadName = strings.TrimSpace(cfg.LeadName)
	cfg.SessionID = strings.TrimSpace(cfg.SessionID)
	cfg.WorkDir = strings.TrimSpace(cfg.WorkDir)
	cfg.Backend = strings.ToLower(strings.TrimSpace(cfg.Backend))
	if cfg.Backend == "" {
		cfg.Backend = "claude"
	}
	if cfg.HarnessName == "" {
		cfg.HarnessName = HarnessNameForBackend(cfg.Backend)
	}
	if cfg.BinaryPath == "" {
		cfg.BinaryPath = cfg.Backend
	}
	if cfg.Stdin == nil {
		cfg.Stdin = os.Stdin
	}
	if cfg.Stdout == nil {
		cfg.Stdout = os.Stdout
	}
	if cfg.Stderr == nil {
		cfg.Stderr = os.Stderr
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return cfg
}

func harnessLeadArgs(cfg HarnessLeadRuntimeConfig) []string {
	args := append([]string{}, cfg.Args...)
	if cfg.Prompt != "" {
		args = append(args, cfg.Prompt)
	}
	return args
}

// harnessTerminalSize sizes the virtual PTY to the human terminal once at
// startup so the wrapper's screen emulator (which drives turn detection) and
// the mirrored output agree. SIGWINCH is deliberately not forwarded: resizing
// only the PTY would desync the emulator.
func harnessTerminalSize(stdout io.Writer) (int, int) {
	if f, ok := stdout.(*os.File); ok {
		if cols, rows, err := term.GetSize(int(f.Fd())); err == nil && cols > 0 && rows > 0 {
			return cols, rows
		}
	}
	return harnessDefaultCols, harnessDefaultRows
}

// forwardHarnessStdin puts the human terminal into raw mode (when stdin is a
// TTY) and copies keystrokes into the harness PTY. The wrapper serializes
// stdin writers, so human typing coexists with programmatic message delivery.
// Returns a restore function that is safe to call multiple times.
func forwardHarnessStdin(ctx context.Context, cfg HarnessLeadRuntimeConfig, conv harnessConversation) func() {
	restore := func() {}
	if f, ok := cfg.Stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		oldState, err := term.MakeRaw(int(f.Fd()))
		if err == nil {
			fd := int(f.Fd())
			restored := false
			restore = func() {
				if !restored {
					restored = true
					_ = term.Restore(fd, oldState)
				}
			}
		} else {
			cfg.Logger.Debug("failed to enter raw mode; continuing cooked", "err", err)
		}
	}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := cfg.Stdin.Read(buf)
			if n > 0 {
				if _, werr := conv.WriteStdin(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			default:
			}
		}
	}()
	return restore
}

// watchHarnessLeadRuntime consumes chat turn events and polls the wrapper
// snapshot, persisting RuntimeStatus transitions onto the lead session and
// backfilling the harness session ID once the adapter extracts it.
func watchHarnessLeadRuntime(
	ctx context.Context,
	cfg HarnessLeadRuntimeConfig,
	conv harnessConversation,
	handle *leadConversationHandle,
	runtime HarnessRuntimeMetadata,
) {
	events := conv.Events()
	ticker := time.NewTicker(harnessStatusPollInterval)
	defer ticker.Stop()
	lastStatus := runtime.Status
	var turnPendingSince time.Time

	persist := func(status string) {
		if status == "" || status == lastStatus {
			return
		}
		lastStatus = status
		runtime.Status = status
		if err := UpdateHarnessRuntimeMetadata(ctx, cfg.Store, cfg.Workspace, cfg.SessionID, runtime); err != nil {
			cfg.Logger.Debug("failed to persist harness runtime status", "status", status, "err", err)
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if handle != nil {
				handle.observeTurnEvent(ev)
			}
			if ev.Turn.Role != chat.RoleAssistant {
				continue
			}
			switch ev.Turn.State {
			case chat.TurnStatePending, chat.TurnStateStreaming:
				turnPendingSince = time.Now()
				persist(RuntimeStatusActive)
			case chat.TurnStateComplete:
				turnPendingSince = time.Time{}
				persist(RuntimeStatusIdle)
			case chat.TurnStateErrored:
				turnPendingSince = time.Time{}
				persist(RuntimeStatusWaitingUserInput)
			}
		case <-ticker.C:
			snap := conv.Snapshot()
			if status := harnessRuntimeStatus(snap); status != RuntimeStatusActive || lastStatus == RuntimeStatusStarting {
				persist(status)
			}
			if runtime.HarnessSessionID == "" {
				if hsid := conv.HarnessSessionID(); hsid != "" {
					runtime.HarnessSessionID = hsid
					if err := UpdateHarnessRuntimeMetadata(ctx, cfg.Store, cfg.Workspace, cfg.SessionID, runtime); err != nil {
						cfg.Logger.Debug("failed to persist harness session id", "err", err)
					}
				}
			}
			if !turnPendingSince.IsZero() && time.Since(turnPendingSince) > harnessWedgedTurnLogWindow {
				if !snap.LastOutputAt.IsZero() && time.Since(snap.LastOutputAt) > harnessWedgedTurnLogWindow {
					cfg.Logger.Warn("harness turn has been pending without output; turn-completion marker may have been missed",
						"lead", cfg.LeadName, "pending_since", turnPendingSince)
					turnPendingSince = time.Time{}
				}
			}
		}
	}
}
