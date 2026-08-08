package runtime //nolint:revive // The approved target architecture names this platform mechanism runtime.

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPinExecutableDirOnPathPrefersPackagedSiblingAndDeduplicates(t *testing.T) {
	executable := filepath.Join(string(filepath.Separator), "Applications", "Loom Agents.app", "Contents", "MacOS", "loom")
	executableDir := filepath.Dir(executable)
	got := PinExecutableDirOnPath([]string{
		"HOME=/tmp/home",
		"PATH=/usr/bin" + string(filepath.ListSeparator) + executableDir + string(filepath.ListSeparator) + "/bin",
	}, executable)
	wantPath := executableDir + string(filepath.ListSeparator) + "/usr/bin" + string(filepath.ListSeparator) + "/bin"
	if !slices.Equal(got, []string{"HOME=/tmp/home", "PATH=" + wantPath}) {
		t.Fatalf("PinExecutableDirOnPath() = %q, want HOME plus pinned PATH %q", got, wantPath)
	}
}

func TestPinExecutableDirOnPathRejectsRelativeExecutable(t *testing.T) {
	env := []string{"PATH=/usr/bin", "HOME=/tmp/home"}
	if got := PinExecutableDirOnPath(env, "loom"); !slices.Equal(got, env) {
		t.Fatalf("PinExecutableDirOnPath() = %q, want unchanged %q", got, env)
	}
}

func TestPinExecutableDirForLoginShellWritesScopedProfiles(t *testing.T) {
	executable := filepath.Join(string(filepath.Separator), "Applications", "Loom Agents.app", "Contents", "MacOS", "loom")
	got, cleanup, err := PinExecutableDirForLoginShell([]string{
		"PATH=/usr/bin",
		"ZDOTDIR=/user/config",
		"BASH_ENV=/user/bash-env",
	}, executable)
	if err != nil {
		t.Fatalf("PinExecutableDirForLoginShell: %v", err)
	}
	t.Cleanup(cleanup)
	env := subprocessEnvMap(got)
	if env["LOOM_PINNED_EXECUTABLE_DIR"] != filepath.Dir(executable) {
		t.Fatalf("pinned executable dir = %q", env["LOOM_PINNED_EXECUTABLE_DIR"])
	}
	if !strings.HasPrefix(env["PATH"], filepath.Dir(executable)+string(os.PathListSeparator)) {
		t.Fatalf("PATH = %q, want executable directory first", env["PATH"])
	}
	for _, profile := range []string{
		filepath.Join(env["ZDOTDIR"], ".zshenv"),
		filepath.Join(env["ZDOTDIR"], ".zprofile"),
		env["BASH_ENV"],
	} {
		content, err := os.ReadFile(profile)
		if err != nil {
			t.Fatalf("read shell profile %q: %v", profile, err)
		}
		if !strings.Contains(string(content), "LOOM_PINNED_EXECUTABLE_DIR") {
			t.Fatalf("shell profile %q does not restore pinned PATH: %q", profile, content)
		}
	}
	cleanup()
	if _, err := os.Stat(env["ZDOTDIR"]); !os.IsNotExist(err) {
		t.Fatalf("shell profile directory remains after cleanup: %v", err)
	}
}

func TestPinExecutableDirForLoginShellSurvivesZshLoginStartup(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not installed")
	}
	binDir := t.TempDir()
	executable := filepath.Join(binDir, "loom")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write fake executable: %v", err)
	}
	env, cleanup, err := PinExecutableDirForLoginShell([]string{
		"PATH=/usr/bin:/bin",
		"HOME=" + t.TempDir(),
	}, executable)
	if err != nil {
		t.Fatalf("PinExecutableDirForLoginShell: %v", err)
	}
	t.Cleanup(cleanup)
	cmd := exec.Command(zsh, "-lc", "command -v loom") //nolint:norawexec // This integration test must exercise real login-shell startup.
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("zsh login lookup: %v: %s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != executable {
		t.Fatalf("zsh resolved loom = %q, want %q", got, executable)
	}
}

