package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/sessionfinalize"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type fakeDaytonaRunner struct {
	mu         sync.Mutex
	requests   []AgentRuntimeRequest
	sandboxIDs []string
	result     AgentRuntimeResult
}

func (f *fakeDaytonaRunner) RunAgent(_ context.Context, req AgentRuntimeRequest) (AgentRuntimeResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := fmt.Sprintf("fake-sandbox-%02d", len(f.requests)+1)
	f.requests = append(f.requests, req)
	f.sandboxIDs = append(f.sandboxIDs, id)
	if f.result.SandboxID == "" && f.result.ExitCode == 0 && f.result.Stdout == "" && f.result.Stderr == "" {
		return AgentRuntimeResult{
			SandboxID:    id,
			ExitCode:     0,
			Stdout:       "remote ok\n",
			Phase:        daytonaRuntimePhaseStopped,
			CleanupState: daytonaCleanupDeleted,
		}, nil
	}
	result := f.result
	if result.SandboxID == "" {
		result.SandboxID = id
	}
	return result, nil
}

func setDaytonaControlPlaneEnv(t *testing.T) {
	t.Helper()
	t.Setenv(bootstrap.EnvFleetDBURL, "https://fleet.example.test")
	t.Setenv(bootstrap.EnvFleetDBAPIKey, "fleet-secret")
	t.Setenv(bootstrap.EnvFleetDBActor, "")
	t.Setenv(bootstrap.EnvWorkspace, "")
	t.Setenv("DAYTONA_API_KEY", "dtn-daytona-test")
	t.Setenv("OPENAI_API_KEY", "sk-daytona-test")
	t.Setenv("LOOM_DAEMON_SOCKET", "/tmp/host-only.sock")
	t.Setenv("CODEX_HOME", "/Users/example/.codex")
	t.Setenv("HOME", "/Users/example")
	t.Setenv("SSH_AUTH_SOCK", "/tmp/ssh-agent.sock")
}

func daytonaTestRoleConfig() cfgpkg.RoleConfig {
	return cfgpkg.RoleConfig{
		Backend:            "codex",
		RuntimeProvider:    domain.RuntimeProviderDaytona,
		RuntimeProfileName: "daytona-agent",
		RuntimeCWD:         "/workspace/project",
		RuntimeDaytona: map[string]any{
			"language":           "typescript",
			"api_key_env":        "DAYTONA_API_KEY",
			"auto_stop_interval": 15,
			"repo_url":           "https://github.com/acme/project.git",
			"setup_commands":     []any{"npm install"},
			"openai_api_key_env": "OPENAI_API_KEY",
		},
	}
}

func newDaytonaTestSupervisor(t *testing.T, runner AgentRuntimeRunner, roleConfig cfgpkg.RoleConfig) *Supervisor {
	t.Helper()
	cfg := makeSupervisorConfig(nil, map[string]cfgpkg.RoleConfig{"task": roleConfig})
	return &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return cfg },
		ProjectDir:     t.TempDir(),
		WorkspaceID:    "WS",
		Shutdown:       make(chan struct{}),
		StoppedAgents:  make(map[string]struct{}),
		Concurrency:    NewConcurrencyTracker(nil),
		RuntimeRunner:  runner,
		EmitEvent:      func(events.Event) {},
	}
}

func TestBuiltInTaskRoleMergesDaytonaRuntimeOverlay(t *testing.T) {
	roleConfig := daytonaTestRoleConfig()
	cfg := makeSupervisorConfig(nil, map[string]cfgpkg.RoleConfig{"task": roleConfig})
	resolved, err := ResolveRoleConfigStatic("task", cfg, t.TempDir())
	if err != nil {
		t.Fatalf("ResolveRoleConfigStatic() error = %v", err)
	}
	if resolved.TaskFilter != "has_design" {
		t.Fatalf("TaskFilter = %q, want built-in task default", resolved.TaskFilter)
	}
	if resolved.RuntimeProvider != domain.RuntimeProviderDaytona ||
		resolved.RuntimeProfileName != "daytona-agent" ||
		resolved.RuntimeCWD != "/workspace/project" {
		t.Fatalf("runtime = provider:%q profile:%q cwd:%q, want Daytona overlay", resolved.RuntimeProvider, resolved.RuntimeProfileName, resolved.RuntimeCWD)
	}
	if resolved.RuntimeDaytona["repo_url"] != "https://github.com/acme/project.git" ||
		resolved.RuntimeDaytona["setup_commands"].([]any)[0] != "npm install" {
		t.Fatalf("RuntimeDaytona = %+v, want overlay config", resolved.RuntimeDaytona)
	}
}

type blockingDaytonaRunner struct {
	started chan AgentRuntimeRequest
	done    chan struct{}
}

func (b *blockingDaytonaRunner) RunAgent(ctx context.Context, req AgentRuntimeRequest) (AgentRuntimeResult, error) {
	b.started <- req
	<-ctx.Done()
	close(b.done)
	return AgentRuntimeResult{}, ctx.Err()
}

type progressBlockingDaytonaRunner struct {
	started chan AgentRuntimeRequest
	done    chan struct{}
}

func (b *progressBlockingDaytonaRunner) RunAgent(ctx context.Context, req AgentRuntimeRequest) (AgentRuntimeResult, error) {
	if req.Progress != nil {
		req.Progress(AgentRuntimeProgress{
			SandboxID:    "progress-sandbox",
			Phase:        daytonaRuntimePhaseSetup,
			CleanupState: daytonaCleanupPending,
		})
		req.Progress(AgentRuntimeProgress{
			SandboxID:    "progress-sandbox",
			Phase:        daytonaRuntimePhaseRunning,
			CleanupState: daytonaCleanupPending,
		})
	}
	b.started <- req
	<-ctx.Done()
	close(b.done)
	return AgentRuntimeResult{}, ctx.Err()
}

type setupFailingDaytonaRunner struct {
	requests []AgentRuntimeRequest
}

func (f *setupFailingDaytonaRunner) RunAgent(_ context.Context, req AgentRuntimeRequest) (AgentRuntimeResult, error) {
	f.requests = append(f.requests, req)
	return AgentRuntimeResult{
		SandboxID:    "fake-setup-failed",
		ExitCode:     1,
		Stderr:       "setup failed\n",
		Phase:        daytonaRuntimePhaseSetup,
		CleanupState: daytonaCleanupDeleted,
	}, nil
}

type erroringDaytonaRunner struct {
	err error
}

func (r erroringDaytonaRunner) RunAgent(_ context.Context, _ AgentRuntimeRequest) (AgentRuntimeResult, error) {
	return AgentRuntimeResult{}, r.err
}

func (f *fakeDaytonaRunner) snapshot() ([]AgentRuntimeRequest, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	reqs := append([]AgentRuntimeRequest(nil), f.requests...)
	ids := append([]string(nil), f.sandboxIDs...)
	return reqs, ids
}

func installFakeDaytonaSDK(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	sdkDir := filepath.Join(root, "node_modules", "@daytona", "sdk")
	if err := os.MkdirAll(sdkDir, 0o755); err != nil {
		t.Fatalf("mkdir fake sdk: %v", err)
	}
	logPath := filepath.Join(t.TempDir(), "daytona-ops.jsonl")
	t.Setenv("DAYTONA_FAKE_LOG", logPath)
	source := `const fs = require("node:fs");

function record(entry) {
  fs.appendFileSync(process.env.DAYTONA_FAKE_LOG, JSON.stringify(entry) + "\n");
}

function makeImage(base) {
  return {
    __sdkImage: true,
    base,
    calls: [],
    runCommands(...commands) {
      this.calls.push({ op: "runCommands", commands });
      return this;
    },
    workdir(dir) {
      this.calls.push({ op: "workdir", dir });
      return this;
    },
    env(vars) {
      this.calls.push({ op: "env", vars });
      return this;
    },
  };
}

const Image = {
  base: (base) => makeImage(base),
  debianSlim: (version) => makeImage("debian-slim:" + version),
  fromDockerfile: (dockerfile) => ({ ...makeImage("dockerfile"), dockerfile }),
};

class Daytona {
  constructor(options = {}) {
    this.options = options;
    record({ type: "client", options });
  }

  async create(params = {}, options = {}) {
    record({ type: "create", params, options });
    if (params.blockCreate) {
      return new Promise(() => setInterval(() => {}, 1000));
    }
    return {
      id: "fake-sdk-sandbox",
      process: {
        async executeCommand(...args) {
          let command = args[0];
          let cwd = args[1];
          let env = args[2];
          let timeout = args[3];
          if (process.env.DAYTONA_FAKE_EXEC_SIGNATURE === "options") {
            if (args.length !== 2 || !args[1] || typeof args[1] !== "object" || Array.isArray(args[1])) {
              throw new Error("expected options object parameters");
            }
            cwd = args[1].cwd;
            env = args[1].env;
            timeout = args[1].timeout;
          }
          if (process.env.DAYTONA_FAKE_EXEC_SIGNATURE === "request") {
            if (args.length !== 1 || !args[0] || typeof args[0] !== "object" || Array.isArray(args[0]) || !args[0].command) {
              throw new Error("expected request object parameters");
            }
            command = args[0].command;
            cwd = args[0].cwd;
            env = args[0].env;
            timeout = args[0].timeout;
          }
          record({ type: "exec", command, cwd, env, timeout });
          if (String(command).includes("fail setup")) {
            return { exitCode: 7, stderr: "setup failed from fake sdk" };
          }
          if (String(command).includes("block forever")) {
            return new Promise(() => setInterval(() => {}, 1000));
          }
          if (process.env.DAYTONA_FAKE_RESPONSE_SHAPE === "daytona") {
            return { exitCode: 0, result: "result:" + command + "\n", artifacts: { stdout: "artifact:" + command + "\n" } };
          }
          return { exitCode: 0, stdout: "ok:" + command + "\n" };
        },
      },
    };
  }

  async delete(sandbox, timeout) {
    record({ type: "delete", sandboxId: sandbox && sandbox.id, timeout });
  }
}

module.exports = { Daytona, Image };
`
	if err := os.WriteFile(filepath.Join(sdkDir, "index.js"), []byte(source), 0o644); err != nil {
		t.Fatalf("write fake sdk: %v", err)
	}
	return root, logPath
}

func readFakeDaytonaOps(t *testing.T, logPath string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake Daytona log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	ops := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var op map[string]any
		if err := json.Unmarshal([]byte(line), &op); err != nil {
			t.Fatalf("decode fake Daytona op %q: %v", line, err)
		}
		ops = append(ops, op)
	}
	return ops
}

func waitForFakeDaytonaExec(t *testing.T, logPath, command string) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(logPath); err == nil && strings.Contains(string(data), command) {
			return readFakeDaytonaOps(t, logPath)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for fake Daytona command %q", command)
	return nil
}

