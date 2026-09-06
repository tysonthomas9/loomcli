package leadcontrol

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNewestCodexThreadWaitsForThreadCreatedAfterRuntimeStart(t *testing.T) {
	startedAt := time.Date(2026, 5, 17, 6, 5, 36, 0, time.UTC)
	threads := []CodexThread{
		{
			ID:          "old-lead-thread",
			Cwd:         "/repo",
			CreatedAtMS: float64(startedAt.Add(-3 * time.Minute).UnixMilli()),
			UpdatedAtMS: float64(startedAt.Add(-1 * time.Second).UnixMilli()),
		},
		{
			ID:          "new-lead-thread",
			Cwd:         "/repo",
			CreatedAtMS: float64(startedAt.Add(500 * time.Millisecond).UnixMilli()),
			UpdatedAtMS: float64(startedAt.Add(2 * time.Second).UnixMilli()),
		},
	}

	got := newestCodexThread(threads, "/repo", startedAt)
	if got == nil || got.ID != "new-lead-thread" {
		t.Fatalf("newestCodexThread() = %+v, want new-lead-thread", got)
	}
}

func TestNewestCodexThreadReturnsNilUntilFreshLeadThreadExists(t *testing.T) {
	startedAt := time.Date(2026, 5, 17, 6, 5, 36, 0, time.UTC)
	threads := []CodexThread{{
		ID:          "old-lead-thread",
		Cwd:         "/repo",
		CreatedAtMS: float64(startedAt.Add(-3 * time.Minute).UnixMilli()),
		UpdatedAtMS: float64(startedAt.Add(5 * time.Second).UnixMilli()),
	}}

	got := newestCodexThread(threads, "/repo", startedAt)
	if got != nil {
		t.Fatalf("newestCodexThread() = %+v, want nil before fresh lead thread exists", got)
	}
}

func TestCodexAppServerTimeoutErrorIncludesProbeAndLogTail(t *testing.T) {
	t.Parallel()

	logPath := filepath.Join(t.TempDir(), "app-server.log")
	logBody := strings.Repeat("x", int(codexAppServerLogTailBytes)+32) + "\nstartup detail\n"
	if err := os.WriteFile(logPath, []byte(logBody), 0600); err != nil {
		t.Fatalf("write app-server log: %v", err)
	}

	err := codexAppServerTimeoutError(
		"ws://127.0.0.1:62085",
		5*time.Second,
		errors.New("connection refused"),
		logPath,
	)
	got := err.Error()
	for _, want := range []string{
		"codex app-server did not become ready at ws://127.0.0.1:62085 within 5s",
		"last readiness probe: connection refused",
		"app-server log tail:",
		"startup detail",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("timeout error missing %q:\n%s", want, got)
		}
	}
}

func TestCodexAppServerTimeoutErrorOmitsMissingLogTail(t *testing.T) {
	t.Parallel()

	err := codexAppServerTimeoutError(
		"ws://127.0.0.1:62085",
		5*time.Second,
		nil,
		filepath.Join(t.TempDir(), "missing.log"),
	)
	got := err.Error()
	if strings.Contains(got, "app-server log tail:") {
		t.Fatalf("timeout error included missing log tail:\n%s", got)
	}
	if strings.Contains(got, "last readiness probe:") {
		t.Fatalf("timeout error included missing probe error:\n%s", got)
	}
}

// The model pin reaches the app-server as a `-c` config overlay, and an empty
// pin adds no argument at all — an unprofiled lead must launch exactly as it
// did before.
func TestCodexAppServerArgsModelPin(t *testing.T) {
	base := []string{"app-server", "--listen", "ws://127.0.0.1:9", "-c", `sqlite_home="/tmp/sq"`}

	got := codexAppServerArgs("ws://127.0.0.1:9", "/tmp/sq", "gpt-5.6-sol")
	want := append(append([]string{}, base...), "-c", `model="gpt-5.6-sol"`)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("codexAppServerArgs(pin) = %#v, want %#v", got, want)
	}

	for _, empty := range []string{"", "   "} {
		if got := codexAppServerArgs("ws://127.0.0.1:9", "/tmp/sq", empty); !reflect.DeepEqual(got, base) {
			t.Fatalf("codexAppServerArgs(%q) = %#v, want %#v", empty, got, base)
		}
	}
}
