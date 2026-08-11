package leadcontrol

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPrepareCodexLeadHomeCopiesStaticProfileWithoutHistoricalState(t *testing.T) {
	t.Parallel()

	sourceHome := t.TempDir()
	for name, body := range map[string]string{
		"config.toml": "model = \"gpt-5.6\"\n",
		"auth.json":   `{"tokens":{"access_token":"test-only"}}`,
		"AGENTS.md":   "lead instructions\n",
	} {
		if err := os.WriteFile(filepath.Join(sourceHome, name), []byte(body), 0600); err != nil {
			t.Fatalf("write source %s: %v", name, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(sourceHome, "sessions"), 0700); err != nil {
		t.Fatalf("create historical state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceHome, "sessions", "old.jsonl"), []byte("old"), 0600); err != nil {
		t.Fatalf("write historical state: %v", err)
	}

	runtimeHome := t.TempDir()
	leadHome := filepath.Join(runtimeHome, "codex-home")
	if err := prepareCodexLeadHome(sourceHome, leadHome); err != nil {
		t.Fatalf("prepareCodexLeadHome() error: %v", err)
	}

	for _, name := range []string{"config.toml", "auth.json", "AGENTS.md"} {
		if _, err := os.Stat(filepath.Join(leadHome, name)); err != nil {
			t.Fatalf("static profile file %s missing: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(leadHome, "sessions")); !os.IsNotExist(err) {
		t.Fatalf("historical sessions copied into lead home, stat error = %v", err)
	}
}

func TestWithCodexHomeReplacesInheritedValue(t *testing.T) {
	t.Parallel()

	env := withCodexHome([]string{"PATH=/bin", "CODEX_HOME=/old", "OTHER=value"}, "/lead")
	if got := envValue(env, "CODEX_HOME"); got != "/lead" {
		t.Fatalf("CODEX_HOME = %q, want /lead", got)
	}
	if got := envValue(env, "OTHER"); got != "value" {
		t.Fatalf("OTHER = %q, want value", got)
	}
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}

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
