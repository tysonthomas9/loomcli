package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// publishAuthEnv points every package-level seam at a controlled stand-in and
// restores it afterwards. No t.Parallel() anywhere in this file: every test here
// mutates these globals (see TestCheckOrphanedTmuxSessions_FixMode).
type publishAuthEnv struct {
	t          *testing.T
	fillOut    string
	fillErr    error
	fillCalls  int
	helperSets []string
}

func newPublishAuthEnv(t *testing.T, platform string) *publishAuthEnv {
	t.Helper()

	env := &publishAuthEnv{t: t}

	origGoos, origFill, origRemote, origEnsure, origFix := goos, gitCredentialFill, gitRemoteURL, ensureCredentialHelper, doctorFix
	t.Cleanup(func() {
		goos, gitCredentialFill, gitRemoteURL, ensureCredentialHelper, doctorFix = origGoos, origFill, origRemote, origEnsure, origFix
	})

	goos = func() string { return platform }
	gitRemoteURL = func(dir, remote string) (string, error) {
		return "https://github.com/tysonthomas9/loomcli.git", nil
	}
	gitCredentialFill = func(ctx context.Context, dir, host string) (string, error) {
		env.fillCalls++
		return env.fillOut, env.fillErr
	}
	ensureCredentialHelper = func(ctx context.Context, repoPath string) error {
		env.helperSets = append(env.helperSets, repoPath)
		return nil
	}

	// A token in the ambient environment would silently satisfy probe 3.
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

	return env
}

// execMock answers the two commands this check runs through deps.Exec.
func execMock(idOut, ghOut string, ghErr error, helperOut string) *MockExecRunner {
	return &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		switch {
		case name == "id":
			return CommandResult{Stdout: idOut}
		case name == "gh":
			return CommandResult{Stdout: ghOut, Err: ghErr}
		case name == "git" && len(args) > 0 && args[0] == "config":
			if helperOut == "" {
				return CommandResult{Err: fmt.Errorf("exit status 1")}
			}
			return CommandResult{Stdout: helperOut}
		}
		return CommandResult{Err: fmt.Errorf("unexpected: %s %v", name, args)}
	}}
}

func TestCheckPublishAuth_DirectoryServices(t *testing.T) {
	t.Run("numeric uid on darwin fails", func(t *testing.T) {
		env := newPublishAuthEnv(t, "darwin")
		env.fillOut = "password=ghp_good\n"
		deps, _, _, _, _ := NewTestDeps(t)
		deps.Exec = execMock("501\n", "ghp_good\n", nil, "")

		result := checkPublishAuth(deps)

		if result.Name != "publish_auth" {
			t.Fatalf("name = %q, want publish_auth", result.Name)
		}
		if result.Status != StatusFail {
			t.Fatalf("status = %v, want fail (%s)", result.Status, result.Summary)
		}
		if !strings.Contains(result.Summary, "directory services unavailable") {
			t.Errorf("summary does not name directory services: %q", result.Summary)
		}
		if !strings.Contains(result.Detail, "501") {
			t.Errorf("detail should name the uid it saw, got %q", result.Detail)
		}
		if env.fillCalls != 0 {
			t.Errorf("probe 1 must win outright; credential fill ran %d times", env.fillCalls)
		}
	})

	t.Run("real username moves on to the credential probe", func(t *testing.T) {
		env := newPublishAuthEnv(t, "darwin")
		env.fillOut = "password=ghp_good\n"
		deps, _, _, _, _ := NewTestDeps(t)
		deps.Exec = execMock("oleh\n", "ghp_good\n", nil, "")

		result := checkPublishAuth(deps)

		if result.Status != StatusPass {
			t.Fatalf("status = %v, want pass (%s)", result.Status, result.Summary)
		}
		if env.fillCalls != 1 {
			t.Errorf("credential fill ran %d times, want 1", env.fillCalls)
		}
	})

	t.Run("numeric uid off darwin is not a failure", func(t *testing.T) {
		env := newPublishAuthEnv(t, "linux")
		env.fillOut = "password=ghp_good\n"
		deps, _, _, _, _ := NewTestDeps(t)
		// `id -un` would answer numerically, but the probe must not even ask.
		deps.Exec = execMock("501\n", "ghp_good\n", nil, "")

		result := checkPublishAuth(deps)

		if result.Status != StatusPass {
			t.Fatalf("status = %v, want pass on linux (%s)", result.Status, result.Summary)
		}
	})
}

