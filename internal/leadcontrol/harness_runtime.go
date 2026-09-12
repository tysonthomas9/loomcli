package leadcontrol

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/olesho/harness-wrapper/pkg/chat"
	"github.com/olesho/harness-wrapper/pkg/chat/memstore"
	"github.com/olesho/harness-wrapper/pkg/wrapper"
	"golang.org/x/term"

	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// Harness adapter names understood by harness-wrapper's pkg/chat.
const (
	HarnessNameClaudeCode = "claude-code"
	HarnessNameCodex      = "codex"
	HarnessNameGemini     = "gemini"
	HarnessNameOpenCode   = "opencode"
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
	// BinaryPath and Args launch the harness. Prompt is appended as the final
	// positional argument unless PromptFlag is set, in which case the runtime
	// appends PromptFlag followed by Prompt. This supports CLIs such as OpenCode
	// whose interactive TUI accepts its startup prompt only through a flag.
	BinaryPath string
	Args       []string
	PromptFlag string
	Env        []string
	// HarnessSessionID is the harness's own session id when the launch args
	// pin one (claude --session-id <uuid>). Persisted with the starting
	// metadata so transcript readers can locate the harness's log from boot;
	// empty means the watcher's TUI scrape is the only source.
	HarnessSessionID string
	// ResumedFromSessionID is the provider-side id this launch asked the
	// harness to resume (claude --resume <uuid>). Empty for a fresh session.
	// Recorded so a harness that rejects the id can say WHICH id it rejected.
	ResumedFromSessionID string

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
	case "opencode":
		return HarnessNameOpenCode
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

	conv, err := openHarnessLeadConversation(ctx, cfg)
	if err != nil {
		return err
	}

	runtime := HarnessRuntimeMetadata{
		Provider:         cfg.Backend,
		HarnessName:      cfg.HarnessName,
		ChatSessionID:    conv.ChatSessionID(),
		HarnessSessionID: cfg.HarnessSessionID,
		PID:              conv.PID(),
		Status:           RuntimeStatusStarting,
		Controlled:       true,
		StartedAt:        time.Now().UTC(),
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
	stopResize := func() {}
	if shouldForwardHarnessResize(cfg.HarnessName) {
		stopResize = startHarnessResizeForwarder(ctx, cfg, conv)
	}
	defer stopResize()

	watchCtx, cancelWatch := context.WithCancel(ctx)
	defer cancelWatch()
	go watchHarnessLeadRuntime(watchCtx, cfg, conv, handle, runtime)
	drainCtx, cancelDrain := context.WithCancel(ctx)
	defer cancelDrain()
	go drainLeadMessageQueue(drainCtx, cfg.Store, cfg.Workspace, cfg.LeadName, cfg.Logger)

	result, waitErr := conv.Wait()

	stopResize()
	cancelWatch()
	cancelDrain()
	unregister()
	restoreTerminal()
	return finalizeHarnessLeadRuntime(cfg, conv, runtime, result, waitErr)
}

// openHarnessLeadConversation starts the harness under harness-wrapper PTY
// supervision, sized to the human terminal.
func openHarnessLeadConversation(ctx context.Context, cfg HarnessLeadRuntimeConfig) (harnessConversation, error) {
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
		// session metadata by RunHarnessLeadRuntime.
		Store: memstore.New(),
	})
	if err != nil {
		return nil, fmt.Errorf("start controlled %s lead session: %w", cfg.Backend, err)
	}
	return conv, nil
}

// finalizeHarnessLeadRuntime closes the conversation, marks the runtime
// disconnected, and maps the harness exit into the runtime's return error.
func finalizeHarnessLeadRuntime(cfg HarnessLeadRuntimeConfig, conv harnessConversation, runtime HarnessRuntimeMetadata, result wrapper.Result, waitErr error) error {
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
		return fmt.Errorf("%s exited with status %d%s", cfg.BinaryPath, result.ExitCode, resumeFailureHint(cfg))
	}
	return nil
}

