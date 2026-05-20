package wrapper_test

import (
	"context"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/harness/wrapper"
)

// awaitAPIError drains events on sess.Events until a non-terminal
// StatusAPIError event arrives, returning it immediately. Fails the
// test if the channel closes or the timeout fires first.
func awaitAPIError(t *testing.T, sess *wrapper.Session, timeout time.Duration) wrapper.SessionEvent {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-sess.Events():
			if !ok {
				t.Fatalf("events channel closed before api_error arrived")
			}
			if ev.Status == wrapper.StatusAPIError && !ev.Terminated {
				return ev
			}
		case <-deadline:
			t.Fatalf("timeout waiting for api_error event after %v", timeout)
		}
	}
}

// countAPIErrorsOver waits for at least one api_error event then keeps
// reading the channel for `extra` more time, returning the total count.
// Used to assert de-duplication.
func countAPIErrorsOver(t *testing.T, sess *wrapper.Session, firstTimeout, extra time.Duration) int {
	t.Helper()
	awaitAPIError(t, sess, firstTimeout)
	count := 1
	deadline := time.After(extra)
	for {
		select {
		case ev, ok := <-sess.Events():
			if !ok {
				return count
			}
			if ev.Status == wrapper.StatusAPIError && !ev.Terminated {
				count++
			}
		case <-deadline:
			return count
		}
	}
}

// processAlive reports whether pid still exists. Uses signal-0
// probing, which on Unix returns nil iff the process is alive.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, syscall.Signal(0)) == nil
}

func apiErrorConfig(stdout *os.File, args ...string) wrapper.Config {
	return wrapper.Config{
		BinaryPath:   mockHarnessBin,
		Args:         args,
		Stdout:       stdout,
		Harness:      "claude",
		IdleQuiet:    200 * time.Millisecond,
		IdleClassify: 200 * time.Millisecond,
		WaitDelay:    500 * time.Millisecond,
	}
}

// S1: API error → keep alive. The wrapper must classify api_error with
// HTTPCode=529 within a short window, the harness must remain alive
// after the event fires, and Snapshot.Status must reflect the mid-run
// classification while the process is still running.
func TestSession_APIError_KeepAlive(t *testing.T) {
	w, drain := captureStdout(t)
	defer drain()

	sess, err := wrapper.Start(context.Background(), apiErrorConfig(w,
		"--mode", "api-error",
		"--api-error-msg", "API Error: 529 Overloaded.",
	))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = sess.Stop(context.Background()) })

	ev := awaitAPIError(t, sess, 3*time.Second)
	if ev.HTTPCode != 529 {
		t.Errorf("HTTPCode = %d, want 529 (event: %+v)", ev.HTTPCode, ev)
	}
	if ev.Terminated {
		t.Errorf("Terminated = true, want false (api_error must be non-terminal)")
	}

	// Wrapper must NOT have killed the harness.
	if !processAlive(sess.PID()) {
		t.Errorf("harness pid %d not alive after api_error event; wrapper should keep it running", sess.PID())
	}
	snap := sess.Snapshot()
	if snap.Status != wrapper.StatusAPIError {
		t.Errorf("Snapshot.Status = %q, want %q", snap.Status, wrapper.StatusAPIError)
	}
}

// S1b: Transport-error variant — no HTTP code, leading tree-character
// prefix. Same keep-alive contract, HTTPCode=0, Reason carries the
// matched message.
func TestSession_APIError_TransportVariant(t *testing.T) {
	w, drain := captureStdout(t)
	defer drain()

	sess, err := wrapper.Start(context.Background(), apiErrorConfig(w,
		"--mode", "api-error",
		"--api-error-msg", "  ⎿  API Error: The socket connection was closed unexpectedly.",
	))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = sess.Stop(context.Background()) })

	ev := awaitAPIError(t, sess, 3*time.Second)
	if ev.HTTPCode != 0 {
		t.Errorf("HTTPCode = %d, want 0 for transport error", ev.HTTPCode)
	}
	if !strings.Contains(ev.Reason, "socket connection was closed") {
		t.Errorf("Reason = %q, want substring %q", ev.Reason, "socket connection was closed")
	}
}

// S2: RetryAfter propagates end-to-end.
func TestSession_APIError_RetryAfter(t *testing.T) {
	w, drain := captureStdout(t)
	defer drain()

	sess, err := wrapper.Start(context.Background(), apiErrorConfig(w,
		"--mode", "api-error",
		"--api-error-msg", "API Error: 429 Too Many Requests. Retry after 30 seconds.",
	))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = sess.Stop(context.Background()) })

	ev := awaitAPIError(t, sess, 3*time.Second)
	if ev.HTTPCode != 429 {
		t.Errorf("HTTPCode = %d, want 429", ev.HTTPCode)
	}
	if ev.RetryAfter != 30*time.Second {
		t.Errorf("RetryAfter = %v, want 30s", ev.RetryAfter)
	}
}

