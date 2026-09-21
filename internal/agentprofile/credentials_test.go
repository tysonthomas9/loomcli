package agentprofile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tokenSentinel appears inside every fixture that carries token-shaped bytes.
// Nothing loom returns, reports or logs may contain it — see
// TestVerifyCredentials_NeverLeaksTokenBytes.
const tokenSentinel = "SENTINEL-DO-NOT-PRINT"

// hollowOnDisk is the shape observed on 2026-09-11: structurally valid,
// correct scopes and subscription, no token bytes at all.
const hollowOnDisk = `{"claudeAiOauth":{"accessToken":"","refreshToken":"",` +
	`"expiresAt":1757000000000,"scopes":["user:inference","user:profile"],` +
	`"subscriptionType":"max"}}`

// stageCredentialsDir returns a claude profile root holding body as its
// .credentials.json, or an empty root when body is the absent marker.
func stageCredentialsDir(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if body != absent {
		if err := os.WriteFile(filepath.Join(dir, CredentialsName), []byte(body), 0o600); err != nil {
			t.Fatalf("write credentials: %v", err)
		}
	}
	return dir
}

// absent is a sentinel body meaning "write no file at all". It cannot collide
// with a real fixture: no credential file contains a NUL byte.
const absent = "\x00absent\x00"

func TestVerifyCredentials(t *testing.T) {
	tests := []struct {
		name string
		body string
		want error // nil means "usable, or nothing to judge"
	}{
		// The healthy configuration. Four profiles ran this way throughout the
		// outage; this is the row that must never regress into a fault.
		{"absent file is healthy", absent, nil},
		{"populated token", `{"claudeAiOauth":{"accessToken":"sk-ant-oat01-real"}}`, nil},
		{"populated top-level token", `{"accessToken":"sk-ant-oat01-real"}`, nil},

		{"empty token (the reported file)", hollowOnDisk, ErrCredentialsHollow},
		{"accessToken key absent from oauth object",
			`{"claudeAiOauth":{"refreshToken":"x","scopes":["user:inference"]}}`, ErrCredentialsHollow},
		{"whitespace-only token", `{"claudeAiOauth":{"accessToken":"   \t\n "}}`, ErrCredentialsHollow},
		{"null token", `{"claudeAiOauth":{"accessToken":null}}`, ErrCredentialsHollow},
		{"numeric token", `{"claudeAiOauth":{"accessToken":0}}`, ErrCredentialsHollow},
		{"object token", `{"claudeAiOauth":{"accessToken":{}}}`, ErrCredentialsHollow},
		{"empty top-level token", `{"accessToken":""}`, ErrCredentialsHollow},
		{"claudeAiOauth is not an object", `{"claudeAiOauth":"nope"}`, ErrCredentialsHollow},

		// Out of scope by design: the harness refreshes, and judging clocks or
		// scope policy here would only add flaky failure modes.
		{"empty refreshToken with a good access token",
			`{"claudeAiOauth":{"accessToken":"sk-ant-oat01-real","refreshToken":""}}`, nil},
		{"long-expired expiresAt with a good access token",
			`{"claudeAiOauth":{"accessToken":"sk-ant-oat01-real","expiresAt":1}}`, nil},

		{"zero-byte file", ``, ErrCredentialsUnreadable},
		{"truncated JSON", `{"claudeAiOauth":{"accessToken":"sk-`, ErrCredentialsUnreadable},
		{"JSON array at top level", `["nope"]`, ErrCredentialsUnreadable},

		{"unrecognized shape", `{"someFutureOauth":{"token":"x"}}`, ErrCredentialsUnknownShape},
		{"empty JSON object", `{}`, ErrCredentialsUnknownShape},
	}

	zeroRetryDelay(t)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := VerifyCredentials(stageCredentialsDir(t, tc.body), "claude")
			if tc.want == nil {
				if err != nil {
					t.Fatalf("VerifyCredentials = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("VerifyCredentials = %v, want %v", err, tc.want)
			}
		})
	}
}

// A directory where the file should be reads as unreadable, not as absent: the
// profile is malformed, but not in a way that is worth refusing a boot over.
func TestVerifyCredentials_DirectoryInPlaceOfFileIsUnreadable(t *testing.T) {
	zeroRetryDelay(t)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, CredentialsName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCredentials(dir, "claude"); !errors.Is(err, ErrCredentialsUnreadable) {
		t.Fatalf("VerifyCredentials = %v, want ErrCredentialsUnreadable", err)
	}
}

// An unreadable file warns and launches. Root ignores the mode, so the test
// states that rather than asserting something untrue about the environment.
func TestVerifyCredentials_PermissionDeniedIsUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
	zeroRetryDelay(t)
	dir := stageCredentialsDir(t, hollowOnDisk)
	path := filepath.Join(dir, CredentialsName)
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	if err := VerifyCredentials(dir, "claude"); !errors.Is(err, ErrCredentialsUnreadable) {
		t.Fatalf("VerifyCredentials = %v, want ErrCredentialsUnreadable", err)
	}
}