// resumeFailureHint annotates an early non-zero exit on a resumed launch. A
// harness that cannot find the conversation (claude prints "No conversation
// found" and exits within seconds) otherwise surfaces as a bare exit status,
// which cannot distinguish a wrong id from a deleted transcript. Naming both
// the id and the directory the transcript would live in makes that a one-look
// diagnosis.
func resumeFailureHint(cfg HarnessLeadRuntimeConfig) string {
	id := strings.TrimSpace(cfg.ResumedFromSessionID)
	if id == "" {
		return ""
	}
	hint := fmt.Sprintf(" while resuming session %s", id)
	if dir := sessions.ClaudeProjectDir(cfg.WorkDir); dir != "" && cfg.Backend == "claude" {
		hint += fmt.Sprintf(" (transcript dir: %s)", dir)
	}
	return hint
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
		if cfg.PromptFlag != "" {
			args = append(args, cfg.PromptFlag)
		}
		args = append(args, cfg.Prompt)
	}
	return args
}

// harnessTerminalSize sizes the virtual PTY to the human terminal at startup.
// startHarnessResizeForwarder keeps the wrapper screen emulator and child PTY
// synchronized with later terminal resizes.
func harnessTerminalSize(stdout io.Writer) (int, int) {
	if cols, rows, ok := currentHarnessTerminalSize(stdout); ok {
		return int(cols), int(rows)
	}
	return harnessDefaultCols, harnessDefaultRows
}

func currentHarnessTerminalSize(stdout io.Writer) (uint16, uint16, bool) {
	f, ok := stdout.(*os.File)
	if !ok {
		return 0, 0, false
	}
	fd, ok := harnessFileDescriptor(f)
	if !ok {
		return 0, 0, false
	}
	cols, rows, err := term.GetSize(fd)
	const maxUint16 = int(^uint16(0))
	if err != nil || cols <= 0 || rows <= 0 || cols > maxUint16 || rows > maxUint16 {
		return 0, 0, false
	}
	return uint16(cols), uint16(rows), true //nolint:gosec // bounds checked above.
}

func harnessFileDescriptor(f *os.File) (int, bool) {
	fd := f.Fd()
	if strconv.IntSize == 32 && fd > uintptr(1<<31-1) {
		return 0, false
	}
	return int(fd), true //nolint:gosec // G115: fd is range-checked for 32-bit int platforms above.
}

func harnessTerminalFileDescriptor(f *os.File) (int, bool) {
	fd, ok := harnessFileDescriptor(f)
	if !ok || !term.IsTerminal(fd) {
		return 0, false
	}
	return fd, true
}

