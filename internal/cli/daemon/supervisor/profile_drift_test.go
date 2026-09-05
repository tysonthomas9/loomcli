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

// writeManifestFor writes a manifest with an EMPTY file list into dir, which is
// what a codex root looks like: everything under it is harness-owned and
// mutates at runtime, so the allowlist names nothing.
func writeManifestFor(t *testing.T, dir, version string) {
	t.Helper()
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
}

// TestProfileHarnessEnv_PatchDriftBootsWithWarning is the whole point of this
// file: a `claude` auto-update within a major must not stop the fleet. Before
// this policy every agent claimed a task, failed to boot, backed off and
// re-claimed — measured four times in six days.
func TestProfileHarnessEnv_PatchDriftBootsWithWarning(t *testing.T) {
	ResetProfileDrifts()
	t.Cleanup(ResetProfileDrifts)
	stubHarnessVersion(t, map[string]string{"claude": "2.1.251 (Claude Code)"})
	projectDir := t.TempDir()
	dir := writeProfile(t, projectDir, "worker", "2.1.250 (Claude Code)", map[string]string{
		"settings.json": `{"model":"opus"}`,
	})

	got, env, err := ProfileHarnessEnv(projectDir, "worker", "claude")
	if err != nil {
		t.Fatalf("patch drift must boot, got %v", err)
	}
	if got != dir {
		t.Errorf("dir = %q, want %q", got, dir)
	}
	if want := "CLAUDE_CONFIG_DIR=" + dir; len(env) == 0 || env[0] != want {
		t.Fatalf("env = %v, want first assignment %q", env, want)
	}

	drifts := ProfileDrifts()
	if len(drifts) != 1 {
		t.Fatalf("want the drift recorded, got %+v", drifts)
	}
	d := drifts[0]
	if d.Dir != dir || d.Binary != "claude" ||
		d.Manifest != "2.1.250 (Claude Code)" || d.Observed != "2.1.251 (Claude Code)" {
		t.Errorf("recorded drift = %+v, want both versions and the dir", d)
	}
	if d.Count != 1 {
		t.Errorf("Count = %d, want 1", d.Count)
	}
	if d.FirstAt.IsZero() {
		t.Error("FirstAt must be stamped")
	}
}

