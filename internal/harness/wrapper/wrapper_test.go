package wrapper_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/tysonthomas9/loomcli/internal/harness/wrapper"
	"github.com/tysonthomas9/loomcli/internal/harness/wrapper/trace"
)

// recordingEmitter is a test helper that captures trace events.
type recordingEmitter struct {
	mu     sync.Mutex
	events []trace.Event
}

func (r *recordingEmitter) Emit(e trace.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recordingEmitter) Events() []trace.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.events)
}

func (r *recordingEmitter) Kinds() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.events))
	for i, e := range r.events {
		out[i] = e.Kind
	}
	return out
}

// captureStdout returns a *os.File the wrapper can write to, plus a
// function the test calls after Run returns to retrieve everything that
// was written. The drain goroutine prevents the wrapper from blocking
// on a full pipe buffer.
func captureStdout(t *testing.T) (write *os.File, drain func() string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()
	drain = func() string {
		_ = w.Close()
		<-done
		_ = r.Close()
		return buf.String()
	}
	return w, drain
}

func TestRun_CompletedMode(t *testing.T) {
	w, drain := captureStdout(t)

	res, err := wrapper.Run(context.Background(), wrapper.Config{
		BinaryPath: mockHarnessBin,
		Args:       []string{"--mode", "completed", "--steps", "2", "--delay", "1ms"},
		Stdout:     w,
	})
	output := drain()

	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if res.Status != wrapper.StatusIdle {
		t.Errorf("Status = %q, want %q", res.Status, wrapper.StatusIdle)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", res.ExitCode)
	}
	if res.PID == 0 {
		t.Errorf("PID = 0, want non-zero")
	}
	if res.LastOutputAt.IsZero() {
		t.Errorf("LastOutputAt is zero; expected non-zero after observed output")
	}
	for _, want := range []string{"Mock Agent CLI", "Step 1/2", "Step 2/2", "DONE"} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, output)
		}
	}
}

func TestRun_FailedMode(t *testing.T) {
	w, drain := captureStdout(t)

	res, err := wrapper.Run(context.Background(), wrapper.Config{
		BinaryPath: mockHarnessBin,
		Args:       []string{"--mode", "failed", "--exit-code", "7"},
		Stdout:     w,
	})
	_ = drain()

	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if res.Status != wrapper.StatusFailed {
		t.Errorf("Status = %q, want %q", res.Status, wrapper.StatusFailed)
	}
	if res.ExitCode != 7 {
		t.Errorf("ExitCode = %d, want 7", res.ExitCode)
	}
	if res.Reason == "" {
		t.Errorf("Reason should be populated for StatusFailed")
	}
}

func TestRun_EmptyBinaryPath(t *testing.T) {
	w, drain := captureStdout(t)
	defer drain()

	_, err := wrapper.Run(context.Background(), wrapper.Config{
		BinaryPath: "",
		Stdout:     w,
	})
	if !errors.Is(err, wrapper.ErrInvalidConfig) {
		t.Errorf("err = %v, want ErrInvalidConfig", err)
	}
}

func TestRun_NilStdout(t *testing.T) {
	_, err := wrapper.Run(context.Background(), wrapper.Config{
		BinaryPath: mockHarnessBin,
	})
	if !errors.Is(err, wrapper.ErrInvalidConfig) {
		t.Errorf("err = %v, want ErrInvalidConfig", err)
	}
}

func TestRun_BinaryNotFound(t *testing.T) {
	w, drain := captureStdout(t)
	defer drain()

	_, err := wrapper.Run(context.Background(), wrapper.Config{
		BinaryPath: "/no/such/binary/harness-wrapper-test-missing",
		Stdout:     w,
	})
	if !errors.Is(err, wrapper.ErrBinaryNotFound) {
		t.Errorf("err = %v, want ErrBinaryNotFound", err)
	}
}

func TestRun_EmitsLifecycleTraceEvents(t *testing.T) {
	w, drain := captureStdout(t)
	emitter := &recordingEmitter{}

	res, err := wrapper.Run(context.Background(), wrapper.Config{
		BinaryPath: mockHarnessBin,
		Args:       []string{"--mode", "completed", "--steps", "1", "--delay", "1ms"},
		Stdout:     w,
		Trace:      emitter,
	})
	_ = drain()

	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	kinds := emitter.Kinds()
	wantOrder := []string{"wrapper_started", "pty_opened", "pty_closed", "harness_exited"}
	if !slices.Equal(kinds, wantOrder) {
		t.Fatalf("event kinds = %v, want %v", kinds, wantOrder)
	}

	events := emitter.Events()
	startedFields := events[0].Fields
	if startedFields["binary_path"] != mockHarnessBin {
		t.Errorf("wrapper_started.binary_path = %v, want %s", startedFields["binary_path"], mockHarnessBin)
	}
	if _, ok := startedFields["args"]; !ok {
		t.Errorf("wrapper_started missing args field")
	}

	openedFields := events[1].Fields
	if openedFields["pid"] != res.PID {
		t.Errorf("pty_opened.pid = %v, want %d", openedFields["pid"], res.PID)
	}

	exitedFields := events[3].Fields
	if exitedFields["status"] != string(wrapper.StatusIdle) {
		t.Errorf("harness_exited.status = %v, want idle", exitedFields["status"])
	}
	if exitedFields["exit_code"] != 0 {
		t.Errorf("harness_exited.exit_code = %v, want 0", exitedFields["exit_code"])
	}
	if dur, ok := exitedFields["duration_ms"].(int64); !ok || dur < 0 {
		t.Errorf("harness_exited.duration_ms = %v (%T), want non-negative int64", exitedFields["duration_ms"], exitedFields["duration_ms"])
	}
}