func waitForFakeDaytonaOp(t *testing.T, logPath, opType string) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(logPath); err == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				if line == "" {
					continue
				}
				var op map[string]any
				if json.Unmarshal([]byte(line), &op) == nil && op["type"] == opType {
					return readFakeDaytonaOps(t, logPath)
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for fake Daytona op %q", opType)
	return nil
}

func TestDaytonaAgentRunnerScriptRunsHealthSetupFinalAndCleanup(t *testing.T) {
	sdkRoot, logPath := installFakeDaytonaSDK(t)
	t.Setenv("DAYTONA_API_KEY", "dtn-test-key")
	env := map[string]string{
		"LOOM_WORKTREE_PATH": "/workspace/project",
		"LOOM_GIT_ASKPASS":   daytonaGitAskpassPath,
		"LOOM_GIT_SSH_KEY":   daytonaGitSSHKeyPath,
	}
	req := AgentRuntimeRequest{
		Provider: domain.RuntimeProviderDaytona,
		Daytona: map[string]any{
			"language":            "typescript",
			"api_key":             "dtn-inline-secret",
			"apiKey":              "dtn-inline-secret-camel",
			"api_key_env":         "DAYTONA_API_KEY",
			"api_url":             "https://daytona.example.test",
			"target":              "us",
			"snapshot":            "loom-node-dev",
			"snapshot_name":       "loom-node-dev-alt",
			"auto_stop_interval":  15,
			"build_logs":          "inherit",
			"id":                  "shared-sandbox-id",
			"sandbox_id":          "shared-sandbox-id",
			"sandboxId":           "shared-sandbox-id",
			"providerWorkspaceId": "shared-sandbox-id",
			"repos":               []any{"frontend"},
			"repo_url":            "https://github.com/acme/private.git",
			"repoURL":             "https://github.com/acme/private-alt.git",
			"branch":              "main",
			"ref":                 "refs/pull/1/head",
			"setup_commands":      []any{"npm install"},
			"setup_timeout":       120,
			"health_timeout":      10,
			"run_timeout":         300,
			"github_token":        "ghp-inline-secret",
			"git_deploy_key":      "inline-private-key",
			"git_token_env":       "GITHUB_TOKEN",
			"git_deploy_key_env":  "GIT_DEPLOY_KEY",
			"git_username":        "x-access-token",
			"openai_api_key":      "sk-inline-secret",
			"openai_api_key_env":  "OPENAI_API_KEY",
			"codex_auth_file_env": "CODEX_AUTH_FILE",
			"password":            "inline-password",
			"secret":              "inline-secret",
			"secret_env":          []any{"OPENAI_API_KEY"},
			"token":               "inline-token",
			"env":                 []any{"GITHUB_TOKEN"},
			"env_grants":          []any{"CODEX_AUTH_FILE"},
			"env_vars":            map[string]any{"NODE_ENV": "test"},
		},
		Command: "echo final",
		Env:     env,
		CWD:     "/workspace/project",
		HealthCheck: &AgentRuntimeCommand{
			Name:    "fleetdb.health",
			Command: "echo health",
			CWD:     "/",
			Env:     env,
		},
		Setup: []AgentRuntimeCommand{
			{Name: "repo.materialize", Command: "echo clone", CWD: "/", Env: env},
			{Name: "setup.1", Command: "echo setup", CWD: "/workspace/project", Env: env},
			{Name: "runtime.prerequisites", Command: "echo prereq", CWD: "/workspace/project", Env: env},
		},
		TimeoutSeconds: 42,
	}

	result, err := (DaytonaAgentRunner{SDKRoot: sdkRoot}).RunAgent(t.Context(), req)
	if err != nil {
		t.Fatalf("RunAgent() error = %v", err)
	}
	if result.SandboxID != "fake-sdk-sandbox" || result.ExitCode != 0 ||
		result.Phase != daytonaRuntimePhaseRunning || result.CleanupState != daytonaCleanupDeleted ||
		!strings.Contains(result.Stdout, "ok:echo final") {
		t.Fatalf("result = %+v, want successful final command", result)
	}

	ops := readFakeDaytonaOps(t, logPath)
	if len(ops) != 9 {
		t.Fatalf("ops = %#v, want client/create/5 exec/delete", ops)
	}
	client := ops[0]
	options := client["options"].(map[string]any)
	if options["apiUrl"] != "https://daytona.example.test" || options["target"] != "us" {
		t.Fatalf("client options = %#v, want API URL and target forwarded to Daytona client", options)
	}
	create := ops[1]
	params := create["params"].(map[string]any)
	if params["language"] != "typescript" {
		t.Fatalf("create params = %#v, want language", params)
	}
	if params["autoStopInterval"] != float64(15) {
		t.Fatalf("create params = %#v, want auto_stop_interval normalized to autoStopInterval", params)
	}
	if params["snapshot"] != "loom-node-dev" || params["snapshotName"] != "loom-node-dev-alt" {
		t.Fatalf("create params = %#v, want snapshot create options preserved", params)
	}
	for _, key := range []string{
		"api_key", "apiKey", "api_key_env", "api_url", "target", "snapshot_name", "auto_stop_interval", "build_logs",
		"id", "sandbox_id", "sandboxId", "providerWorkspaceId",
		"repos", "repo_url", "repoURL", "branch", "ref",
		"setup_commands", "setup_timeout", "health_timeout", "run_timeout",
		"github_token", "git_deploy_key", "git_token_env", "git_deploy_key_env", "git_username",
		"openai_api_key", "openai_api_key_env", "codex_auth_file_env",
		"password", "secret", "secret_env", "token", "env", "env_grants",
	} {
		if _, ok := params[key]; ok {
			t.Fatalf("create params leaked Loom-only key %s: %#v", key, params)
		}
	}
	if params["envVars"].(map[string]any)["NODE_ENV"] != "test" {
		t.Fatalf("create params envVars = %#v, want env_vars normalized", params["envVars"])
	}
	var commands []string
	var cwds []string
	for _, op := range ops {
		if op["type"] == "exec" {
			commands = append(commands, op["command"].(string))
			cwds = append(cwds, op["cwd"].(string))
		}
	}
	wantCommands := []string{"echo health", "echo clone", "echo setup", "echo prereq", "echo final", "rm -f '/tmp/loom-git-askpass' '/tmp/loom-git-deploy-key'"}
	if strings.Join(commands, "\n") != strings.Join(wantCommands, "\n") {
		t.Fatalf("commands = %#v, want %#v", commands, wantCommands)
	}
	if cwds[4] != "/workspace/project" {
		t.Fatalf("final cwd = %q, want hydrated repo cwd", cwds[4])
	}
	if ops[len(ops)-1]["type"] != "delete" {
		t.Fatalf("last op = %#v, want delete after cleanup", ops[len(ops)-1])
	}
}

