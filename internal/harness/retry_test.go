package harness

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/wrapper"
)

// installFakes swaps runOnce and sleepFn with the provided fakes for
// the duration of the test and restores the originals on cleanup.
func installFakes(t *testing.T, run runOnceFn, sleep sleepFnT) {
	t.Helper()
	origRun, origSleep := runOnce, sleepFn
	runOnce = run
	sleepFn = sleep
	t.Cleanup(func() {
		runOnce = origRun
		sleepFn = origSleep
	})
}

// scriptStep is one entry in a scripted runOnceFn replay.
type scriptStep struct {
	res attemptResult
	err error
}

// scriptedRun returns a runOnceFn that replays a fixed sequence of
// attemptResult/err pairs and panics if the caller drains past the end.
func scriptedRun(t *testing.T, script ...scriptStep) (runOnceFn, *int) {
	t.Helper()
	var calls int
	return func(ctx context.Context, cfg wrapper.Config, _ RetryPolicy) (attemptResult, error) {
		if calls >= len(script) {
			t.Fatalf("runOnce called %d times, script only has %d entries", calls+1, len(script))
		}
		step := script[calls]
		calls++
		return step.res, step.err
	}, &calls
}

// recordingSleep accumulates the durations RunWithRetry asks to sleep
// for, returning ctxCancel as the last error if cancelOnAttempt > 0.
func recordingSleep(cancel context.CancelFunc, cancelOnAttempt int) (sleepFnT, *[]time.Duration) {
	var slept []time.Duration
	return func(ctx context.Context, d time.Duration) error {
		slept = append(slept, d)
		if cancelOnAttempt > 0 && len(slept) == cancelOnAttempt {
			cancel()
			return context.Canceled
		}
		return nil
	}, &slept
}

func TestRunWithRetry_Table(t *testing.T) {
	tests := []struct {
		name        string
		script      []scriptStep
		policy      RetryPolicy
		wantStatus  wrapper.Status
		wantCalls   int
		wantSleeps  []time.Duration
		wantErrIs   error
		cancelAfter int // sleeps; 0 means no cancellation
	}{
		{
			name: "success_first_try",
			script: []scriptStep{
				{res: attemptResult{Result: wrapper.Result{Status: wrapper.StatusIdle}}},
			},
			policy:     RetryPolicy{Max: 3, BaseBackoff: time.Second, MaxBackoff: 10 * time.Second},
			wantStatus: wrapper.StatusIdle,
			wantCalls:  1,
			wantSleeps: nil,
		},
		{
			name: "retry_then_success",
			script: []scriptStep{
				{res: attemptResult{Result: wrapper.Result{Status: wrapper.StatusAPIError}}},
				{res: attemptResult{Result: wrapper.Result{Status: wrapper.StatusIdle}}},
			},
			policy:     RetryPolicy{Max: 3, BaseBackoff: time.Second, MaxBackoff: 10 * time.Second},
			wantStatus: wrapper.StatusIdle,
			wantCalls:  2,
			wantSleeps: []time.Duration{1 * time.Second},
		},
		{
			name: "exhausted_retries",
			script: []scriptStep{
				{res: attemptResult{Result: wrapper.Result{Status: wrapper.StatusRetryLater}}},
				{res: attemptResult{Result: wrapper.Result{Status: wrapper.StatusRetryLater}}},
				{res: attemptResult{Result: wrapper.Result{Status: wrapper.StatusRetryLater}}},
				{res: attemptResult{Result: wrapper.Result{Status: wrapper.StatusRetryLater}}},
			},
			policy:     RetryPolicy{Max: 3, BaseBackoff: time.Second, MaxBackoff: 10 * time.Second},
			wantStatus: wrapper.StatusRetryLater,
			wantCalls:  4,
			wantSleeps: []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second},
		},
		{
			name: "respects_retry_after",
			script: []scriptStep{
				{res: attemptResult{Result: wrapper.Result{Status: wrapper.StatusAPIError}, RetryAfter: 7 * time.Second}},
				{res: attemptResult{Result: wrapper.Result{Status: wrapper.StatusIdle}}},
			},
			policy:     RetryPolicy{Max: 3, BaseBackoff: time.Second, MaxBackoff: 60 * time.Second},
			wantStatus: wrapper.StatusIdle,
			wantCalls:  2,
			wantSleeps: []time.Duration{7 * time.Second},
		},
		{
			name: "retry_after_capped_by_max",
			script: []scriptStep{
				{res: attemptResult{Result: wrapper.Result{Status: wrapper.StatusAPIError}, RetryAfter: 999 * time.Second}},
				{res: attemptResult{Result: wrapper.Result{Status: wrapper.StatusIdle}}},
			},
			policy:     RetryPolicy{Max: 3, BaseBackoff: time.Second, MaxBackoff: 30 * time.Second},
			wantStatus: wrapper.StatusIdle,
			wantCalls:  2,
			wantSleeps: []time.Duration{30 * time.Second},
		},
		{
			name: "non_retryable_status",
			script: []scriptStep{
				{res: attemptResult{Result: wrapper.Result{Status: wrapper.StatusBlockedByCost}}},
			},
			policy:     RetryPolicy{Max: 3, BaseBackoff: time.Second, MaxBackoff: 10 * time.Second},
			wantStatus: wrapper.StatusBlockedByCost,
			wantCalls:  1,
			wantSleeps: nil,
		},
		{
			name: "failed_status_not_retried",
			script: []scriptStep{
				{res: attemptResult{Result: wrapper.Result{Status: wrapper.StatusFailed, ExitCode: 2}}},
			},
			policy:     RetryPolicy{Max: 3, BaseBackoff: time.Second, MaxBackoff: 10 * time.Second},
			wantStatus: wrapper.StatusFailed,
			wantCalls:  1,
			wantSleeps: nil,
		},
		{
			name: "failed_status_with_api_error_signal_retries",
			script: []scriptStep{
				{res: attemptResult{Result: wrapper.Result{Status: wrapper.StatusFailed, ExitCode: 1}, SawAPIError: true, RetryAfter: 0}},
				{res: attemptResult{Result: wrapper.Result{Status: wrapper.StatusIdle}}},
			},
			policy:     RetryPolicy{Max: 3, BaseBackoff: time.Second, MaxBackoff: 10 * time.Second},
			wantStatus: wrapper.StatusIdle,
			wantCalls:  2,
			wantSleeps: []time.Duration{1 * time.Second},
		},
		{
			name: "wrapper_level_error_no_retry",
			script: []scriptStep{
				{res: attemptResult{Result: wrapper.Result{ExitCode: -1}}, err: wrapper.ErrPTYAllocation},
			},
			policy:     RetryPolicy{Max: 3, BaseBackoff: time.Second, MaxBackoff: 10 * time.Second},
			wantStatus: "",
			wantCalls:  1,
			wantSleeps: nil,
			wantErrIs:  wrapper.ErrPTYAllocation,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			run, calls := scriptedRun(t, tc.script...)
			var slept []time.Duration
			sleep := sleepFnT(func(ctx context.Context, d time.Duration) error {
				slept = append(slept, d)
				return nil
			})
			installFakes(t, run, sleep)

			res, err := RunWithRetry(context.Background(), wrapper.Config{}, tc.policy)
			if tc.wantErrIs != nil {
				if !errors.Is(err, tc.wantErrIs) {
					t.Fatalf("err: got %v, want errors.Is %v", err, tc.wantErrIs)
				}
			} else if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if res.Status != tc.wantStatus {
				t.Errorf("status: got %q, want %q", res.Status, tc.wantStatus)
			}
			if *calls != tc.wantCalls {
				t.Errorf("calls: got %d, want %d", *calls, tc.wantCalls)
			}
			if !equalDurations(slept, tc.wantSleeps) {
				t.Errorf("sleeps: got %v, want %v", slept, tc.wantSleeps)
			}
		})
	}
}

