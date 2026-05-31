//go:build realcli

package backends

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/sessionfinalize"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

const defaultRealCLIPrompt = "Reply with exactly: loom real CLI smoke ok. Do not inspect files, edit files, or run tools."

func TestRealCLISessionSmoke(t *testing.T) {
	unsetEnv(t, "GIT_DIR")
	unsetEnv(t, "GIT_WORK_TREE")
	unsetEnv(t, "GIT_INDEX_FILE")
	t.Setenv("LOOM_REDACT_TRANSCRIPTS", "off")
	if os.Getenv("LOOM_MAX_BUDGET_USD") == "" {
		t.Setenv("LOOM_MAX_BUDGET_USD", "0.50")
	}

	root := realCLIRoot(t)
	prompt := envOrDefault("LOOM_REAL_CLI_PROMPT", defaultRealCLIPrompt)
	timeout := realCLITimeout(t)
	backendsToRun := selectedRealCLIBackends()
	skipMissing := envBool("LOOM_REAL_CLI_SKIP_MISSING")
	requireCost := envBool("LOOM_REAL_CLI_REQUIRE_COST")

	t.Logf("real CLI smoke root: %s", root)
	t.Logf("real CLI smoke backends: %s", strings.Join(backendsToRun, ","))
	t.Logf("real CLI smoke timeout per backend: %s", timeout)

	ran := 0
	for _, backendName := range backendsToRun {
		backendName := backendName
		if _, err := exec.LookPath(backendName); err != nil {
			if skipMissing {
				t.Logf("%s: binary not found on PATH; skipping because LOOM_REAL_CLI_SKIP_MISSING is set", backendName)
				continue
			}
			t.Fatalf("%s binary not found on PATH", backendName)
		}
		ran++
		t.Run(backendName, func(t *testing.T) {
			runRealCLIBackendSmoke(t, root, backendName, prompt, timeout, requireCost)
		})
	}
	if ran == 0 {
		t.Fatal("no real CLI backends were run")
	}
}