// forwardHarnessStdin puts the human terminal into raw mode (when stdin is a
// TTY) and copies keystrokes into the harness PTY. The wrapper serializes
// stdin writers, so human typing coexists with programmatic message delivery.
// Returns a restore function that is safe to call multiple times.
func forwardHarnessStdin(ctx context.Context, cfg HarnessLeadRuntimeConfig, conv harnessConversation) func() {
	restore := func() {}
	if f, ok := cfg.Stdin.(*os.File); ok {
		fd, isTerminal := harnessTerminalFileDescriptor(f)
		if isTerminal {
			oldState, err := term.MakeRaw(fd)
			if err == nil {
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
	w := &harnessLeadRuntimeWatcher{
		cfg:        cfg,
		conv:       conv,
		handle:     handle,
		runtime:    runtime,
		lastStatus: runtime.Status,
	}
	events := conv.Events()
	ticker := time.NewTicker(harnessStatusPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			w.observeConversationEvent(ctx, ev)
		case <-ticker.C:
			w.poll(ctx)
		}
	}
}

// harnessLeadRuntimeWatcher holds the mutable state of one
// watchHarnessLeadRuntime loop; its methods are the loop body phases and are
// only ever called from that single goroutine.
type harnessLeadRuntimeWatcher struct {
	cfg              HarnessLeadRuntimeConfig
	conv             harnessConversation
	handle           *leadConversationHandle
	runtime          HarnessRuntimeMetadata
	lastStatus       string
	turnPendingSince time.Time
}

func (w *harnessLeadRuntimeWatcher) persist(ctx context.Context, status string) {
	if status == "" || status == w.lastStatus {
		return
	}
	w.lastStatus = status
	w.runtime.Status = status
	if err := UpdateHarnessRuntimeMetadata(ctx, w.cfg.Store, w.cfg.Workspace, w.cfg.SessionID, w.runtime); err != nil {
		w.cfg.Logger.Debug("failed to persist harness runtime status", "status", status, "err", err)
	}
}

func (w *harnessLeadRuntimeWatcher) observeConversationEvent(ctx context.Context, ev chat.ConversationEvent) {
	// Input request lifecycle events share the conversation stream but do not
	// represent assistant activity. Delivery still observes them so raw PTY
	// staging pauses while an interactive harness dialog is pending.
	if w.handle != nil {
		w.handle.observeConversationEvent(ev)
	}
	if ev.Type != chat.EventTurn {
		return
	}
	if ev.Turn.Role != chat.RoleAssistant {
		return
	}
	switch ev.Turn.State {
	case chat.TurnStatePending, chat.TurnStateStreaming:
		w.turnPendingSince = time.Now()
		w.persist(ctx, RuntimeStatusActive)
	case chat.TurnStateComplete:
		w.turnPendingSince = time.Time{}
		w.persist(ctx, RuntimeStatusIdle)
	case chat.TurnStateErrored:
		w.turnPendingSince = time.Time{}
		w.persist(ctx, RuntimeStatusWaitingUserInput)
	}
}

func (w *harnessLeadRuntimeWatcher) poll(ctx context.Context) {
	snap := w.conv.Snapshot()
	if status := harnessRuntimeStatus(snap); status != RuntimeStatusActive || w.lastStatus == RuntimeStatusStarting {
		w.persist(ctx, status)
	}
	w.backfillHarnessSessionID(ctx)
	w.logWedgedTurn(snap)
}

// backfillHarnessSessionID reconciles the pinned harness session id against
// the one the TUI actually reports.
//
// It used to return early whenever a launch pinned an id (claude --session-id),
// treating the pin as authoritative. It is not: claude ROTATES its session id
// on a first boot that clears the folder-trust dialog, and on resume it may
// fork the conversation into a new id. The id whose transcript exists on disk
// is the scraped one, so when the scrape disagrees we adopt it — otherwise the
// next `loom lead --continue` resumes an id with no transcript behind it.
//
// Two guards keep a garbled scrape from overwriting a good pin: the scrape must
// parse as a UUID, and the runtime must already have recorded its launch
// instant (StartedAt), which is the marker transcript readers use to tell this
// run's transcripts from an earlier run's.
func (w *harnessLeadRuntimeWatcher) backfillHarnessSessionID(ctx context.Context) {
	hsid := strings.TrimSpace(w.conv.HarnessSessionID())
	if hsid == "" || hsid == w.runtime.HarnessSessionID {
		return
	}
	if w.runtime.HarnessSessionID != "" {
		if uuid.Validate(hsid) != nil || w.runtime.StartedAt.IsZero() {
			return
		}
		w.cfg.Logger.Info("harness rotated its session id; adopting the scraped id",
			"lead", w.cfg.LeadName, "pinned", w.runtime.HarnessSessionID, "scraped", hsid)
	}
	w.runtime.HarnessSessionID = hsid
	if err := UpdateHarnessRuntimeMetadata(ctx, w.cfg.Store, w.cfg.Workspace, w.cfg.SessionID, w.runtime); err != nil {
		w.cfg.Logger.Debug("failed to persist harness session id", "err", err)
	}
}

func (w *harnessLeadRuntimeWatcher) logWedgedTurn(snap wrapper.Snapshot) {
	if w.turnPendingSince.IsZero() || time.Since(w.turnPendingSince) <= harnessWedgedTurnLogWindow {
		return
	}
	if snap.LastOutputAt.IsZero() || time.Since(snap.LastOutputAt) <= harnessWedgedTurnLogWindow {
		return
	}
	w.cfg.Logger.Warn("harness turn has been pending without output; turn-completion marker may have been missed",
		"lead", w.cfg.LeadName, "pending_since", w.turnPendingSince)
	w.turnPendingSince = time.Time{}
}
