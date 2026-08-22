package leadcontrol

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// HarnessNameHeadless is the MetadataHarnessName value that marks a headless
// lead runtime: no TUI, no PTY supervision. The lead's thread lives in the
// backend's own chat storage and every turn is one short-lived
// non-interactive process resuming that thread (cursor-agent -p --resume).
// Delivery and status reporting reuse the harness inbox machinery; only the
// turn transport differs.
const HarnessNameHeadless = "headless"

// ErrHeadlessTurnInFlight is returned by startTurn while a turn process is
// still running; the inbox drain retries on its next tick.
var ErrHeadlessTurnInFlight = errors.New("headless lead turn is in flight")

// HeadlessLeadRuntimeConfig configures a headless controlled lead session.
type HeadlessLeadRuntimeConfig struct {
	Store     store.Store
	Workspace string
	LeadName  string
	SessionID string
	WorkDir   string
	Prompt    string

	// Backend is the loom backend name recorded as the runtime provider.
	Backend string
	// BinaryPath and Args run one turn; the runtime appends ResumeFlag,
	// ChatSessionID and the turn text as the final positional argument.
	BinaryPath string
	Args       []string
	ResumeFlag string
	// ChatSessionID is the backend thread id resumed on every turn. Generated
	// (UUIDv4) when empty; cursor-agent creates the chat on first use.
	ChatSessionID string
	Env           []string

	Stdout io.Writer
	Stderr io.Writer
	Logger *slog.Logger
}

// RunHeadlessLeadRuntime runs the seed prompt as the first turn, then serves
// queued inbox messages one turn at a time until ctx is cancelled.
func RunHeadlessLeadRuntime(ctx context.Context, cfg HeadlessLeadRuntimeConfig) error {
	cfg = normalizeHeadlessLeadRuntimeConfig(cfg)
	if cfg.BinaryPath == "" {
		return fmt.Errorf("headless lead runtime: binary path required")
	}
	h := &headlessLeadRuntime{
		cfg: cfg,
		runtime: HarnessRuntimeMetadata{
			Provider:      cfg.Backend,
			HarnessName:   HarnessNameHeadless,
			ChatSessionID: cfg.ChatSessionID,
			PID:           os.Getpid(),
			Status:        RuntimeStatusStarting,
			Controlled:    true,
			StartedAt:     time.Now().UTC(),
		},
	}
	h.persist(ctx, RuntimeStatusStarting)
	unregister := registerHeadlessRuntime(cfg.SessionID, h)
	defer unregister()

	_, _ = fmt.Fprintf(cfg.Stdout, "Launching headless %s lead session (chat %s)...\n\n", cfg.Backend, cfg.ChatSessionID)

	turnCtx, cancelTurns := context.WithCancel(ctx)
	defer cancelTurns()
	h.turnCtx = turnCtx

	// Seed turn runs synchronously: a lead that cannot even start is a
	// launch failure, not a wedged session.
	if err := h.startTurn(cfg.Prompt); err != nil {
		return err
	}
	h.waitTurn()
	if h.seedErr != nil {
		h.persist(context.Background(), RuntimeStatusFailed)
		return fmt.Errorf("headless lead seed turn: %w", h.seedErr)
	}

	drainCtx, cancelDrain := context.WithCancel(ctx)
	defer cancelDrain()
	go drainLeadMessageQueue(drainCtx, cfg.Store, cfg.Workspace, cfg.LeadName, cfg.Logger)

	<-ctx.Done()
	cancelDrain()
	cancelTurns()
	h.waitTurn()
	unregister()
	h.persist(context.Background(), RuntimeStatusDisconnected)
	return nil
}

