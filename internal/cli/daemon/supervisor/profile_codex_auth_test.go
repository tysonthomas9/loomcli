package supervisor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCodexAuth lays down a codex login shaped like the real one: the fields
// this package reads plus the ones it deliberately ignores, so a fixture that
// starts exercising an expiry check would fail loudly rather than quietly.
func writeCodexAuth(t *testing.T, dir, refreshToken string) string {
	t.Helper()
	path := filepath.Join(dir, "auth.json")
	body := `{"OPENAI_API_KEY":null,"tokens":{"id_token":"eyJhbGciOiJub25lIn0.e30.",` +
		`"access_token":"at-` + refreshToken + `","refresh_token":"` + refreshToken + `",` +
		`"account_id":"3fefd96f-0000-4000-8000-000000000000"},"last_refresh":"2026-09-05T00:00:00Z"}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The claude path must be byte-identical: profileAuthFile has no claude entry,
// so the codex gate short-circuits before it can read anything. A claude root
// with a token and no auth.json is the whole fleet's normal state.
func TestCheckProfileAuth_ClaudeIsUnaffected(t *testing.T) {
	dir := t.TempDir()
	if err := CheckProfileAuth(dir, "claude"); err != nil {
		t.Fatalf("claude has no login file to own, got %v", err)
	}
	if err := CheckProfileAuth(dir, "unknown"); err != nil {
		t.Fatalf("an unknown harness must short-circuit, got %v", err)
	}
	if err := CheckProfileAuth("", "codex"); err != nil {
		t.Fatalf("an unresolvable dir must short-circuit, got %v", err)
	}
	if got := ProfileAuthPath(dir, "claude"); got != "" {
		t.Errorf("ProfileAuthPath(claude) = %q, want empty", got)
	}

	// And the injected-token path still behaves exactly as before: the gate
	// runs first and returns nothing to say about a claude root.
	if err := os.WriteFile(filepath.Join(dir, "oauth-token"), []byte("sk-ant-oat01-x"), 0o600); err != nil {
		t.Fatal(err)
	}
	env, err := ProfileSecretEnv(dir, "claude")
	if err != nil {
		t.Fatalf("claude token must still export: %v", err)
	}
	if len(env) != 1 || env[0] != "CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-x" {
		t.Fatalf("ProfileSecretEnv = %v, want the unchanged claude assignment", env)
	}
}

// Every shape of "this root has no login of its own" is one sentinel with one
// repair, and every one of them names the directory to run codex login in.
func TestCheckProfileAuth_RefusesEveryShapeOfNoLogin(t *testing.T) {
	cases := []struct {
		name string
		body string // "" means write no file at all
	}{
		{name: "absent"},
		{name: "unparseable", body: "{not json"},
		{name: "apikey mode, no tokens", body: `{"auth_mode":"apikey","OPENAI_API_KEY":"x","tokens":null}`},
		{name: "tokens object with an empty refresh token", body: `{"tokens":{"account_id":"a","refresh_token":""}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.body != "" {
				if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(tc.body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			err := CheckProfileAuth(dir, "codex")
			if !errors.Is(err, ErrProfileCodexAuthMissing) {
				t.Fatalf("err = %v, want ErrProfileCodexAuthMissing", err)
			}
			if !strings.Contains(err.Error(), filepath.Join(dir, "auth.json")) {
				t.Errorf("error must name the file, got %q", err)
			}
			// ProfileSecretEnv is the gate both boot paths funnel through, so
			// the refusal has to reach it and not just the checker.
			if _, err := ProfileSecretEnv(dir, "codex"); !errors.Is(err, ErrProfileCodexAuthMissing) {
				t.Errorf("ProfileSecretEnv = %v, want the boot to refuse too", err)
			}
		})
	}
}

