package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/agentprofile"
	"github.com/tysonthomas9/loomcli/internal/harnessprofile"
)

// These cover the standalone `loom agent` path, the third and last entry point
// into the profile policy. The policy's own behavior is exhaustively tested in
// internal/harnessprofile; what matters here is that a `loom agent` run gets
// it AT ALL — the bug was that it got none and authenticated as the operator.

// fakeAgentHarnessVersion is what the stub `claude` on PATH reports.
const fakeAgentHarnessVersion = "9.9.9 (Claude Code)"

// stubClaudeForAgent shims `claude --version` first on PATH. It resets the
// probe cache on both ends: the cache is process-global, so without this a
// version another test already probed off the real binary would decide this
// test's outcome, by order.
func stubClaudeForAgent(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	harnessprofile.ResetHarnessVersionCache()
	t.Cleanup(harnessprofile.ResetHarnessVersionCache)
	script := "#!/bin/sh\necho '" + fakeAgentHarnessVersion + "'\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o700); err != nil { //nolint:gosec // G306: test fixture must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// clearAgentProfileEnv starts every test from the bare `loom agent` case,
// whatever the operator environment running the suite holds.
func clearAgentProfileEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "CLAUDE_CODE_OAUTH_TOKEN"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
}

// writeAgentProfile materializes a claude profile root for agent under
// runtimeDir's agent-profiles tree, with a manifest matching its contents.
func writeAgentProfile(t *testing.T, runtimeDir, agent, version string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(runtimeDir, ".loom", agentprofile.DirName, agent, "claude")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(files[name]), 0o600); err != nil {
			t.Fatal(err)
		}
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write([]byte(files[name]))
	}
	raw, err := json.Marshal(map[string]any{
		"files":           names,
		"fingerprint":     hex.EncodeToString(h.Sum(nil)),
		"harness_version": version,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, agentprofile.ManifestName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The bug, stated as a test: a standalone run resolved neither the profile root
// nor the profile's own credential, so it authenticated as the operator.
func TestAgentRun_InjectsProfileRootAndToken(t *testing.T) {
	clearAgentProfileEnv(t)
	stubClaudeForAgent(t)
	runtimeDir := t.TempDir()
	dir := writeAgentProfile(t, runtimeDir, "critic", fakeAgentHarnessVersion, map[string]string{
		"settings.json": `{"model":"opus"}`,
	})
	if err := os.WriteFile(filepath.Join(dir, "oauth-token"), []byte("sk-ant-oat01-critic\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := enforceAgentProfile(runtimeDir, "critic"); err != nil {
		t.Fatalf("enforcement must succeed, got %v", err)
	}
	if got := os.Getenv("CLAUDE_CONFIG_DIR"); got != dir {
		t.Errorf("CLAUDE_CONFIG_DIR = %q, want %q", got, dir)
	}
	if got := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); got != "sk-ant-oat01-critic" {
		t.Errorf("CLAUDE_CODE_OAUTH_TOKEN = %q, want the profile's own token", got)
	}
}

// An inherited root is a decision someone else already made; re-resolving it
// would silently move an agent that was pointed somewhere on purpose.
func TestAgentRun_InheritedConfigDirIsNotOverridden(t *testing.T) {
	clearAgentProfileEnv(t)
	stubClaudeForAgent(t)
	runtimeDir := t.TempDir()
	inherited := writeAgentProfile(t, runtimeDir, "nova", fakeAgentHarnessVersion, map[string]string{
		"settings.json": `{"model":"opus"}`,
	})
	writeAgentProfile(t, runtimeDir, "critic", fakeAgentHarnessVersion, map[string]string{
		"settings.json": `{"model":"other"}`,
	})
	t.Setenv("CLAUDE_CONFIG_DIR", inherited)

	if err := enforceAgentProfile(runtimeDir, "critic"); err != nil {
		t.Fatalf("a valid inherited profile must proceed, got %v", err)
	}
	if got := os.Getenv("CLAUDE_CONFIG_DIR"); got != inherited {
		t.Fatalf("CLAUDE_CONFIG_DIR = %q, want the inherited %q", got, inherited)
	}
}

// An operator's own config root is outside the agent-profiles tree: nothing
// here provisioned it, so it is neither verified, nor overridden, nor read.
func TestAgentRun_InheritedOperatorRootIsLeftAlone(t *testing.T) {
	clearAgentProfileEnv(t)
	stubClaudeForAgent(t)
	runtimeDir := t.TempDir()
	outside := filepath.Join(t.TempDir(), ".claude")
	if err := os.MkdirAll(outside, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "oauth-token"), []byte("sk-ant-oat01-stray"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", outside)

	if err := enforceAgentProfile(runtimeDir, "critic"); err != nil {
		t.Fatalf("an operator's own config root must proceed, got %v", err)
	}
	if got := os.Getenv("CLAUDE_CONFIG_DIR"); got != outside {
		t.Errorf("CLAUDE_CONFIG_DIR = %q, want the operator's %q", got, outside)
	}
	if got := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); got != "" {
		t.Errorf("nothing may be read out of an operator's own root, got %q", got)
	}
}

// A worktree with no profile is the supported legacy case and must stay so:
// enforcement is a boot gate, not a provisioning requirement.
func TestAgentRun_UnmappedWorktreeRunsWithoutProfile(t *testing.T) {
	clearAgentProfileEnv(t)
	// No stub on PATH: an unprofiled agent must not even probe the harness.
	if err := enforceAgentProfile(t.TempDir(), "unmapped"); err != nil {
		t.Fatalf("an unmapped worktree must proceed, got %v", err)
	}
	if got := os.Getenv("CLAUDE_CONFIG_DIR"); got != "" {
		t.Fatalf("CLAUDE_CONFIG_DIR = %q, want unset", got)
	}
}

// A profile that exists but does not verify refuses the run. Degrading to the
// operator's ~/.claude is the leak this whole feature closes.
func TestAgentRun_UnverifiableProfileRefuses(t *testing.T) {
	clearAgentProfileEnv(t)
	stubClaudeForAgent(t)
	runtimeDir := t.TempDir()
	dir := filepath.Join(runtimeDir, ".loom", agentprofile.DirName, "critic", "claude")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}

	err := enforceAgentProfile(runtimeDir, "critic")
	if !errors.Is(err, harnessprofile.ErrProfileManifestMissing) {
		t.Fatalf("want missing manifest, got %v", err)
	}
	if got, want := harnessprofile.Repair(err, harnessprofile.FailedDir(err)), "scripts/provision-profile.sh critic"; got != want {
		t.Errorf("repair = %q, want %q", got, want)
	}
	if got := os.Getenv("CLAUDE_CONFIG_DIR"); got != "" {
		t.Errorf("a refused run must not export a profile, got %q", got)
	}
}

// The only place a token value may go is the child's environment — never an
// error, a log line, or stdout. The failure report is printed to stderr, so a
// token leaking into the error text would be written to the operator's screen.
func TestAgentRun_TokenNeverAppearsInError(t *testing.T) {
	clearAgentProfileEnv(t)
	stubClaudeForAgent(t)
	runtimeDir := t.TempDir()
	dir := writeAgentProfile(t, runtimeDir, "critic", fakeAgentHarnessVersion, map[string]string{
		"settings.json": `{"model":"opus"}`,
	})
	const secret = "sk-ant-oat01-secret-value"
	if err := os.WriteFile(filepath.Join(dir, "oauth-token"), []byte(secret), 0o000); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0000 file regardless of mode")
	}

	err := enforceAgentProfile(runtimeDir, "critic")
	if !errors.Is(err, harnessprofile.ErrProfileTokenUnreadable) {
		t.Fatalf("an unreadable token must refuse, got %v", err)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), secret[:12]) {
		t.Fatalf("error text carries the credential: %v", err)
	}
	if got, want := harnessprofile.Repair(err, harnessprofile.FailedDir(err)), "scripts/setup-profile-token.sh critic"; got != want {
		t.Errorf("repair = %q, want %q", got, want)
	}
}

