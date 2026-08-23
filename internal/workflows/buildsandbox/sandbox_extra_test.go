package buildsandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// xtSkipIfNoSh skips the caller when /bin/sh is unavailable, so the env/timeout
// behavior tests do not fail on a host without a POSIX shell.
func xtSkipIfNoSh(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh unavailable")
	}
}

// TestAllowedEnvDropsSecretsKeepsToolchain pins the env allowlist: toolchain and
// locale vars pass, while Loom-internal names and credential-shaped names never
// do. DEBUG is gated on LOOM_WORKFLOW_BUILD_DEBUG=1, which is unset here.
func TestAllowedEnvDropsSecretsKeepsToolchain(t *testing.T) {
	os.Unsetenv("LOOM_WORKFLOW_BUILD_DEBUG")
	cases := []struct {
		key  string
		want bool
	}{
		{"PATH", true},
		{"HOME", true},
		{"TMPDIR", true},
		{"NODE_OPTIONS", true},
		{"LC_ALL", true},
		{"LOOM_LOCAL_RUNTIME", false},
		{"SECRET_TOKEN", false},
		{"AWS_SECRET_ACCESS_KEY", false},
		{"GITHUB_TOKEN", false},
		{"DEBUG", false},
	}
	for _, tc := range cases {
		if got := allowed(tc.key); got != tc.want {
			t.Errorf("allowed(%q) = %v, want %v", tc.key, got, tc.want)
		}
	}
}

// TestRunDropsInheritedSecretsFromChildEnv proves the allowlist strips a
// caller-supplied secret before exec: the child sees PATH but never the secret
// key or value.
func TestRunDropsInheritedSecretsFromChildEnv(t *testing.T) {
	xtSkipIfNoSh(t)
	res := Run(context.Background(), Request{
		Command: []string{"/bin/sh", "-c", "env"},
		Env: map[string]string{
			"PATH":         "/usr/bin:/bin",
			"HOME":         os.Getenv("HOME"),
			"SECRET_TOKEN": "leak-me-123",
		},
	})
	if res.Err != nil {
		t.Fatalf("Run err = %v, out=%q", res.Err, res.Output)
	}
	if strings.Contains(res.Output, "leak-me-123") {
		t.Fatalf("secret value reached child env:\n%s", res.Output)
	}
	if strings.Contains(res.Output, "SECRET_TOKEN") {
		t.Fatalf("secret key reached child env:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "PATH=") {
		t.Fatalf("allowlisted PATH did not pass through:\n%s", res.Output)
	}
}

// TestRunTimeoutKillsProcessGroup proves the deadline both reports ErrTimeout and
// kills the child's process group: a delayed side effect (touching a sentinel)
// never lands because the sleep is SIGKILLed before it can run.
func TestRunTimeoutKillsProcessGroup(t *testing.T) {
	xtSkipIfNoSh(t)
	sentinel := filepath.Join(t.TempDir(), "sentinel")
	res := Run(context.Background(), Request{
		Command: []string{"/bin/sh", "-c", "sleep 5 && touch " + sentinel},
		Timeout: 300 * time.Millisecond,
	})
	if res.Err == nil {
		t.Fatalf("expected timeout error, got nil (out=%q)", res.Output)
	}
	if !errors.Is(res.Err, ErrTimeout) {
		t.Fatalf("err = %v, want errors.Is(..., ErrTimeout)", res.Err)
	}
	time.Sleep(1 * time.Second)
	if _, err := os.Stat(sentinel); err == nil {
		t.Fatalf("sentinel %s exists: timed-out child was not killed via its process group", sentinel)
	}
}

// TestProfileStringContainsExpectedRules checks the rendered Seatbelt profile
// carries the fixed header rules, confines writes to the build/output roots, and
// denies the well-known credential stores under HOME — without a blanket HOME
// read-deny, which would break Node module resolution. The paths are
// absolute-but-nonexistent, so path canonicalization falls back to the cleaned
// literal, keeping the output deterministic across platforms.
func TestProfileStringContainsExpectedRules(t *testing.T) {
	p := Profile(ProfileSpec{
		BuildRoot:  "/nonexistent-build-xyz",
		OutputRoot: "/nonexistent-out-xyz",
		Home:       "/nonexistent-home-xyz",
	})
	want := []string{
		"(version 1)",
		"(allow default)",
		"(deny network*)",
		"(deny file-write*)",
		`(allow file-write* (subpath "/nonexistent-build-xyz"))`,
		`(allow file-write* (subpath "/nonexistent-out-xyz"))`,
		`(deny file-read* (subpath "/nonexistent-home-xyz/.ssh"))`,
		`(deny file-read* (subpath "/nonexistent-home-xyz/.aws"))`,
	}
	for _, w := range want {
		if !strings.Contains(p, w) {
			t.Errorf("profile missing %q:\n%s", w, p)
		}
	}
	// Reads must stay allow-default: no blanket HOME read-deny, and no per-path
	// read-allow rules (those existed only in the abandoned read-jail design).
	if strings.Contains(p, `(deny file-read* (subpath "/nonexistent-home-xyz"))`) {
		t.Errorf("profile must not blanket-deny HOME reads (breaks module resolution):\n%s", p)
	}
	if strings.Contains(p, "(allow file-read*") {
		t.Errorf("profile should not grant explicit file-read allows under allow-default:\n%s", p)
	}
}

// TestModeReportsSeatbeltWhenAvailable ties Mode's report to the presence of
// sandbox-exec: on a host that has it (macOS) both build modes report seatbelt;
// on a host without it a package build fails closed while a non-package build
// degrades to "none".
func TestModeReportsSeatbeltWhenAvailable(t *testing.T) {
	_, statErr := os.Stat("/usr/bin/sandbox-exec")
	if statErr == nil {
		if got, err := Mode(false); got != "seatbelt" || err != nil {
			t.Fatalf("Mode(false) = (%q, %v), want (\"seatbelt\", nil)", got, err)
		}
		if got, err := Mode(true); got != "seatbelt" || err != nil {
			t.Fatalf("Mode(true) = (%q, %v), want (\"seatbelt\", nil)", got, err)
		}
		return
	}
	if got, err := Mode(true); err == nil {
		t.Fatalf("Mode(true) = (%q, nil), want non-nil error when sandbox-exec absent", got)
	}
	if got, err := Mode(false); got != "none" || err != nil {
		t.Fatalf("Mode(false) = (%q, %v), want (\"none\", nil)", got, err)
	}
}
