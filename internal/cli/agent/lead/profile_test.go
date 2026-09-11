package lead

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/agentprofile"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/harnessprofile"
)

// The resolve-verify-export policy itself is covered in
// internal/harnessprofile, which owns it and is shared with the supervisor's
// spawn path and with `loom agent`. What is lead-specific — and therefore
// tested here — is the wiring: which workspace root, and which agent id.

// fakeHarnessVersion is what the stub `claude` on PATH reports.
const fakeHarnessVersion = "9.9.9 (Claude Code)"

// clearProfileEnv unsets both harness config roots so a test starts from the
// bare `loom lead` case regardless of the operator environment running it.
func clearProfileEnv(t *testing.T) {
	t.Helper()
	for _, harness := range harnessprofile.ProfileHarnesses() {
		t.Setenv(harnessprofile.ProfileEnvVar(harness), "")
		if err := os.Unsetenv(harnessprofile.ProfileEnvVar(harness)); err != nil {
			t.Fatal(err)
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

// stubClaudeOnPath puts a `claude --version` shim first on PATH, so the check
// runs against a known version instead of whatever the machine has installed.
func stubClaudeOnPath(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	// Startup enforcement probes the real binary, so a cached version from an
	// earlier test would decide this one's outcome.
	harnessprofile.ResetHarnessVersionCache()
	t.Cleanup(harnessprofile.ResetHarnessVersionCache)
	script := "#!/bin/sh\necho '" + fakeHarnessVersion + "'\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o700); err != nil { //nolint:gosec // G306: test fixture must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// writeLeadProfile materializes a claude profile root under the workspace's
// agent-profiles tree and writes a manifest matching it.
func writeLeadProfile(t *testing.T, runtimeDir, agent string, files map[string]string) string {
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
		"harness_version": fakeHarnessVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, agentprofile.ManifestName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// enforceLeadProfile must resolve the workspace root and the lead's own agent
// id, not some other agent's: a lead that injected "lead"'s profile while
// running as "nova" would authenticate as the wrong identity.
func TestEnforceLeadProfile_InjectsTheResolvedLeadAgentsProfile(t *testing.T) {
	clearProfileEnv(t)
	stubClaudeOnPath(t)
	runtimeDir := isolateLeadWorkspace(t)
	t.Setenv(envAgentName, "nova")
	nova := writeLeadProfile(t, runtimeDir, "nova", map[string]string{"settings.json": `{"model":"opus"}`})
	writeLeadProfile(t, runtimeDir, "lead", map[string]string{"settings.json": `{"model":"other"}`})

	enforceLeadProfile()

	if got := os.Getenv("CLAUDE_CONFIG_DIR"); got != nova {
		t.Fatalf("CLAUDE_CONFIG_DIR = %q, want the resolved lead agent's profile %q", got, nova)
	}
}

// The unprofiled lead is supported and must stay silent — this is also the
// path every other test in this package takes through isolateLeadWorkspace.
func TestEnforceLeadProfile_NoProfileOnDiskInjectsNothing(t *testing.T) {
	clearProfileEnv(t)
	isolateLeadWorkspace(t)

	enforceLeadProfile()

	if got := os.Getenv("CLAUDE_CONFIG_DIR"); got != "" {
		t.Fatalf("CLAUDE_CONFIG_DIR = %q, want unset", got)
	}
}