// The daemon-spawned path re-enters enforcement in the child process. That must
// be a no-op: the supervisor already resolved and verified these values, and
// overriding either of them would move the agent out from under its own spawn.
func TestDaemonModeStillBootsWithSupervisorInjectedEnv(t *testing.T) {
	clearAgentProfileEnv(t)
	stubClaudeForAgent(t)
	runtimeDir := t.TempDir()
	dir := writeAgentProfile(t, runtimeDir, "critic", fakeAgentHarnessVersion, map[string]string{
		"settings.json": `{"model":"opus"}`,
	})
	if err := os.WriteFile(filepath.Join(dir, "oauth-token"), []byte("sk-ant-oat01-critic"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "sk-ant-oat01-critic")

	if err := enforceAgentProfile(runtimeDir, "critic"); err != nil {
		t.Fatalf("re-entry under the supervisor must be a no-op, got %v", err)
	}
	if got := os.Getenv("CLAUDE_CONFIG_DIR"); got != dir {
		t.Errorf("CLAUDE_CONFIG_DIR = %q, want %q", got, dir)
	}
	if got := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); got != "sk-ant-oat01-critic" {
		t.Errorf("CLAUDE_CODE_OAUTH_TOKEN = %q, want it unchanged", got)
	}
}

// runAgent must reach enforcement before it can reach a backend — the gate is
// worthless if a mode branch runs first. This pins the ordering against the
// source, which is the one thing the unit tests above cannot observe.
func TestRunAgent_EnforcesProfileBeforeAnyModeBranch(t *testing.T) {
	src, err := os.ReadFile("agent_cmd.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	enforce := strings.Index(body, "enforceAgentProfile(")
	if enforce < 0 {
		t.Fatal("runAgent no longer enforces the harness profile")
	}
	for _, branch := range []string{"if agentDaemonMode {", "if agentAutoMode {", "runAgentSingleTask("} {
		if at := strings.Index(body, branch); at < 0 || at < enforce {
			t.Errorf("%q at %d runs before enforcement at %d", branch, at, enforce)
		}
	}
}