func TestCheckPublishAuth_Credentials(t *testing.T) {
	t.Run("password field present passes", func(t *testing.T) {
		env := newPublishAuthEnv(t, "linux")
		env.fillOut = "protocol=https\nhost=github.com\nusername=x-access-token\npassword=ghp_secret\n"
		deps, _, _, _, _ := NewTestDeps(t)
		deps.Exec = execMock("oleh\n", "ghp_secret\n", nil, "")

		result := checkPublishAuth(deps)

		if result.Status != StatusPass {
			t.Fatalf("status = %v, want pass (%s)", result.Status, result.Summary)
		}
		if !strings.Contains(result.Summary, "github.com") {
			t.Errorf("summary should name the host, got %q", result.Summary)
		}
		if strings.Contains(result.Summary, "ghp_secret") || strings.Contains(result.Detail, "ghp_secret") {
			t.Fatalf("credential leaked into the result: %q / %q", result.Summary, result.Detail)
		}
	})

	t.Run("failed to get -50 fails without echoing the probe output", func(t *testing.T) {
		env := newPublishAuthEnv(t, "linux")
		env.fillOut = "password=ghp_secret\n"
		env.fillErr = fmt.Errorf("git credential fill: exit status 1: fatal: failed to get: -50")
		deps, _, _, _, _ := NewTestDeps(t)
		deps.Exec = execMock("oleh\n", "ghp_secret\n", nil, "")

		result := checkPublishAuth(deps)

		if result.Status != StatusFail {
			t.Fatalf("status = %v, want fail (%s)", result.Status, result.Summary)
		}
		if !strings.Contains(result.Summary, "github.com") || !strings.Contains(result.Summary, "pushes will fail") {
			t.Errorf("summary = %q", result.Summary)
		}
		if strings.Contains(result.Detail, "ghp_secret") {
			t.Fatalf("resolved credential reached Detail: %q", result.Detail)
		}
		if !strings.Contains(result.Detail, "loom doctor --fix") {
			t.Errorf("detail should point at the fix, got %q", result.Detail)
		}
	})

	t.Run("empty password field is a failure", func(t *testing.T) {
		env := newPublishAuthEnv(t, "linux")
		// What loom's helper emits when no token is in the environment.
		env.fillOut = "username=x-access-token\npassword=\n"
		deps, _, _, _, _ := NewTestDeps(t)
		deps.Exec = execMock("oleh\n", "", fmt.Errorf("exit status 1"), "")

		result := checkPublishAuth(deps)

		if result.Status != StatusFail {
			t.Fatalf("status = %v, want fail (%s)", result.Status, result.Summary)
		}
	})

	t.Run("non-https remote skips the probe", func(t *testing.T) {
		env := newPublishAuthEnv(t, "linux")
		gitRemoteURL = func(dir, remote string) (string, error) {
			return "git@github.com:tysonthomas9/loomcli.git", nil
		}
		deps, _, _, _, _ := NewTestDeps(t)
		deps.Exec = execMock("oleh\n", "ghp_good\n", nil, "")

		result := checkPublishAuth(deps)

		if result.Status != StatusPass {
			t.Fatalf("status = %v, want pass (%s)", result.Status, result.Summary)
		}
		if env.fillCalls != 0 {
			t.Errorf("credential fill ran %d times for an ssh remote, want 0", env.fillCalls)
		}
		if strings.Contains(result.Summary, "credentials resolve") {
			t.Errorf("summary claims a probe that never ran: %q", result.Summary)
		}
	})
}

func TestCheckPublishAuth_GhToken(t *testing.T) {
	t.Run("gh auth token failing warns rather than fails", func(t *testing.T) {
		env := newPublishAuthEnv(t, "linux")
		env.fillOut = "password=ghp_secret\n"
		deps, _, _, _, _ := NewTestDeps(t)
		deps.Exec = execMock("oleh\n", "", fmt.Errorf("exit status 1"), "")

		result := checkPublishAuth(deps)

		if result.Status != StatusWarn {
			t.Fatalf("status = %v, want warn (%s)", result.Status, result.Summary)
		}
		if !strings.Contains(result.Summary, "gh has no usable token") {
			t.Errorf("summary = %q", result.Summary)
		}
		if !strings.Contains(result.Detail, "gh auth login --with-token") {
			t.Errorf("detail = %q", result.Detail)
		}
	})

	t.Run("token in the environment needs no gh", func(t *testing.T) {
		env := newPublishAuthEnv(t, "linux")
		env.fillOut = "password=ghp_secret\n"
		t.Setenv("GITHUB_TOKEN", "ghp_from_env")
		deps, _, _, _, _ := NewTestDeps(t)
		deps.Exec = execMock("oleh\n", "", fmt.Errorf("gh not installed"), "")

		result := checkPublishAuth(deps)

		if result.Status != StatusPass {
			t.Fatalf("status = %v, want pass (%s)", result.Status, result.Summary)
		}
	})
}