func TestDaytonaAgentRunnerScriptReportsSandboxProgress(t *testing.T) {
	sdkRoot, _ := installFakeDaytonaSDK(t)
	var mu sync.Mutex
	var progress []AgentRuntimeProgress
	req := AgentRuntimeRequest{
		Provider: domain.RuntimeProviderDaytona,
		Daytona:  map[string]any{"language": "typescript"},
		Command:  "echo final",
		Env:      map[string]string{"LOOM_WORKTREE_PATH": "/workspace/project"},
		CWD:      "/workspace/project",
		HealthCheck: &AgentRuntimeCommand{
			Name:    "fleetdb.health",
			Command: "echo health",
			CWD:     "/",
		},
		Setup: []AgentRuntimeCommand{
			{Name: "setup.1", Command: "echo setup", CWD: "/workspace/project"},
		},
		Progress: func(update AgentRuntimeProgress) {
			mu.Lock()
			defer mu.Unlock()
			progress = append(progress, update)
		},
	}

	result, err := (DaytonaAgentRunner{SDKRoot: sdkRoot}).RunAgent(t.Context(), req)
	if err != nil {
		t.Fatalf("RunAgent() error = %v", err)
	}
	if result.SandboxID != "fake-sdk-sandbox" || result.ExitCode != 0 {
		t.Fatalf("result = %+v, want successful Daytona run", result)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(progress) < 5 {
		t.Fatalf("progress = %#v, want provisioning/setup/running/cleanup updates", progress)
	}
	var sawSandbox, sawSetup, sawRunning, sawDeleted bool
	for _, update := range progress {
		if update.SandboxID == "fake-sdk-sandbox" {
			sawSandbox = true
		}
		if update.Phase == daytonaRuntimePhaseSetup {
			sawSetup = true
		}
		if update.Phase == daytonaRuntimePhaseRunning {
			sawRunning = true
		}
		if update.CleanupState == daytonaCleanupDeleted {
			sawDeleted = true
		}
	}
	if !sawSandbox || !sawSetup || !sawRunning || !sawDeleted {
		t.Fatalf("progress = %#v, want sandbox id, setup, running, and cleanup updates", progress)
	}
}

func TestDaytonaAgentRunnerScriptRehydratesDeclarativeImage(t *testing.T) {
	sdkRoot, logPath := installFakeDaytonaSDK(t)
	req := AgentRuntimeRequest{
		Provider: domain.RuntimeProviderDaytona,
		Daytona: map[string]any{
			"language": "typescript",
			"image": map[string]any{
				"__loomType": "daytona_image",
				"base":       "debian-slim:3.12",
				"steps": []any{
					map[string]any{"op": "runCommands", "commands": []any{"apt-get update", "npm install"}},
					map[string]any{"op": "workdir", "dir": "/workspace/project"},
					map[string]any{"op": "env", "vars": map[string]any{"NODE_ENV": "test"}},
				},
			},
		},
		Command: "echo final",
		Env:     map[string]string{"LOOM_WORKTREE_PATH": "/workspace/project"},
		CWD:     "/workspace/project",
	}

	result, err := (DaytonaAgentRunner{SDKRoot: sdkRoot}).RunAgent(t.Context(), req)
	if err != nil {
		t.Fatalf("RunAgent() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("result = %+v, want successful final command", result)
	}
	ops := readFakeDaytonaOps(t, logPath)
	params := ops[1]["params"].(map[string]any)
	image := params["image"].(map[string]any)
	if image["__sdkImage"] != true || image["base"] != "debian-slim:3.12" {
		t.Fatalf("image = %#v, want rehydrated SDK Image", image)
	}
	calls := image["calls"].([]any)
	if len(calls) != 3 {
		t.Fatalf("image calls = %#v, want runCommands/workdir/env", calls)
	}
	first := calls[0].(map[string]any)
	if first["op"] != "runCommands" {
		t.Fatalf("first image call = %#v, want runCommands", first)
	}
	commands := first["commands"].([]any)
	if len(commands) != 2 || commands[0] != "apt-get update" || commands[1] != "npm install" {
		t.Fatalf("runCommands args = %#v, want flattened command args", commands)
	}
	second := calls[1].(map[string]any)
	if second["op"] != "workdir" || second["dir"] != "/workspace/project" {
		t.Fatalf("second image call = %#v, want workdir", second)
	}
	third := calls[2].(map[string]any)
	vars := third["vars"].(map[string]any)
	if third["op"] != "env" || vars["NODE_ENV"] != "test" {
		t.Fatalf("third image call = %#v, want env", third)
	}
}

func TestDaytonaAgentRunnerScriptSupportsCommandOptionsSignature(t *testing.T) {
	sdkRoot, logPath := installFakeDaytonaSDK(t)
	t.Setenv("DAYTONA_FAKE_EXEC_SIGNATURE", "options")
	req := AgentRuntimeRequest{
		Provider: domain.RuntimeProviderDaytona,
		Daytona:  map[string]any{"language": "typescript"},
		Command:  "echo final",
		Env:      map[string]string{"LOOM_WORKTREE_PATH": "/workspace/project", "OPENAI_API_KEY": "sk-test"},
		CWD:      "/workspace/project",
		Setup: []AgentRuntimeCommand{
			{Name: "setup.1", Command: "echo setup", CWD: "/workspace/project", Env: map[string]string{"SETUP": "1"}},
		},
		TimeoutSeconds: 42,
	}

	result, err := (DaytonaAgentRunner{SDKRoot: sdkRoot}).RunAgent(t.Context(), req)
	if err != nil {
		t.Fatalf("RunAgent() error = %v", err)
	}
	if result.ExitCode != 0 || !strings.Contains(result.Stdout, "ok:echo final") {
		t.Fatalf("result = %+v, want successful final command", result)
	}
	ops := readFakeDaytonaOps(t, logPath)
	var execs []map[string]any
	for _, op := range ops {
		if op["type"] == "exec" {
			execs = append(execs, op)
		}
	}
	if len(execs) != 2 {
		t.Fatalf("execs = %#v, want setup and final exec", execs)
	}
	if execs[1]["cwd"] != "/workspace/project" || execs[1]["timeout"] != float64(42) {
		t.Fatalf("final exec = %#v, want cwd and timeout forwarded through options object", execs[1])
	}
	env := execs[1]["env"].(map[string]any)
	if env["OPENAI_API_KEY"] != "sk-test" {
		t.Fatalf("final env = %#v, want declared env forwarded through options object", env)
	}
}

func TestDaytonaAgentRunnerScriptReadsDocumentedDaytonaOutputShape(t *testing.T) {
	sdkRoot, _ := installFakeDaytonaSDK(t)
	t.Setenv("DAYTONA_FAKE_RESPONSE_SHAPE", "daytona")
	req := AgentRuntimeRequest{
		Provider: domain.RuntimeProviderDaytona,
		Daytona:  map[string]any{"language": "typescript"},
		Command:  "echo final",
		Env:      map[string]string{"LOOM_WORKTREE_PATH": "/workspace/project"},
		CWD:      "/workspace/project",
	}

	result, err := (DaytonaAgentRunner{SDKRoot: sdkRoot}).RunAgent(t.Context(), req)
	if err != nil {
		t.Fatalf("RunAgent() error = %v", err)
	}
	if result.ExitCode != 0 || !strings.Contains(result.Stdout, "artifact:echo final") {
		t.Fatalf("result = %+v, want stdout from Daytona artifacts/result response shape", result)
	}
}

func TestDaytonaAgentRunnerScriptRetentionPolicyKeepsSandbox(t *testing.T) {
	for name, daytona := range map[string]map[string]any{
		"delete_after_run_false": {"delete_after_run": false},
		"deleteAfterRun_false":   {"deleteAfterRun": false},
		"keep_sandbox_true":      {"keep_sandbox": true},
		"keepSandbox_true":       {"keepSandbox": true},
	} {
		t.Run(name, func(t *testing.T) {
			sdkRoot, logPath := installFakeDaytonaSDK(t)
			req := AgentRuntimeRequest{
				Provider: domain.RuntimeProviderDaytona,
				Daytona:  daytona,
				Command:  "echo final",
				Env:      map[string]string{"LOOM_WORKTREE_PATH": "/workspace/project"},
				CWD:      "/workspace/project",
			}

			result, err := (DaytonaAgentRunner{SDKRoot: sdkRoot}).RunAgent(t.Context(), req)
			if err != nil {
				t.Fatalf("RunAgent() error = %v", err)
			}
			if result.SandboxID != "fake-sdk-sandbox" ||
				result.ExitCode != 0 ||
				result.Phase != daytonaRuntimePhaseRunning ||
				result.CleanupState != daytonaCleanupRetained {
				t.Fatalf("result = %+v, want retained sandbox", result)
			}
			ops := readFakeDaytonaOps(t, logPath)
			if len(ops) < 2 {
				t.Fatalf("ops = %#v, want client/create", ops)
			}
			params := ops[1]["params"].(map[string]any)
			if _, ok := params["delete_after_run"]; ok {
				t.Fatalf("create params leaked delete_after_run: %#v", params)
			}
			if _, ok := params["deleteAfterRun"]; ok {
				t.Fatalf("create params leaked deleteAfterRun: %#v", params)
			}
			if _, ok := params["keep_sandbox"]; ok {
				t.Fatalf("create params leaked keep_sandbox: %#v", params)
			}
			if _, ok := params["keepSandbox"]; ok {
				t.Fatalf("create params leaked keepSandbox: %#v", params)
			}
			for _, op := range ops {
				if op["type"] == "delete" {
					t.Fatalf("delete ran despite retention policy: %#v", op)
				}
			}
		})
	}
}

func TestDaytonaAgentRunnerScriptSetupFailureSkipsFinalCommand(t *testing.T) {
	sdkRoot, logPath := installFakeDaytonaSDK(t)
	env := map[string]string{"LOOM_WORKTREE_PATH": "/workspace/project"}
	req := AgentRuntimeRequest{
		Provider: domain.RuntimeProviderDaytona,
		Daytona:  map[string]any{"language": "typescript"},
		Command:  "echo final",
		Env:      env,
		CWD:      "/workspace/project",
		Setup: []AgentRuntimeCommand{
			{Name: "repo.materialize", Command: "echo clone", CWD: "/", Env: env},
			{Name: "setup.1", Command: "fail setup", CWD: "/workspace/project", Env: env},
		},
	}

	result, err := (DaytonaAgentRunner{SDKRoot: sdkRoot}).RunAgent(t.Context(), req)
	if err != nil {
		t.Fatalf("RunAgent() error = %v", err)
	}
	if result.ExitCode != 1 || result.Phase != daytonaRuntimePhaseSetup || result.CleanupState != daytonaCleanupDeleted ||
		!strings.Contains(result.Stderr, "setup.1 failed") ||
		!strings.Contains(result.Stderr, "setup failed from fake sdk") {
		t.Fatalf("result = %+v, want setup failure", result)
	}
	ops := readFakeDaytonaOps(t, logPath)
	for _, op := range ops {
		if op["type"] == "exec" && op["command"] == "echo final" {
			t.Fatalf("final command ran after setup failure: %#v", ops)
		}
	}
	if ops[len(ops)-1]["type"] != "delete" {
		t.Fatalf("last op = %#v, want delete", ops[len(ops)-1])
	}
}

func TestDaytonaAgentRunnerScriptCancellationDuringProvisioningReturnsNotStarted(t *testing.T) {
	sdkRoot, logPath := installFakeDaytonaSDK(t)
	req := AgentRuntimeRequest{
		Provider: domain.RuntimeProviderDaytona,
		Daytona:  map[string]any{"language": "typescript", "blockCreate": true},
		Command:  "echo final",
		Env:      map[string]string{"LOOM_WORKTREE_PATH": "/workspace/project"},
		CWD:      "/workspace/project",
	}
	ctx, cancel := context.WithCancel(t.Context())
	type runResult struct {
		result AgentRuntimeResult
		err    error
	}
	done := make(chan runResult, 1)
	go func() {
		result, err := (DaytonaAgentRunner{SDKRoot: sdkRoot}).RunAgent(ctx, req)
		done <- runResult{result: result, err: err}
	}()

	waitForFakeDaytonaOp(t, logPath, "create")
	cancel()
	var got runResult
	select {
	case got = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunAgent did not return after provisioning cancellation")
	}
	if got.err != nil {
		t.Fatalf("RunAgent() error = %v", got.err)
	}
	if got.result.SandboxID != "" ||
		got.result.ExitCode != 130 ||
		got.result.Phase != daytonaRuntimePhaseStopping ||
		got.result.CleanupState != "not_started" {
		t.Fatalf("result = %+v, want canceled provisioning without sandbox", got.result)
	}
	ops := readFakeDaytonaOps(t, logPath)
	for _, op := range ops {
		if op["type"] == "delete" {
			t.Fatalf("delete ran despite no sandbox being created: %#v", ops)
		}
	}
}

func TestDaytonaAgentRunnerScriptCancellationDuringSetupDeletesSandbox(t *testing.T) {
	sdkRoot, logPath := installFakeDaytonaSDK(t)
	env := map[string]string{"LOOM_WORKTREE_PATH": "/workspace/project"}
	req := AgentRuntimeRequest{
		Provider: domain.RuntimeProviderDaytona,
		Daytona:  map[string]any{"language": "typescript"},
		Command:  "echo final",
		Env:      env,
		CWD:      "/workspace/project",
		Setup: []AgentRuntimeCommand{
			{Name: "setup.1", Command: "block forever setup", CWD: "/workspace/project", Env: env},
		},
	}
	ctx, cancel := context.WithCancel(t.Context())
	type runResult struct {
		result AgentRuntimeResult
		err    error
	}
	done := make(chan runResult, 1)
	go func() {
		result, err := (DaytonaAgentRunner{SDKRoot: sdkRoot}).RunAgent(ctx, req)
		done <- runResult{result: result, err: err}
	}()

	waitForFakeDaytonaExec(t, logPath, "block forever setup")
	cancel()
	var got runResult
	select {
	case got = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunAgent did not return after setup cancellation")
	}
	if got.err != nil {
		t.Fatalf("RunAgent() error = %v", got.err)
	}
	if got.result.SandboxID != "fake-sdk-sandbox" ||
		got.result.ExitCode != 130 ||
		got.result.Phase != daytonaRuntimePhaseStopping ||
		got.result.CleanupState != daytonaCleanupDeleted {
		t.Fatalf("result = %+v, want canceled setup with deleted sandbox", got.result)
	}
	ops := readFakeDaytonaOps(t, logPath)
	for _, op := range ops {
		if op["type"] == "exec" && op["command"] == "echo final" {
			t.Fatalf("final command ran after setup cancellation: %#v", ops)
		}
	}
	if ops[len(ops)-1]["type"] != "delete" {
		t.Fatalf("last op = %#v, want delete after setup cancellation; ops=%#v", ops[len(ops)-1], ops)
	}
}

func TestDaytonaAgentRunnerScriptCancellationDeletesSandbox(t *testing.T) {
	sdkRoot, logPath := installFakeDaytonaSDK(t)
	req := AgentRuntimeRequest{
		Provider: domain.RuntimeProviderDaytona,
		Daytona:  map[string]any{"language": "typescript"},
		Command:  "block forever",
		Env:      map[string]string{"LOOM_WORKTREE_PATH": "/workspace/project"},
		CWD:      "/workspace/project",
	}
	ctx, cancel := context.WithCancel(t.Context())
	type runResult struct {
		result AgentRuntimeResult
		err    error
	}
	done := make(chan runResult, 1)
	go func() {
		result, err := (DaytonaAgentRunner{SDKRoot: sdkRoot}).RunAgent(ctx, req)
		done <- runResult{result: result, err: err}
	}()

	waitForFakeDaytonaExec(t, logPath, "block forever")
	cancel()
	var got runResult
	select {
	case got = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunAgent did not return after cancellation")
	}
	if got.err != nil {
		t.Fatalf("RunAgent() error = %v", got.err)
	}
	if got.result.SandboxID != "fake-sdk-sandbox" ||
		got.result.ExitCode != 130 ||
		got.result.Phase != daytonaRuntimePhaseStopping ||
		got.result.CleanupState != daytonaCleanupDeleted {
		t.Fatalf("result = %+v, want canceled run with deleted sandbox", got.result)
	}
	ops := readFakeDaytonaOps(t, logPath)
	if ops[len(ops)-1]["type"] != "delete" {
		t.Fatalf("last op = %#v, want delete after cancellation; ops=%#v", ops[len(ops)-1], ops)
	}
}

func TestDaytonaRuntimeFanoutCreatesSandboxPerAgent(t *testing.T) {
	setDaytonaControlPlaneEnv(t)
	localWorktree := t.TempDir()
	runner := &fakeDaytonaRunner{}
	roleConfig := daytonaTestRoleConfig()
	s := newDaytonaTestSupervisor(t, runner, roleConfig)

	for i := 0; i < 10; i++ {
		ap := &AgentProcess{
			Entry:          cfgpkg.AgentEntry{Worktree: fmt.Sprintf("worker-%02d", i+1), Role: "task", Mode: domain.AgentModeEphemeral},
			RoleConfig:     roleConfig,
			WorktreePath:   localWorktree,
			AssignedTaskID: fmt.Sprintf("task-%02d", i+1),
		}
		if !s.Concurrency.Acquire(ap.Entry.Role) {
			t.Fatal("concurrency acquire failed")
		}
		s.spawnAndWait(ap)
		if ap.Cmd != nil || ap.Pid != 0 {
			t.Fatalf("agent %s ran local subprocess: cmd=%v pid=%d", ap.Entry.Worktree, ap.Cmd, ap.Pid)
		}
		if ap.DaytonaSandboxID == "" {
			t.Fatalf("agent %s missing Daytona sandbox id", ap.Entry.Worktree)
		}
		if ap.DaytonaRuntimePhase != daytonaRuntimePhaseStopped {
			t.Fatalf("agent %s runtime phase = %q, want stopped", ap.Entry.Worktree, ap.DaytonaRuntimePhase)
		}
		if ap.LastExitCode != 0 {
			t.Fatalf("agent %s exit = %d, want 0", ap.Entry.Worktree, ap.LastExitCode)
		}
	}

	requests, sandboxIDs := runner.snapshot()
	if len(requests) != 10 || len(sandboxIDs) != 10 {
		t.Fatalf("runner calls = %d sandbox ids = %d, want 10 each", len(requests), len(sandboxIDs))
	}
	seen := map[string]bool{}
	for i, req := range requests {
		if seen[sandboxIDs[i]] {
			t.Fatalf("duplicate sandbox id %q in %v", sandboxIDs[i], sandboxIDs)
		}
		seen[sandboxIDs[i]] = true
		if req.Provider != domain.RuntimeProviderDaytona {
			t.Fatalf("request provider = %q, want daytona", req.Provider)
		}
		if req.CWD != "/workspace/project" || len(req.Args) < 3 || req.Args[2] != "/workspace/project" {
			t.Fatalf("request cwd/args = %q/%v, want remote worktree", req.CWD, req.Args)
		}
		if req.HealthCheck == nil || !strings.Contains(req.HealthCheck.Command, "https://fleet.example.test/health") {
			t.Fatalf("request health check = %#v, want FleetDB /health check", req.HealthCheck)
		}
		if !strings.Contains(req.HealthCheck.Command, "X-Fleet-API-Key: ${LOOM_FLEET_DB_API_KEY}") ||
			!strings.Contains(req.HealthCheck.Command, "X-Actor: ${LOOM_FLEET_DB_ACTOR}") {
			t.Fatalf("request health check = %q, want FleetDB auth headers from env", req.HealthCheck.Command)
		}
		if strings.Contains(req.HealthCheck.Command, "fleet-secret") {
			t.Fatalf("request health check leaked FleetDB API key: %q", req.HealthCheck.Command)
		}
		if len(req.Setup) < 2 ||
			req.Setup[0].Name != "repo.materialize" ||
			!strings.Contains(req.Setup[0].Command, "git") ||
			!strings.Contains(req.Setup[0].Command, "https://github.com/acme/project.git") ||
			req.Setup[1].Command != "npm install" {
			t.Fatalf("request setup = %#v, want clone before install", req.Setup)
		}
		if strings.Contains(req.Command, localWorktree) {
			t.Fatalf("remote command contains local worktree %q: %s", localWorktree, req.Command)
		}
		if got := req.Env["LOOM_WORKTREE_PATH"]; got != "/workspace/project" {
			t.Fatalf("LOOM_WORKTREE_PATH = %q, want remote cwd", got)
		}
		if got := req.Env["LOOM_YIELD_FILE"]; got != "/workspace/project/"+YieldFileName {
			t.Fatalf("LOOM_YIELD_FILE = %q, want remote yield path", got)
		}
		if req.Env[bootstrap.EnvFleetDBURL] != "https://fleet.example.test" ||
			req.Env[bootstrap.EnvWorkspace] != "WS" ||
			req.Env["LOOM_ISSUE_BACKEND"] != "fleetdb" {
			t.Fatalf("remote control-plane env = %+v", req.Env)
		}
		for _, forbidden := range []string{"CODEX_HOME", "HOME", "PATH", "PWD", "SSH_AUTH_SOCK", "LOOM_DAEMON_SOCKET", "LOOM_EVENTS_DIR", "LOOM_WORKSPACE_RUNTIME_DIR"} {
			if _, ok := req.Env[forbidden]; ok {
				t.Fatalf("remote env leaked local path variable %s in %+v", forbidden, req.Env)
			}
		}
	}
}

func TestDaytonaRuntimeSupportsFleetDBActorOnlyAuth(t *testing.T) {
	setDaytonaControlPlaneEnv(t)
	t.Setenv(bootstrap.EnvFleetDBAPIKey, "")
	t.Setenv(bootstrap.EnvFleetDBActor, "daytona-dev-actor")
	roleConfig := daytonaTestRoleConfig()
	runner := &fakeDaytonaRunner{}
	s := newDaytonaTestSupervisor(t, runner, roleConfig)
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker-actor-auth", Role: "task"},
		RoleConfig:   roleConfig,
		WorktreePath: t.TempDir(),
	}
	if _, err := s.runDaytonaAgent(ap); err != nil {
		t.Fatalf("runDaytonaAgent: %v", err)
	}
	requests, _ := runner.snapshot()
	if len(requests) != 1 {
		t.Fatalf("runner calls = %d, want 1", len(requests))
	}
	if requests[0].Env[bootstrap.EnvFleetDBAPIKey] != "" ||
		requests[0].Env[bootstrap.EnvFleetDBActor] != "daytona-dev-actor" {
		t.Fatalf("FleetDB auth env = %+v, want actor-only auth", requests[0].Env)
	}
	if requests[0].HealthCheck == nil ||
		!strings.Contains(requests[0].HealthCheck.Command, "X-Actor: ${LOOM_FLEET_DB_ACTOR}") {
		t.Fatalf("health check = %#v, want actor header support", requests[0].HealthCheck)
	}
}

func TestDaytonaRuntimeCancelsRemoteRunOnAgentStop(t *testing.T) {
	setDaytonaControlPlaneEnv(t)
	roleConfig := daytonaTestRoleConfig()
	runner := &blockingDaytonaRunner{
		started: make(chan AgentRuntimeRequest, 1),
		done:    make(chan struct{}),
	}
	s := newDaytonaTestSupervisor(t, runner, roleConfig)
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker-stop", Role: "task", Mode: domain.AgentModeEphemeral},
		RoleConfig:   roleConfig,
		WorktreePath: t.TempDir(),
		StopCh:       make(chan struct{}),
	}
	if !s.Concurrency.Acquire(ap.Entry.Role) {
		t.Fatal("concurrency acquire failed")
	}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		s.spawnAndWait(ap)
	}()

	select {
	case <-runner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("Daytona runner did not start")
	}
	close(ap.StopCh)
	select {
	case <-runner.done:
	case <-time.After(2 * time.Second):
		t.Fatal("Daytona runner was not canceled after agent stop")
	}
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("spawnAndWait did not return after remote runner cancellation")
	}
	if ap.Cmd != nil || ap.Pid != 0 {
		t.Fatalf("remote agent should not have local process state: cmd=%v pid=%d", ap.Cmd, ap.Pid)
	}
}

