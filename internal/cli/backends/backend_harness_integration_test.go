package backends

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	hwharness "github.com/olesho/harness-wrapper/pkg/harness"
	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/harness"
)

// buildFakeHarness compiles the in-tree fake-harness mock binary and
// returns its absolute path. The binary lives at
// internal/harness/fakeharness/mock and supports several scripted
// behaviors (completed, failed, stuck, cost-limited, api-error) that
// the wrapper classifier knows how to recognize.
func buildFakeHarness(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fakeharness")
	cmd := exec.Command("go", "build", "-o", bin, "github.com/tysonthomas9/loomcli/internal/harness/fakeharness/mock")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake harness: %v\n%s", err, out)
	}
	return bin
}

// runHarnessIntegrationEnv prepends dir (which contains a fakeharness
// binary symlinked under a chosen name) to PATH so exec.LookPath in
// runHarness resolves it.
func setupFakeOnPath(t *testing.T, binarySource, alias string) string {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, alias)
	if err := os.Symlink(binarySource, target); err != nil {
		t.Fatalf("symlink %s -> %s: %v", target, binarySource, err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return target
}

func TestIntegration_RunHarness_CompletedMode(t *testing.T) {
	skipUnlessIntegration(t)
	bin := buildFakeHarness(t)
	setupFakeOnPath(t, bin, "fakeharness")

	var lines []string
	err := runHarness(context.Background(), nil, harnessInvocation{
		BinaryName:  "fakeharness",
		Args:        []string{"--mode", "completed", "--steps", "2", "--delay", "10ms"},
		WorkDir:     t.TempDir(),
		Prompt:      "",
		HarnessName: "",
		LineHandler: func(line string) { lines = append(lines, line) },
		RetryPolicy: harness.DefaultRetryPolicy(),
	})
	if err != nil {
		t.Fatalf("runHarness returned err: %v (want nil for clean exit)", err)
	}
	// "DONE" should appear in the captured output. PTY conversion may
	// surface lines with carriage returns; check substring.
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "DONE") {
		t.Errorf("captured output missing DONE marker:\n%s", joined)
	}
}

func TestIntegration_RunHarness_FailedMode(t *testing.T) {
	skipUnlessIntegration(t)
	bin := buildFakeHarness(t)
	setupFakeOnPath(t, bin, "fakeharness")

	err := runHarness(context.Background(), nil, harnessInvocation{
		BinaryName:  "fakeharness",
		Args:        []string{"--mode", "failed", "--exit-code", "7"},
		WorkDir:     t.TempDir(),
		HarnessName: "",
		LineHandler: func(string) {},
		RetryPolicy: harness.RetryPolicy{Max: 0, BaseBackoff: 1, MaxBackoff: 1},
	})
	if err == nil {
		t.Fatal("got nil, want non-nil error for failed mode")
	}
	var invErr *InvocationError
	if !errors.As(err, &invErr) {
		t.Fatalf("err: got %T (%v), want *InvocationError", err, err)
	}
	if invErr.ExitCode != 7 {
		t.Errorf("ExitCode: got %d, want 7", invErr.ExitCode)
	}
}

func TestIntegration_RunHarness_CostLimitedMode(t *testing.T) {
	skipUnlessIntegration(t)
	bin := buildFakeHarness(t)
	setupFakeOnPath(t, bin, "fakeharness")

	err := runHarness(context.Background(), nil, harnessInvocation{
		BinaryName:  "fakeharness",
		Args:        []string{"--mode", "cost-limited", "--exit-code", "1"},
		WorkDir:     t.TempDir(),
		HarnessName: "", // generic classifier detects "quota exceeded"
		LineHandler: func(string) {},
		RetryPolicy: harness.RetryPolicy{Max: 0, BaseBackoff: 1, MaxBackoff: 1},
	})
	if err == nil {
		t.Fatal("got nil, want non-nil error for cost-limited mode")
	}
	var invErr *InvocationError
	if !errors.As(err, &invErr) {
		t.Fatalf("err: got %T (%v), want *InvocationError", err, err)
	}
	// The classifier should pick up "quota exceeded" and tag the run
	// as StatusBlockedByCost; the synthesized reason flows into the
	// OutputTail by wrapWrapperResult.
	tail := strings.ToLower(invErr.OutputTail)
	if !strings.Contains(tail, "quota") && !strings.Contains(tail, "blocked_by_cost") {
		t.Errorf("OutputTail %q missing 'quota' or 'blocked_by_cost' marker", invErr.OutputTail)
	}
}

// TestIntegration_RunWithRetry_RecoversAfterTransientFailures spawns a
// stateful shell-script fake that fails twice then succeeds; the
// harness.RunWithRetry layer must respawn the harness up to its Max
// retry budget and surface the final StatusIdle. The mock binary that
// ships with harness-wrapper doesn't carry attempt-counting state, so
// we hand-roll a tiny shell script that increments a counter file.
func TestIntegration_RunWithRetry_RecoversAfterTransientFailures(t *testing.T) {
	skipUnlessIntegration(t)

	dir := t.TempDir()
	counterFile := filepath.Join(dir, "attempts")
	// Sleep briefly after printing the API-error line so the wrapper's
	// classifier (poll cadence ~100ms) has a chance to scan the
	// recent-output buffer before the process exits. Without this
	// breather the script can finish before any classifier tick,
	// leaving the run as StatusFailed without an api-error signal.
	script := fmt.Sprintf(`#!/bin/sh
N=$(cat %q 2>/dev/null || echo 0)
echo $((N+1)) > %q
if [ "$N" -lt 2 ]; then
  echo "API Error: 529 Overloaded."
  sleep 0.3
  exit 1
fi
echo "DONE"
exit 0
`, counterFile, counterFile)
	bin := filepath.Join(dir, "fakeharness-retry")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake script: %v", err)
	}
	t.Setenv("PATH", filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	res, err := harness.RunWithRetry(ctx, hwharness.Config{Wrapper: wrapper.Config{
		BinaryPath: bin,
		Args:       nil,
		WorkingDir: dir,
		Stdout:     os.Stdout,
		// The claude classifier recognizes "API Error: 529 Overloaded."
		// as StatusAPIError; the retry layer pairs that signal with
		// the subsequent StatusFailed terminal status to decide a
		// retry is warranted.
		Harness:      "claude",
		IdleClassify: 500 * time.Millisecond,
		IdleQuiet:    200 * time.Millisecond,
	}}, harness.RetryPolicy{Max: 3, BaseBackoff: 100 * time.Millisecond, MaxBackoff: 500 * time.Millisecond})
	if err != nil {
		t.Fatalf("RunWithRetry returned err: %v", err)
	}

	attempts, readErr := os.ReadFile(counterFile)
	if readErr != nil {
		t.Fatalf("read attempts file: %v", readErr)
	}
	got := strings.TrimSpace(string(attempts))
	// 2 transient + 1 successful = 3 total spawns expected. Allow
	// 2-4 to absorb timing flakiness on slow CI: the classifier
	// runs on an internal poll cadence so it may occasionally miss
	// or double-fire on the first attempt's short window.
	switch got {
	case "2", "3", "4":
		// acceptable
	default:
		t.Errorf("attempts counter: got %s, want 2-4 (script fails twice then succeeds)", got)
	}
	if res.Status != wrapper.StatusIdle {
		t.Errorf("final status: got %q, want idle", res.Status)
	}
}