// A dangling symlink reads as IsNotExist. "Missing" is the right bucket: the
// identity is not there, and the repair is to mint one.
func TestCheckProfileAuth_DanglingSymlinkIsMissing(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink(filepath.Join(dir, "nowhere"), filepath.Join(dir, "auth.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := CheckProfileAuth(dir, "codex"); !errors.Is(err, ErrProfileCodexAuthMissing) {
		t.Fatalf("dangling auth.json symlink must report missing, got %v", err)
	}
}

func TestCheckProfileAuth_ValidLoginBoots(t *testing.T) {
	dir := t.TempDir()
	writeCodexAuth(t, dir, "rt-valid")
	if err := CheckProfileAuth(dir, "codex"); err != nil {
		t.Fatalf("a codex root with its own login must boot, got %v", err)
	}
	env, err := ProfileSecretEnv(dir, "codex")
	if err != nil || len(env) != 0 {
		t.Fatalf("ProfileSecretEnv = %v (err %v), want nothing injected for codex", env, err)
	}
}

// codex refreshes its own tokens whenever it runs, so a long-expired access
// token and a stale last_refresh describe a working profile. Refusing on
// either would ground a profile that is fine — a worse failure than the one
// this check exists to catch.
func TestCheckProfileAuth_ExpiryIsDeliberatelyUnchecked(t *testing.T) {
	dir := t.TempDir()
	// exp 2020-01-01, and a last_refresh ninety days older still.
	body := `{"tokens":{"id_token":"eyJhbGciOiJub25lIn0.eyJleHAiOjE1Nzc4MzY4MDB9.",` +
		`"access_token":"at-old","refresh_token":"rt-old","account_id":"acct"},` +
		`"last_refresh":"2025-06-07T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckProfileAuth(dir, "codex"); err != nil {
		t.Fatalf("an expired access token must still boot, got %v", err)
	}
}

// A credential must never travel into an error an operator pastes into a
// ticket. The tokens in the fixture are distinctive strings precisely so this
// assertion can look for them.
func TestCheckProfileAuth_ErrorNeverCarriesTheCredential(t *testing.T) {
	dir := t.TempDir()
	body := `{"tokens":{"access_token":"at-SECRETVALUE","refresh_token":"rt-SECRETVALUE","account_id":"acct"}` // truncated: unparseable
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	err := CheckProfileAuth(dir, "codex")
	if !errors.Is(err, ErrProfileCodexAuthMissing) {
		t.Fatalf("truncated JSON must refuse, got %v", err)
	}
	for _, secret := range []string{"SECRETVALUE", "refresh_token", "access_token"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error %q must not carry %q", err, secret)
		}
	}
}

func TestProfileAuthIdentity_FingerprintsWithoutLeakingTheToken(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	pathA := writeCodexAuth(t, dirA, "rt-shared")
	pathB := writeCodexAuth(t, dirB, "rt-shared")

	accountA, fpA, err := ProfileAuthIdentity(pathA)
	if err != nil {
		t.Fatalf("ProfileAuthIdentity: %v", err)
	}
	if accountA != "3fefd96f-0000-4000-8000-000000000000" {
		t.Errorf("account = %q, want the verbatim account_id", accountA)
	}
	if len(fpA) != 8 {
		t.Errorf("fingerprint = %q, want 8 hex chars", fpA)
	}
	if strings.Contains(fpA, "rt-shared") {
		t.Errorf("fingerprint must not carry the token, got %q", fpA)
	}
	if _, fpB, err := ProfileAuthIdentity(pathB); err != nil || fpB != fpA {
		t.Errorf("identical refresh tokens must fingerprint identically: %q vs %q (err %v)", fpA, fpB, err)
	}

	pathC := writeCodexAuth(t, t.TempDir(), "rt-distinct")
	if _, fpC, err := ProfileAuthIdentity(pathC); err != nil || fpC == fpA {
		t.Errorf("a different refresh token must fingerprint differently, got %q (err %v)", fpC, err)
	}
}

func TestProfileAuthIdentity_RejectsAFileWithNoTokens(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(path, []byte(`{"auth_mode":"apikey","tokens":null}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ProfileAuthIdentity(path); !errors.Is(err, ErrProfileCodexAuthMissing) {
		t.Fatalf("err = %v, want ErrProfileCodexAuthMissing", err)
	}
}