func TestDaytonaRuntimeProgressUpdatesAgentStateWhileRunning(t *testing.T) {
	setDaytonaControlPlaneEnv(t)
	roleConfig := daytonaTestRoleConfig()
	runner := &progressBlockingDaytonaRunner{
		started: make(chan AgentRuntimeRequest, 1),
		done:    make(chan struct{}),
	}
	s := newDaytonaTestSupervisor(t, runner, roleConfig)
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker-progress", Role: "task", Mode: domain.AgentModeEphemeral},
		RoleConfig:   roleConfig,
		WorktreePath: t.TempDir(),
		StopCh:       make(chan struct{}),
	}
	if !s.Concurrency.Acquire(ap.Entry.Role) {
		t.Fatal("concurrency acquire failed")
	}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		s.spawnAndWait(ap)
	}()

	select {
	case <-runner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("Daytona runner did not start")
	}
	ap.Mu.Lock()
	sandboxID := ap.DaytonaSandboxID
	phase := ap.DaytonaRuntimePhase
	cleanup := ap.DaytonaCleanupState
	ap.Mu.Unlock()
	if sandboxID != "progress-sandbox" || phase != daytonaRuntimePhaseRunning || cleanup != daytonaCleanupPending {
		t.Fatalf("runtime state while running = sandbox:%q phase:%q cleanup:%q, want live progress state",
			sandboxID, phase, cleanup)
	}

	close(ap.StopCh)
	select {
	case <-runner.done:
	case <-time.After(2 * time.Second):
		t.Fatal("Daytona runner was not canceled after agent stop")
	}
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("spawnAndWait did not return after remote runner cancellation")
	}
}

