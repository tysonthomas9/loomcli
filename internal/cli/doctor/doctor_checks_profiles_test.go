package doctor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agentprofile"
)

// stageProfileWorkspace points the workspace runtime dir at a temp dir and
// stubs the harness probe, so no test in this file needs `claude` on PATH.
func stageProfileWorkspace(t *testing.T, reported string) string {
	t.Helper()
	runtimeDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	ResetWorkspaceRuntimeDirCache()
	t.Cleanup(ResetWorkspaceRuntimeDirCache)

	prev := probeVersion
	probeVersion = func(binary string, _ time.Duration) string { return reported }
	t.Cleanup(func() { probeVersion = prev })

	return runtimeDir
}

// stageProfile provisions one agent's claude profile: one content file plus a
// manifest whose fingerprint is computed the same way the verifier computes it.
func stageProfile(t *testing.T, runtimeDir, agent, pinnedVersion string) string {
	t.Helper()
	dir := filepath.Join(runtimeDir, ".loom", "agent-profiles", agent, "claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir profile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"agent":"`+agent+`"}`), 0o644); err != nil {
		t.Fatalf("write settings.json: %v", err)
	}
	files := []string{"settings.json"}
	sum, err := agentprofile.Fingerprint(dir, files)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	writeManifest(t, dir, agentprofile.Manifest{Files: files, Fingerprint: sum, HarnessVersion: pinnedVersion})
	// A provisioned claude profile carries its own minted credential; doctor
	// now reports one that does not. It is outside the manifest's file list, so
	// it does not enter the fingerprint above.
	writeProfileToken(t, dir, "sk-ant-oat01-fixture")
	return dir
}

// writeProfileToken writes (or, given "", removes) a profile's oauth-token.
func writeProfileToken(t *testing.T, dir, token string) {
	t.Helper()
	path := filepath.Join(dir, "oauth-token")
	if token == "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Fatalf("remove oauth-token: %v", err)
		}
		return
	}
	if err := os.WriteFile(path, []byte(token), 0o600); err != nil {
		t.Fatalf("write oauth-token: %v", err)
	}
}