// A symlink is followed and judged by its target's content. No special casing:
// the harness reads it the same way.
func TestVerifyCredentials_SymlinkIsFollowed(t *testing.T) {
	zeroRetryDelay(t)
	target := filepath.Join(t.TempDir(), "elsewhere.json")
	if err := os.WriteFile(target, []byte(hollowOnDisk), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Symlink(target, filepath.Join(dir, CredentialsName)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := VerifyCredentials(dir, "claude"); !errors.Is(err, ErrCredentialsHollow) {
		t.Fatalf("VerifyCredentials = %v, want ErrCredentialsHollow", err)
	}
}

// A harness with no entry in credentialsFile verifies vacuously: a codex root
// holding a hollow-looking file changes nothing, because codex never reads it.
func TestVerifyCredentials_CodexIsVacuous(t *testing.T) {
	zeroRetryDelay(t)
	dir := stageCredentialsDir(t, hollowOnDisk)
	if err := VerifyCredentials(dir, "codex"); err != nil {
		t.Fatalf("codex must verify vacuously, got %v", err)
	}
	if err := VerifyCredentials(dir, ""); err != nil {
		t.Fatalf("an unknown harness must verify vacuously, got %v", err)
	}
}

// Secret hygiene is a property, not a convention: it is asserted, not reviewed.
func TestVerifyCredentials_NeverLeaksTokenBytes(t *testing.T) {
	zeroRetryDelay(t)
	fixtures := map[string]string{
		"hollow with a sentinel refresh token": `{"claudeAiOauth":{"accessToken":"",` +
			`"refreshToken":"` + tokenSentinel + `"}}`,
		"unknown shape carrying a sentinel":  `{"futureOauth":{"token":"` + tokenSentinel + `"}}`,
		"truncated file carrying a sentinel": `{"claudeAiOauth":{"accessToken":"` + tokenSentinel,
		"whitespace token beside a sentinel": `{"claudeAiOauth":{"accessToken":"  ",` +
			`"refreshToken":"` + tokenSentinel + `"}}`,
	}
	for name, body := range fixtures {
		t.Run(name, func(t *testing.T) {
			err := VerifyCredentials(stageCredentialsDir(t, body), "claude")
			if err == nil {
				t.Fatal("fixture must produce a fault")
			}
			if strings.Contains(err.Error(), tokenSentinel) || strings.Contains(err.Error(), "SENTINEL") {
				t.Fatalf("error leaked credential bytes: %q", err.Error())
			}
		})
	}

	// A populated credential is the case with something to leak, and there is
	// no error to leak it through.
	err := VerifyCredentials(stageCredentialsDir(t,
		`{"claudeAiOauth":{"accessToken":"`+tokenSentinel+`"}}`), "claude")
	if err != nil {
		t.Fatalf("a populated credential must verify, got %v", err)
	}
}

// The retry exists because the harness rewrites this file underneath us on
// token refresh. These two cases prove it is real rather than decorative: a
// file repaired between the attempts verifies, and one still malformed on the
// second look is a fault.
func TestVerifyCredentials_RetryObservesTheRewrite(t *testing.T) {
	zeroRetryDelay(t)
	dir := stageCredentialsDir(t, `{"claudeAiOauth":{"accessToken":`)
	path := filepath.Join(dir, CredentialsName)

	prev := credentialsRetrySleep
	attempts := 0
	credentialsRetrySleep = func(time.Duration) {
		attempts++
		if err := os.WriteFile(path, []byte(`{"claudeAiOauth":{"accessToken":"sk-ant-oat01-real"}}`), 0o600); err != nil {
			t.Errorf("stage the rewrite: %v", err)
		}
	}
	t.Cleanup(func() { credentialsRetrySleep = prev })

	if err := VerifyCredentials(dir, "claude"); err != nil {
		t.Fatalf("a file repaired between attempts must verify, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("retried %d times, want exactly 1", attempts)
	}
}

func TestVerifyCredentials_StillMalformedAfterRetry(t *testing.T) {
	zeroRetryDelay(t)
	dir := stageCredentialsDir(t, `{"claudeAiOauth":{"accessToken":`)
	if err := VerifyCredentials(dir, "claude"); !errors.Is(err, ErrCredentialsUnreadable) {
		t.Fatalf("VerifyCredentials = %v, want ErrCredentialsUnreadable", err)
	}
}

func TestCredentialsPath(t *testing.T) {
	if got, want := CredentialsPath("/p/claude", "claude"), filepath.Join("/p/claude", CredentialsName); got != want {
		t.Errorf("CredentialsPath(claude) = %q, want %q", got, want)
	}
	if got := CredentialsPath("/p/codex", "codex"); got != "" {
		t.Errorf("CredentialsPath(codex) = %q, want \"\"", got)
	}
	if got := CredentialsPath("", "claude"); got != "" {
		t.Errorf("CredentialsPath(\"\") = %q, want \"\"", got)
	}
}

// zeroRetryDelay keeps the retry path from adding 50ms to every unreadable
// fixture in the table.
func zeroRetryDelay(t *testing.T) {
	t.Helper()
	prev := credentialsReadRetryDelay
	credentialsReadRetryDelay = 0
	t.Cleanup(func() { credentialsReadRetryDelay = prev })
}
