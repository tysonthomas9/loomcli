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
	return dir
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
