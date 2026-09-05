package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const codexVersion = "codex-cli 0.147.0"

// writeCodexAuth writes (or, given an empty refresh token, removes) a codex
// root's own login. The two identity fields are separate parameters because
// the regression guard below turns on their being independent.
func writeCodexAuth(t *testing.T, dir, refreshToken, accountID string) {
	t.Helper()
	path := filepath.Join(dir, "auth.json")
	if refreshToken == "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Fatalf("remove auth.json: %v", err)
		}
		return
	}
	body := `{"tokens":{"id_token":"eyJhbGciOiJub25lIn0.e30.","access_token":"at-` + refreshToken +
		`","refresh_token":"` + refreshToken + `","account_id":"` + accountID +
		`"},"last_refresh":"2026-09-05T00:00:00Z"}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}
}

// writeHomeCodexAuth plants the operator's own ~/.codex/auth.json, the file a
// copied credential is most often copied FROM.
func writeHomeCodexAuth(t *testing.T, refreshToken, accountID string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir ~/.codex: %v", err)
	}
	t.Setenv("HOME", filepath.Dir(dir))
	writeCodexAuth(t, dir, refreshToken, accountID)
}

// The fault this whole check exists for: two roots holding the same
// refresh_token can only have got there by copying auth.json, and codex
// rotates that token, so the first root to refresh invalidates the other.
func TestCheckAgentProfiles_SharedCodexRefreshTokenFails(t *testing.T) {
	runtimeDir := stageProfileWorkspace(t, codexVersion)
	a := stageCodexProfile(t, runtimeDir, "worker-1", codexVersion)
	b := stageCodexProfile(t, runtimeDir, "worker-2", codexVersion)
	writeCodexAuth(t, a, "rt-cloned", "acct-a")
	writeCodexAuth(t, b, "rt-cloned", "acct-b")

	got := checkAgentProfiles()
	if got.Status != StatusFail {
		t.Fatalf("Status = %v, want StatusFail; detail:\n%s", got.Status, got.Detail)
	}
	for _, want := range []string{"worker-1", "worker-2", "identical refresh_token", "refresh_token_reused"} {
		if !strings.Contains(got.Detail, want) {
			t.Errorf("detail must contain %q, got:\n%s", want, got.Detail)
		}
	}
	if !strings.Contains(got.Detail, "codex login") || !strings.Contains(got.Detail, "never copy auth.json") {
		t.Errorf("detail must name the repair, got:\n%s", got.Detail)
	}
	// Fingerprints only: no byte of the credential may reach a report an
	// operator pastes into a ticket.
	if strings.Contains(got.Detail, "rt-cloned") {
		t.Errorf("report must never carry the credential, got:\n%s", got.Detail)
	}
}

// The regression guard, and the most important test in this file. Two
// independent `codex login`s against the same ChatGPT account produce the SAME
// account_id and DIFFERENT refresh tokens — that is the healthy end state this
// work is driving the fleet towards. Bucketing on account_id, as the original
// decision doc asked, would make `loom doctor` fail permanently on a correctly
// provisioned fleet.
func TestCheckAgentProfiles_SameAccountDifferentTokensIsHealthy(t *testing.T) {
	runtimeDir := stageProfileWorkspace(t, codexVersion)
	a := stageCodexProfile(t, runtimeDir, "worker-1", codexVersion)
	b := stageCodexProfile(t, runtimeDir, "worker-2", codexVersion)
	writeCodexAuth(t, a, "rt-one", "3fefd96f-shared-account")
	writeCodexAuth(t, b, "rt-two", "3fefd96f-shared-account")

	got := checkAgentProfiles()
	if got.Status != StatusPass {
		t.Fatalf("Status = %v, want StatusPass; detail:\n%s", got.Status, got.Detail)
	}
}

// ~/.codex is the operator's own file and the source most copies come from, so
// it takes part in the comparison — but it is never itself reported: it is not
// loom's to repair, and only the profile that copied it needs a new login.
func TestCheckAgentProfiles_SharingWithTheOperatorsHomeIsFlagged(t *testing.T) {
	runtimeDir := stageProfileWorkspace(t, codexVersion)
	dir := stageCodexProfile(t, runtimeDir, "worker-1", codexVersion)
	writeCodexAuth(t, dir, "rt-operator", "acct")
	writeHomeCodexAuth(t, "rt-operator", "acct")

	got := checkAgentProfiles()
	if got.Status != StatusFail {
		t.Fatalf("Status = %v, want StatusFail; detail:\n%s", got.Status, got.Detail)
	}
	if !strings.Contains(got.Detail, "~/.codex") {
		t.Errorf("detail must name the peer, got:\n%s", got.Detail)
	}
	if !strings.Contains(got.Summary, "1 of 1 agent profile(s) failed") {
		t.Errorf("only the profile is a fault, not the operator's own file, got: %s", got.Summary)
	}
}

// An operator's own ~/.codex that nothing copied is just another login. One
// identity is not a bucket.
func TestCheckAgentProfiles_UnsharedHomeCodexIsSilent(t *testing.T) {
	runtimeDir := stageProfileWorkspace(t, codexVersion)
	stageCodexProfile(t, runtimeDir, "worker-1", codexVersion)
	writeHomeCodexAuth(t, "rt-operator-only", "acct")

	got := checkAgentProfiles()
	if got.Status != StatusPass {
		t.Fatalf("Status = %v, want StatusPass; detail:\n%s", got.Status, got.Detail)
	}
}

// A root with no login is reported ONCE, by checkProfileCredential, with the
// login repair. The sharing pass skips what it cannot read rather than filing
// a second fault for the same directory.
func TestCheckAgentProfiles_MissingCodexLoginReportedOnce(t *testing.T) {
	runtimeDir := stageProfileWorkspace(t, codexVersion)
	dir := stageCodexProfile(t, runtimeDir, "worker-1", codexVersion)
	writeCodexAuth(t, dir, "", "")

	got := checkAgentProfiles()
	if got.Status != StatusFail {
		t.Fatalf("Status = %v, want StatusFail; detail:\n%s", got.Status, got.Detail)
	}
	if !strings.Contains(got.Detail, "profile has no codex login") {
		t.Errorf("detail must state the fault, got:\n%s", got.Detail)
	}
	if !strings.Contains(got.Detail, "CODEX_HOME="+dir+" codex login") {
		t.Errorf("detail must name the repair with the concrete directory, got:\n%s", got.Detail)
	}
	if !strings.Contains(got.Summary, "1 of 1 agent profile(s) failed") {
		t.Errorf("Summary = %q, want the profile counted exactly once", got.Summary)
	}
	if strings.Contains(got.Detail, "shared") {
		t.Errorf("an unreadable login must not also be reported as sharing, got:\n%s", got.Detail)
	}
}

// --fix re-blesses drift; it may never touch a credential, and no automated
// path may drive an interactive login. A shared credential therefore survives
// --fix and keeps the check failing.
func TestCheckAgentProfiles_FixDoesNotClearSharedCodexCredential(t *testing.T) {
	runtimeDir := stageProfileWorkspace(t, codexVersion)
	a := stageCodexProfile(t, runtimeDir, "worker-1", codexVersion)
	b := stageCodexProfile(t, runtimeDir, "worker-2", codexVersion)
	writeCodexAuth(t, a, "rt-cloned", "acct")
	writeCodexAuth(t, b, "rt-cloned", "acct")

	doctorFix = true
	t.Cleanup(func() { doctorFix = false })

	got := checkAgentProfiles()
	if got.Status != StatusFail {
		t.Fatalf("Status = %v, want StatusFail even under --fix; detail:\n%s", got.Status, got.Detail)
	}
}

// A claude-only fleet must be byte-identical to what it was before the codex
// pass existed: profileAuthFile has no claude entry, so nothing here runs.
func TestCodexAuthSharingFaults_ClaudeOnlyFleetIsUntouched(t *testing.T) {
	const version = "2.1.237 (Claude Code)"
	runtimeDir := stageProfileWorkspace(t, version)
	stageProfile(t, runtimeDir, "worker-1", version)
	stageProfile(t, runtimeDir, "worker-2", version)

	got := checkAgentProfiles()
	if got.Status != StatusPass {
		t.Fatalf("Status = %v, want StatusPass; detail:\n%s", got.Status, got.Detail)
	}
}