func normalizeHeadlessLeadRuntimeConfig(cfg HeadlessLeadRuntimeConfig) HeadlessLeadRuntimeConfig {
	cfg.Workspace = strings.TrimSpace(cfg.Workspace)
	cfg.LeadName = strings.TrimSpace(cfg.LeadName)
	cfg.SessionID = strings.TrimSpace(cfg.SessionID)
	cfg.WorkDir = strings.TrimSpace(cfg.WorkDir)
	cfg.Backend = strings.ToLower(strings.TrimSpace(cfg.Backend))
	cfg.ChatSessionID = strings.TrimSpace(cfg.ChatSessionID)
	if cfg.ChatSessionID == "" {
		cfg.ChatSessionID = uuid.NewString()
	}
	if cfg.ResumeFlag == "" {
		cfg.ResumeFlag = "--resume"
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

// headlessLeadRuntime is one live headless session: at most one turn process
// at a time, status mirrored into session metadata for leadmsg --status.
type headlessLeadRuntime struct {
	cfg     HeadlessLeadRuntimeConfig
	turnCtx context.Context

	mu         sync.Mutex
	runtime    HarnessRuntimeMetadata
	lastStatus string
	busy       bool
	done       chan struct{}
	turns      int
	seedErr    error
}

func (h *headlessLeadRuntime) persist(ctx context.Context, status string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.persistLocked(ctx, status)
}

func (h *headlessLeadRuntime) persistLocked(ctx context.Context, status string) {
	h.runtime.Status = status
	h.lastStatus = status
	if err := UpdateHarnessRuntimeMetadata(ctx, h.cfg.Store, h.cfg.Workspace, h.cfg.SessionID, h.runtime); err != nil {
		h.cfg.Logger.Debug("failed to persist headless runtime status", "status", status, "err", err)
	}
}

func (h *headlessLeadRuntime) status() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastStatus
}

// startTurn launches one turn process for message and returns immediately.
// ErrHeadlessTurnInFlight when a turn is already running.
func (h *headlessLeadRuntime) startTurn(message string) error {
	h.mu.Lock()
	if h.busy {
		h.mu.Unlock()
		return ErrHeadlessTurnInFlight
	}
	h.busy = true
	h.turns++
	turn := h.turns
	h.done = make(chan struct{})
	done := h.done
	h.persistLocked(h.turnCtx, RuntimeStatusActive)
	h.mu.Unlock()

	go func() {
		defer close(done)
		err := h.runTurn(h.turnCtx, turn, message)
		h.mu.Lock()
		defer h.mu.Unlock()
		h.busy = false
		if turn == 1 {
			h.seedErr = err
		}
		if err != nil {
			h.cfg.Logger.Warn("headless lead turn failed", "turn", turn, "err", err)
			_, _ = fmt.Fprintf(h.cfg.Stdout, "\n[lead turn %d failed: %v]\n", turn, err)
		}
		if h.turnCtx.Err() != nil {
			return
		}
		// A failed turn leaves the session idle on purpose: the next queued
		// message must still be attempted (failed would block delivery).
		h.persistLocked(h.turnCtx, RuntimeStatusIdle)
	}()
	return nil
}

// waitTurn blocks until the in-flight turn (if any) has finished.
func (h *headlessLeadRuntime) waitTurn() {
	h.mu.Lock()
	done := h.done
	h.mu.Unlock()
	if done != nil {
		<-done
	}
}

func (h *headlessLeadRuntime) turnArgs(message string) []string {
	args := append([]string{}, h.cfg.Args...)
	args = append(args, h.cfg.ResumeFlag, h.cfg.ChatSessionID, message)
	return args
}

// runTurn executes one turn process to completion, echoing assistant text to
// Stdout and the result summary (usage, duration) to the logger.
func (h *headlessLeadRuntime) runTurn(ctx context.Context, turn int, message string) error {
	cmd := exec.CommandContext(ctx, h.cfg.BinaryPath, h.turnArgs(message)...)
	cmd.Dir = h.cfg.WorkDir
	cmd.Env = h.cfg.Env
	cmd.Stderr = h.cfg.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(h.cfg.Stdout, "\n=== lead turn %d (%s) ===\n", turn, time.Now().UTC().Format(time.RFC3339))
	if err := cmd.Start(); err != nil {
		return err
	}
	summary := h.consumeTurnStream(stdout, turn)
	waitErr := cmd.Wait()
	if summary.isError {
		return fmt.Errorf("turn %d reported is_error: %s", turn, truncateForLog(summary.result, 300))
	}
	if waitErr != nil {
		return fmt.Errorf("turn %d: %w", turn, waitErr)
	}
	return nil
}

type headlessTurnSummary struct {
	isError bool
	result  string
}

// headlessStreamEvent is the subset of the stream-json event shape the
// runtime cares about (cursor-agent --output-format stream-json).
type headlessStreamEvent struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	Message   struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
	IsError    bool            `json:"is_error"`
	Result     string          `json:"result"`
	DurationMS int64           `json:"duration_ms"`
	Usage      json.RawMessage `json:"usage"`
}