func TestSubprocessEnvProfilesPreserveTrustPlacement(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"HOME=/home/loom",
		"LANG=C.UTF-8",
		"LC_ALL=C",
		"CODEX_HOME=/home/loom/.codex",
		"CLAUDE_CONFIG_DIR=/home/loom/.claude",
		"LOOM_WORKTREE_PATH=/worktree",
		"LOOM_CONFIG_DIR=/home/loom/.loom",
		"LOOM_HOST_BRIDGE_HELPER=1",
		"LOOM_DRIVER_TASK_RUNNER_CMD_JSON=[\"/tmp/runner\"]",
		"LOOM_DRIVER_TASK_RUNNER_CMD=/tmp/runner",
		"OPENAI_API_KEY=openai-secret",
		"CLAUDE_CODE_OAUTH_TOKEN=claude-secret",
		"GOOGLE_APPLICATION_CREDENTIALS=/secrets/google.json",
		"LOOM_CODEX_BIN=/definitely/missing/codex",
		"CUSTOM_VAR=value",
		"malformed",
	}

	tests := []struct {
		name      string
		profile   SubprocessEnvProfile
		allowed   []string
		forbidden []string
	}{
		{
			name:    "trusted local CLI",
			profile: SubprocessEnvTrustedLocalCLI,
			allowed: []string{
				"PATH", "HOME", "LANG", "LC_ALL", "CODEX_HOME",
				"LOOM_WORKTREE_PATH", "LOOM_CONFIG_DIR", "LOOM_HOST_BRIDGE_HELPER",
				"LOOM_DRIVER_TASK_RUNNER_CMD_JSON", "LOOM_DRIVER_TASK_RUNNER_CMD",
				"OPENAI_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN",
			},
			forbidden: []string{
				"CLAUDE_CONFIG_DIR", "GOOGLE_APPLICATION_CREDENTIALS", "CUSTOM_VAR",
			},
		},
		{
			name:    "Interaction child",
			profile: SubprocessEnvInteractionChild,
			allowed: []string{
				"PATH", "HOME", "LANG", "LC_ALL", "CODEX_HOME", "CLAUDE_CONFIG_DIR",
			},
			forbidden: []string{
				"LOOM_WORKTREE_PATH", "LOOM_CONFIG_DIR", "LOOM_HOST_BRIDGE_HELPER",
				"LOOM_DRIVER_TASK_RUNNER_CMD_JSON", "LOOM_DRIVER_TASK_RUNNER_CMD",
				"OPENAI_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN",
				"GOOGLE_APPLICATION_CREDENTIALS", "LOOM_CODEX_BIN", "CUSTOM_VAR",
			},
		},
		{
			name:    "Driver remote",
			profile: SubprocessEnvDriverRemote,
			allowed: []string{
				"PATH", "HOME", "LANG", "LC_ALL", "LOOM_CONFIG_DIR",
				"LOOM_HOST_BRIDGE_HELPER", "LOOM_DRIVER_TASK_RUNNER_CMD_JSON",
				"LOOM_DRIVER_TASK_RUNNER_CMD",
			},
			forbidden: []string{
				"CODEX_HOME", "CLAUDE_CONFIG_DIR", "LOOM_WORKTREE_PATH",
				"OPENAI_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN",
				"GOOGLE_APPLICATION_CREDENTIALS", "LOOM_CODEX_BIN", "CUSTOM_VAR",
			},
		},
		{
			name:    "Driver local task runner",
			profile: SubprocessEnvDriverLocalTaskRunner,
			allowed: []string{
				"PATH", "HOME", "LANG", "LC_ALL", "CODEX_HOME", "LOOM_CONFIG_DIR",
				"LOOM_HOST_BRIDGE_HELPER", "LOOM_DRIVER_TASK_RUNNER_CMD_JSON",
				"LOOM_DRIVER_TASK_RUNNER_CMD", "OPENAI_API_KEY",
				"CLAUDE_CODE_OAUTH_TOKEN", "GOOGLE_APPLICATION_CREDENTIALS", "LOOM_CODEX_BIN",
			},
			forbidden: []string{
				"CLAUDE_CONFIG_DIR", "LOOM_WORKTREE_PATH", "CUSTOM_VAR",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := subprocessEnvMap(FilterSubprocessEnv(tc.profile, env))
			for _, name := range tc.allowed {
				if _, ok := got[name]; !ok {
					t.Errorf("%s missing from profile: %+v", name, got)
				}
			}
			for _, name := range tc.forbidden {
				if _, ok := got[name]; ok {
					t.Errorf("%s unexpectedly admitted by profile: %+v", name, got)
				}
			}
		})
	}
}