// S3: De-duplication. The mock prints the same line three times with a
// short gap; exactly ONE api_error SessionEvent must surface.
func TestSession_APIError_DeDuplicates(t *testing.T) {
	w, drain := captureStdout(t)
	defer drain()

	sess, err := wrapper.Start(context.Background(), apiErrorConfig(w,
		"--mode", "api-error",
		"--api-error-msg", "API Error: 529 Overloaded.",
		"--api-error-repeat", "3",
		"--api-error-repeat-gap", "100ms",
	))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = sess.Stop(context.Background()) })

	// Wait for the first event, then keep reading for 1.5s — enough
	// for several classifier ticks across all three prints. Dedup must
	// collapse identical Status+Reason into a single event.
	seen := countAPIErrorsOver(t, sess, 3*time.Second, 1500*time.Millisecond)
	if seen != 1 {
		t.Errorf("api_error event count = %d, want 1 (dedup must collapse repeated identical lines)", seen)
	}
}

// S4: Recovery — after an api_error fires mid-run, the harness produces
// fresh output and exits cleanly. The terminal Result.Status must be
// StatusIdle, not StatusAPIError (which is non-terminal and must not
// contaminate the final outcome).
func TestSession_APIError_RecoveryExitsIdle(t *testing.T) {
	w, drain := captureStdout(t)
	defer drain()

	sess, err := wrapper.Start(context.Background(), apiErrorConfig(w,
		"--mode", "api-error",
		"--api-error-msg", "API Error: 529 Overloaded.",
		"--api-error-recover", "true",
		"--steps", "2",
		"--delay", "1ms",
	))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	awaitAPIError(t, sess, 3*time.Second)

	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res.Status != wrapper.StatusIdle {
		t.Errorf("Result.Status = %q, want StatusIdle (non-terminal api_error must not contaminate final status)", res.Status)
	}
}

// S5: Stop overrides the mid-run StatusAPIError on Result.Status. The
// wrapper records api_error in Snapshot during the run, but the
// terminal Result must reflect StatusInterrupted because the caller
// asked for shutdown.
func TestSession_APIError_StopOverridesTerminalStatus(t *testing.T) {
	w, drain := captureStdout(t)
	defer drain()

	sess, err := wrapper.Start(context.Background(), apiErrorConfig(w,
		"--mode", "api-error",
		"--api-error-msg", "API Error: 529 Overloaded.",
	))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Confirm the mid-run classification first.
	awaitAPIError(t, sess, 3*time.Second)
	if got := sess.Snapshot().Status; got != wrapper.StatusAPIError {
		t.Errorf("mid-run Snapshot.Status = %q, want %q", got, wrapper.StatusAPIError)
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := sess.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res.Status != wrapper.StatusInterrupted {
		t.Errorf("Result.Status = %q, want StatusInterrupted (Stop must override mid-run api_error)", res.Status)
	}
}

// S6: harness_api_error trace event fires for parity with the existing
// harness_blocked_by_cost / harness_retry_later kinds.
func TestSession_APIError_TraceEmission(t *testing.T) {
	w, drain := captureStdout(t)
	defer drain()

	rec := &recordingEmitter{}
	cfg := apiErrorConfig(w,
		"--mode", "api-error",
		"--api-error-msg", "API Error: 529 Overloaded.",
	)
	cfg.Trace = rec

	sess, err := wrapper.Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = sess.Stop(context.Background()) })

	awaitAPIError(t, sess, 3*time.Second)

	// The trace emit happens just before the SessionEvent goes out, but
	// give it a moment to settle.
	time.Sleep(200 * time.Millisecond)

	found := false
	for _, e := range rec.Events() {
		if e.Kind != "harness_api_error" {
			continue
		}
		found = true
		if code, ok := e.Fields["http_code"].(int); !ok || code != 529 {
			t.Errorf("harness_api_error http_code field = %v (%T), want 529 int", e.Fields["http_code"], e.Fields["http_code"])
		}
	}
	if !found {
		t.Errorf("no harness_api_error trace event among kinds %v", rec.Kinds())
	}
}

// S7: No-regression — a clean idle harness with no api error stays
// idle and emits no api_error events.
func TestSession_APIError_NoRegressionOnIdleHarness(t *testing.T) {
	w, drain := captureStdout(t)
	defer drain()

	sess, err := wrapper.Start(context.Background(), wrapper.Config{
		BinaryPath: mockHarnessBin,
		Args:       []string{"--mode", "completed", "--steps", "2", "--delay", "1ms"},
		Stdout:     w,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	sawAPIError := false
	for ev := range sess.Events() {
		if ev.Status == wrapper.StatusAPIError {
			sawAPIError = true
		}
	}
	if sawAPIError {
		t.Errorf("clean completed run unexpectedly emitted a StatusAPIError event")
	}

	res, _ := sess.Wait()
	if res.Status != wrapper.StatusIdle {
		t.Errorf("Result.Status = %q, want StatusIdle", res.Status)
	}
}