func TestRun_IdleClassifierEmitsQuietAndClassifyEvents(t *testing.T) {
	w, drain := captureStdout(t)
	emitter := &recordingEmitter{}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	_, _ = wrapper.Run(ctx, wrapper.Config{
		BinaryPath:   mockHarnessBin,
		Args:         []string{"--mode", "stuck"},
		Stdout:       w,
		Trace:        emitter,
		IdleQuiet:    50 * time.Millisecond,
		IdleClassify: 200 * time.Millisecond,
	})
	_ = drain()

	kinds := emitter.Kinds()
	if !slices.Contains(kinds, "output_quiet") {
		t.Errorf("expected output_quiet event, got kinds: %v", kinds)
	}
	if !slices.Contains(kinds, "output_classify_threshold") {
		t.Errorf("expected output_classify_threshold event, got kinds: %v", kinds)
	}
}

func TestRun_IdleClassifierEmitsBlockedByCostForLimitPrompt(t *testing.T) {
	w, drain := captureStdout(t)
	emitter := &recordingEmitter{}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	res, err := wrapper.Run(ctx, wrapper.Config{
		BinaryPath:   mockHarnessBin,
		Args:         []string{"--mode", "needs-input", "--prompt", "You've hit your limit - resets 5:50pm. What do you want to do? "},
		Stdout:       w,
		Trace:        emitter,
		IdleQuiet:    50 * time.Millisecond,
		IdleClassify: 200 * time.Millisecond,
	})
	_ = drain()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != wrapper.StatusBlockedByCost {
		t.Errorf("Status = %q, want StatusBlockedByCost", res.Status)
	}

	if kinds := emitter.Kinds(); !slices.Contains(kinds, "harness_blocked_by_cost") {
		t.Errorf("expected harness_blocked_by_cost event, got kinds: %v", kinds)
	}
}

func TestRun_ContextCancelInterruptsAndDoesNotPropagateErr(t *testing.T) {
	w, drain := captureStdout(t)

	ctx, cancel := context.WithCancel(context.Background())

	type runResult struct {
		res wrapper.Result
		err error
	}
	resCh := make(chan runResult, 1)
	go func() {
		res, err := wrapper.Run(ctx, wrapper.Config{
			BinaryPath: mockHarnessBin,
			Args:       []string{"--mode", "stuck"},
			Stdout:     w,
			WaitDelay:  500 * time.Millisecond,
		})
		resCh <- runResult{res, err}
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case rr := <-resCh:
		_ = drain()
		if rr.err != nil {
			t.Fatalf("Run returned error: %v (ctx.Err should NOT propagate)", rr.err)
		}
		if rr.res.Status != wrapper.StatusInterrupted {
			t.Errorf("Status = %q, want %q", rr.res.Status, wrapper.StatusInterrupted)
		}
		if !strings.Contains(rr.res.Reason, "context cancelled") {
			t.Errorf("Reason = %q, want to contain 'context cancelled'", rr.res.Reason)
		}
	case <-time.After(3 * time.Second):
		_ = drain()
		t.Fatal("Run did not return within 3s after cancel")
	}
}

func TestRun_HeadlessSkipsRawMode(t *testing.T) {
	w, drain := captureStdout(t)
	emitter := &recordingEmitter{}

	_, err := wrapper.Run(context.Background(), wrapper.Config{
		BinaryPath: mockHarnessBin,
		Args:       []string{"--mode", "completed", "--steps", "1", "--delay", "1ms"},
		Stdout:     w,
		Trace:      emitter,
	})
	_ = drain()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, kind := range emitter.Kinds() {
		if kind == "raw_mode_enabled" || kind == "winsize_initial" || kind == "winsize_changed" {
			t.Errorf("unexpected TTY event %q on headless run; emitted kinds: %v", kind, emitter.Kinds())
		}
	}
}