func runRealCLIBackendSmoke(t *testing.T, root, backendName, prompt string, timeout time.Duration, requireCost bool) {
	t.Helper()

	workDir, beforeRef := prepareRealCLIWorktree(t, root, backendName)
	runtimeDir := filepath.Join(root, backendName, "runtime")
	store, err := sessions.NewStore(runtimeDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	agentName := "real-cli-" + backendName
	sess, err := store.CreateSession(sessions.CreateOptions{
		AgentName: agentName,
		Backend:   backendName,
		Phase:     "real-cli-smoke",
		Prompt:    prompt,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	SetActiveSessionRuntimeEnv(runtimeDir, sess.SessionID())
	defer ClearActiveSessionEnv()
	ClearLastCapturedSessionID()

	collector := usage.NewCollector(backendName, agentName)
	shutdown := make(chan struct{})
	timer := time.AfterFunc(timeout, func() {
		close(shutdown)
	})
	startedAt := time.Now()
	invokeErr := realCLIBackend(t, backendName).InvokeNonInteractive(workDir, prompt, agentName, shutdown, collector)
	_ = timer.Stop()
	endedAt := time.Now()

	exitCode := 0
	if invokeErr != nil {
		exitCode = 1
	}
	record := collector.Finalize("real-cli-smoke", "", startedAt, endedAt, exitCode)
	_, finalizeErr := sessionfinalize.WithWorktree(sess, sessionfinalize.WithWorktreeOptions{
		WorktreePath:     workDir,
		BeforeRef:        beforeRef,
		TaskID:           "real-cli-smoke",
		ExitCode:         exitCode,
		ClaudeSessionID:  GetLastCapturedSessionID(),
		InputTokens:      record.InputTokens,
		OutputTokens:     record.OutputTokens,
		CacheReadTokens:  record.CacheReadTokens,
		CacheWriteTokens: record.CacheWriteTokens,
		EstimatedCostUSD: record.EstimatedCostUSD,
		Model:            record.Model,
	})
	if finalizeErr != nil {
		t.Fatalf("finalize session: %v", finalizeErr)
	}

	meta, err := store.LoadMetadata(sess.SessionID())
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}
	nativePath := store.NativeTranscriptPath(sess.SessionID())
	nativeCaptured := fileExists(nativePath)
	t.Logf("%s session_id=%s metadata=%s native_transcript=%s native_captured=%t tokens(in=%d out=%d cache_read=%d cache_write=%d) cost_usd=%.6f model=%q",
		backendName,
		sess.SessionID(),
		filepath.Join(runtimeDir, "sessions", sess.SessionID(), "metadata.json"),
		nativePath,
		nativeCaptured,
		meta.InputTokens,
		meta.OutputTokens,
		meta.CacheReadTokens,
		meta.CacheWriteTokens,
		meta.EstimatedCostUSD,
		meta.Model,
	)

	if invokeErr != nil {
		t.Fatalf("%s invocation failed: %v", backendName, invokeErr)
	}
	if meta.Status != sessions.StatusCompleted {
		t.Fatalf("session status = %s, want %s", meta.Status, sessions.StatusCompleted)
	}
	if meta.InputTokens+meta.OutputTokens+meta.CacheReadTokens+meta.CacheWriteTokens == 0 {
		t.Errorf("%s did not report token usage into session metadata", backendName)
	}
	if backendName == "claude" || backendName == "codex" {
		if !nativeCaptured {
			t.Errorf("%s did not mirror a native transcript into the Loom session", backendName)
		}
	}
	if backendName == "claude" || requireCost {
		if meta.EstimatedCostUSD == 0 {
			t.Errorf("%s did not report backend/session cost into session metadata", backendName)
		}
	}
}

func realCLIBackend(t *testing.T, backendName string) cli.Backend {
	t.Helper()
	switch backendName {
	case "claude":
		return &ClaudeBackend{}
	case "codex":
		return &CodexBackend{}
	case "opencode":
		return &OpenCodeBackend{}
	default:
		t.Fatalf("unsupported real CLI backend %q", backendName)
		return nil
	}
}

func prepareRealCLIWorktree(t *testing.T, root, backendName string) (string, string) {
	t.Helper()
	workDir := filepath.Join(root, backendName, "repo")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	runGit(t, workDir, "init", "-q")
	runGit(t, workDir, "config", "user.email", "real-cli-smoke@example.test")
	runGit(t, workDir, "config", "user.name", "Real CLI Smoke")
	runGit(t, workDir, "commit", "--allow-empty", "-m", "seed", "-q")
	beforeRef := strings.TrimSpace(runGit(t, workDir, "rev-parse", "HEAD"))
	if beforeRef == "" {
		t.Fatal("empty HEAD ref after git seed commit")
	}
	return workDir, beforeRef
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	result := cli.DefaultDeps().Git.Run(dir, args...)
	if result.Err != nil {
		t.Fatalf("git %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), result.Err, result.Stdout, result.Stderr)
	}
	return result.Stdout
}

func realCLIRoot(t *testing.T) string {
	t.Helper()
	if env := strings.TrimSpace(os.Getenv("LOOM_REAL_CLI_ROOT")); env != "" {
		if err := os.MkdirAll(env, 0o755); err != nil {
			t.Fatalf("create LOOM_REAL_CLI_ROOT: %v", err)
		}
		return env
	}
	if envBool("LOOM_REAL_CLI_KEEP") {
		dir, err := os.MkdirTemp("", "loom-real-clis-*")
		if err != nil {
			t.Fatalf("mktemp: %v", err)
		}
		return dir
	}
	return t.TempDir()
}

func selectedRealCLIBackends() []string {
	raw := strings.TrimSpace(os.Getenv("LOOM_REAL_CLI_BACKENDS"))
	if raw == "" {
		raw = "claude,codex,opencode"
	}
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	})
	out := make([]string, 0, len(fields))
	seen := make(map[string]bool)
	for _, field := range fields {
		name := strings.ToLower(strings.TrimSpace(field))
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func realCLITimeout(t *testing.T) time.Duration {
	t.Helper()
	raw := envOrDefault("LOOM_REAL_CLI_TIMEOUT", "3m")
	d, err := time.ParseDuration(raw)
	if err != nil {
		t.Fatalf("parse LOOM_REAL_CLI_TIMEOUT=%q: %v", raw, err)
	}
	if d <= 0 {
		t.Fatalf("LOOM_REAL_CLI_TIMEOUT must be positive, got %s", d)
	}
	return d
}

func envOrDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func unsetEnv(t *testing.T, key string) {
	t.Helper()
	orig, hadOrig := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		if hadOrig {
			if err := os.Setenv(key, orig); err != nil {
				t.Errorf("restore %s: %v", key, err)
			}
			return
		}
		if err := os.Unsetenv(key); err != nil {
			t.Errorf("restore unset %s: %v", key, err)
		}
	})
}

func envBool(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil || !errors.Is(err, os.ErrNotExist)
}

func TestRealCLISelectedBackendsParser(t *testing.T) {
	t.Setenv("LOOM_REAL_CLI_BACKENDS", " claude,codex opencode,claude ")
	got := selectedRealCLIBackends()
	want := []string{"claude", "codex", "opencode"}
	if !slices.Equal(got, want) {
		t.Fatalf("selectedRealCLIBackends() = %v, want %v", got, want)
	}
}