// TestProfileHarnessEnv_CodexMinorDriftBoots pins the other version shape in
// the fleet. lead's codex root is a 0.x pin that moves every few weeks; a
// MAJOR.MINOR gate would refuse lead's boot on every ordinary codex release.
func TestProfileHarnessEnv_CodexMinorDriftBoots(t *testing.T) {
	ResetProfileDrifts()
	t.Cleanup(ResetProfileDrifts)
	stubHarnessVersion(t, map[string]string{"codex": "codex-cli 0.149.1"})
	projectDir := t.TempDir()
	dir := filepath.Join(projectDir, ".loom", AgentProfilesDirName, "lead", "codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeManifestFor(t, dir, "codex-cli 0.144.5")
	writeCodexAuth(t, dir, "rt-lead")

	if _, _, err := ProfileHarnessEnv(projectDir, "lead", "codex"); err != nil {
		t.Fatalf("codex minor drift must boot, got %v", err)
	}
	if len(ProfileDrifts()) != 1 {
		t.Fatalf("want the codex drift recorded, got %+v", ProfileDrifts())
	}
}

// TestProfileHarnessEnv_FingerprintMismatchBeatsDrift: content that is not what
// was provisioned refuses even when the version also drifted. agentprofile.Verify
// checks the fingerprint first, so the softening must be unreachable here.
func TestProfileHarnessEnv_FingerprintMismatchBeatsDrift(t *testing.T) {
	ResetProfileDrifts()
	t.Cleanup(ResetProfileDrifts)
	stubHarnessVersion(t, map[string]string{"claude": "2.1.251 (Claude Code)"})
	projectDir := t.TempDir()
	dir := writeProfile(t, projectDir, "worker", "2.1.250 (Claude Code)", map[string]string{
		"settings.json": `{"model":"opus"}`,
	})
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"model":"haiku"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := ProfileHarnessEnv(projectDir, "worker", "claude")
	if !errors.Is(err, ErrProfileFingerprintMismatch) {
		t.Fatalf("want fingerprint mismatch, got %v", err)
	}
	if errors.Is(err, ErrProfileVersionDrift) {
		t.Error("a fingerprint mismatch must never be reported as mere drift")
	}
	if len(ProfileDrifts()) != 0 {
		t.Errorf("a refused boot must record no drift: %+v", ProfileDrifts())
	}
}

// TestProfileHarnessEnv_UnparseableVersionRefuses is the fail-closed edge: a
// version shape neither side recognizes is not "probably a patch bump".
func TestProfileHarnessEnv_UnparseableVersionRefuses(t *testing.T) {
	ResetProfileDrifts()
	t.Cleanup(ResetProfileDrifts)
	stubHarnessVersion(t, map[string]string{"claude": "nightly-build"})
	projectDir := t.TempDir()
	writeProfile(t, projectDir, "worker", "2.1.250 (Claude Code)", map[string]string{
		"settings.json": `{"model":"opus"}`,
	})

	_, _, err := ProfileHarnessEnv(projectDir, "worker", "claude")
	if !errors.Is(err, ErrProfileVersionDrift) {
		t.Fatalf("want a refusal on an unparseable version, got %v", err)
	}
	if len(ProfileDrifts()) != 0 {
		t.Errorf("a refused boot must record no drift: %+v", ProfileDrifts())
	}
}

// TestProfileHarnessEnv_MissingManifestStillRefuses: the softening is for
// version drift only. Falling back to ~/.claude is the leak profiles close.
func TestProfileHarnessEnv_MissingManifestStillRefuses(t *testing.T) {
	ResetProfileDrifts()
	t.Cleanup(ResetProfileDrifts)
	stubHarnessVersion(t, map[string]string{"claude": "2.1.251 (Claude Code)"})
	projectDir := t.TempDir()
	dir := filepath.Join(projectDir, ".loom", AgentProfilesDirName, "worker", "claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, _, err := ProfileHarnessEnv(projectDir, "worker", "claude"); !errors.Is(err, ErrProfileManifestMissing) {
		t.Fatalf("want missing-manifest refusal, got %v", err)
	}
}

// TestProfileHarnessEnv_DriftClearedByReBless: after `loom doctor --fix`
// rewrites the pin, the next boot verifies clean and the recorded condition
// must go with it. A status line that outlives its condition is worse than none.
func TestProfileHarnessEnv_DriftClearedByReBless(t *testing.T) {
	ResetProfileDrifts()
	t.Cleanup(ResetProfileDrifts)
	stubHarnessVersion(t, map[string]string{"claude": "2.1.251 (Claude Code)"})
	projectDir := t.TempDir()
	dir := writeProfile(t, projectDir, "worker", "2.1.250 (Claude Code)", map[string]string{
		"settings.json": `{"model":"opus"}`,
	})
	if _, _, err := ProfileHarnessEnv(projectDir, "worker", "claude"); err != nil {
		t.Fatalf("patch drift must boot, got %v", err)
	}
	if len(ProfileDrifts()) != 1 {
		t.Fatalf("want the drift recorded first, got %+v", ProfileDrifts())
	}

	if err := agentprofile.Bless(dir, "2.1.251 (Claude Code)"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ProfileHarnessEnv(projectDir, "worker", "claude"); err != nil {
		t.Fatalf("re-blessed profile must verify, got %v", err)
	}
	if got := ProfileDrifts(); len(got) != 0 {
		t.Errorf("a re-blessed profile must clear its drift, got %+v", got)
	}
}

// TestRecordProfileDrift_WarnsOncePerDrift: twelve agents in a restart storm
// must produce ONE warning line, not one per boot.
func TestRecordProfileDrift_WarnsOncePerDrift(t *testing.T) {
	ResetProfileDrifts()
	t.Cleanup(ResetProfileDrifts)

	if !recordProfileDrift("/p/worker/claude", "claude", "2.1.250", "2.1.251") {
		t.Fatal("first observation must report first=true")
	}
	for i := 0; i < 11; i++ {
		if recordProfileDrift("/p/worker/claude", "claude", "2.1.250", "2.1.251") {
			t.Fatalf("repeat observation %d must report first=false", i+2)
		}
	}
	drifts := ProfileDrifts()
	if len(drifts) != 1 || drifts[0].Count != 12 {
		t.Fatalf("want one drift with Count=12, got %+v", drifts)
	}

	// A second upgrade is a new condition: warn again, and count it from one.
	if !recordProfileDrift("/p/worker/claude", "claude", "2.1.250", "2.1.252") {
		t.Error("a changed observed version must warn again")
	}
	drifts = ProfileDrifts()
	if len(drifts) != 1 || drifts[0].Observed != "2.1.252" || drifts[0].Count != 1 {
		t.Fatalf("want the new drift with Count=1, got %+v", drifts)
	}
}

func TestProfileDrifts_SnapshotIsACopy(t *testing.T) {
	ResetProfileDrifts()
	t.Cleanup(ResetProfileDrifts)
	recordProfileDrift("/p/a/claude", "claude", "2.1.250", "2.1.251")

	snap := ProfileDrifts()
	snap[0].Count = 99
	if again := ProfileDrifts(); again[0].Count != 1 {
		t.Errorf("mutating a snapshot must not reach the record, got Count=%d", again[0].Count)
	}
}

// TestCheckProfileManifest_ExportedWrapperSharesThePolicy: `loom lead` reaches
// the boot policy through this wrapper, and must get the same answer the
// supervisor's own spawn path gets.
func TestCheckProfileManifest_ExportedWrapperSharesThePolicy(t *testing.T) {
	ResetProfileDrifts()
	t.Cleanup(ResetProfileDrifts)
	stubHarnessVersion(t, map[string]string{"claude": "2.1.251 (Claude Code)"})
	projectDir := t.TempDir()
	dir := writeProfile(t, projectDir, "lead", "2.1.250 (Claude Code)", map[string]string{
		"settings.json": `{"model":"opus"}`,
	})

	if err := CheckProfileManifest(dir, "claude"); err != nil {
		t.Fatalf("lead must boot under patch drift, got %v", err)
	}
	// The strict view is still available and still reports the drift, which is
	// what `loom doctor` relies on.
	err := VerifyProfileManifest(dir, "claude")
	if !errors.Is(err, ErrProfileVersionDrift) {
		t.Fatalf("VerifyProfileManifest must stay strict, got %v", err)
	}
	if strings.Contains(err.Error(), "major version change") {
		t.Error("the strict path must not acquire the boot policy's wording")
	}
}
