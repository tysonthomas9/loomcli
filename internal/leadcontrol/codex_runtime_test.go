package leadcontrol

import (
	"errors"
	"os"
	"path/filepath"
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

func TestCodexLeadRuntimeDirsSQLiteHomeIsStablePerLead(t *testing.T) {
	t.Parallel()

	runtimeDir := t.TempDir()
	base := CodexLeadRuntimeConfig{Workspace: "PUPPET", LeadName: "Lead One", RuntimeDir: runtimeDir}

	first := base
	first.SessionID = "session-aaa"
	secondCfg := base
	secondCfg.SessionID = "session-bbb"

	runtimeA, sqliteA := codexLeadRuntimeDirs(normalizeCodexLeadRuntimeConfig(first))
	runtimeB, sqliteB := codexLeadRuntimeDirs(normalizeCodexLeadRuntimeConfig(secondCfg))

	if sqliteA != sqliteB {
		t.Fatalf("sqlite home is per session: %q vs %q", sqliteA, sqliteB)
	}
	wantSQLite := filepath.Join(runtimeDir, ".loom", "lead-sessions", "puppet", "lead-one", "sqlite")
	if sqliteA != wantSQLite {
		t.Fatalf("sqlite home = %q, want %q", sqliteA, wantSQLite)
	}
	if runtimeA == runtimeB {
		t.Fatalf("runtime home is shared across sessions: %q", runtimeA)
	}
	if want := filepath.Join(runtimeDir, ".loom", "lead-sessions", "puppet", "lead-one", "runs", "session-aaa"); runtimeA != want {
		t.Fatalf("runtime home = %q, want %q", runtimeA, want)
	}
}

func TestCodexLeadRuntimeDirsStayOutOfTheOSCacheDir(t *testing.T) {
	t.Parallel()

	cacheDir, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(cacheDir) == "" {
		t.Skip("no user cache dir on this host")
	}
	runtimeDir := t.TempDir()
	cfg := normalizeCodexLeadRuntimeConfig(CodexLeadRuntimeConfig{
		Workspace:  "PUPPET",
		LeadName:   "lead-one",
		SessionID:  "session-aaa",
		RuntimeDir: runtimeDir,
	})

	runtimeHome, sqliteHome := codexLeadRuntimeDirs(cfg)
	for _, got := range []string{runtimeHome, sqliteHome} {
		rel, err := filepath.Rel(cacheDir, got)
		if err == nil && !strings.HasPrefix(rel, "..") {
			t.Fatalf("%q is under the OS cache dir %q", got, cacheDir)
		}
	}
}

func TestCodexLeadRuntimeDirsPreferInjectedRuntimeDirOverEnvAndTempFallback(t *testing.T) {
	envDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", envDir)

	// An explicit RuntimeDir wins over the environment.
	injected := t.TempDir()
	cfg := normalizeCodexLeadRuntimeConfig(CodexLeadRuntimeConfig{
		Workspace:  "PUPPET",
		LeadName:   "lead-one",
		SessionID:  "session-aaa",
		RuntimeDir: injected,
	})
	if _, sqliteHome := codexLeadRuntimeDirs(cfg); !strings.HasPrefix(sqliteHome, injected) {
		t.Fatalf("sqlite home = %q, want it under the injected runtime dir %q", sqliteHome, injected)
	}

	// With no explicit RuntimeDir the environment wins over os.TempDir().
	cfg = normalizeCodexLeadRuntimeConfig(CodexLeadRuntimeConfig{
		Workspace: "PUPPET",
		LeadName:  "lead-one",
		SessionID: "session-aaa",
	})
	_, sqliteHome := codexLeadRuntimeDirs(cfg)
	if !strings.HasPrefix(sqliteHome, envDir) {
		t.Fatalf("sqlite home = %q, want it under LOOM_WORKSPACE_RUNTIME_DIR %q", sqliteHome, envDir)
	}

	// Only with neither does it fall back to the temp dir.
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", "")
	cfg = normalizeCodexLeadRuntimeConfig(CodexLeadRuntimeConfig{
		Workspace: "PUPPET",
		LeadName:  "lead-one",
		SessionID: "session-aaa",
	})
	if _, sqliteHome := codexLeadRuntimeDirs(cfg); !strings.HasPrefix(sqliteHome, os.TempDir()) {
		t.Fatalf("sqlite home = %q, want the os.TempDir() fallback %q", sqliteHome, os.TempDir())
	}
}

func TestLegacyCodexLeadCacheRootIsPerLead(t *testing.T) {
	t.Parallel()

	cacheDir, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(cacheDir) == "" {
		t.Skip("no user cache dir on this host")
	}
	cfg := normalizeCodexLeadRuntimeConfig(CodexLeadRuntimeConfig{
		Workspace: "PUPPET",
		LeadName:  "Lead One",
		SessionID: "session-aaa",
	})
	want := filepath.Join(cacheDir, "loom", "codex-leads", "puppet", "lead-one")
	if got := legacyCodexLeadCacheRoot(cfg); got != want {
		t.Fatalf("legacyCodexLeadCacheRoot() = %q, want %q", got, want)
	}
}