// stageCodexProfile provisions a codex profile, which has no credential file of
// its own and must never be reported for lacking one.
func stageCodexProfile(t *testing.T, runtimeDir, agent, pinnedVersion string) string {
	t.Helper()
	dir := filepath.Join(runtimeDir, ".loom", "agent-profiles", agent, "codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir codex profile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("# "+agent+"\n"), 0o644); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	files := []string{"config.toml"}
	sum, err := agentprofile.Fingerprint(dir, files)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	writeManifest(t, dir, agentprofile.Manifest{Files: files, Fingerprint: sum, HarnessVersion: pinnedVersion})
	return dir
}

// agentprofile.Verify never looks at the credential — the token is deliberately
// outside the manifest — so a profile that was never minted verified clean and
// doctor reported the whole fleet green while those agents died on their first
// API call. worker-2 and worker-3 were exactly this on disk. The repair is the
// interactive minting script, not the provisioner.
func TestCheckAgentProfiles_MissingOAuthTokenFails(t *testing.T) {
	const version = "2.1.237 (Claude Code)"
	runtimeDir := stageProfileWorkspace(t, version)
	dir := stageProfile(t, runtimeDir, "worker-2", version)
	writeProfileToken(t, dir, "")
	stageProfile(t, runtimeDir, "planner", version)

	got := checkAgentProfiles()
	if got.Status != StatusFail {
		t.Fatalf("Status = %v, want StatusFail (an identity-less profile is not green)", got.Status)
	}
	if !strings.Contains(got.Detail, "worker-2") {
		t.Errorf("detail must name the profile, got:\n%s", got.Detail)
	}
	if !strings.Contains(got.Detail, "no oauth-token: profile was never minted") {
		t.Errorf("detail must state the fault, got:\n%s", got.Detail)
	}
	if !strings.Contains(got.Detail, "scripts/setup-profile-token.sh worker-2") {
		t.Errorf("detail must name the minting repair, got:\n%s", got.Detail)
	}
	if strings.Contains(got.Detail, "planner") {
		t.Errorf("a minted profile must not be reported, got:\n%s", got.Detail)
	}
}

// An empty token is a broken minting run rather than an absent one, so it keeps
// the provisioner repair and must not be laundered into the missing bucket.
func TestCheckAgentProfiles_EmptyOAuthTokenFails(t *testing.T) {
	const version = "2.1.237 (Claude Code)"
	runtimeDir := stageProfileWorkspace(t, version)
	dir := stageProfile(t, runtimeDir, "worker-3", version)
	writeProfileToken(t, dir, "\n")

	got := checkAgentProfiles()
	if got.Status != StatusFail {
		t.Fatalf("Status = %v, want StatusFail", got.Status)
	}
	if !strings.Contains(got.Detail, "oauth-token unusable") {
		t.Errorf("detail must state the fault, got:\n%s", got.Detail)
	}
	if strings.Contains(got.Detail, "setup-profile-token.sh") {
		t.Errorf("a broken token is re-provisioned, not re-minted, got:\n%s", got.Detail)
	}
}

// codex has no credential file at all, so the probe must stay silent about it.
// Without this, the check reports every codex profile in the fleet as broken.
func TestCheckAgentProfiles_CodexNeedsNoToken(t *testing.T) {
	const version = "codex-cli 0.147.0"
	runtimeDir := stageProfileWorkspace(t, version)
	stageCodexProfile(t, runtimeDir, "worker", version)

	got := checkAgentProfiles()
	if got.Status != StatusPass {
		t.Fatalf("Status = %v, want StatusPass; detail:\n%s", got.Status, got.Detail)
	}
}

func writeManifest(t *testing.T, dir string, m agentprofile.Manifest) {
	t.Helper()
	raw, err := json.MarshalIndent(m, "", " ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, agentprofile.ManifestName), raw, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

// breakFingerprint mutates a manifested file so the profile's content no longer
// matches its manifest — the "someone edited the profile" fault, which --fix
// must refuse to launder.
func breakFingerprint(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"agent":"tampered"}`), 0o644); err != nil {
		t.Fatalf("tamper settings.json: %v", err)
	}
}

func readManifest(t *testing.T, dir string) agentprofile.Manifest {
	t.Helper()
	m, err := agentprofile.LoadManifest(dir)
	if err != nil {
		t.Fatalf("LoadManifest(%s): %v", dir, err)
	}
	return m
}

func readManifestBytes(t *testing.T, dir string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, agentprofile.ManifestName))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	return raw
}

// TestCheckAgentProfiles_NoProfilesIsSilent is the "changes nothing for a fleet
// that does not use profiles" contract: not a pass line, no line at all.
func TestCheckAgentProfiles_NoProfilesIsSilent(t *testing.T) {
	stageProfileWorkspace(t, "2.1.237 (Claude Code)")

	res := checkAgentProfiles()
	if res.Name != "" {
		t.Fatalf("result = %+v, want zero CheckResult (skipped)", res)
	}
}

// TestCheckAgentProfiles_AgentDirWithoutHarnessRootIsSkipped guards against a
// half-created directory being reported as a broken profile.
func TestCheckAgentProfiles_AgentDirWithoutHarnessRootIsSkipped(t *testing.T) {
	runtimeDir := stageProfileWorkspace(t, "2.1.237 (Claude Code)")
	if err := os.MkdirAll(filepath.Join(runtimeDir, ".loom", "agent-profiles", "halfway"), 0o755); err != nil {
		t.Fatal(err)
	}

	res := checkAgentProfiles()
	if res.Name != "" {
		t.Fatalf("result = %+v, want zero CheckResult (skipped)", res)
	}
}

func TestCheckAgentProfiles_AllCurrent(t *testing.T) {
	const version = "2.1.237 (Claude Code)"
	runtimeDir := stageProfileWorkspace(t, version)
	stageProfile(t, runtimeDir, "observer", version)
	stageProfile(t, runtimeDir, "lead", version)

	res := checkAgentProfiles()
	if res.Status != StatusPass {
		t.Fatalf("status = %v, want pass (summary=%q detail=%q)", res.Status, res.Summary, res.Detail)
	}
	if res.Name != "agent_profiles" {
		t.Errorf("name = %q, want agent_profiles", res.Name)
	}
	if !strings.Contains(res.Summary, "2 agent profile(s) verified") || !strings.Contains(res.Summary, version) {
		t.Errorf("summary = %q, want count and version", res.Summary)
	}
}

// TestCheckAgentProfiles_DriftFails covers the reason the check exists: a
// harness auto-update silently bricks every spawn, and the operator needs both
// version strings and the repair in the output.
func TestCheckAgentProfiles_DriftFails(t *testing.T) {
	runtimeDir := stageProfileWorkspace(t, "2.1.237 (Claude Code)")
	stageProfile(t, runtimeDir, "observer", "2.1.236 (Claude Code)")
	stageProfile(t, runtimeDir, "lead", "2.1.237 (Claude Code)")

	res := checkAgentProfiles()
	if res.Status != StatusFail {
		t.Fatalf("status = %v, want fail (summary=%q)", res.Status, res.Summary)
	}
	// The summary says UNVERIFIED, not "bricked": since the spawn path softened
	// same-major drift these agents boot. The check still FAILS, because an
	// unverified fleet is a condition the operator must clear.
	if !strings.Contains(res.Summary, "1 of 2") || !strings.Contains(res.Summary, "running UNVERIFIED") {
		t.Errorf("summary = %q, want unverified summary naming 1 of 2", res.Summary)
	}
	if !strings.Contains(res.Detail, "drift is no longer fatal") {
		t.Errorf("detail must say agents boot with a warning:\n%s", res.Detail)
	}
	for _, want := range []string{"observer", "2.1.236 (Claude Code)", "2.1.237 (Claude Code)", "loom doctor --fix"} {
		if !strings.Contains(res.Detail, want) {
			t.Errorf("detail missing %q:\n%s", want, res.Detail)
		}
	}
	if strings.Contains(res.Detail, "lead") {
		t.Errorf("detail names the healthy profile:\n%s", res.Detail)
	}
}

// TestCheckAgentProfiles_FixReBlessesDrift is the one-command repair: exit 0 at
// warn, manifest updated on disk, and a second run clean.
func TestCheckAgentProfiles_FixReBlessesDrift(t *testing.T) {
	const version = "2.1.237 (Claude Code)"
	runtimeDir := stageProfileWorkspace(t, version)
	dir := stageProfile(t, runtimeDir, "observer", "2.1.236 (Claude Code)")

	doctorFix = true
	t.Cleanup(func() { doctorFix = false })

	res := checkAgentProfiles()
	if res.Status != StatusWarn {
		t.Fatalf("status = %v, want warn (summary=%q detail=%q)", res.Status, res.Summary, res.Detail)
	}
	if !strings.Contains(res.Summary, "Re-blessed 1") || !strings.Contains(res.Summary, version) {
		t.Errorf("summary = %q, want re-blessed count and version", res.Summary)
	}
	if !strings.Contains(res.Detail, "observer") {
		t.Errorf("detail = %q, want the re-blessed agent named", res.Detail)
	}
	if got := readManifest(t, dir).HarnessVersion; got != version {
		t.Errorf("manifest harness_version = %q, want %q", got, version)
	}

	// Second run: nothing left to fix.
	if res2 := checkAgentProfiles(); res2.Status != StatusPass {
		t.Errorf("second run status = %v, want pass (summary=%q)", res2.Status, res2.Summary)
	}
}

// TestCheckAgentProfiles_FixRefusesFingerprintMismatch pins the safety property
// that makes --fix defensible: it cannot bless content it has not verified.
func TestCheckAgentProfiles_FixRefusesFingerprintMismatch(t *testing.T) {
	const version = "2.1.237 (Claude Code)"
	runtimeDir := stageProfileWorkspace(t, version)
	dir := stageProfile(t, runtimeDir, "critic", version)
	breakFingerprint(t, dir)
	before := readManifestBytes(t, dir)

	doctorFix = true
	t.Cleanup(func() { doctorFix = false })

	res := checkAgentProfiles()
	if res.Status != StatusFail {
		t.Fatalf("status = %v, want fail (summary=%q detail=%q)", res.Status, res.Summary, res.Detail)
	}
	if !strings.Contains(res.Detail, "fingerprint mismatch") ||
		!strings.Contains(res.Detail, "provision-profile.sh critic") {
		t.Errorf("detail missing mismatch reason or provisioner repair:\n%s", res.Detail)
	}
	if got := readManifestBytes(t, dir); string(got) != string(before) {
		t.Errorf("manifest rewritten under --fix:\n%s", got)
	}
}

// TestCheckAgentProfiles_FixMixedDriftAndMismatch is the realistic fleet: one
// profile is repairable and one is not, and the repairable one must not be held
// hostage by the other.
func TestCheckAgentProfiles_FixMixedDriftAndMismatch(t *testing.T) {
	const version = "2.1.237 (Claude Code)"
	runtimeDir := stageProfileWorkspace(t, version)
	drifted := stageProfile(t, runtimeDir, "observer", "2.1.236 (Claude Code)")
	mismatched := stageProfile(t, runtimeDir, "critic", version)
	breakFingerprint(t, mismatched)
	mismatchedBefore := readManifestBytes(t, mismatched)

	doctorFix = true
	t.Cleanup(func() { doctorFix = false })

	res := checkAgentProfiles()
	if res.Status != StatusFail {
		t.Fatalf("status = %v, want fail (summary=%q)", res.Status, res.Summary)
	}
	if !strings.Contains(res.Summary, "1 of 2") {
		t.Errorf("summary = %q, want 1 of 2 failing", res.Summary)
	}
	if got := readManifest(t, drifted).HarnessVersion; got != version {
		t.Errorf("drifted manifest harness_version = %q, want re-blessed %q", got, version)
	}
	if got := readManifestBytes(t, mismatched); string(got) != string(mismatchedBefore) {
		t.Errorf("mismatched manifest rewritten under --fix:\n%s", got)
	}
	if !strings.Contains(res.Detail, "re-blessed: observer") {
		t.Errorf("detail does not report the successful re-bless:\n%s", res.Detail)
	}
	if !strings.Contains(res.Detail, "critic") {
		t.Errorf("detail does not report the unfixed profile:\n%s", res.Detail)
	}
}

// TestCheckAgentProfiles_MissingManifestFails: a directory that was never
// provisioned is a fault with its own repair, and --fix must not create a
// manifest for it.
func TestCheckAgentProfiles_MissingManifestFails(t *testing.T) {
	runtimeDir := stageProfileWorkspace(t, "2.1.237 (Claude Code)")
	dir := filepath.Join(runtimeDir, ".loom", "agent-profiles", "decomposer", "claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	doctorFix = true
	t.Cleanup(func() { doctorFix = false })

	res := checkAgentProfiles()
	if res.Status != StatusFail {
		t.Fatalf("status = %v, want fail (summary=%q)", res.Status, res.Summary)
	}
	if !strings.Contains(res.Detail, "never provisioned") {
		t.Errorf("detail = %q, want the never-provisioned reason", res.Detail)
	}
	if _, err := os.Stat(filepath.Join(dir, agentprofile.ManifestName)); !os.IsNotExist(err) {
		t.Errorf("--fix created a manifest for an unprovisioned profile (err=%v)", err)
	}
}

// TestCheckAgentProfiles_UnknownVersionWarns: no harness binary on PATH is not
// evidence of drift, so it must not fail the command.
func TestCheckAgentProfiles_UnknownVersionWarns(t *testing.T) {
	runtimeDir := stageProfileWorkspace(t, "") // probe produces nothing
	stageProfile(t, runtimeDir, "observer", "2.1.236 (Claude Code)")

	res := checkAgentProfiles()
	if res.Status != StatusWarn {
		t.Fatalf("status = %v, want warn (summary=%q)", res.Status, res.Summary)
	}
	if !strings.Contains(res.Summary, "claude --version produced nothing") {
		t.Errorf("summary = %q, want the unknown-version summary", res.Summary)
	}
}

// TestCheckAgentProfiles_ProbesOncePerHarness: four profiles must cost one
// node startup, not four.
func TestCheckAgentProfiles_ProbesOncePerHarness(t *testing.T) {
	const version = "2.1.237 (Claude Code)"
	runtimeDir := stageProfileWorkspace(t, version)
	for _, agent := range []string{"observer", "lead", "critic", "decomposer"} {
		stageProfile(t, runtimeDir, agent, version)
	}

	calls := 0
	prev := probeVersion
	probeVersion = func(binary string, _ time.Duration) string {
		calls++
		return version
	}
	t.Cleanup(func() { probeVersion = prev })

	if res := checkAgentProfiles(); res.Status != StatusPass {
		t.Fatalf("status = %v, want pass (summary=%q detail=%q)", res.Status, res.Summary, res.Detail)
	}
	if calls != 1 {
		t.Errorf("probe called %d times for 4 claude profiles, want 1", calls)
	}
}

// stageManagedProfile provisions an agent whose settings.json is MANAGED: the
// pristine baseline under .provisioned/ is byte-hashed, the live file is not.
func stageManagedProfile(t *testing.T, runtimeDir, agent, pinnedVersion, baseline, live string) string {
	t.Helper()
	dir := filepath.Join(runtimeDir, ".loom", "agent-profiles", agent, "claude")
	if err := os.MkdirAll(filepath.Join(dir, agentprofile.ProvisionedDirName), 0o755); err != nil {
		t.Fatalf("mkdir profile: %v", err)
	}
	baseRel := filepath.Join(agentprofile.ProvisionedDirName, "settings.json")
	if err := os.WriteFile(filepath.Join(dir, baseRel), []byte(baseline), 0o644); err != nil {
		t.Fatalf("write baseline: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(live), 0o644); err != nil {
		t.Fatalf("write settings.json: %v", err)
	}
	files := []string{baseRel}
	sum, err := agentprofile.Fingerprint(dir, files)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	writeManifest(t, dir, agentprofile.Manifest{
		Files:          files,
		Managed:        []string{"settings.json"},
		Fingerprint:    sum,
		HarnessVersion: pinnedVersion,
	})
	// A managed profile is otherwise VALID: since PUPPET-275 a profile with no
	// oauth-token is refused before its managed content is ever compared, so
	// without this the managed-content cases would assert on the token gate
	// instead of on what they are named for.
	writeProfileToken(t, dir, "sk-ant-oat01-test")
	return dir
}

// A live settings.json that only reordered its keys and gained a runtime key is
// the 2026-08-30 outage. `loom doctor` must report it as healthy.
func TestCheckAgentProfiles_ManagedReorderAndExtraKeyPasses(t *testing.T) {
	runtimeDir := stageProfileWorkspace(t, "2.1.240 (Claude Code)")
	stageManagedProfile(t, runtimeDir, "worker", "2.1.240 (Claude Code)",
		`{"permissions":{"defaultMode":"auto"},"disableRemoteControl":true}`,
		`{"disableRemoteControl":true,"enabledPlugins":{"x@y":true},"permissions":{"defaultMode":"auto"}}`)

	res := checkAgentProfiles()
	if res.Status != StatusPass {
		t.Fatalf("status = %v, want StatusPass\n%s\n%s", res.Status, res.Summary, res.Detail)
	}
}

// A changed provisioned key fails the check, and the report names the path so
// the operator knows what to look at.
func TestCheckAgentProfiles_ManagedDriftFails(t *testing.T) {
	runtimeDir := stageProfileWorkspace(t, "2.1.240 (Claude Code)")
	stageManagedProfile(t, runtimeDir, "worker", "2.1.240 (Claude Code)",
		`{"permissions":{"defaultMode":"auto"}}`,
		`{"permissions":{"defaultMode":"plan"}}`)

	res := checkAgentProfiles()
	if res.Status != StatusFail {
		t.Fatalf("status = %v, want StatusFail", res.Status)
	}
	if !strings.Contains(res.Detail, "permissions.defaultMode") {
		t.Fatalf("detail does not name the diverging path:\n%s", res.Detail)
	}
	if !strings.Contains(res.Detail, "provision-profile.sh worker") {
		t.Fatalf("detail does not route to the provisioner:\n%s", res.Detail)
	}
}

// Edge 17: --fix must not "repair" managed drift. It only touches the drifted
// (version) bucket, and Bless re-verifies first — assert it rather than assume.
func TestCheckAgentProfiles_FixDoesNotRepairManagedDrift(t *testing.T) {
	runtimeDir := stageProfileWorkspace(t, "2.1.251 (Claude Code)")
	dir := stageManagedProfile(t, runtimeDir, "worker", "2.1.240 (Claude Code)",
		`{"permissions":{"defaultMode":"auto"}}`,
		`{"permissions":{"defaultMode":"plan"}}`)
	before := readManifestBytes(t, dir)

	doctorFix = true
	t.Cleanup(func() { doctorFix = false })

	res := checkAgentProfiles()
	if res.Status != StatusFail {
		t.Fatalf("status = %v, want StatusFail: --fix must not launder managed drift", res.Status)
	}
	if after := readManifestBytes(t, dir); string(after) != string(before) {
		t.Fatalf("--fix rewrote the manifest of a managed-drifted profile:\n%s", after)
	}
	if readManifest(t, dir).HarnessVersion != "2.1.240 (Claude Code)" {
		t.Fatal("--fix re-blessed a profile whose provisioned settings had changed")
	}
}