func (h *headlessLeadRuntime) consumeTurnStream(r io.Reader, turn int) headlessTurnSummary {
	var summary headlessTurnSummary
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			h.observeStreamLine(strings.TrimRight(line, "\r\n"), turn, &summary)
		}
		if err != nil {
			return summary
		}
	}
}

func (h *headlessLeadRuntime) observeStreamLine(line string, turn int, summary *headlessTurnSummary) {
	if !strings.HasPrefix(line, "{") {
		_, _ = fmt.Fprintln(h.cfg.Stdout, line)
		return
	}
	var ev headlessStreamEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		_, _ = fmt.Fprintln(h.cfg.Stdout, line)
		return
	}
	switch ev.Type {
	case "assistant":
		for _, c := range ev.Message.Content {
			if c.Type == "text" && c.Text != "" {
				_, _ = fmt.Fprintln(h.cfg.Stdout, c.Text)
			}
		}
	case "result":
		summary.isError = ev.IsError
		summary.result = ev.Result
		h.cfg.Logger.Info("headless lead turn complete",
			"turn", turn, "is_error", ev.IsError, "duration_ms", ev.DurationMS,
			"usage", string(ev.Usage), "chat", ev.SessionID)
		_, _ = fmt.Fprintf(h.cfg.Stdout, "[turn %d done in %dms usage=%s]\n", turn, ev.DurationMS, string(ev.Usage))
	}
	if ev.SessionID != "" && ev.SessionID != h.cfg.ChatSessionID {
		h.cfg.Logger.Warn("headless lead chat id drifted", "want", h.cfg.ChatSessionID, "got", ev.SessionID)
	}
}

func truncateForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ---- in-process registry (mirrors registerLeadConversation) ----

var (
	headlessRegistryMu sync.Mutex
	headlessRegistry   = map[string]*headlessLeadRuntime{}
)

func registerHeadlessRuntime(sessionID string, h *headlessLeadRuntime) func() {
	headlessRegistryMu.Lock()
	headlessRegistry[sessionID] = h
	headlessRegistryMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			headlessRegistryMu.Lock()
			if headlessRegistry[sessionID] == h {
				delete(headlessRegistry, sessionID)
			}
			headlessRegistryMu.Unlock()
		})
	}
}

func lookupHeadlessRuntime(sessionID string) *headlessLeadRuntime {
	headlessRegistryMu.Lock()
	defer headlessRegistryMu.Unlock()
	return headlessRegistry[sessionID]
}

// ---- deliverer ----

// headlessTurnDeliverer injects inbox messages into an in-process headless
// runtime. Cross-process callers (leadmsg) only enqueue; the runtime's own
// drain goroutine performs the delivery.
type headlessTurnDeliverer struct {
	harnessTurnDeliverer
}

func newHeadlessTurnDeliverer(provider string, session *domain.AgentSession) *headlessTurnDeliverer {
	return &headlessTurnDeliverer{harnessTurnDeliverer: *newHarnessTurnDeliverer(provider, session)}
}

func (d *headlessTurnDeliverer) deliverTurn(
	_ context.Context,
	_ store.Store,
	_ string,
	sessionID string,
	result *DeliveryResult,
	message string,
	_ string,
) (*DeliveryResult, error) {
	h := lookupHeadlessRuntime(sessionID)
	if h == nil {
		result.Reason = harnessRegistryMissReason
		return result, nil
	}
	if status := h.status(); status == RuntimeStatusFailed || status == RuntimeStatusDisconnected {
		result.Reason = "headless lead runtime is " + status
		return result, nil
	}
	if err := h.startTurn(message); err != nil {
		result.Reason = err.Error()
		return result, nil
	}
	// startTurn already persisted "active"; writing it again here would race
	// a fast turn's "idle" and leave the session reported busy forever.
	d.runtime.Status = RuntimeStatusActive
	result.HarnessRuntime = d.runtime
	result.State = DeliveryStateDelivered
	result.Reason = ""
	return result, nil
}
