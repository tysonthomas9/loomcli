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
	bin := t.TempDir()
	script := "#!/bin/sh\necho '" + fakeHarnessVersion + "'\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o700); err != nil { //nolint:gosec // G306: test fixture must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// writeLeadProfile materializes a profile root under the workspace's
// agent-profiles tree and writes a manifest matching it.
func writeLeadProfile(t *testing.T, runtimeDir, agent, version string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(runtimeDir, ".loom", agentprofile.DirName, agent, "claude")
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

func TestVerifyLeadProfile_UnsetConfigDirIsNotVerified(t *testing.T) {
	// No stub on PATH: an unprofiled lead must not even probe the harness.
	if err := verifyLeadProfile(t.TempDir(), ""); err != nil {
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
	if err := verifyLeadProfile(runtimeDir, outside); err != nil {
		t.Fatalf("config dir outside the profile root must stay silent, got %v", err)
	}
	// The agent-profiles root itself is not a profile either.
	root := filepath.Join(runtimeDir, ".loom", agentprofile.DirName)
	if err := verifyLeadProfile(runtimeDir, root); err != nil {
		t.Fatalf("profile root itself must stay silent, got %v", err)
	}
}

func TestVerifyLeadProfile_DriftedProfileRefusesWithDoctorRepair(t *testing.T) {
	stubClaudeOnPath(t)
	runtimeDir := t.TempDir()
	dir := writeLeadProfile(t, runtimeDir, "lead", "2.1.235 (Claude Code)", map[string]string{
		"settings.json": `{"model":"opus"}`,
	})

	err := verifyLeadProfile(runtimeDir, dir)
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

	err := verifyLeadProfile(runtimeDir, dir)
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

	if err := verifyLeadProfile(runtimeDir, dir); err != nil {
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

	err := verifyLeadProfile(runtimeDir, dir)
	if !errors.Is(err, supervisor.ErrProfileManifestMissing) {
		t.Fatalf("want missing manifest, got %v", err)
	}
	if got := leadProfileRepair(err, dir); got != "scripts/provision-profile.sh lead" {
		t.Fatalf("repair = %q, want the provisioner", got)
	}
}

func TestVerifyLeadProfile_EmptyRuntimeDirVerifiesNothing(t *testing.T) {
	if err := verifyLeadProfile("", "/somewhere/.loom/agent-profiles/lead/claude"); err != nil {
		t.Fatalf("unresolvable workspace root must stay silent, got %v", err)
	}
}
