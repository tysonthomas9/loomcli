package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
)

// stageBindingWorkspace stages a runtime dir with one provisioned claude
// profile root and clears every harness binding, so each test states its own.
// No harness binary is needed: this check never probes one.
func stageBindingWorkspace(t *testing.T) (runtimeDir, profileDir string) {
	t.Helper()
	runtimeDir = t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	ResetWorkspaceRuntimeDirCache()
	t.Cleanup(ResetWorkspaceRuntimeDirCache)

	for _, harness := range supervisor.ProfileHarnesses() {
		envVar := supervisor.ProfileEnvVar(harness)
		t.Setenv(envVar, "")
		if err := os.Unsetenv(envVar); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
	if err := os.Unsetenv("CLAUDE_CODE_OAUTH_TOKEN"); err != nil {
		t.Fatal(err)
	}

	profileDir = filepath.Join(runtimeDir, ".loom", "agent-profiles", "lead", "claude")
	if err := os.MkdirAll(profileDir, 0o750); err != nil {
		t.Fatal(err)
	}
	return runtimeDir, profileDir
}

func TestCheckLeadProfileBinding_UnsetIsPass(t *testing.T) {
	stageBindingWorkspace(t)

	got := checkLeadProfileBinding()
	if got.Status != StatusPass {
		t.Fatalf("status = %v, want pass (%s)", got.Status, got.Detail)
	}
	if !strings.Contains(got.Detail, "CLAUDE_CONFIG_DIR unset") {
		t.Errorf("detail must say the variable is unset, got %q", got.Detail)
	}
}

// The binding `loom lead` now refuses outright is the one that fails the
// check, so a wrapper script sees a non-zero exit.
func TestCheckLeadProfileBinding_RelativeRootFails(t *testing.T) {
	stageBindingWorkspace(t)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(".loom", "agent-profiles", "lead", "claude"))

	got := checkLeadProfileBinding()
	if got.Status != StatusFail {
		t.Fatalf("status = %v, want fail (%s)", got.Status, got.Detail)
	}
	if !strings.Contains(got.Detail, "is relative") || !strings.Contains(got.Detail, "absolute path") {
		t.Errorf("detail must name the fault and its repair, got %q", got.Detail)
	}
}

func TestCheckLeadProfileBinding_CanonicalRootIsPass(t *testing.T) {
	_, profileDir := stageBindingWorkspace(t)
	t.Setenv("CLAUDE_CONFIG_DIR", profileDir)

	got := checkLeadProfileBinding()
	if got.Status != StatusPass {
		t.Fatalf("status = %v, want pass (%s)", got.Status, got.Detail)
	}
	if !strings.Contains(got.Detail, "lead profile") {
		t.Errorf("detail must name the agent this shell binds to, got %q", got.Detail)
	}
}

// A second spelling of the same directory verifies fine — that is the fix —
// but the operator should still be told their export does not match what the
// workspace recorded.
func TestCheckLeadProfileBinding_NonCanonicalSpellingWarns(t *testing.T) {
	runtimeDir, _ := stageBindingWorkspace(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(runtimeDir, link); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(link, ".loom", "agent-profiles", "lead", "claude"))

	got := checkLeadProfileBinding()
	if got.Status != StatusWarn {
		t.Fatalf("status = %v, want warn (%s)", got.Status, got.Detail)
	}
	if !strings.Contains(got.Detail, "different spelling") {
		t.Errorf("detail must name the spelling mismatch, got %q", got.Detail)
	}
}

func TestCheckLeadProfileBinding_OutsideRootWarns(t *testing.T) {
	stageBindingWorkspace(t)
	outside := filepath.Join(t.TempDir(), ".claude")
	if err := os.MkdirAll(outside, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", outside)

	got := checkLeadProfileBinding()
	if got.Status != StatusWarn {
		t.Fatalf("status = %v, want warn (%s)", got.Status, got.Detail)
	}
	if !strings.Contains(got.Detail, "not a workspace profile") {
		t.Errorf("detail must say the root is not a workspace profile, got %q", got.Detail)
	}
}

func TestCheckLeadProfileBinding_UnsetTokenReportsKeychainFallback(t *testing.T) {
	_, profileDir := stageBindingWorkspace(t)
	writeProfileToken(t, profileDir, "sk-ant-oat01-kpfnu2ce")
	t.Setenv("CLAUDE_CONFIG_DIR", profileDir)

	got := checkLeadProfileBinding()
	if !strings.Contains(got.Detail, "fall back to the harness keychain") {
		t.Errorf("detail must report the fallback, got %q", got.Detail)
	}
}

func TestCheckLeadProfileBinding_MatchingTokenIsReportedAsMatching(t *testing.T) {
	_, profileDir := stageBindingWorkspace(t)
	writeProfileToken(t, profileDir, "sk-ant-oat01-kpfnu2ce")
	t.Setenv("CLAUDE_CONFIG_DIR", profileDir)
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "sk-ant-oat01-kpfnu2ce")

	got := checkLeadProfileBinding()
	if !strings.Contains(got.Detail, "matches the profile") {
		t.Errorf("detail must report the match, got %q", got.Detail)
	}
	assertNoTokenLeak(t, got, "sk-ant-oat01-kpfnu2ce")
}

// The whole point of the credential half is to say "these disagree" without
// ever becoming a way to read either value out of a terminal or a log.
func TestCheckLeadProfileBinding_DifferingTokenNeverLeaksEitherValue(t *testing.T) {
	_, profileDir := stageBindingWorkspace(t)
	writeProfileToken(t, profileDir, "sk-ant-oat01-zqxjw7vk")
	t.Setenv("CLAUDE_CONFIG_DIR", profileDir)
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "sk-ant-oat01-mbdrl4hg")

	got := checkLeadProfileBinding()
	if !strings.Contains(got.Detail, "authenticate as someone else") {
		t.Errorf("detail must report the disagreement, got %q", got.Detail)
	}
	assertNoTokenLeak(t, got, "sk-ant-oat01-zqxjw7vk", "sk-ant-oat01-mbdrl4hg")
}

func writeProfileToken(t *testing.T, dir, token string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "oauth-token"), []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}
}

// assertNoTokenLeak scans the rendered result for any 8-character window of a
// secret. Substrings, not equality: a truncated "prefix for readability" is
// exactly the well-meant change this guards against.
func assertNoTokenLeak(t *testing.T, got CheckResult, secrets ...string) {
	t.Helper()
	rendered := got.Summary + "\n" + got.Detail
	for _, secret := range secrets {
		for i := 0; i+8 <= len(secret); i++ {
			if strings.Contains(rendered, secret[i:i+8]) {
				t.Fatalf("rendered result leaks %q from a credential", secret[i:i+8])
			}
		}
	}
}
