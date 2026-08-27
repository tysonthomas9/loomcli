package lead

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
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
)

// fakeHarnessVersion is what the stub `claude` on PATH reports. Every test in
// this file uses the same string on purpose: the supervisor caches a probe per
// binary for a couple of minutes, so tests that disagreed about the installed
// version would pass or fail depending on their order.
const fakeHarnessVersion = "9.9.9 (Claude Code)"

// stubClaudeOnPath puts a `claude --version` shim first on PATH, so the check
// runs against a known version instead of whatever the machine has installed.
func stubClaudeOnPath(t *testing.T) {
	t.Helper()
	stubHarnessesOnPath(t, "claude")
}

// stubHarnessesOnPath shims every named harness binary to report
// fakeHarnessVersion. They deliberately share one version string: the
// supervisor caches a probe per binary for a couple of minutes, so tests that
// disagreed about the installed version would pass or fail by order.
func stubHarnessesOnPath(t *testing.T, binaries ...string) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\necho '" + fakeHarnessVersion + "'\n"
	// Startup enforcement probes the real binaries, so a cached version from
	// an earlier test would decide this one's outcome.
	supervisor.ResetHarnessVersionCache()
	t.Cleanup(supervisor.ResetHarnessVersionCache)
	for _, name := range binaries {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o700); err != nil { //nolint:gosec // G306: test fixture must be executable
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// writeLeadProfile materializes a profile root under the workspace's
// agent-profiles tree and writes a manifest matching it.
func writeLeadProfile(t *testing.T, runtimeDir, agent, version string, files map[string]string) string {
	t.Helper()
	return writeLeadHarnessProfile(t, runtimeDir, agent, "claude", version, files)
}

// writeLeadHarnessProfile is writeLeadProfile for a named harness root.
func writeLeadHarnessProfile(t *testing.T, runtimeDir, agent, harness, version string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(runtimeDir, ".loom", agentprofile.DirName, agent, harness)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sortStrings(names)
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
	if err := os.WriteFile(filepath.Join(dir, supervisor.ProfileManifestName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func sortStrings(s []string) {
	for i := 0; i < len(s); i++ {
		for j := i + 1; j < len(s); j++ {
			if s[j] < s[i] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

// isolateLeadWorkspace points the resolved workspace at an empty tree, so a
// test that runs the lead startup path enforces against nothing instead of
// against the operator's live profiles — which it could refuse, taking the
// whole test binary down with os.Exit(1).
func isolateLeadWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", dir)
	cli.ResetWorkspaceRuntimeDirCache()
	t.Cleanup(cli.ResetWorkspaceRuntimeDirCache)
	return dir
}

func TestVerifyLeadProfile_UnsetConfigDirIsNotVerified(t *testing.T) {
	// No stub on PATH: an unprofiled lead must not even probe the harness.
	if err := verifyLeadProfile(t.TempDir(), "", "claude"); err != nil {
		t.Fatalf("unprofiled lead must stay silent, got %v", err)
	}
}

func TestVerifyLeadProfile_ConfigDirOutsideProfileRootIsNotVerified(t *testing.T) {
	runtimeDir := t.TempDir()
	// An operator's own config root: no manifest, and none of our business.
	outside := filepath.Join(t.TempDir(), ".claude")
	if err := os.MkdirAll(outside, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := verifyLeadProfile(runtimeDir, outside, "claude"); err != nil {
		t.Fatalf("config dir outside the profile root must stay silent, got %v", err)
	}
	// The agent-profiles root itself is not a profile either.
	root := filepath.Join(runtimeDir, ".loom", agentprofile.DirName)
	if err := verifyLeadProfile(runtimeDir, root, "claude"); err != nil {
		t.Fatalf("profile root itself must stay silent, got %v", err)
	}
}

func TestVerifyLeadProfile_DriftedProfileRefusesWithDoctorRepair(t *testing.T) {
	stubClaudeOnPath(t)
	runtimeDir := t.TempDir()
	dir := writeLeadProfile(t, runtimeDir, "lead", "2.1.235 (Claude Code)", map[string]string{
		"settings.json": `{"model":"opus"}`,
	})

	err := verifyLeadProfile(runtimeDir, dir, "claude")
	if !errors.Is(err, supervisor.ErrProfileVersionDrift) {
		t.Fatalf("want version drift, got %v", err)
	}
	if !strings.Contains(err.Error(), "2.1.235 (Claude Code)") || !strings.Contains(err.Error(), fakeHarnessVersion) {
		t.Fatalf("error must name both versions, got %v", err)
	}
	if got := leadProfileRepair(err, dir); got != "loom doctor --fix" {
		t.Fatalf("drift repair = %q, want the doctor re-bless", got)
	}
}

func TestVerifyLeadProfile_TamperedProfileRefusesWithProvisionRepair(t *testing.T) {
	stubClaudeOnPath(t)
	runtimeDir := t.TempDir()
	dir := writeLeadProfile(t, runtimeDir, "lead", fakeHarnessVersion, map[string]string{
		"settings.json": `{"model":"opus"}`,
	})
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"model":"other"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	err := verifyLeadProfile(runtimeDir, dir, "claude")
	if !errors.Is(err, supervisor.ErrProfileFingerprintMismatch) {
		t.Fatalf("want fingerprint mismatch, got %v", err)
	}
	if got := leadProfileRepair(err, dir); got != "scripts/provision-profile.sh lead" {
		t.Fatalf("content repair = %q, want the provisioner named for this agent", got)
	}
}

func TestVerifyLeadProfile_VerifyingProfileProceeds(t *testing.T) {
	stubClaudeOnPath(t)
	runtimeDir := t.TempDir()
	dir := writeLeadProfile(t, runtimeDir, "lead", fakeHarnessVersion, map[string]string{
		"CLAUDE.md":     "house rules\n",
		"settings.json": `{"model":"opus"}`,
	})
	// Harness-owned files are outside the manifest allowlist and must not
	// count as tampering.
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := verifyLeadProfile(runtimeDir, dir, "claude"); err != nil {
		t.Fatalf("verifying profile must proceed, got %v", err)
	}
}

func TestVerifyLeadProfile_UnprovisionedProfileRefuses(t *testing.T) {
	stubClaudeOnPath(t)
	runtimeDir := t.TempDir()
	dir := filepath.Join(runtimeDir, ".loom", agentprofile.DirName, "lead", "claude")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}

	err := verifyLeadProfile(runtimeDir, dir, "claude")
	if !errors.Is(err, supervisor.ErrProfileManifestMissing) {
		t.Fatalf("want missing manifest, got %v", err)
	}
	if got := leadProfileRepair(err, dir); got != "scripts/provision-profile.sh lead" {
		t.Fatalf("repair = %q, want the provisioner", got)
	}
}

func TestVerifyLeadProfile_EmptyRuntimeDirVerifiesNothing(t *testing.T) {
	if err := verifyLeadProfile("", "/somewhere/.loom/agent-profiles/lead/claude", "claude"); err != nil {
		t.Fatalf("unresolvable workspace root must stay silent, got %v", err)
	}
}

// --- injection -------------------------------------------------------------
//
// Verification alone only ever closed half the hole: it checks a value someone
// else exported. These cover the half that makes `loom lead` carry a profile
// no matter how it was started.

// clearProfileEnv unsets both harness config roots so a test starts from the
// bare `loom lead` case regardless of the operator environment running it.
func clearProfileEnv(t *testing.T) {
	t.Helper()
	for _, harness := range supervisor.ProfileHarnesses() {
		t.Setenv(supervisor.ProfileEnvVar(harness), "")
		if err := os.Unsetenv(supervisor.ProfileEnvVar(harness)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestApplyLeadProfile_InjectsClaudeConfigDirWhenUnset(t *testing.T) {
	clearProfileEnv(t)
	stubClaudeOnPath(t)
	runtimeDir := t.TempDir()
	dir := writeLeadProfile(t, runtimeDir, "lead", fakeHarnessVersion, map[string]string{
		"settings.json": `{"model":"opus"}`,
	})

	if _, err := applyLeadProfile(runtimeDir, "lead", "claude"); err != nil {
		t.Fatalf("injection must succeed, got %v", err)
	}
	if got := os.Getenv("CLAUDE_CONFIG_DIR"); got != dir {
		t.Fatalf("CLAUDE_CONFIG_DIR = %q, want %q", got, dir)
	}
}

// The codex half is the reason injection exists at all: the codex lead runtime
// hands its app-server and TUI a bare os.Environ(), so a CODEX_HOME that is
// never set is a lead running on the operator's own ~/.codex.
func TestApplyLeadProfile_InjectsCodexHomeWhenUnset(t *testing.T) {
	clearProfileEnv(t)
	stubHarnessesOnPath(t, "codex")
	runtimeDir := t.TempDir()
	dir := writeLeadHarnessProfile(t, runtimeDir, "lead", "codex", fakeHarnessVersion, map[string]string{
		"config.toml": "model = \"gpt-5\"\n",
	})

	if _, err := applyLeadProfile(runtimeDir, "lead", "codex"); err != nil {
		t.Fatalf("injection must succeed, got %v", err)
	}
	if got := os.Getenv("CODEX_HOME"); got != dir {
		t.Fatalf("CODEX_HOME = %q, want %q", got, dir)
	}
}

// Both roots at once, through the loop enforceLeadProfile runs: the failure
// this task fixes was a lead that had one and not the other.
func TestApplyLeadProfile_InjectsBothHarnessRoots(t *testing.T) {
	clearProfileEnv(t)
	stubHarnessesOnPath(t, "claude", "codex")
	runtimeDir := t.TempDir()
	claudeDir := writeLeadProfile(t, runtimeDir, "lead", fakeHarnessVersion, map[string]string{
		"settings.json": `{"model":"opus"}`,
	})
	codexDir := writeLeadHarnessProfile(t, runtimeDir, "lead", "codex", fakeHarnessVersion, map[string]string{
		"config.toml": "model = \"gpt-5\"\n",
	})

	for _, harness := range supervisor.ProfileHarnesses() {
		if _, err := applyLeadProfile(runtimeDir, "lead", harness); err != nil {
			t.Fatalf("%s: %v", harness, err)
		}
	}
	if got := os.Getenv("CLAUDE_CONFIG_DIR"); got != claudeDir {
		t.Errorf("CLAUDE_CONFIG_DIR = %q, want %q", got, claudeDir)
	}
	if got := os.Getenv("CODEX_HOME"); got != codexDir {
		t.Errorf("CODEX_HOME = %q, want %q", got, codexDir)
	}
}

// No profile on disk is the supported unprofiled lead: silent, and nothing set,
// so the harness falls back to the operator's roots exactly as before.
func TestApplyLeadProfile_NoProfileOnDiskLeavesEnvUnset(t *testing.T) {
	clearProfileEnv(t)
	// No stub on PATH: an unprofiled lead must not even probe the harness.
	runtimeDir := t.TempDir()

	for _, harness := range supervisor.ProfileHarnesses() {
		if _, err := applyLeadProfile(runtimeDir, "lead", harness); err != nil {
			t.Fatalf("%s: unprofiled lead must stay silent, got %v", harness, err)
		}
		if got := os.Getenv(supervisor.ProfileEnvVar(harness)); got != "" {
			t.Fatalf("%s = %q, want unset", supervisor.ProfileEnvVar(harness), got)
		}
	}
}

// An unprovisioned profile root refuses the launch and names the provisioner —
// it must never degrade to the operator's ~/.claude, which is the leak the
// whole feature closes.
func TestApplyLeadProfile_UnverifiableProfileRefusesInjection(t *testing.T) {
	clearProfileEnv(t)
	stubClaudeOnPath(t)
	runtimeDir := t.TempDir()
	dir := filepath.Join(runtimeDir, ".loom", agentprofile.DirName, "lead", "claude")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}

	failed, err := applyLeadProfile(runtimeDir, "lead", "claude")
	if !errors.Is(err, supervisor.ErrProfileManifestMissing) {
		t.Fatalf("want missing manifest, got %v", err)
	}
	if failed != dir {
		t.Fatalf("failure names %q, want the profile root %q", failed, dir)
	}
	if got := leadProfileRepair(err, failed); got != "scripts/provision-profile.sh lead" {
		t.Fatalf("repair = %q, want the provisioner named for this agent", got)
	}
	if got := os.Getenv("CLAUDE_CONFIG_DIR"); got != "" {
		t.Fatalf("a refused launch must not export a profile, got %q", got)
	}
}

// An inherited value that verifies is preserved byte for byte: re-resolving it
// from the workspace would silently move a lead an operator pointed somewhere
// on purpose.
func TestApplyLeadProfile_ValidInheritedValueIsPreserved(t *testing.T) {
	clearProfileEnv(t)
	stubClaudeOnPath(t)
	runtimeDir := t.TempDir()
	inherited := writeLeadProfile(t, runtimeDir, "nova", fakeHarnessVersion, map[string]string{
		"settings.json": `{"model":"opus"}`,
	})
	writeLeadProfile(t, runtimeDir, "lead", fakeHarnessVersion, map[string]string{
		"settings.json": `{"model":"other"}`,
	})
	t.Setenv("CLAUDE_CONFIG_DIR", inherited)

	if _, err := applyLeadProfile(runtimeDir, "lead", "claude"); err != nil {
		t.Fatalf("valid inherited profile must proceed, got %v", err)
	}
	if got := os.Getenv("CLAUDE_CONFIG_DIR"); got != inherited {
		t.Fatalf("CLAUDE_CONFIG_DIR = %q, want the inherited %q", got, inherited)
	}
}

func TestApplyLeadProfile_InvalidInheritedValueRefuses(t *testing.T) {
	clearProfileEnv(t)
	stubClaudeOnPath(t)
	runtimeDir := t.TempDir()
	inherited := writeLeadProfile(t, runtimeDir, "lead", "2.1.235 (Claude Code)", map[string]string{
		"settings.json": `{"model":"opus"}`,
	})
	t.Setenv("CLAUDE_CONFIG_DIR", inherited)

	dir, err := applyLeadProfile(runtimeDir, "lead", "claude")
	if !errors.Is(err, supervisor.ErrProfileVersionDrift) {
		t.Fatalf("want version drift, got %v", err)
	}
	if got := leadProfileRepair(err, dir); got != "loom doctor --fix" {
		t.Fatalf("drift repair = %q, want the doctor re-bless", got)
	}
}

// An operator's own config root is outside the agent-profiles tree: not
// verified (nothing here provisioned it) and not overwritten (they chose it).
func TestApplyLeadProfile_OutsideConfigRootIsLeftAlone(t *testing.T) {
	clearProfileEnv(t)
	stubClaudeOnPath(t)
	runtimeDir := t.TempDir()
	// A workspace profile exists AND is drifted: it must not be consulted at
	// all, or an operator running against their own root gets a refusal for a
	// profile they are not using.
	writeLeadProfile(t, runtimeDir, "lead", "2.1.235 (Claude Code)", map[string]string{
		"settings.json": `{"model":"opus"}`,
	})
	outside := filepath.Join(t.TempDir(), ".claude")
	if err := os.MkdirAll(outside, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", outside)

	if _, err := applyLeadProfile(runtimeDir, "lead", "claude"); err != nil {
		t.Fatalf("an operator's own config root must stay silent, got %v", err)
	}
	if got := os.Getenv("CLAUDE_CONFIG_DIR"); got != outside {
		t.Fatalf("CLAUDE_CONFIG_DIR = %q, want the operator's %q", got, outside)
	}
}

// The agent name comes from the environment, so it is the one input that can
// be junk. agentprofile.Dir rejects anything that is not a single segment, and
// the lead must then degrade to legacy env rather than resolve a profile
// outside the workspace.
func TestApplyLeadProfile_UnusableAgentNameDegradesToLegacyEnv(t *testing.T) {
	clearProfileEnv(t)
	runtimeDir := t.TempDir()
	for _, agent := range []string{"", "..", "../escape", "a/b"} {
		dir, err := applyLeadProfile(runtimeDir, agent, "claude")
		if err != nil || dir != "" {
			t.Fatalf("agent %q: dir=%q err=%v, want a silent no-op", agent, dir, err)
		}
		if got := os.Getenv("CLAUDE_CONFIG_DIR"); got != "" {
			t.Fatalf("agent %q exported %q", agent, got)
		}
	}
}

// The workspace root must come from the resolved workspace, not os.Getwd():
// lead's cwd is not fixed, and a cwd-derived profile root would move with it.
func TestApplyLeadProfile_UnresolvableWorkspaceRootInjectsNothing(t *testing.T) {
	clearProfileEnv(t)
	if dir, err := applyLeadProfile("", "lead", "claude"); err != nil || dir != "" {
		t.Fatalf("dir=%q err=%v, want a silent no-op", dir, err)
	}
	if got := os.Getenv("CLAUDE_CONFIG_DIR"); got != "" {
		t.Fatalf("CLAUDE_CONFIG_DIR = %q, want unset", got)
	}
}

// Lead resolves the version-pin binary through the supervisor rather than
// assuming the harness name doubles as the binary name. If the provisioner's
// Step 0 pin ever moves off PATH resolution, this is the assertion that fails.
func TestLeadVerifiesAgainstTheSupervisorsHarnessBinary(t *testing.T) {
	for _, harness := range supervisor.ProfileHarnesses() {
		if got := supervisor.ProfileHarnessBinary(harness); got == "" {
			t.Errorf("harness %q has no version-pin binary", harness)
		}
		if got := supervisor.ProfileEnvVar(harness); got == "" {
			t.Errorf("harness %q has no config-root variable", harness)
		}
	}
}

// clearLeadToken makes CLAUDE_CODE_OAUTH_TOKEN unset for the test and restored
// after it, so an injection is visible as a change and never leaks out.
func clearLeadToken(t *testing.T) {
	t.Helper()
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
	if err := os.Unsetenv("CLAUDE_CODE_OAUTH_TOKEN"); err != nil {
		t.Fatal(err)
	}
}

// A lead that resolves its own profile root must also pick up that root's
// identity, or it authenticates as whoever the operator last logged in as.
func TestApplyLeadProfile_InjectsProfileOAuthToken(t *testing.T) {
	clearProfileEnv(t)
	clearLeadToken(t)
	stubClaudeOnPath(t)
	runtimeDir := t.TempDir()
	dir := writeLeadProfile(t, runtimeDir, "lead", fakeHarnessVersion, map[string]string{
		"settings.json": `{"model":"opus"}`,
	})
	if err := os.WriteFile(filepath.Join(dir, "oauth-token"), []byte("sk-ant-oat01-lead\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := applyLeadProfile(runtimeDir, "lead", "claude"); err != nil {
		t.Fatalf("injection must succeed, got %v", err)
	}
	if got := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); got != "sk-ant-oat01-lead" {
		t.Fatalf("CLAUDE_CODE_OAUTH_TOKEN = %q, want the profile's own token", got)
	}
}

// The launcher script exports the config root and nothing else. Verifying that
// inherited root is not enough: without reading its token too, a launcher-
// started lead runs its own profile's settings on the operator's credential —
// the precise pairing that expires on someone else's schedule.
func TestApplyLeadProfile_InheritedRootStillYieldsItsToken(t *testing.T) {
	clearProfileEnv(t)
	stubClaudeOnPath(t)
	runtimeDir := t.TempDir()
	inherited := writeLeadProfile(t, runtimeDir, "lead", fakeHarnessVersion, map[string]string{
		"settings.json": `{"model":"opus"}`,
	})
	if err := os.WriteFile(filepath.Join(inherited, "oauth-token"), []byte("sk-ant-oat01-lead"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", inherited)
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "sk-ant-oat01-operator")

	if _, err := applyLeadProfile(runtimeDir, "lead", "claude"); err != nil {
		t.Fatalf("valid inherited profile must proceed, got %v", err)
	}
	if got := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); got != "sk-ant-oat01-lead" {
		t.Fatalf("CLAUDE_CODE_OAUTH_TOKEN = %q, want the profile's own token", got)
	}
}

// An operator's own config root is not this feature's business, so nothing is
// read out of it and the token they exported stays exactly as they set it.
func TestApplyLeadProfile_OutsideConfigRootLeavesTokenAlone(t *testing.T) {
	clearProfileEnv(t)
	stubClaudeOnPath(t)
	runtimeDir := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "oauth-token"), []byte("sk-ant-oat01-stray"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", outside)
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "sk-ant-oat01-operator")

	if _, err := applyLeadProfile(runtimeDir, "lead", "claude"); err != nil {
		t.Fatalf("an operator's own config root must proceed, got %v", err)
	}
	if got := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); got != "sk-ant-oat01-operator" {
		t.Fatalf("CLAUDE_CODE_OAUTH_TOKEN = %q, want the operator's own value untouched", got)
	}
}

// Unmigrated profiles are the majority and must be untouched: no token file,
// no injection, no failure.
func TestApplyLeadProfile_NoTokenFileLeavesTokenUnset(t *testing.T) {
	clearProfileEnv(t)
	clearLeadToken(t)
	stubClaudeOnPath(t)
	runtimeDir := t.TempDir()
	writeLeadProfile(t, runtimeDir, "lead", fakeHarnessVersion, map[string]string{
		"settings.json": `{"model":"opus"}`,
	})

	if _, err := applyLeadProfile(runtimeDir, "lead", "claude"); err != nil {
		t.Fatalf("a profile without a token must proceed, got %v", err)
	}
	if got := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); got != "" {
		t.Fatalf("no oauth-token file must inject nothing, got %q", got)
	}
}

// Minting an identity is a different act from provisioning a profile, so a
// broken token gets the script that mints one — not the one that copies files.
func TestLeadProfileRepair_TokenFailureNamesTheTokenScript(t *testing.T) {
	clearProfileEnv(t)
	clearLeadToken(t)
	stubClaudeOnPath(t)
	runtimeDir := t.TempDir()
	dir := writeLeadProfile(t, runtimeDir, "lead", fakeHarnessVersion, map[string]string{
		"settings.json": `{"model":"opus"}`,
	})
	if err := os.WriteFile(filepath.Join(dir, "oauth-token"), []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	failed, err := applyLeadProfile(runtimeDir, "lead", "claude")
	if !errors.Is(err, supervisor.ErrProfileTokenUnreadable) {
		t.Fatalf("an empty token file must refuse, got %v", err)
	}
	if got, want := leadProfileRepair(err, failed), "scripts/setup-profile-token.sh lead"; got != want {
		t.Errorf("repair = %q, want %q", got, want)
	}
	if got := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); got != "" {
		t.Errorf("a refused profile must export nothing, got %q", got)
	}
}

// --print-prompt must never reach enforceLeadProfile: `loom lead --print-prompt
// > .../profiles/lead/claude/CLAUDE.md` is how a lead profile's CLAUDE.md is
// generated, so gating that path on a verified profile is a bootstrap deadlock
// — no profile could ever be created. The fixture here is a profile that CANNOT
// verify: if enforcement ran, it would os.Exit(1) and take this test binary
// with it, which is a louder failure than any assertion.
func TestRunLeadPrintPromptSkipsProfileEnforcement(t *testing.T) {
	runtimeDir := isolateLeadWorkspace(t)
	clearProfileEnv(t)
	dir := writeLeadProfile(t, runtimeDir, "lead", fakeHarnessVersion, map[string]string{
		"CLAUDE.md": "house rules\n",
	})
	// Tamper after the manifest is written, so the fingerprint no longer matches.
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("tampered\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(supervisor.ProfileEnvVar("claude"), dir)
	supervisor.ResetHarnessVersionCache()
	t.Cleanup(supervisor.ResetHarnessVersionCache)

	if err := verifyLeadProfile(runtimeDir, dir, "claude"); err == nil {
		t.Fatal("fixture verifies, so reaching enforcement would not be visible here")
	}

	output, _ := capturePrintPromptRun(t, "")
	if !strings.Contains(output, "INTERACTIVE MODE: Project Lead") {
		t.Fatalf("--print-prompt did not print the built-in prompt: %q", output)
	}
}