func TestDaytonaRuntimeDrainAgentCancelsRemoteRun(t *testing.T) {
	setDaytonaControlPlaneEnv(t)
	roleConfig := daytonaTestRoleConfig()
	runner := &blockingDaytonaRunner{
		started: make(chan AgentRuntimeRequest, 1),
		done:    make(chan struct{}),
	}
	s := newDaytonaTestSupervisor(t, runner, roleConfig)
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker-drain", Role: "task", Mode: domain.AgentModeEphemeral},
		RoleConfig:   roleConfig,
		WorktreePath: t.TempDir(),
		StopCh:       make(chan struct{}),
		Done:         make(chan struct{}),
	}
	s.AgentsMu.Lock()
	s.Agents = append(s.Agents, ap)
	s.AgentsMu.Unlock()
	s.startAgentSupervisor(ap)

	select {
	case <-runner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("Daytona runner did not start")
	}

	drained := make(chan error, 1)
	go func() {
		drained <- s.DrainAgent("worker-drain")
	}()
	select {
	case <-runner.done:
	case <-time.After(2 * time.Second):
		t.Fatal("Daytona runner was not canceled by DrainAgent")
	}
	select {
	case err := <-drained:
		if err != nil {
			t.Fatalf("DrainAgent error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("DrainAgent did not return after remote cancellation")
	}
	if s.AgentCount() != 0 {
		t.Fatalf("AgentCount() = %d after drain, want 0", s.AgentCount())
	}
	if ap.Cmd != nil || ap.Pid != 0 {
		t.Fatalf("remote agent should not have local process state: cmd=%v pid=%d", ap.Cmd, ap.Pid)
	}
	if ap.DaytonaRuntimePhase != daytonaRuntimePhaseStopping {
		t.Fatalf("runtime phase = %q, want stopping after drain cancellation", ap.DaytonaRuntimePhase)
	}
	if ap.DaytonaCleanupState != daytonaCleanupFailed {
		t.Fatalf("cleanup state = %q, want failed for runner-level cancellation without sandbox result", ap.DaytonaCleanupState)
	}
}

func TestDaytonaRuntimePreflightRequiresRemoteFleetDB(t *testing.T) {
	roleConfig := daytonaTestRoleConfig()
	for _, tc := range []struct {
		name string
		url  string
		want string
	}{
		{name: "missing", url: "", want: bootstrap.EnvFleetDBURL},
		{name: "localhost", url: "http://localhost:8080", want: "local-only"},
		{name: "loopback", url: "http://127.0.0.1:8080", want: "local-only"},
		{name: "ipv6-loopback", url: "http://[::1]:8080", want: "local-only"},
		{name: "private", url: "http://10.0.0.12:8080", want: "local-only"},
		{name: "host-docker", url: "http://host.docker.internal:8080", want: "local-only"},
		{name: "unix-path", url: "/tmp/fleet.sock", want: "scheme"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(bootstrap.EnvFleetDBURL, tc.url)
			t.Setenv(bootstrap.EnvFleetDBAPIKey, "fleet-secret")
			t.Setenv("OPENAI_API_KEY", "sk-daytona-test")
			runner := &fakeDaytonaRunner{}
			s := newDaytonaTestSupervisor(t, runner, roleConfig)
			ap := &AgentProcess{
				Entry:        cfgpkg.AgentEntry{Worktree: "worker-preflight", Role: "task"},
				RoleConfig:   roleConfig,
				WorktreePath: t.TempDir(),
			}
			_, err := s.runDaytonaAgent(ap)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("runDaytonaAgent error = %v, want containing %q", err, tc.want)
			}
			requests, _ := runner.snapshot()
			if len(requests) != 0 {
				t.Fatalf("runner called despite failed preflight: %#v", requests)
			}
			if ap.DaytonaRuntimePhase != daytonaRuntimePhaseFailed {
				t.Fatalf("runtime phase = %q, want failed", ap.DaytonaRuntimePhase)
			}
		})
	}
}

func TestDaytonaRuntimePreflightRequiresWorkspaceAndFleetDBAuth(t *testing.T) {
	roleConfig := daytonaTestRoleConfig()
	for _, tc := range []struct {
		name        string
		workspaceID string
		apiKey      string
		actor       string
		want        string
	}{
		{name: "missing-workspace", workspaceID: "", apiKey: "fleet-secret", want: bootstrap.EnvWorkspace},
		{name: "missing-auth", workspaceID: "WS", want: bootstrap.EnvFleetDBAPIKey},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(bootstrap.EnvFleetDBURL, "https://fleet.example.test")
			t.Setenv(bootstrap.EnvFleetDBAPIKey, tc.apiKey)
			t.Setenv(bootstrap.EnvFleetDBActor, tc.actor)
			t.Setenv("OPENAI_API_KEY", "sk-daytona-test")
			runner := &fakeDaytonaRunner{}
			s := newDaytonaTestSupervisor(t, runner, roleConfig)
			s.WorkspaceID = tc.workspaceID
			ap := &AgentProcess{
				Entry:        cfgpkg.AgentEntry{Worktree: "worker-control-plane", Role: "task"},
				RoleConfig:   roleConfig,
				WorktreePath: t.TempDir(),
			}
			_, err := s.runDaytonaAgent(ap)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("runDaytonaAgent error = %v, want containing %q", err, tc.want)
			}
			requests, _ := runner.snapshot()
			if len(requests) != 0 {
				t.Fatalf("runner called despite failed preflight: %#v", requests)
			}
			if ap.DaytonaRuntimePhase != daytonaRuntimePhaseFailed {
				t.Fatalf("runtime phase = %q, want failed", ap.DaytonaRuntimePhase)
			}
		})
	}
}

func TestDaytonaRuntimePreflightRequiresDaytonaProviderAPIKey(t *testing.T) {
	for _, tc := range []struct {
		name      string
		apiKeyEnv string
		value     string
		want      string
	}{
		{name: "default-missing", value: "", want: "DAYTONA_API_KEY"},
		{name: "custom-missing", apiKeyEnv: "DAYTONA_KEY", value: "", want: "DAYTONA_KEY"},
		{name: "invalid-env-name", apiKeyEnv: "1BAD", value: "dtn-present", want: "valid environment variable name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setDaytonaControlPlaneEnv(t)
			roleConfig := daytonaTestRoleConfig()
			envName := "DAYTONA_API_KEY"
			if tc.apiKeyEnv != "" {
				roleConfig.RuntimeDaytona["api_key_env"] = tc.apiKeyEnv
				envName = tc.apiKeyEnv
			}
			if isShellIdentifier(envName) {
				t.Setenv(envName, tc.value)
			}
			runner := &fakeDaytonaRunner{}
			s := newDaytonaTestSupervisor(t, runner, roleConfig)
			ap := &AgentProcess{
				Entry:        cfgpkg.AgentEntry{Worktree: "worker-daytona-auth", Role: "task"},
				RoleConfig:   roleConfig,
				WorktreePath: t.TempDir(),
			}
			_, err := s.runDaytonaAgent(ap)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("runDaytonaAgent error = %v, want containing %q", err, tc.want)
			}
			requests, _ := runner.snapshot()
			if len(requests) != 0 {
				t.Fatalf("runner called despite failed preflight: %#v", requests)
			}
			if ap.DaytonaRuntimePhase != daytonaRuntimePhaseFailed {
				t.Fatalf("runtime phase = %q, want failed", ap.DaytonaRuntimePhase)
			}
		})
	}
}

func TestDaytonaRuntimePreflightRejectsDirectSecretConfig(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
	}{
		{name: "daytona-api-key", key: "api_key"},
		{name: "openai-api-key", key: "openai_api_key"},
		{name: "github-token", key: "github_token"},
		{name: "deploy-key", key: "git_deploy_key"},
		{name: "generic-token", key: "token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setDaytonaControlPlaneEnv(t)
			roleConfig := daytonaTestRoleConfig()
			roleConfig.RuntimeDaytona[tc.key] = "inline-secret-value"
			runner := &fakeDaytonaRunner{}
			s := newDaytonaTestSupervisor(t, runner, roleConfig)
			ap := &AgentProcess{
				Entry:        cfgpkg.AgentEntry{Worktree: "worker-direct-secret", Role: "task"},
				RoleConfig:   roleConfig,
				WorktreePath: t.TempDir(),
			}
			_, err := s.runDaytonaAgent(ap)
			if err == nil || !strings.Contains(err.Error(), tc.key) || !strings.Contains(err.Error(), "*_env") {
				t.Fatalf("runDaytonaAgent error = %v, want direct credential rejection for %s", err, tc.key)
			}
			requests, _ := runner.snapshot()
			if len(requests) != 0 {
				t.Fatalf("runner called despite failed direct-secret preflight: %#v", requests)
			}
			if ap.DaytonaRuntimePhase != daytonaRuntimePhaseFailed {
				t.Fatalf("runtime phase = %q, want failed", ap.DaytonaRuntimePhase)
			}
		})
	}
}

func TestDaytonaRuntimePreflightRequiresRepoURL(t *testing.T) {
	setDaytonaControlPlaneEnv(t)
	roleConfig := daytonaTestRoleConfig()
	delete(roleConfig.RuntimeDaytona, "repo_url")
	runner := &fakeDaytonaRunner{}
	s := newDaytonaTestSupervisor(t, runner, roleConfig)
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker-missing-repo", Role: "task"},
		RoleConfig:   roleConfig,
		WorktreePath: t.TempDir(),
	}
	_, err := s.runDaytonaAgent(ap)
	if err == nil || !strings.Contains(err.Error(), "repo URL") {
		t.Fatalf("runDaytonaAgent error = %v, want missing repo URL preflight", err)
	}
	requests, _ := runner.snapshot()
	if len(requests) != 0 {
		t.Fatalf("runner called despite failed preflight: %#v", requests)
	}
	if ap.DaytonaRuntimePhase != daytonaRuntimePhaseFailed {
		t.Fatalf("runtime phase = %q, want failed", ap.DaytonaRuntimePhase)
	}
}