func TestRunWithRetry_ContextCancelDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	run, calls := scriptedRun(t,
		scriptStep{res: attemptResult{Result: wrapper.Result{Status: wrapper.StatusRetryLater}}},
	)
	sleep, slept := recordingSleep(cancel, 1)
	installFakes(t, run, sleep)

	res, err := RunWithRetry(ctx, wrapper.Config{}, RetryPolicy{Max: 3, BaseBackoff: time.Second})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err: got %v, want context.Canceled", err)
	}
	if res.Status != wrapper.StatusRetryLater {
		t.Errorf("status: got %q, want %q", res.Status, wrapper.StatusRetryLater)
	}
	if *calls != 1 {
		t.Errorf("calls: got %d, want 1 (second attempt should be cancelled)", *calls)
	}
	if got, want := len(*slept), 1; got != want {
		t.Errorf("sleep calls: got %d, want %d", got, want)
	}
}

func TestRunWithRetry_ZeroPolicyUsesDefaults(t *testing.T) {
	run, _ := scriptedRun(t,
		scriptStep{res: attemptResult{Result: wrapper.Result{Status: wrapper.StatusIdle}}},
	)
	installFakes(t, run, func(ctx context.Context, d time.Duration) error { return nil })

	if _, err := RunWithRetry(context.Background(), wrapper.Config{}, RetryPolicy{}); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// Default policy is just exercised via the call; the concrete
	// values are verified by TestDefaultRetryPolicy.
}

func TestDefaultRetryPolicy(t *testing.T) {
	p := DefaultRetryPolicy()
	if p.Max != 3 {
		t.Errorf("Max: got %d, want 3", p.Max)
	}
	if p.BaseBackoff != 2*time.Second {
		t.Errorf("BaseBackoff: got %v, want 2s", p.BaseBackoff)
	}
	if p.MaxBackoff != 60*time.Second {
		t.Errorf("MaxBackoff: got %v, want 60s", p.MaxBackoff)
	}
}

