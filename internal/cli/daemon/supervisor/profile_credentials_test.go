package supervisor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/agentprofile"
)

const credentialTestVersion = "2.1.234 (Claude Code)"

// hollowCredentials is the file five profiles carried on 2026-09-11:
// structurally valid, correct scopes, no token bytes.
const hollowCredentials = `{"claudeAiOauth":{"accessToken":"","refreshToken":"",` +
	`"scopes":["user:inference"],"subscriptionType":"max"}}`

func writeCredentials(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, agentprofile.CredentialsName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// stageTokenProfile provisions a verified claude profile with a good
// oauth-token — a profile that boots today — so each test below varies exactly
// one thing: the content of .credentials.json.
func stageTokenProfile(t *testing.T, projectDir string) string {
	t.Helper()
	dir := writeProfile(t, projectDir, "worker", credentialTestVersion, map[string]string{
		"settings.json": `{"model":"opus"}`,
	})
	if err := os.WriteFile(filepath.Join(dir, "oauth-token"), []byte("sk-ant-oat01-profile"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The outage, closed at its source. A hollow .credentials.json shadows the
// token we are about to export, so the harness blocks on an interactive login
// prompt under its PTY and the run is reaped on the turn deadline with nothing
// to show. Refusing here costs one spawn; launching costs a whole deadline and
// a held worktree.
func TestProfileSecretEnv_HollowCredentialsRefuses(t *testing.T) {
	stubHarnessVersion(t, map[string]string{"claude": credentialTestVersion})
	dir := stageTokenProfile(t, t.TempDir())
	writeCredentials(t, dir, hollowCredentials)

	env, err := ProfileSecretEnv(dir, "claude")
	if !errors.Is(err, ErrProfileCredentialsHollow) {
		t.Fatalf("a hollow credential must refuse, got %v", err)
	}
	// Nothing returned, so no caller can half-export a refused profile.
	if env != nil {
		t.Errorf("a refusal must return no assignments, got %v", env)
	}
	if !strings.Contains(err.Error(), agentprofile.CredentialsName) {
		t.Errorf("error must name the file to repair, got %q", err)
	}
}

// The same refusal through the path the supervisor actually takes: the spawn
// does not happen, and the error an operator sees names the agent.
func TestAppendProfileEnv_HollowCredentialsRefusesBoot(t *testing.T) {
	stubHarnessVersion(t, map[string]string{"claude": credentialTestVersion})
	projectDir := t.TempDir()
	writeCredentials(t, stageTokenProfile(t, projectDir), hollowCredentials)

	if _, _, err := ProfileHarnessEnv(projectDir, "worker", "claude"); !errors.Is(err, ErrProfileCredentialsHollow) {
		t.Fatalf("ProfileHarnessEnv must refuse, got %v", err)
	}

	env, err := AppendProfileEnv([]string{"A=1"}, projectDir, "worker")
	if !errors.Is(err, ErrProfileCredentialsHollow) {
		t.Fatalf("AppendProfileEnv must refuse, got %v", err)
	}
	if env != nil {
		t.Errorf("a refused boot must return no environment, got %v", env)
	}
}

// The recommended configuration, and the one four profiles ran on throughout
// the outage: no credential file at all. Its environment must be byte-identical
// to what it was before this check existed.
func TestProfileSecretEnv_AbsentCredentialsUnchanged(t *testing.T) {
	stubHarnessVersion(t, map[string]string{"claude": credentialTestVersion})
	dir := stageTokenProfile(t, t.TempDir())

	env, err := ProfileSecretEnv(dir, "claude")
	if err != nil {
		t.Fatalf("an absent credential file must not refuse, got %v", err)
	}
	if want := []string{"CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-profile"}; !equalStrings(env, want) {
		t.Errorf("assignments = %v, want %v", env, want)
	}
}

// A populated credential shadows nothing, so it must change nothing.
func TestProfileSecretEnv_PopulatedCredentialsUnchanged(t *testing.T) {
	stubHarnessVersion(t, map[string]string{"claude": credentialTestVersion})
	dir := stageTokenProfile(t, t.TempDir())
	writeCredentials(t, dir, `{"claudeAiOauth":{"accessToken":"sk-ant-oat01-harness"}}`)

	env, err := ProfileSecretEnv(dir, "claude")
	if err != nil {
		t.Fatalf("a populated credential must not refuse, got %v", err)
	}
	if want := []string{"CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-profile"}; !equalStrings(env, want) {
		t.Errorf("assignments = %v, want %v", env, want)
	}
}

// The severity split, asserted at the gate rather than only at the reporter.
// The harness rewrites this file at runtime, so a read can lose that race, and
// a future format change must not refuse every boot in the fleet — both cases
// launch and are reported by `loom doctor` as warnings instead.
func TestProfileSecretEnv_WarnClassCredentialsStillLaunch(t *testing.T) {
	stubHarnessVersion(t, map[string]string{"claude": credentialTestVersion})
	for name, body := range map[string]string{
		"truncated write":    `{"claudeAiOauth":{"accessToken":"sk-`,
		"zero bytes":         ``,
		"unrecognized shape": `{"futureOauth":{"token":"x"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := stageTokenProfile(t, t.TempDir())
			writeCredentials(t, dir, body)

			env, err := ProfileSecretEnv(dir, "claude")
			if err != nil {
				t.Fatalf("a warn-class credential must still launch, got %v", err)
			}
			if got := findAssignment(env, "CLAUDE_CODE_OAUTH_TOKEN"); got != "sk-ant-oat01-profile" {
				t.Errorf("CLAUDE_CODE_OAUTH_TOKEN = %q, want the profile's own token", got)
			}
		})
	}
}

// codex has no credentials file in the map, so a codex root carrying one
// verifies vacuously and behaves exactly as it did before.
func TestProfileSecretEnv_CodexIgnoresCredentials(t *testing.T) {
	dir := t.TempDir()
	writeCredentials(t, dir, hollowCredentials)

	env, err := ProfileSecretEnv(dir, "codex")
	if err != nil || len(env) != 0 {
		t.Errorf("codex: got %v (err %v), want nothing", env, err)
	}
}

// The refusal travels to a human, so it must carry the path and the reason and
// nothing else — not a byte of whatever the file held.
func TestProfileSecretEnv_RefusalNeverCarriesCredentialBytes(t *testing.T) {
	stubHarnessVersion(t, map[string]string{"claude": credentialTestVersion})
	dir := stageTokenProfile(t, t.TempDir())
	writeCredentials(t, dir, `{"claudeAiOauth":{"accessToken":"","refreshToken":"SENTINEL-DO-NOT-PRINT"}}`)

	_, err := ProfileSecretEnv(dir, "claude")
	if err == nil {
		t.Fatal("a hollow credential must refuse")
	}
	if strings.Contains(err.Error(), "SENTINEL") {
		t.Fatalf("refusal leaked credential bytes: %q", err)
	}
}
