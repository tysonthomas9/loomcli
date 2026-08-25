package supervisor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/agentprofile"
)

// stubHarnessVersion replaces the --version probe and clears the cache on both
// ends, so a test never reads (or leaves behind) another test's probe result.
func stubHarnessVersion(t *testing.T, versions map[string]string) *int {
	t.Helper()
	calls := 0
	prev := probeHarnessVersion
	probeHarnessVersion = func(binary string) string {
		calls++
		return versions[binary]
	}
	resetHarnessVersionCache()
	t.Cleanup(func() {
		probeHarnessVersion = prev
		resetHarnessVersionCache()
	})
	return &calls
}

func resetHarnessVersionCache() {
	harnessVersionMu.Lock()
	harnessVersionCache = map[string]harnessVersionEntry{}
	harnessVersionMu.Unlock()
}

// writeProfile materializes a claude profile root for worktree and writes a
// manifest over the given files. version is what the manifest pins; a caller
// that wants a stale fingerprint mutates a file afterwards.
func writeProfile(t *testing.T, projectDir, worktree, version string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(projectDir, ".loom", AgentProfilesDirName, worktree, "claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	// Manifest order is the hash order; sort so the fixture matches the
	// provisioner's sorted list.
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	h := sha256.New()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(files[name]), 0o600); err != nil {
			t.Fatal(err)
		}
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write([]byte(files[name]))
	}
	m := agentprofile.Manifest{Files: names, Fingerprint: hex.EncodeToString(h.Sum(nil)), HarnessVersion: version}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ProfileManifestName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestAppendProfileEnv_AbsentDirsLeaveEnvUntouched(t *testing.T) {
	stubHarnessVersion(t, nil)
	projectDir := t.TempDir()
	env, err := AppendProfileEnv([]string{"A=1"}, projectDir, "worker")
	if err != nil {
		t.Fatalf("no profile dir must stay legacy, got %v", err)
	}
	if len(env) != 1 {
		t.Fatalf("expected env untouched, got %v", env)
	}
}