func TestBackoffFor(t *testing.T) {
	p := RetryPolicy{BaseBackoff: time.Second, MaxBackoff: 10 * time.Second}
	tests := []struct {
		name    string
		attempt int
		hint    time.Duration
		want    time.Duration
	}{
		{"exp_attempt0", 0, 0, 1 * time.Second},
		{"exp_attempt1", 1, 0, 2 * time.Second},
		{"exp_attempt2", 2, 0, 4 * time.Second},
		{"exp_capped_by_max", 5, 0, 10 * time.Second},
		{"hint_overrides_exp", 1, 3 * time.Second, 3 * time.Second},
		{"hint_capped_by_max", 0, 999 * time.Second, 10 * time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := backoffFor(p, tc.attempt, tc.hint)
			if got != tc.want {
				t.Errorf("backoffFor(attempt=%d, hint=%v) = %v, want %v", tc.attempt, tc.hint, got, tc.want)
			}
		})
	}
}

func TestShouldRetry(t *testing.T) {
	type input struct {
		status      wrapper.Status
		sawAPIError bool
	}
	cases := map[input]bool{
		// Without API-error signal in events:
		{wrapper.StatusIdle, false}:            false,
		{wrapper.StatusFailed, false}:          false,
		{wrapper.StatusBlockedByCost, false}:   false,
		{wrapper.StatusInterrupted, false}:     false,
		{wrapper.StatusUnknown, false}:         false,
		{wrapper.StatusWaitingForInput, false}: false,
		{wrapper.StatusStale, false}:           false,
		{wrapper.StatusRetryLater, false}:      true,
		{wrapper.StatusAPIError, false}:        true,
		// API-error signal observed mid-run: failed/unknown become retryable.
		{wrapper.StatusFailed, true}:  true,
		{wrapper.StatusUnknown, true}: true,
		// Other statuses still don't retry even with api-error signal —
		// e.g. blocked_by_cost is a definitive non-retry verdict.
		{wrapper.StatusBlockedByCost, true}: false,
		{wrapper.StatusInterrupted, true}:   false,
		{wrapper.StatusIdle, true}:          false,
	}
	for in, want := range cases {
		if got := shouldRetry(in.status, in.sawAPIError); got != want {
			t.Errorf("shouldRetry(%q, %v) = %v, want %v", in.status, in.sawAPIError, got, want)
		}
	}
}

func equalDurations(a, b []time.Duration) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSleepDefault_ZeroAndNegativeReturnImmediately(t *testing.T) {
	ctx := context.Background()
	start := time.Now()
	if err := sleepDefault(ctx, 0); err != nil {
		t.Errorf("d=0: got err %v, want nil", err)
	}
	if err := sleepDefault(ctx, -5*time.Second); err != nil {
		t.Errorf("d<0: got err %v, want nil", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Millisecond {
		t.Errorf("returned in %v, want immediate", elapsed)
	}
}

func TestSleepDefault_HonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sleepDefault(ctx, 10*time.Second) }()
	// Give the goroutine a moment to enter the select.
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("got err %v, want context.Canceled", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("sleepDefault did not return after context cancel")
	}
}

func TestSleepDefault_FiresTimerOnShortSleep(t *testing.T) {
	ctx := context.Background()
	start := time.Now()
	if err := sleepDefault(ctx, 30*time.Millisecond); err != nil {
		t.Errorf("got err %v, want nil", err)
	}
	if elapsed := time.Since(start); elapsed < 25*time.Millisecond {
		t.Errorf("returned in %v, want at least 25ms", elapsed)
	}
}

func TestRunOnceDefault_BinaryNotFoundReturnsWrapperError(t *testing.T) {
	out, err := runOnceDefault(context.Background(), wrapper.Config{
		BinaryPath: "/path/that/does/not/exist/xyz123",
		Stdout:     io.Discard,
	}, RetryPolicy{})
	if err == nil {
		t.Fatal("got nil, want wrapper-level error for missing binary")
	}
	if out.Result.ExitCode != -1 {
		t.Errorf("ExitCode: got %d, want -1", out.Result.ExitCode)
	}
}

func TestRunOnceDefault_HappyPathReachesIdle(t *testing.T) {
	// /bin/true (or /usr/bin/true) exits cleanly and produces no
	// output. Use the absolute path to avoid PATH lookup variance
	// across hosts.
	binaries := []string{"/usr/bin/true", "/bin/true"}
	var bin string
	for _, candidate := range binaries {
		if _, err := os.Stat(candidate); err == nil {
			bin = candidate
			break
		}
	}
	if bin == "" {
		t.Skip("no /usr/bin/true or /bin/true on this host")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := runOnceDefault(ctx, wrapper.Config{
		BinaryPath: bin,
		Stdout:     io.Discard,
		// Tighter thresholds so the classifier picks up the clean
		// exit quickly.
		IdleQuiet:    100 * time.Millisecond,
		IdleClassify: 300 * time.Millisecond,
	}, RetryPolicy{})
	if err != nil {
		t.Fatalf("runOnceDefault err: %v", err)
	}
	if out.Result.Status != wrapper.StatusIdle {
		t.Errorf("status: got %q, want %q", out.Result.Status, wrapper.StatusIdle)
	}
	if out.SawAPIError {
		t.Error("SawAPIError: got true, want false for clean exit")
	}
}