func TestSubprocessEnvProfilesAlwaysDenyForgeFleetDBAndGitRedirection(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"GITHUB_TOKEN=github-secret",
		"GH_TOKEN=github-secret",
		"GITLAB_TOKEN=gitlab-secret",
		"DAYTONA_API_KEY=daytona-secret",
		"DAYTONA_CREDENTIAL_FILE=/tmp/daytona-secret",
		"DAYTONA_SDK_IMPORT=/tmp/provider-sdk.mjs",
		"LOOM_FLEET_API_KEY=fleet-secret",
		"LOOM_FLEET_DB_URL=https://fleet.invalid",
		"LOOM_FLEET_DB_API_KEY=fleet-secret",
		"LOOM_FLEET_DB_ACTOR=forged-actor",
		"LOOM_DRIVER_FLEET_DB_API_KEY=driver-fleet-secret",
		"LOOM_FLEETDB_REDIS_PASSWORD=redis-secret",
		"FLEET_TOKEN=fleet-token",
		`LOOM_TASK_RUN_REQUEST_JSON={"lease_token":"task-secret"}`,
		"GIT_DIR=/tmp/other-repo",
		"GIT_WORK_TREE=/tmp/other-worktree",
		"GIT_INDEX_FILE=/tmp/index",
		"GIT_OBJECT_DIRECTORY=/tmp/objects",
		"GIT_ALTERNATE_OBJECT_DIRECTORIES=/tmp/alternate",
		"GIT_CEILING_DIRECTORIES=/tmp",
		"GIT_COMMON_DIR=/tmp/common",
		"GIT_EXEC_PATH=/tmp/exec",
		"GIT_TEMPLATE_DIR=/tmp/template",
		"GIT_ASKPASS=/tmp/askpass",
		"GIT_HOOKS_PATH=/tmp/hooks",
		"GIT_CONFIG=/tmp/config",
		"GIT_CONFIG_GLOBAL=/tmp/global",
		"GIT_CONFIG_SYSTEM=/tmp/system",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=core.hooksPath",
		"GIT_CONFIG_VALUE_0=/tmp/hooks",
	}
	profiles := []SubprocessEnvProfile{
		SubprocessEnvTrustedLocalCLI,
		SubprocessEnvInteractionChild,
		SubprocessEnvDriverRemote,
		SubprocessEnvDriverLocalTaskRunner,
	}
	for _, profile := range profiles {
		got := FilterSubprocessEnv(profile, env)
		if !slices.Equal(got, []string{"PATH=/usr/bin"}) {
			t.Errorf("profile %d = %v, want only PATH", profile, got)
		}
	}
}

func TestTrustedLocalCLINonSecretLoomContextIsNotBroadCredentialFallback(t *testing.T) {
	got := subprocessEnvMap(FilterSubprocessEnv(SubprocessEnvTrustedLocalCLI, []string{
		"LOOM_AGENT_NAME=planner",
		"LOOM_WORKTREE_PATH=/worktree",
		"LOOM_AGENT_LEASE_TOKEN=ambient-lease",
		"LOOM_NOTIFY_TOKEN=ambient-notify",
		"LOOM_REDIS_PASSWORD=ambient-password",
		"LOOM_ARBITRARY_API_KEY=ambient-key",
	}))
	for _, name := range []string{"LOOM_AGENT_NAME", "LOOM_WORKTREE_PATH"} {
		if _, ok := got[name]; !ok {
			t.Errorf("%s missing from trusted local context: %+v", name, got)
		}
	}
	for _, name := range []string{
		"LOOM_AGENT_LEASE_TOKEN", "LOOM_NOTIFY_TOKEN", "LOOM_REDIS_PASSWORD", "LOOM_ARBITRARY_API_KEY",
	} {
		if _, ok := got[name]; ok {
			t.Errorf("%s leaked through broad LOOM_ allowance: %+v", name, got)
		}
	}
}

func TestFilterSubprocessEnvMalformedEmptyAndUnknownFailClosed(t *testing.T) {
	for _, env := range [][]string{nil, {}, {"malformed", "", " =value"}} {
		got := FilterSubprocessEnv(SubprocessEnvTrustedLocalCLI, env)
		if got == nil || len(got) != 0 {
			t.Errorf("FilterSubprocessEnv(%v) = %#v, want non-nil empty", env, got)
		}
	}
	if got := FilterSubprocessEnv(SubprocessEnvProfile(255), []string{"PATH=/usr/bin"}); got == nil || len(got) != 0 {
		t.Fatalf("unknown profile = %#v, want non-nil empty", got)
	}
}

func TestCurrentSubprocessEnvUsesSelectedProfile(t *testing.T) {
	t.Setenv("LOOM_TEST_ENVFILTER", "hello")
	t.Setenv("LOOM_NOTIFY_TOKEN", "must-not-flow")
	got := CurrentSubprocessEnv(SubprocessEnvTrustedLocalCLI)
	if !slices.Contains(got, "LOOM_TEST_ENVFILTER=hello") {
		t.Fatalf("current trusted-local env missing test marker: %v", got)
	}
	if slices.ContainsFunc(got, func(entry string) bool {
		return strings.HasPrefix(entry, "LOOM_NOTIFY_TOKEN=")
	}) {
		t.Fatalf("current trusted-local env leaked notify token: %v", got)
	}
}

func subprocessEnvMap(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, entry := range env {
		name, value, ok := strings.Cut(entry, "=")
		if ok {
			out[name] = value
		}
	}
	return out
}