func TestAppendProfileEnv_InjectsVerifiedProfileRoots(t *testing.T) {
	stubHarnessVersion(t, map[string]string{"claude": "2.1.234 (Claude Code)", "codex": "codex-cli 0.147.0"})
	projectDir := t.TempDir()
	claudeDir := writeProfile(t, projectDir, "worker", "2.1.234 (Claude Code)", map[string]string{
		"settings.json": `{"model":"opus"}`,
		"CLAUDE.md":     "house rules\n",
	})
	codexDir := filepath.Join(projectDir, ".loom", AgentProfilesDirName, "worker", "codex")
	if err := os.MkdirAll(filepath.Join(codexDir, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(agentprofile.Manifest{
		Files:          []string{},
		Fingerprint:    hex.EncodeToString(sha256.New().Sum(nil)),
		HarnessVersion: "codex-cli 0.147.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, ProfileManifestName), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	env, err := AppendProfileEnv(nil, projectDir, "worker")
	if err != nil {
		t.Fatalf("verified profile must boot: %v", err)
	}
	want := map[string]bool{
		"CLAUDE_CONFIG_DIR=" + claudeDir: false,
		"CODEX_HOME=" + codexDir:         false,
	}
	for _, kv := range env {
		if _, ok := want[kv]; ok {
			want[kv] = true
		}
	}
	for kv, seen := range want {
		if !seen {
			t.Errorf("missing %s in %v", kv, env)
		}
	}

	// A different agent with no profile dir stays legacy.
	extra, err := AppendProfileEnv(nil, projectDir, "critic")
	if err != nil || len(extra) != 0 {
		t.Errorf("critic should have no profile env, got %v (err %v)", extra, err)
	}
}

func TestAppendProfileEnv_ContentMismatchRefusesBoot(t *testing.T) {
	stubHarnessVersion(t, map[string]string{"claude": "2.1.234 (Claude Code)"})
	projectDir := t.TempDir()
	dir := writeProfile(t, projectDir, "worker", "2.1.234 (Claude Code)", map[string]string{
		"settings.json": `{"model":"opus"}`,
	})
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"model":"tampered"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	env, err := AppendProfileEnv(nil, projectDir, "worker")
	if !errors.Is(err, ErrProfileFingerprintMismatch) {
		t.Fatalf("want fingerprint mismatch, got %v", err)
	}
	if env != nil {
		t.Errorf("a refused boot must not return env, got %v", env)
	}
}

func TestAppendProfileEnv_ManifestedFileDeletedRefusesBoot(t *testing.T) {
	stubHarnessVersion(t, map[string]string{"claude": "2.1.234 (Claude Code)"})
	projectDir := t.TempDir()
	dir := writeProfile(t, projectDir, "worker", "2.1.234 (Claude Code)", map[string]string{
		"settings.json": `{"model":"opus"}`,
	})
	if err := os.Remove(filepath.Join(dir, "settings.json")); err != nil {
		t.Fatal(err)
	}

	if _, err := AppendProfileEnv(nil, projectDir, "worker"); !errors.Is(err, ErrProfileManifestUnreadable) {
		t.Fatalf("want unreadable-manifest error for a missing listed file, got %v", err)
	}
}

func TestAppendProfileEnv_VersionDriftRefusesBoot(t *testing.T) {
	stubHarnessVersion(t, map[string]string{"claude": "2.2.0 (Claude Code)"})
	projectDir := t.TempDir()
	writeProfile(t, projectDir, "worker", "2.1.234 (Claude Code)", map[string]string{
		"settings.json": `{"model":"opus"}`,
	})

	_, err := AppendProfileEnv(nil, projectDir, "worker")
	if !errors.Is(err, ErrProfileVersionDrift) {
		t.Fatalf("want version drift, got %v", err)
	}
}

func TestAppendProfileEnv_UnknownVersionRefusesBoot(t *testing.T) {
	stubHarnessVersion(t, nil) // probe yields ""
	projectDir := t.TempDir()
	writeProfile(t, projectDir, "worker", "2.1.234 (Claude Code)", map[string]string{
		"settings.json": `{"model":"opus"}`,
	})

	if _, err := AppendProfileEnv(nil, projectDir, "worker"); !errors.Is(err, ErrProfileVersionUnknown) {
		t.Fatalf("want unknown-version refusal, got %v", err)
	}
}

func TestAppendProfileEnv_MissingManifestRefusesBoot(t *testing.T) {
	stubHarnessVersion(t, map[string]string{"claude": "2.1.234 (Claude Code)"})
	projectDir := t.TempDir()
	dir := filepath.Join(projectDir, ".loom", AgentProfilesDirName, "worker", "claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	env, err := AppendProfileEnv(nil, projectDir, "worker")
	if !errors.Is(err, ErrProfileManifestMissing) {
		t.Fatalf("want missing-manifest refusal, got %v", err)
	}
	// The whole point: never a silent fallback to the operator's ~/.claude.
	if env != nil {
		t.Errorf("unprovisioned profile dir must not fall back to legacy env, got %v", env)
	}
}

func TestAppendProfileEnv_CorruptManifestRefusesBoot(t *testing.T) {
	stubHarnessVersion(t, map[string]string{"claude": "2.1.234 (Claude Code)"})
	projectDir := t.TempDir()
	dir := filepath.Join(projectDir, ".loom", AgentProfilesDirName, "worker", "claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ProfileManifestName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := AppendProfileEnv(nil, projectDir, "worker"); !errors.Is(err, ErrProfileManifestUnreadable) {
		t.Fatalf("want unreadable-manifest refusal, got %v", err)
	}
}

// The version probe forks a node-based CLI, so it is amortized across the
// whole spawn cycle rather than paid once per agent.
func TestHarnessVersionProbedOncePerBinary(t *testing.T) {
	calls := stubHarnessVersion(t, map[string]string{"claude": "2.1.234 (Claude Code)"})
	projectDir := t.TempDir()
	for _, agent := range []string{"worker", "critic", "tester"} {
		writeProfile(t, projectDir, agent, "2.1.234 (Claude Code)", map[string]string{
			"settings.json": `{"model":"opus"}`,
		})
		if _, err := AppendProfileEnv(nil, projectDir, agent); err != nil {
			t.Fatalf("%s: %v", agent, err)
		}
	}
	if *calls != 1 {
		t.Errorf("expected 1 version probe across 3 agent boots, got %d", *calls)
	}
}

// A failed probe must not poison the cache: the next boot re-probes.
func TestHarnessVersionFailureNotCached(t *testing.T) {
	resetHarnessVersionCache()
	prev := probeHarnessVersion
	t.Cleanup(func() { probeHarnessVersion = prev; resetHarnessVersionCache() })
	results := []string{"", "2.1.234 (Claude Code)"}
	probeHarnessVersion = func(string) string {
		out := results[0]
		if len(results) > 1 {
			results = results[1:]
		}
		return out
	}
	if got := harnessVersion("claude"); got != "" {
		t.Fatalf("first probe should fail, got %q", got)
	}
	if got := harnessVersion("claude"); got != "2.1.234 (Claude Code)" {
		t.Errorf("second probe should re-run, got %q", got)
	}
}

// An agent name agentprofile.Dir refuses resolves to no profile root at all,
// which is the same situation as an absent directory: legacy env, no error.
func TestAppendProfileEnv_UnresolvableAgentNameLeavesEnvUntouched(t *testing.T) {
	stubHarnessVersion(t, map[string]string{"claude": "2.1.234 (Claude Code)"})
	projectDir := t.TempDir()
	for _, name := range []string{"", "..", filepath.Join("a", "b")} {
		env, err := AppendProfileEnv([]string{"A=1"}, projectDir, name)
		if err != nil {
			t.Fatalf("worktree %q: want legacy env, got %v", name, err)
		}
		if len(env) != 1 || env[0] != "A=1" {
			t.Errorf("worktree %q: expected env untouched, got %v", name, env)
		}
	}
}

// writeCodexProfile materializes a codex profile root, whose manifest pins the
// codex binary's version rather than claude's.
func writeCodexProfile(t *testing.T, projectDir, agent, version string) string {
	t.Helper()
	dir := filepath.Join(projectDir, ".loom", AgentProfilesDirName, agent, "codex")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(agentprofile.Manifest{
		Files:          []string{},
		Fingerprint:    hex.EncodeToString(sha256.New().Sum(nil)),
		HarnessVersion: version,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ProfileManifestName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Which variables an agent gets is decided by which directories exist, one
// harness at a time. The half-provisioned rows are the ones that matter: a
// claude-only agent must still inherit ~/.codex, and `loom lead` — which
// injects per harness — depends on exactly this independence.
func TestAppendProfileEnv_ExportsPerExistingHarnessRoot(t *testing.T) {
	const claudeVersion = "2.1.234 (Claude Code)"
	const codexVersion = "codex-cli 0.147.0"

	for _, tc := range []struct {
		name          string
		claude, codex bool
	}{
		{name: "neither"},
		{name: "claude only", claude: true},
		{name: "codex only", codex: true},
		{name: "both", claude: true, codex: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubHarnessVersion(t, map[string]string{"claude": claudeVersion, "codex": codexVersion})
			projectDir := t.TempDir()
			want := map[string]string{}
			if tc.claude {
				want["CLAUDE_CONFIG_DIR"] = writeProfile(t, projectDir, "worker", claudeVersion, map[string]string{
					"settings.json": `{"model":"opus"}`,
				})
			}
			if tc.codex {
				want["CODEX_HOME"] = writeCodexProfile(t, projectDir, "worker", codexVersion)
			}

			env, err := AppendProfileEnv(nil, projectDir, "worker")
			if err != nil {
				t.Fatalf("verified profiles must boot: %v", err)
			}
			got := map[string]string{}
			for _, kv := range env {
				k, v, _ := strings.Cut(kv, "=")
				got[k] = v
			}
			if len(got) != len(want) {
				t.Fatalf("env = %v, want exactly %v", env, want)
			}
			for k, v := range want {
				if got[k] != v {
					t.Errorf("%s = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

// An agent name that is not a single path segment must resolve no profile at
// all. The supervisor's names come from daemon config, but `loom lead` reads
// its own from the environment, and a traversing name there would otherwise
// point a live harness at a config root outside the workspace.
func TestAppendProfileEnv_UnusableAgentNameResolvesNothing(t *testing.T) {
	stubHarnessVersion(t, map[string]string{"claude": "2.1.234 (Claude Code)"})
	projectDir := t.TempDir()
	writeProfile(t, projectDir, "worker", "2.1.234 (Claude Code)", map[string]string{
		"settings.json": `{"model":"opus"}`,
	})

	for _, agent := range []string{"", ".", "..", "../worker", "sub/worker"} {
		env, err := AppendProfileEnv(nil, projectDir, agent)
		if err != nil || len(env) != 0 {
			t.Errorf("agent %q: env=%v err=%v, want a silent no-op", agent, env, err)
		}
	}
}

// The three harness tables are one vocabulary split across three maps: a
// harness missing from any of them either exports nothing, verifies against
// nothing, or panics a caller that assumed symmetry. The binary must stay a
// BARE name so it resolves on PATH exactly as the backends layer launches it —
// which is also what the provisioner pins its manifest version from.
func TestProfileHarnessTablesAgree(t *testing.T) {
	harnesses := ProfileHarnesses()
	if len(harnesses) != len(profileHarnessEnvVar) || len(harnesses) != len(agentprofile.HarnessBinary) {
		t.Fatalf("harnesses %v, env vars %v, binaries %v", harnesses, profileHarnessEnvVar, agentprofile.HarnessBinary)
	}
	for _, harness := range harnesses {
		if ProfileEnvVar(harness) == "" {
			t.Errorf("harness %q exports no config-root variable", harness)
		}
		binary := ProfileHarnessBinary(harness)
		if binary == "" {
			t.Errorf("harness %q pins no version binary", harness)
		}
		if filepath.Base(binary) != binary {
			t.Errorf("harness %q binary %q must be a bare PATH name", harness, binary)
		}
	}
	// ProfileHarnesses hands out a copy; mutating it must not move the order
	// every agent's environment is built in.
	harnesses[0] = "mutated"
	if ProfileHarnesses()[0] == "mutated" {
		t.Error("ProfileHarnesses leaked its backing array")
	}
}
