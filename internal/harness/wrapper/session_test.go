package wrapper_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/harness/wrapper"
)

func TestSession_StartWaitCompletedMatchesRun(t *testing.T) {
	w, drain := captureStdout(t)
	defer drain()

	sess, err := wrapper.Start(context.Background(), wrapper.Config{
		BinaryPath: mockHarnessBin,
		Args:       []string{"--mode", "completed", "--steps", "1", "--delay", "1ms"},
		Stdout:     w,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res.Status != wrapper.StatusIdle {
		t.Errorf("Status = %q, want StatusIdle", res.Status)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", res.ExitCode)
	}
	if sess.PID() == 0 {
		t.Errorf("PID = 0; want non-zero after harness started")
	}
	snap := sess.Snapshot()
	if snap.Status != wrapper.StatusIdle {
		t.Errorf("Snapshot.Status = %q after termination, want StatusIdle", snap.Status)
	}
}

func TestSession_WaitIsIdempotent(t *testing.T) {
	w, drain := captureStdout(t)
	defer drain()

	sess, err := wrapper.Start(context.Background(), wrapper.Config{
		BinaryPath: mockHarnessBin,
		Args:       []string{"--mode", "completed", "--steps", "1", "--delay", "1ms"},
		Stdout:     w,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	first, _ := sess.Wait()
	second, _ := sess.Wait()
	if first != second {
		t.Errorf("Wait results differ across calls: %+v vs %+v", first, second)
	}
}

func TestSession_EventsClosedAfterTerminatedEvent(t *testing.T) {
	w, drain := captureStdout(t)
	defer drain()

	sess, err := wrapper.Start(context.Background(), wrapper.Config{
		BinaryPath: mockHarnessBin,
		Args:       []string{"--mode", "completed", "--steps", "1", "--delay", "1ms"},
		Stdout:     w,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Drain events; the last one before the channel closes must have
	// Terminated=true and a populated Status.
	var last wrapper.SessionEvent
	var seen int
	for ev := range sess.Events() {
		last = ev
		seen++
	}
	if seen == 0 {
		t.Fatal("Events delivered no events; want at least one (terminated)")
	}
	if !last.Terminated {
		t.Errorf("last event not marked Terminated: %+v", last)
	}
	if last.Status == "" {
		t.Errorf("terminated event Status is empty")
	}

	if _, err := sess.Wait(); err != nil {
		t.Fatalf("Wait after Events drained: %v", err)
	}
}

func TestSession_StopRequestsGracefulTermination(t *testing.T) {
	w, drain := captureStdout(t)
	defer drain()

	sess, err := wrapper.Start(context.Background(), wrapper.Config{
		BinaryPath: mockHarnessBin,
		Args:       []string{"--mode", "stuck"},
		Stdout:     w,
		WaitDelay:  500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Give the harness a moment to print its first line so the test
	// exercises termination of an actively-running process, not a
	// just-spawned one.
	time.Sleep(100 * time.Millisecond)

	stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := sess.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait after Stop: %v", err)
	}
	if res.Status != wrapper.StatusInterrupted {
		t.Errorf("Status = %q after Stop, want StatusInterrupted", res.Status)
	}
}

func TestSession_StopIsIdempotent(t *testing.T) {
	w, drain := captureStdout(t)
	defer drain()

	sess, err := wrapper.Start(context.Background(), wrapper.Config{
		BinaryPath: mockHarnessBin,
		Args:       []string{"--mode", "stuck"},
		Stdout:     w,
		WaitDelay:  500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := sess.Stop(ctx); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	if err := sess.Stop(ctx); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	if _, err := sess.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

func TestSession_FailedHarnessReportsFailedStatus(t *testing.T) {
	w, drain := captureStdout(t)
	defer drain()

	sess, err := wrapper.Start(context.Background(), wrapper.Config{
		BinaryPath: mockHarnessBin,
		Args:       []string{"--mode", "failed", "--exit-code", "7"},
		Stdout:     w,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res.Status != wrapper.StatusFailed {
		t.Errorf("Status = %q, want StatusFailed", res.Status)
	}
	if res.ExitCode != 7 {
		t.Errorf("ExitCode = %d, want 7", res.ExitCode)
	}
}

func TestSession_CostLimitedClassifierEscalates(t *testing.T) {
	w, drain := captureStdout(t)
	defer drain()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	sess, err := wrapper.Start(ctx, wrapper.Config{
		BinaryPath:   mockHarnessBin,
		Args:         []string{"--mode", "needs-input", "--prompt", "You've hit your limit - resets 5pm. What now? "},
		Stdout:       w,
		IdleQuiet:    50 * time.Millisecond,
		IdleClassify: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res.Status != wrapper.StatusBlockedByCost {
		t.Errorf("Status = %q, want StatusBlockedByCost", res.Status)
	}
}

func TestSession_WaitingForInputEmittedMidRun(t *testing.T) {
	w, drain := captureStdout(t)
	defer drain()

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	t.Cleanup(func() { _ = stdinR.Close() })

	// Hold stdin so the harness pauses at the prompt long enough for the
	// classifier to fire. After we've seen waiting_for_input, send "y".
	go func() {
		time.Sleep(400 * time.Millisecond)
		_, _ = stdinW.Write([]byte("y\n"))
		_ = stdinW.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	sess, err := wrapper.Start(ctx, wrapper.Config{
		BinaryPath:   mockHarnessBin,
		Args:         []string{"--mode", "needs-input", "--prompt", "Continue? ", "--expected-input", "y"},
		Stdin:        stdinR,
		Stdout:       w,
		Harness:      "claude",
		IdleQuiet:    50 * time.Millisecond,
		IdleClassify: 1 * time.Second,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	var sawWaiting bool
	var last wrapper.SessionEvent
	for ev := range sess.Events() {
		if ev.Status == wrapper.StatusWaitingForInput && !ev.Terminated {
			sawWaiting = true
		}
		last = ev
	}
	if !sawWaiting {
		t.Errorf("expected a mid-run waiting_for_input event; final event was %+v", last)
	}

	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	// Once the test pumped "y" the harness completes cleanly; the final
	// terminal status should be idle (mock exits 0 on accepted input).
	if res.Status != wrapper.StatusIdle {
		t.Errorf("Status after input = %q, want StatusIdle", res.Status)
	}
}

func TestSession_StartReturnsErrorOnMissingBinary(t *testing.T) {
	w, drain := captureStdout(t)
	defer drain()

	_, err := wrapper.Start(context.Background(), wrapper.Config{
		BinaryPath: "/no/such/bin/harness-wrapper-test-missing",
		Stdout:     w,
	})
	if !errors.Is(err, wrapper.ErrBinaryNotFound) {
		t.Errorf("err = %v, want ErrBinaryNotFound", err)
	}
}

func TestSession_StartValidatesConfig(t *testing.T) {
	_, err := wrapper.Start(context.Background(), wrapper.Config{})
	if !errors.Is(err, wrapper.ErrInvalidConfig) {
		t.Errorf("err = %v, want ErrInvalidConfig", err)
	}
}

func TestSession_CustomClassifierWins(t *testing.T) {
	w, drain := captureStdout(t)
	defer drain()

	called := make(chan struct{}, 1)
	classifier := wrapper.ClassifierFunc(func(input wrapper.ClassifierInput) wrapper.Classification {
		select {
		case called <- struct{}{}:
		default:
		}
		if !strings.Contains(input.RecentOutput, "Step") {
			return wrapper.Classification{}
		}
		// Don't actually classify; we just want to verify the function is
		// being invoked. Returning the zero Classification falls through
		// to default behavior (idle on clean exit).
		return wrapper.Classification{}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	sess, err := wrapper.Start(ctx, wrapper.Config{
		BinaryPath:   mockHarnessBin,
		Args:         []string{"--mode", "stuck"},
		Stdout:       w,
		Classifier:   classifier,
		IdleQuiet:    50 * time.Millisecond,
		IdleClassify: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Hit the classifier path then stop the run.
	time.Sleep(300 * time.Millisecond)
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopCancel()
	_ = sess.Stop(stopCtx)
	_, _ = sess.Wait()

	select {
	case <-called:
	default:
		t.Errorf("custom Classifier was never invoked")
	}
}

// TestSession_StaleEmitsNonTerminalEvent verifies the W3 contract:
// when the harness has been quiet longer than cfg.StaleThreshold, the
// wrapper emits a non-terminal StatusStale SessionEvent and a
// harness_stale trace event, without terminating the run. Final
// Result.Status is whatever the exit yields, not StatusStale.
func TestSession_StaleEmitsNonTerminalEvent(t *testing.T) {
	w, drain := captureStdout(t)
	defer drain()

	emitter := &recordingEmitter{}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sess, err := wrapper.Start(ctx, wrapper.Config{
		BinaryPath:     mockHarnessBin,
		Args:           []string{"--mode", "stuck"},
		Stdout:         w,
		Trace:          emitter,
		IdleQuiet:      40 * time.Millisecond,
		IdleClassify:   100 * time.Millisecond,
		StaleThreshold: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Subscribe to events; record the first StatusStale we see.
	staleEvent := make(chan wrapper.SessionEvent, 1)
	go func() {
		for ev := range sess.Events() {
			if ev.Status == wrapper.StatusStale && !ev.Terminated {
				select {
				case staleEvent <- ev:
				default:
				}
			}
		}
	}()

	select {
	case ev := <-staleEvent:
		if ev.Reason == "" {
			t.Errorf("StatusStale event has empty Reason; want a since-last-output description")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no StatusStale SessionEvent fired within timeout")
	}

	// Confirm the trace also recorded harness_stale.
	if !slices.Contains(emitter.Kinds(), "harness_stale") {
		t.Errorf("trace events missing harness_stale; got %v", emitter.Kinds())
	}

	// Tear down; the run must NOT have terminated due to staleness.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopCancel()
	_ = sess.Stop(stopCtx)
	res, _ := sess.Wait()
	if res.Status == wrapper.StatusStale {
		t.Errorf("Result.Status = StatusStale; want a terminal status (StatusInterrupted via Stop)")
	}
}

// TestSession_RecentOutputReflectsObservedBytes verifies the W2 contract:
// Session.RecentOutput returns the bytes the wrapper has buffered, mid-run
// and after termination. We use a custom Classifier as a synchronization
// point: by the time it observes any input, recentOutput is already
// populated.
func TestSession_RecentOutputReflectsObservedBytes(t *testing.T) {
	w, drain := captureStdout(t)
	defer drain()

	classifierSaw := make(chan string, 1)
	classifier := wrapper.ClassifierFunc(func(input wrapper.ClassifierInput) wrapper.Classification {
		select {
		case classifierSaw <- input.RecentOutput:
		default:
		}
		return wrapper.Classification{}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	sess, err := wrapper.Start(ctx, wrapper.Config{
		BinaryPath:   mockHarnessBin,
		Args:         []string{"--mode", "stuck"},
		Stdout:       w,
		Classifier:   classifier,
		IdleQuiet:    50 * time.Millisecond,
		IdleClassify: 150 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer stopCancel()
		_ = sess.Stop(stopCtx)
		_, _ = sess.Wait()
	}()

	select {
	case classifierInput := <-classifierSaw:
		recent := sess.RecentOutput()
		if recent == "" {
			t.Errorf("Session.RecentOutput() = \"\"; expected non-empty mid-run")
		}
		// Both views should agree that some baseline content was observed.
		// They need not be byte-identical (recentOutput keeps growing while
		// the classifier sees a snapshot), but both should reflect the mock
		// harness's startup banner.
		if !strings.Contains(recent, "Mock Agent CLI") {
			t.Errorf("RecentOutput missing mock harness banner; got %q", recent)
		}
		if !strings.Contains(classifierInput, "Mock Agent CLI") {
			t.Errorf("Classifier RecentOutput missing mock harness banner; got %q", classifierInput)
		}
	case <-ctx.Done():
		t.Fatal("classifier never invoked within timeout")
	}
}