func TestDaytonaRuntimePreflightRejectsHostLocalRepoURL(t *testing.T) {
	for _, tc := range []struct {
		name string
		url  string
		want string
	}{
		{name: "absolute-path", url: "/tmp/project.git", want: "host-local"},
		{name: "relative-path", url: "../project.git", want: "host-local"},
		{name: "file-url", url: "file:///tmp/project.git", want: "file://"},
		{name: "localhost-url", url: "https://localhost/acme/project.git", want: "local-only"},
		{name: "loopback-ssh-url", url: "ssh://git@127.0.0.1/acme/project.git", want: "local-only"},
		{name: "scp-localhost", url: "git@localhost:acme/project.git", want: "local-only"},
		{name: "unsupported-scheme", url: "ftp://git.example.test/acme/project.git", want: "unsupported scheme"},
		{name: "malformed-https-scheme", url: "https:git.example.test/acme/project.git", want: "malformed scheme"},
		{name: "scp-missing-path", url: "git@github.com:", want: "Git remote URL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setDaytonaControlPlaneEnv(t)
			roleConfig := daytonaTestRoleConfig()
			roleConfig.RuntimeDaytona["repo_url"] = tc.url
			runner := &fakeDaytonaRunner{}
			s := newDaytonaTestSupervisor(t, runner, roleConfig)
			ap := &AgentProcess{
				Entry:        cfgpkg.AgentEntry{Worktree: "worker-local-repo", Role: "task"},
				RoleConfig:   roleConfig,
				WorktreePath: t.TempDir(),
			}
			_, err := s.runDaytonaAgent(ap)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("runDaytonaAgent error = %v, want containing %q", err, tc.want)
			}
			requests, _ := runner.snapshot()
			if len(requests) != 0 {
				t.Fatalf("runner called despite failed repo URL preflight: %#v", requests)
			}
			if ap.DaytonaRuntimePhase != daytonaRuntimePhaseFailed {
				t.Fatalf("runtime phase = %q, want failed", ap.DaytonaRuntimePhase)
			}
		})
	}
}

func TestDaytonaRuntimePreflightRequiresConfiguredGitAuthEnv(t *testing.T) {
	for _, tc := range []struct {
		name       string
		runtimeKey string
		envName    string
		want       string
	}{
		{name: "missing-token", runtimeKey: "git_token_env", envName: "MISSING_GITHUB_TOKEN", want: "git token env MISSING_GITHUB_TOKEN"},
		{name: "missing-deploy-key", runtimeKey: "git_deploy_key_env", envName: "MISSING_GIT_DEPLOY_KEY", want: "git deploy key env MISSING_GIT_DEPLOY_KEY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setDaytonaControlPlaneEnv(t)
			roleConfig := daytonaTestRoleConfig()
			roleConfig.RuntimeDaytona[tc.runtimeKey] = tc.envName
			runner := &fakeDaytonaRunner{}
			s := newDaytonaTestSupervisor(t, runner, roleConfig)
			ap := &AgentProcess{
				Entry:        cfgpkg.AgentEntry{Worktree: "worker-missing-git-auth", Role: "task"},
				RoleConfig:   roleConfig,
				WorktreePath: t.TempDir(),
			}
			_, err := s.runDaytonaAgent(ap)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("runDaytonaAgent error = %v, want containing %q", err, tc.want)
			}
			requests, _ := runner.snapshot()
			if len(requests) != 0 {
				t.Fatalf("runner called despite failed git auth preflight: %#v", requests)
			}
			if ap.DaytonaRuntimePhase != daytonaRuntimePhaseFailed {
				t.Fatalf("runtime phase = %q, want failed", ap.DaytonaRuntimePhase)
			}
		})
	}
}