func TestCheckPublishAuth_Fix(t *testing.T) {
	t.Run("installs the helper and re-probes", func(t *testing.T) {
		env := newPublishAuthEnv(t, "linux")
		t.Setenv("GITHUB_TOKEN", "ghp_from_env")
		doctorFix = true

		// Fails until the helper is installed, then resolves.
		env.fillErr = fmt.Errorf("git credential fill: exit status 1: failed to get: -50")
		gitCredentialFill = func(ctx context.Context, dir, host string) (string, error) {
			env.fillCalls++
			if len(env.helperSets) > 0 {
				return "password=ghp_from_env\n", nil
			}
			return "", env.fillErr
		}

		deps, _, _, _, _ := NewTestDeps(t)
		deps.Exec = execMock("oleh\n", "ghp_from_env\n", nil, "") // helper absent

		result := checkPublishAuth(deps)

		if len(env.helperSets) != 1 {
			t.Fatalf("EnsureCredentialHelper called %d times, want 1", len(env.helperSets))
		}
		if env.fillCalls != 2 {
			t.Errorf("credential fill ran %d times, want 2 (probe + re-probe)", env.fillCalls)
		}
		if result.Status != StatusPass {
			t.Fatalf("status = %v, want pass after fix (%s)", result.Status, result.Summary)
		}
	})

	t.Run("no token means nothing safe to fix", func(t *testing.T) {
		env := newPublishAuthEnv(t, "linux")
		doctorFix = true
		env.fillErr = fmt.Errorf("failed to get: -50")

		deps, _, _, _, _ := NewTestDeps(t)
		deps.Exec = execMock("oleh\n", "", fmt.Errorf("exit status 1"), "")

		result := checkPublishAuth(deps)

		if len(env.helperSets) != 0 {
			t.Fatalf("installed a helper with no token to feed it: %v", env.helperSets)
		}
		if result.Status != StatusFail {
			t.Fatalf("status = %v, want fail (%s)", result.Status, result.Summary)
		}
	})

	t.Run("helper already configured is not reinstalled", func(t *testing.T) {
		env := newPublishAuthEnv(t, "linux")
		t.Setenv("GITHUB_TOKEN", "ghp_from_env")
		doctorFix = true
		env.fillErr = fmt.Errorf("failed to get: -50")

		deps, _, _, _, _ := NewTestDeps(t)
		deps.Exec = execMock("oleh\n", "ghp_from_env\n", nil,
			"\n"+`!f() { test "$1" = get || exit 0; echo username=x-access-token; echo "password=${GITHUB_TOKEN:-$GH_TOKEN}"; }; f`+"\n")

		result := checkPublishAuth(deps)

		if len(env.helperSets) != 0 {
			t.Fatalf("reinstalled an already-configured helper: %v", env.helperSets)
		}
		if result.Status != StatusFail {
			t.Fatalf("status = %v, want fail (%s)", result.Status, result.Summary)
		}
		if !strings.Contains(result.Detail, "has loom's credential helper configured") {
			t.Errorf("detail should report the helper as present, got %q", result.Detail)
		}
	})
}

func TestCredentialFillHasPassword(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want bool
	}{
		{"non-empty password", "username=x\npassword=ghp_abc\n", true},
		{"empty password", "username=x\npassword=\n", false},
		{"no password field", "username=x\nprotocol=https\n", false},
		{"empty output", "", false},
		{"password not at line start", "note=password=ghp_abc\n", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := credentialFillHasPassword(tc.out); got != tc.want {
				t.Errorf("credentialFillHasPassword(%q) = %v, want %v", tc.out, got, tc.want)
			}
		})
	}
}

func TestCredentialFailureDetail(t *testing.T) {
	tests := []struct {
		name      string
		tokenSet  bool
		helperSet bool
		want      []string
	}{
		{"neither", false, false, []string{"Neither GITHUB_TOKEN", "no loom credential helper", "loom doctor --fix"}},
		{"token only", true, false, []string{"GITHUB_TOKEN or GH_TOKEN is set", "no loom credential helper"}},
		{"both", true, true, []string{"GITHUB_TOKEN or GH_TOKEN is set", "has loom's credential helper configured"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := credentialFailureDetail(tc.tokenSet, tc.helperSet)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("detail %q missing %q", got, want)
				}
			}
		})
	}
}

func TestCheckPublishAuth_JSONShape(t *testing.T) {
	env := newPublishAuthEnv(t, "darwin")
	env.fillOut = "password=ghp_secret\n"
	deps, _, _, _, _ := NewTestDeps(t)
	deps.Exec = execMock("501\n", "ghp_secret\n", nil, "")

	data, err := json.Marshal(DoctorOutput{Checks: []CheckResult{checkPublishAuth(deps)}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed struct {
		Checks []struct {
			Name    string `json:"name"`
			Status  string `json:"status"`
			Summary string `json:"summary"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(parsed.Checks) != 1 {
		t.Fatalf("checks = %d, want 1", len(parsed.Checks))
	}
	if parsed.Checks[0].Name != "publish_auth" {
		t.Errorf("name = %q, want publish_auth", parsed.Checks[0].Name)
	}
	switch parsed.Checks[0].Status {
	case "pass", "warn", "fail":
	default:
		t.Errorf("status = %q, want pass|warn|fail", parsed.Checks[0].Status)
	}
	// The human renderer prints Summary alone, so it must stand on its own.
	if parsed.Checks[0].Summary == "" {
		t.Error("summary must be self-identifying, got empty")
	}
	if strings.Contains(string(data), "ghp_secret") {
		t.Fatalf("credential reached the JSON output: %s", data)
	}
}
