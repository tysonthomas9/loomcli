package agentprofile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubVersions replaces the --version probe and clears the cache on both ends.
func stubVersions(t *testing.T, versions map[string]string) *int {
	t.Helper()
	calls := 0
	prev := ProbeVersionFunc
	ProbeVersionFunc = func(binary string) string { calls++; return versions[binary] }
	ResetVersionCache()
	t.Cleanup(func() { ProbeVersionFunc = prev; ResetVersionCache() })
	return &calls
}

// TestHarnessEnv covers the resolve-verify-export policy every launcher
// shares: a verified root exports its variable (and a claude root its own
// token), an absent root or an unusable agent name exports nothing, and an
// invalid existing root refuses with its typed error, never falling back.
func TestHarnessEnv(t *testing.T) {
	stubVersions(t, map[string]string{"claude": testVersion, "codex": "codex-cli 0.157.1"})
	project := t.TempDir()
	root := Dir(project, "agent")
	codex := writeProfile(t, root, "codex", "codex-cli 0.157.1", map[string]string{"config.toml": "x"})
	claude := writeProfile(t, root, "claude", testVersion, map[string]string{"settings.json": "{}"})
	if err := os.WriteFile(filepath.Join(claude, "oauth-token"), []byte("tok\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir, env, err := HarnessEnv(project, "agent", "codex")
	if err != nil || dir != codex || strings.Join(env, ",") != "CODEX_HOME="+codex {
		t.Fatalf("codex: %q %v %v", dir, env, err)
	}
	dir, env, err = HarnessEnv(project, "agent", "claude")
	if err != nil || dir != claude || strings.Join(env, ",") != "CLAUDE_CONFIG_DIR="+claude+",CLAUDE_CODE_OAUTH_TOKEN=tok" {
		t.Fatalf("claude: %q %d assignments, %v", dir, len(env), err)
	}
	for _, agent := range []string{"absent", "", "../agent"} {
		if dir, env, err := HarnessEnv(project, agent, "codex"); dir != "" || env != nil || err != nil {
			t.Fatalf("agent %q: %q %v %v, want nothing", agent, dir, env, err)
		}
	}

	if err := os.WriteFile(filepath.Join(codex, "config.toml"), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if dir, env, err := HarnessEnv(project, "agent", "codex"); !errors.Is(err, ErrFingerprintMismatch) || dir != "" || env != nil {
		t.Fatalf("tampered codex root: %q %v %v, want ErrFingerprintMismatch", dir, env, err)
	}
}

// TestVersionProbedOncePerBinary: the probe forks a node CLI, so it is
// amortized across a spawn cycle.
func TestVersionProbedOncePerBinary(t *testing.T) {
	calls := stubVersions(t, map[string]string{"claude": testVersion})
	for range 3 {
		if got := cachedVersion("claude"); got != testVersion {
			t.Fatalf("got %q", got)
		}
	}
	if *calls != 1 {
		t.Fatalf("%d probes, want 1", *calls)
	}
}

// TestVersionFailureNotCached: a failed probe must not poison the cache.
func TestVersionFailureNotCached(t *testing.T) {
	stubVersions(t, nil)
	results := []string{"", testVersion}
	ProbeVersionFunc = func(string) string {
		out := results[0]
		if len(results) > 1 {
			results = results[1:]
		}
		return out
	}
	if got := cachedVersion("claude"); got != "" {
		t.Fatalf("first probe should fail, got %q", got)
	}
	if got := cachedVersion("claude"); got != testVersion {
		t.Errorf("second probe should re-run, got %q", got)
	}
}

// TestHarnessTablesAgree: the three harness tables are one vocabulary split
// across three maps; the binary stays a BARE PATH name, and Harnesses hands
// out a copy.
func TestHarnessTablesAgree(t *testing.T) {
	hs := Harnesses()
	if len(hs) != len(harnessEnvVar) || len(hs) != len(HarnessBinary) {
		t.Fatalf("harnesses %v, env vars %v, binaries %v", hs, harnessEnvVar, HarnessBinary)
	}
	for _, h := range hs {
		if EnvVar(h) == "" {
			t.Errorf("harness %q exports no config-root variable", h)
		}
		if b := HarnessBinary[h]; b == "" || filepath.Base(b) != b {
			t.Errorf("harness %q binary %q must be a bare PATH name", h, b)
		}
	}
	hs[0] = "mutated"
	if Harnesses()[0] == "mutated" {
		t.Error("Harnesses leaked its backing array")
	}
}

// TestSecretEnv: the credential never travels into an error, an empty file
// refuses, and a harness with no token file (codex) gets nothing even when a
// stray file of that name is there.
func TestSecretEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "oauth-token")
	if err := os.MkdirAll(path, 0o755); err != nil { // a directory: an unreadable file
		t.Fatal(err)
	}
	if _, err := SecretEnv(dir, "claude"); !errors.Is(err, ErrTokenUnreadable) || !strings.Contains(err.Error(), path) {
		t.Fatalf("unreadable token: %v", err)
	}
	empty := t.TempDir()
	if err := os.WriteFile(filepath.Join(empty, "oauth-token"), []byte(" \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := SecretEnv(empty, "claude"); !errors.Is(err, ErrTokenUnreadable) {
		t.Fatalf("empty token: %v", err)
	}
	stray := t.TempDir()
	if err := os.WriteFile(filepath.Join(stray, "oauth-token"), []byte("sk-ant-oat01-stray"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"codex", "unknown"} {
		if got, err := SecretEnv(stray, h); err != nil || len(got) != 0 {
			t.Errorf("harness %q: got %v (err %v), want nothing", h, got, err)
		}
	}
}