func TestDaytonaRuntimeNormalizesAndRejectsUnsafeRemoteCWD(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cwd     string
		wantCWD string
		wantErr string
	}{
		{name: "trailing-slash", cwd: "/workspace/project/", wantCWD: "/workspace/project"},
		{name: "workspace-root", cwd: "/workspace/", wantErr: `"/workspace"`},
		{name: "dotdot-to-workspace-root", cwd: "/workspace/project/..", wantErr: `"/workspace"`},
		{name: "home-root", cwd: "/home/daytona/..", wantErr: `"/home"`},
		{name: "root", cwd: "/workspace/../..", wantErr: `"/"`},
		{name: "relative", cwd: "workspace/project", wantErr: `"workspace/project"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setDaytonaControlPlaneEnv(t)
			roleConfig := daytonaTestRoleConfig()
			roleConfig.RuntimeCWD = tc.cwd
			runner := &fakeDaytonaRunner{}
			s := newDaytonaTestSupervisor(t, runner, roleConfig)
			ap := &AgentProcess{
				Entry:        cfgpkg.AgentEntry{Worktree: "worker-cwd", Role: "task"},
				RoleConfig:   roleConfig,
				WorktreePath: t.TempDir(),
			}
			_, err := s.runDaytonaAgent(ap)
			requests, _ := runner.snapshot()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("runDaytonaAgent error = %v, want containing %q", err, tc.wantErr)
				}
				if len(requests) != 0 {
					t.Fatalf("runner called despite unsafe remote cwd: %#v", requests)
				}
				return
			}
			if err != nil {
				t.Fatalf("runDaytonaAgent error = %v", err)
			}
			if len(requests) != 1 {
				t.Fatalf("runner calls = %d, want 1", len(requests))
			}
			if requests[0].CWD != tc.wantCWD || requests[0].Setup[0].CWD != "/" || !strings.Contains(requests[0].Setup[0].Command, tc.wantCWD) {
				t.Fatalf("request cwd/setup = %q/%#v, want normalized cwd %q", requests[0].CWD, requests[0].Setup[0], tc.wantCWD)
			}
		})
	}
}

func TestDaytonaRuntimeMapsCustomOpenAIAPIKeyEnv(t *testing.T) {
	setDaytonaControlPlaneEnv(t)
	t.Setenv("MY_OPENAI_KEY", "sk-custom-openai")
	roleConfig := daytonaTestRoleConfig()
	roleConfig.RuntimeDaytona["openai_api_key_env"] = "MY_OPENAI_KEY"
	runner := &fakeDaytonaRunner{}
	s := newDaytonaTestSupervisor(t, runner, roleConfig)
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker-openai", Role: "task"},
		RoleConfig:   roleConfig,
		WorktreePath: t.TempDir(),
	}
	if _, err := s.runDaytonaAgent(ap); err != nil {
		t.Fatalf("runDaytonaAgent: %v", err)
	}
	requests, _ := runner.snapshot()
	if len(requests) != 1 {
		t.Fatalf("runner calls = %d, want 1", len(requests))
	}
	if got := requests[0].Env["MY_OPENAI_KEY"]; got != "sk-custom-openai" {
		t.Fatalf("MY_OPENAI_KEY = %q, want custom key forwarded", got)
	}
	if got := requests[0].Env["OPENAI_API_KEY"]; got != "sk-custom-openai" {
		t.Fatalf("OPENAI_API_KEY = %q, want mapped custom key", got)
	}
}

func TestDaytonaRuntimeMapsCodexAuthFileEnvToRemoteCodexHome(t *testing.T) {
	setDaytonaControlPlaneEnv(t)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("CODEX_AUTH_FILE", "/home/daytona/.codex/auth.json")
	roleConfig := daytonaTestRoleConfig()
	delete(roleConfig.RuntimeDaytona, "openai_api_key_env")
	roleConfig.RuntimeDaytona["codex_auth_file_env"] = "CODEX_AUTH_FILE"
	runner := &fakeDaytonaRunner{}
	s := newDaytonaTestSupervisor(t, runner, roleConfig)
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker-codex-auth", Role: "task"},
		RoleConfig:   roleConfig,
		WorktreePath: t.TempDir(),
	}
	if _, err := s.runDaytonaAgent(ap); err != nil {
		t.Fatalf("runDaytonaAgent: %v", err)
	}
	requests, _ := runner.snapshot()
	if len(requests) != 1 {
		t.Fatalf("runner calls = %d, want 1", len(requests))
	}
	if got := requests[0].Env["CODEX_AUTH_FILE"]; got != "/home/daytona/.codex/auth.json" {
		t.Fatalf("CODEX_AUTH_FILE = %q, want remote auth path", got)
	}
	if got := requests[0].Env["CODEX_HOME"]; got != "/home/daytona/.codex" {
		t.Fatalf("CODEX_HOME = %q, want remote auth dir", got)
	}
}

func TestDaytonaRuntimeRejectsHostLocalCodexAuthFile(t *testing.T) {
	setDaytonaControlPlaneEnv(t)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("CODEX_AUTH_FILE", "/Users/example/.codex/auth.json")
	roleConfig := daytonaTestRoleConfig()
	delete(roleConfig.RuntimeDaytona, "openai_api_key_env")
	roleConfig.RuntimeDaytona["codex_auth_file_env"] = "CODEX_AUTH_FILE"
	runner := &fakeDaytonaRunner{}
	s := newDaytonaTestSupervisor(t, runner, roleConfig)
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker-host-auth", Role: "task"},
		RoleConfig:   roleConfig,
		WorktreePath: t.TempDir(),
	}
	_, err := s.runDaytonaAgent(ap)
	if err == nil || !strings.Contains(err.Error(), "host-local") {
		t.Fatalf("runDaytonaAgent error = %v, want host-local auth path rejection", err)
	}
	requests, _ := runner.snapshot()
	if len(requests) != 0 {
		t.Fatalf("runner called despite failed preflight: %#v", requests)
	}
	if ap.DaytonaRuntimePhase != daytonaRuntimePhaseFailed {
		t.Fatalf("runtime phase = %q, want failed", ap.DaytonaRuntimePhase)
	}
}

func TestDaytonaRuntimeCodexRequiresDeclaredCredentials(t *testing.T) {
	setDaytonaControlPlaneEnv(t)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("CODEX_AUTH_FILE", "")
	roleConfig := daytonaTestRoleConfig()
	delete(roleConfig.RuntimeDaytona, "openai_api_key_env")
	delete(roleConfig.RuntimeDaytona, "codex_auth_file_env")
	runner := &fakeDaytonaRunner{}
	s := newDaytonaTestSupervisor(t, runner, roleConfig)
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker-missing-auth", Role: "task"},
		RoleConfig:   roleConfig,
		WorktreePath: t.TempDir(),
	}
	_, err := s.runDaytonaAgent(ap)
	if err == nil || !strings.Contains(err.Error(), "OPENAI_API_KEY") || !strings.Contains(err.Error(), "codex_auth_file_env") {
		t.Fatalf("runDaytonaAgent error = %v, want missing Codex auth preflight", err)
	}
	requests, _ := runner.snapshot()
	if len(requests) != 0 {
		t.Fatalf("runner called despite failed preflight: %#v", requests)
	}
	if ap.DaytonaRuntimePhase != daytonaRuntimePhaseFailed {
		t.Fatalf("runtime phase = %q, want failed", ap.DaytonaRuntimePhase)
	}
}

func TestDaytonaRuntimeFinalCommandFailureRecordsFailedPhase(t *testing.T) {
	setDaytonaControlPlaneEnv(t)
	roleConfig := daytonaTestRoleConfig()
	runner := &fakeDaytonaRunner{
		result: AgentRuntimeResult{
			ExitCode:     2,
			Stderr:       "final command failed\n",
			Phase:        daytonaRuntimePhaseRunning,
			CleanupState: daytonaCleanupDeleted,
		},
	}
	s := newDaytonaTestSupervisor(t, runner, roleConfig)
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker-final-fail", Role: "task"},
		RoleConfig:   roleConfig,
		WorktreePath: t.TempDir(),
	}
	if _, err := s.runDaytonaAgent(ap); err != nil {
		t.Fatalf("runDaytonaAgent: %v", err)
	}
	if ap.LastExitCode != 2 ||
		ap.DaytonaRuntimePhase != daytonaRuntimePhaseFailed ||
		ap.DaytonaCleanupState != daytonaCleanupDeleted {
		t.Fatalf("agent exit/phase/cleanup = %d/%q/%q, want final command failure",
			ap.LastExitCode, ap.DaytonaRuntimePhase, ap.DaytonaCleanupState)
	}
}

func TestDaytonaRuntimeMaterializeUsesConfiguredGitAuth(t *testing.T) {
	setDaytonaControlPlaneEnv(t)
	t.Setenv("GITHUB_TOKEN", "ghp-secret-token")
	t.Setenv("GIT_DEPLOY_KEY", "-----BEGIN PRIVATE KEY-----\nsecret-key\n-----END PRIVATE KEY-----")
	roleConfig := daytonaTestRoleConfig()
	roleConfig.RuntimeDaytona["git_token_env"] = "GITHUB_TOKEN"
	roleConfig.RuntimeDaytona["git_username"] = "x-access-token"
	roleConfig.RuntimeDaytona["git_deploy_key_env"] = "GIT_DEPLOY_KEY"
	runner := &fakeDaytonaRunner{}
	s := newDaytonaTestSupervisor(t, runner, roleConfig)
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker-git-auth", Role: "task"},
		RoleConfig:   roleConfig,
		WorktreePath: t.TempDir(),
	}
	if _, err := s.runDaytonaAgent(ap); err != nil {
		t.Fatalf("runDaytonaAgent: %v", err)
	}
	requests, _ := runner.snapshot()
	if len(requests) != 1 {
		t.Fatalf("runner calls = %d, want 1", len(requests))
	}
	req := requests[0]
	if req.Env["GITHUB_TOKEN"] != "ghp-secret-token" || req.Env["GIT_DEPLOY_KEY"] == "" {
		t.Fatalf("git auth env not forwarded by explicit grant: %+v", req.Env)
	}
	clone := req.Setup[0].Command
	for _, want := range []string{"GIT_ASKPASS", "GIT_SSH_COMMAND", "GITHUB_TOKEN", "GIT_DEPLOY_KEY"} {
		if !strings.Contains(clone, want) {
			t.Fatalf("clone command = %q, want %s auth wiring", clone, want)
		}
	}
	if strings.Contains(clone, "ghp-secret-token") || strings.Contains(clone, "secret-key") {
		t.Fatalf("clone command leaked secret value: %q", clone)
	}
}

func TestDaytonaRuntimeSetupFailurePreventsSuccessfulAgentRun(t *testing.T) {
	setDaytonaControlPlaneEnv(t)
	roleConfig := daytonaTestRoleConfig()
	runner := &setupFailingDaytonaRunner{}
	s := newDaytonaTestSupervisor(t, runner, roleConfig)
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker-setup-fail", Role: "task", Mode: domain.AgentModeEphemeral},
		RoleConfig:   roleConfig,
		WorktreePath: t.TempDir(),
	}
	if !s.Concurrency.Acquire(ap.Entry.Role) {
		t.Fatal("concurrency acquire failed")
	}
	s.spawnAndWait(ap)
	if len(runner.requests) != 1 {
		t.Fatalf("runner calls = %d, want 1", len(runner.requests))
	}
	if ap.LastExitCode != 1 ||
		ap.DaytonaRuntimePhase != daytonaRuntimePhaseSetup ||
		ap.DaytonaCleanupState != daytonaCleanupDeleted {
		t.Fatalf("agent exit/phase/cleanup = %d/%q/%q, want setup failure",
			ap.LastExitCode, ap.DaytonaRuntimePhase, ap.DaytonaCleanupState)
	}
}

func TestDaytonaRuntimeForwardsOnlyDeclaredSecretEnv(t *testing.T) {
	setDaytonaControlPlaneEnv(t)
	t.Setenv("OPENAI_API_KEY", "sk-openai")
	t.Setenv("GITHUB_TOKEN", "ghp-ungranted")
	t.Setenv("LOOM_NOTIFY_TOKEN", "notify-ungranted")
	t.Setenv("LOOM_SECRET_TOKEN", "loom-secret-ungranted")
	t.Setenv("CUSTOM_GRANTED_HOST_PATH", "/Users/host/.cache")
	roleConfig := daytonaTestRoleConfig()
	roleConfig.RuntimeDaytona["openai_api_key_env"] = "OPENAI_API_KEY"
	roleConfig.RuntimeDaytona["env"] = []any{"CUSTOM_GRANTED_HOST_PATH"}
	roleConfig.RuntimeDaytona["env_vars"] = map[string]any{
		"ANTHROPIC_API_KEY":     "sk-configured",
		"CUSTOM_CACHE_DIR":      "/Users/host/.cache",
		"CUSTOM_FILE_URI":       "file:///Users/host/.cache",
		"CUSTOM_PATHS":          "/workspace/bin:/Users/host/bin",
		"CUSTOM_PRIVATE_SOCKET": "/private/tmp/host.sock",
		"HOME":                  "/Users/host",
		"REMOTE_CACHE_DIR":      "/workspace/.cache",
	}
	runner := &fakeDaytonaRunner{}
	s := newDaytonaTestSupervisor(t, runner, roleConfig)
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker-env", Role: "task"},
		RoleConfig:   roleConfig,
		WorktreePath: t.TempDir(),
	}
	if _, err := s.runDaytonaAgent(ap); err != nil {
		t.Fatalf("runDaytonaAgent: %v", err)
	}
	requests, _ := runner.snapshot()
	if len(requests) != 1 {
		t.Fatalf("runner calls = %d, want 1", len(requests))
	}
	if got := requests[0].Env["OPENAI_API_KEY"]; got != "sk-openai" {
		t.Fatalf("OPENAI_API_KEY = %q, want granted value", got)
	}
	if got := requests[0].Env["ANTHROPIC_API_KEY"]; got != "sk-configured" {
		t.Fatalf("ANTHROPIC_API_KEY = %q, want explicit runtime env var", got)
	}
	if _, ok := requests[0].Env["HOME"]; ok {
		t.Fatalf("HOME was forwarded from explicit env_vars despite host-local block: %+v", requests[0].Env)
	}
	for _, key := range []string{"CUSTOM_CACHE_DIR", "CUSTOM_FILE_URI", "CUSTOM_PATHS", "CUSTOM_PRIVATE_SOCKET"} {
		if _, ok := requests[0].Env[key]; ok {
			t.Fatalf("%s was forwarded despite host-local path value: %+v", key, requests[0].Env)
		}
	}
	if _, ok := requests[0].Env["CUSTOM_GRANTED_HOST_PATH"]; ok {
		t.Fatalf("CUSTOM_GRANTED_HOST_PATH was forwarded despite host-local path grant: %+v", requests[0].Env)
	}
	if got := requests[0].Env["REMOTE_CACHE_DIR"]; got != "/workspace/.cache" {
		t.Fatalf("REMOTE_CACHE_DIR = %q, want remote path forwarded", got)
	}
	if _, ok := requests[0].Env["GITHUB_TOKEN"]; ok {
		t.Fatalf("GITHUB_TOKEN was forwarded without env grant: %+v", requests[0].Env)
	}
	if _, ok := requests[0].Env["LOOM_NOTIFY_TOKEN"]; ok {
		t.Fatalf("LOOM_NOTIFY_TOKEN was forwarded without env grant: %+v", requests[0].Env)
	}
	if _, ok := requests[0].Env["LOOM_SECRET_TOKEN"]; ok {
		t.Fatalf("LOOM_SECRET_TOKEN was forwarded without env grant: %+v", requests[0].Env)
	}
	if requests[0].Env["LOOM_AGENT_NAME"] != "worker-env" ||
		requests[0].Env[bootstrap.EnvFleetDBURL] != "https://fleet.example.test" ||
		requests[0].Env[bootstrap.EnvWorkspace] != "WS" {
		t.Fatalf("required Loom runtime env missing after filtering: %+v", requests[0].Env)
	}
}

func TestDaytonaRuntimeForwardsDeclaredLoomEnvGrant(t *testing.T) {
	setDaytonaControlPlaneEnv(t)
	t.Setenv("LOOM_NOTIFY_TOKEN", "notify-granted")
	roleConfig := daytonaTestRoleConfig()
	roleConfig.RuntimeDaytona["env"] = []any{"LOOM_NOTIFY_TOKEN"}
	runner := &fakeDaytonaRunner{}
	s := newDaytonaTestSupervisor(t, runner, roleConfig)
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker-loom-env", Role: "task"},
		RoleConfig:   roleConfig,
		WorktreePath: t.TempDir(),
	}
	if _, err := s.runDaytonaAgent(ap); err != nil {
		t.Fatalf("runDaytonaAgent: %v", err)
	}
	requests, _ := runner.snapshot()
	if len(requests) != 1 {
		t.Fatalf("runner calls = %d, want 1", len(requests))
	}
	if got := requests[0].Env["LOOM_NOTIFY_TOKEN"]; got != "notify-granted" {
		t.Fatalf("LOOM_NOTIFY_TOKEN = %q, want explicit env grant", got)
	}
}

func TestDaytonaRuntimeRedactsSecretOutput(t *testing.T) {
	setDaytonaControlPlaneEnv(t)
	t.Setenv("OPENAI_API_KEY", "sk-redact-me")
	roleConfig := daytonaTestRoleConfig()
	roleConfig.RuntimeDaytona["env"] = []any{"OPENAI_API_KEY"}
	runner := &fakeDaytonaRunner{
		result: AgentRuntimeResult{
			ExitCode:     0,
			Stdout:       "stdout sk-redact-me\n",
			Stderr:       "stderr fleet-secret\n",
			Phase:        daytonaRuntimePhaseStopped,
			CleanupState: daytonaCleanupDeleted,
		},
	}
	cfg := makeSupervisorConfig(nil, map[string]cfgpkg.RoleConfig{"task": roleConfig})
	cfg.Daemon.LogDir = "logs"
	s := newDaytonaTestSupervisor(t, runner, roleConfig)
	s.ConfigSnapshot = func() *cfgpkg.DaemonConfig { return cfg }
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker-redact", Role: "task"},
		RoleConfig:   roleConfig,
		WorktreePath: t.TempDir(),
	}
	if _, err := s.runDaytonaAgent(ap); err != nil {
		t.Fatalf("runDaytonaAgent: %v", err)
	}
	data, err := os.ReadFile(ap.LogFilePath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	logText := string(data)
	if strings.Contains(logText, "sk-redact-me") || strings.Contains(logText, "fleet-secret") {
		t.Fatalf("log leaked secret: %q", logText)
	}
	if !strings.Contains(logText, "[REDACTED]") {
		t.Fatalf("log = %q, want redaction marker", logText)
	}
}

func TestDaytonaRuntimeRedactsProviderAPIKeyFromRunnerError(t *testing.T) {
	setDaytonaControlPlaneEnv(t)
	t.Setenv("DAYTONA_KEY", "dtn-redact-me")
	roleConfig := daytonaTestRoleConfig()
	roleConfig.RuntimeDaytona["api_key_env"] = "DAYTONA_KEY"
	s := newDaytonaTestSupervisor(t, erroringDaytonaRunner{
		err: fmt.Errorf("Daytona SDK rejected api key dtn-redact-me"),
	}, roleConfig)
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker-redact-provider", Role: "task"},
		RoleConfig:   roleConfig,
		WorktreePath: t.TempDir(),
	}
	_, err := s.runDaytonaAgent(ap)
	if err == nil {
		t.Fatal("runDaytonaAgent error = nil, want runner error")
	}
	if strings.Contains(err.Error(), "dtn-redact-me") {
		t.Fatalf("runner error leaked Daytona API key: %v", err)
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("runner error = %v, want redaction marker", err)
	}
}

func TestDaytonaRuntimeMergesNamedRuntimeProfile(t *testing.T) {
	setDaytonaControlPlaneEnv(t)
	t.Setenv("OPENAI_API_KEY", "sk-profile")
	st := memstore.New()
	if _, err := st.RuntimeProfiles().Upsert(t.Context(), store.RuntimeProfileUpsert{
		WorkspaceKey: "WS",
		Name:         "profile-daytona",
		Version:      "v1",
		Provider:     domain.RuntimeProviderDaytona,
		Env:          []string{"OPENAI_API_KEY"},
		Manifest: []byte(`{
			"cwd": "/workspace/profile",
			"daytona": {
				"repo_url": "https://github.com/acme/profile.git",
				"setup_commands": ["npm ci"]
			}
		}`),
		Status: domain.DefinitionStatusActive,
	}); err != nil {
		t.Fatalf("upsert runtime profile: %v", err)
	}
	roleConfig := cfgpkg.RoleConfig{
		Backend:            "codex",
		RuntimeProvider:    domain.RuntimeProviderDaytona,
		RuntimeProfileName: "profile-daytona",
	}
	runner := &fakeDaytonaRunner{}
	s := newDaytonaTestSupervisor(t, runner, roleConfig)
	s.ControlStore = st
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker-profile", Role: "task"},
		RoleConfig:   roleConfig,
		WorktreePath: t.TempDir(),
	}
	if _, err := s.runDaytonaAgent(ap); err != nil {
		t.Fatalf("runDaytonaAgent: %v", err)
	}
	requests, _ := runner.snapshot()
	if len(requests) != 1 {
		t.Fatalf("runner calls = %d, want 1", len(requests))
	}
	req := requests[0]
	if req.CWD != "/workspace/profile" || req.Args[2] != "/workspace/profile" {
		t.Fatalf("request cwd/args = %q/%v, want profile cwd", req.CWD, req.Args)
	}
	if len(req.Setup) < 2 ||
		!strings.Contains(req.Setup[0].Command, "https://github.com/acme/profile.git") ||
		req.Setup[1].Command != "npm ci" {
		t.Fatalf("request setup = %#v, want profile clone then setup", req.Setup)
	}
	if req.Env["OPENAI_API_KEY"] != "sk-profile" {
		t.Fatalf("OPENAI_API_KEY = %q, want profile env grant", req.Env["OPENAI_API_KEY"])
	}
}

func TestDaytonaRuntimeUsesBoundFleetDBRepoRemoteURL(t *testing.T) {
	setDaytonaControlPlaneEnv(t)
	st := memstore.New()
	if _, err := st.Repos().Create(t.Context(), store.RepoCreate{
		WorkspaceKey:  "WS",
		Name:          "app",
		RemoteURL:     "https://github.com/acme/app.git",
		DefaultBranch: "trunk",
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	roleConfig := daytonaTestRoleConfig()
	roleConfig.RuntimeProfileName = ""
	delete(roleConfig.RuntimeDaytona, "repo_url")
	runner := &fakeDaytonaRunner{}
	s := newDaytonaTestSupervisor(t, runner, roleConfig)
	s.ControlStore = st
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker-repo", Role: "task", Repo: "app"},
		RoleConfig:   roleConfig,
		WorktreePath: t.TempDir(),
	}
	if _, err := s.runDaytonaAgent(ap); err != nil {
		t.Fatalf("runDaytonaAgent: %v", err)
	}
	requests, _ := runner.snapshot()
	if len(requests) != 1 {
		t.Fatalf("runner calls = %d, want 1", len(requests))
	}
	clone := requests[0].Setup[0].Command
	if !strings.Contains(clone, "https://github.com/acme/app.git") || !strings.Contains(clone, "trunk") {
		t.Fatalf("clone command = %q, want FleetDB remote URL and default branch", clone)
	}
}

func TestDaytonaRuntimeUsesSingleRuntimeReposEntryForFleetDBRepo(t *testing.T) {
	setDaytonaControlPlaneEnv(t)
	st := memstore.New()
	if _, err := st.Repos().Create(t.Context(), store.RepoCreate{
		WorkspaceKey:  "WS",
		Name:          "frontend",
		RemoteURL:     "https://github.com/acme/frontend.git",
		DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	roleConfig := daytonaTestRoleConfig()
	roleConfig.RuntimeProfileName = ""
	delete(roleConfig.RuntimeDaytona, "repo_url")
	roleConfig.RuntimeDaytona["repos"] = []any{"frontend"}
	runner := &fakeDaytonaRunner{}
	s := newDaytonaTestSupervisor(t, runner, roleConfig)
	s.ControlStore = st
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker-repos", Role: "task"},
		RoleConfig:   roleConfig,
		WorktreePath: t.TempDir(),
	}
	if _, err := s.runDaytonaAgent(ap); err != nil {
		t.Fatalf("runDaytonaAgent: %v", err)
	}
	requests, _ := runner.snapshot()
	if len(requests) != 1 {
		t.Fatalf("runner calls = %d, want 1", len(requests))
	}
	clone := requests[0].Setup[0].Command
	if !strings.Contains(clone, "https://github.com/acme/frontend.git") || !strings.Contains(clone, "main") {
		t.Fatalf("clone command = %q, want runtime repos FleetDB remote URL and default branch", clone)
	}
}

func TestDaytonaRuntimeMaterializeChecksOutConfiguredBranchAndRef(t *testing.T) {
	setDaytonaControlPlaneEnv(t)
	roleConfig := daytonaTestRoleConfig()
	roleConfig.RuntimeDaytona["branch"] = "feature/slack-clone"
	roleConfig.RuntimeDaytona["ref"] = "refs/pull/42/head"
	runner := &fakeDaytonaRunner{}
	s := newDaytonaTestSupervisor(t, runner, roleConfig)
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker-ref", Role: "task"},
		RoleConfig:   roleConfig,
		WorktreePath: t.TempDir(),
	}
	if _, err := s.runDaytonaAgent(ap); err != nil {
		t.Fatalf("runDaytonaAgent: %v", err)
	}
	requests, _ := runner.snapshot()
	if len(requests) != 1 {
		t.Fatalf("runner calls = %d, want 1", len(requests))
	}
	materialize := requests[0].Setup[0].Command
	if !strings.Contains(materialize, "'git' 'clone' '--branch' 'feature/slack-clone' '--single-branch'") ||
		!strings.Contains(materialize, "'https://github.com/acme/project.git' '/workspace/project'") {
		t.Fatalf("materialize command = %q, want configured branch clone", materialize)
	}
	if !strings.Contains(materialize, "'git' '-C' '/workspace/project' 'fetch' '--depth=1' 'origin' 'refs/pull/42/head'") ||
		!strings.Contains(materialize, "'git' '-C' '/workspace/project' 'checkout' '--detach' 'FETCH_HEAD'") {
		t.Fatalf("materialize command = %q, want configured ref checkout", materialize)
	}
}

func TestDaytonaRuntimeMetadataRecordedOnAgentSession(t *testing.T) {
	st := memstore.New()
	s := newControlPlaneTestSupervisor(st)
	s.ProjectDir = t.TempDir()
	ap := &AgentProcess{
		Entry: cfgpkg.AgentEntry{Worktree: "worker-1", Role: "task", Repo: "repo-a"},
		RoleConfig: cfgpkg.RoleConfig{
			Backend:            "codex",
			RuntimeProvider:    domain.RuntimeProviderDaytona,
			RuntimeProfileName: "daytona-agent",
			RuntimeCWD:         "/workspace/project",
		},
		WorktreePath:   t.TempDir(),
		AssignedTaskID: "task-1",
	}

	s.createAgentSession(ap, "epic-1")
	ap.Mu.Lock()
	ap.DaytonaSandboxID = "fake-sandbox-01"
	ap.DaytonaRuntimePhase = daytonaRuntimePhaseRunning
	ap.DaytonaCleanupState = daytonaCleanupRetained
	ap.Mu.Unlock()
	s.markControlPlaneAgentSessionRunning(ap)

	sessionID := ap.AgentSessionID
	s.completeControlPlaneAgentSession(ap, agentSessionCompletionInput{
		sessionID:  sessionID,
		leaseID:    ap.AgentLeaseID,
		leaseToken: ap.AgentLeaseToken,
		exitCode:   0,
		taskID:     "task-1",
		diffResult: sessionfinalizeNoDiff(),
	})
	session, err := st.AgentSessions().Get(t.Context(), "WS", sessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.Metadata["runtime_provider"] != "daytona" ||
		session.Metadata["runtime_profile_name"] != "daytona-agent" ||
		session.Metadata["runtime_cwd"] != "/workspace/project" ||
		session.Metadata["daytona_sandbox_id"] != "fake-sandbox-01" ||
		session.Metadata["runtime_phase"] != daytonaRuntimePhaseRunning ||
		session.Metadata["runtime_cleanup_state"] != daytonaCleanupRetained {
		t.Fatalf("session metadata = %#v, want Daytona runtime metadata", session.Metadata)
	}
}

func sessionfinalizeNoDiff() sessionfinalize.WithWorktreeResult {
	return sessionfinalize.WithWorktreeResult{DiffStats: sessions.DiffStats{}}
}