func TestRun_TTYEnablesRawModeAndForwardsWinsize(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Skipf("pty.Open not supported in this environment: %v", err)
	}
	t.Cleanup(func() {
		_ = master.Close()
		_ = slave.Close()
	})

	// Drain the master so the wrapper's writes don't block on a full
	// PTY buffer.
	go func() { _, _ = io.Copy(io.Discard, master) }()

	// Set an initial size on the slave so the wrapper's GetsizeFull picks
	// up real numbers.
	_ = pty.Setsize(slave, &pty.Winsize{Rows: 24, Cols: 80})

	emitter := &recordingEmitter{}
	_, err = wrapper.Run(context.Background(), wrapper.Config{
		BinaryPath: mockHarnessBin,
		Args:       []string{"--mode", "completed", "--steps", "1", "--delay", "1ms"},
		Stdin:      slave,
		Stdout:     slave,
		Trace:      emitter,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	kinds := emitter.Kinds()
	if !slices.Contains(kinds, "raw_mode_enabled") {
		t.Errorf("expected raw_mode_enabled event for TTY stdin/stdout, got kinds: %v", kinds)
	}
	if !slices.Contains(kinds, "winsize_initial") {
		t.Errorf("expected winsize_initial event for TTY stdin/stdout, got kinds: %v", kinds)
	}
}

func TestRun_NeedsInputModeForwardsStdin(t *testing.T) {
	w, drain := captureStdout(t)

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() { _ = stdinR.Close() })

	go func() {
		time.Sleep(150 * time.Millisecond)
		_, _ = stdinW.Write([]byte("y\n"))
		_ = stdinW.Close()
	}()

	res, err := wrapper.Run(context.Background(), wrapper.Config{
		BinaryPath: mockHarnessBin,
		Args:       []string{"--mode", "needs-input", "--expected-input", "y"},
		Stdin:      stdinR,
		Stdout:     w,
	})
	output := drain()

	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != wrapper.StatusIdle {
		t.Errorf("Status = %q, want StatusIdle (mock should accept 'y' and exit 0)", res.Status)
	}
	if !strings.Contains(output, "Approved. DONE") {
		t.Errorf("expected approval marker in output, got: %q", output)
	}
}

func TestRun_CostLimitedModeReportsBlockedByCost(t *testing.T) {
	w, drain := captureStdout(t)

	res, err := wrapper.Run(context.Background(), wrapper.Config{
		BinaryPath: mockHarnessBin,
		Args:       []string{"--mode", "cost-limited", "--exit-code", "3"},
		Stdout:     w,
	})
	_ = drain()

	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != wrapper.StatusBlockedByCost {
		t.Errorf("Status = %q, want StatusBlockedByCost", res.Status)
	}
	if res.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", res.ExitCode)
	}
	if !strings.Contains(res.Reason, "limit") {
		t.Errorf("Reason = %q, want limit context", res.Reason)
	}
}

func TestRun_NilTraceUsesDiscard(t *testing.T) {
	w, drain := captureStdout(t)

	_, err := wrapper.Run(context.Background(), wrapper.Config{
		BinaryPath: mockHarnessBin,
		Args:       []string{"--mode", "completed", "--steps", "1", "--delay", "1ms"},
		Stdout:     w,
	})
	_ = drain()

	if err != nil {
		t.Fatalf("Run with nil Trace returned error: %v", err)
	}
}

func TestRun_IdleClassifyLessThanQuietRejected(t *testing.T) {
	w, drain := captureStdout(t)
	defer drain()

	_, err := wrapper.Run(context.Background(), wrapper.Config{
		BinaryPath:   mockHarnessBin,
		Stdout:       w,
		IdleQuiet:    100,
		IdleClassify: 10,
	})
	if !errors.Is(err, wrapper.ErrInvalidConfig) {
		t.Errorf("err = %v, want ErrInvalidConfig", err)
	}
}

// TestRun_AcceptsIOReaderWriterStdio verifies the W1 contract: Config.Stdin
// accepts any io.Reader (here a strings.Reader) and Config.Stdout accepts
// any io.Writer (here a bytes.Buffer), without any *os.File ceremony.
// Headless mode (no raw-mode setup) is implicit because neither value is
// a *os.File pointing at a TTY.
func TestRun_AcceptsIOReaderWriterStdio(t *testing.T) {
	var stdoutBuf bytes.Buffer
	stdinReader := strings.NewReader("y\n")

	res, err := wrapper.Run(context.Background(), wrapper.Config{
		BinaryPath: mockHarnessBin,
		Args:       []string{"--mode", "needs-input", "--expected-input", "y"},
		Stdin:      stdinReader,
		Stdout:     &stdoutBuf,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != wrapper.StatusIdle {
		t.Errorf("Status = %q, want StatusIdle", res.Status)
	}
	output := stdoutBuf.String()
	if !strings.Contains(output, "Approved. DONE") {
		t.Errorf("expected approval marker in output, got: %q", output)
	}
}
