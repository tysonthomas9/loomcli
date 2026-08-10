//nolint:revive // Tests use the established driver package name to exercise unexported helpers.
package driver

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/localnodeconfig"
	artifactsmodule "github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// Malformed runner outputs must fail closed as invalid_task_result (§4.2) and
// NEVER persist artifacts, patches, log refs, or transcripts. Each helper mode
// emits a patch/artifacts/logs payload alongside the invalid status so the test
// can prove none of it reached the store or the worktree.
func TestHostBridgeTaskExecutorFailsClosedOnInvalidResult(t *testing.T) {
	cases := []struct {
		name string
		mode string
	}{
		{name: "empty object", mode: "invalid-empty"},
		{name: "null", mode: "invalid-null"},
		{name: "missing status", mode: "invalid-missing-status"},
		{name: "unknown status", mode: "invalid-unknown-status"},
		{name: "non-terminal status", mode: "invalid-nonterminal"},
		{name: "completed with non-zero exit", mode: "invalid-completed-nonzero"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st := memstore.New()
			repo := newPatchBackRepo(t)
			base := repo.commitFile("file.txt", "old\n", "base")

			executor := HostBridgeTaskExecutor{
				Store:        st,
				WorktreePath: repo.dir,
				APIBaseURL:   testTaskRunAPIURL,
				Command:      hostBridgeHelperCommand(t, tc.mode, base, "unused-patch"),
			}
			result, err := executor.ExecuteTask(ctx, hostBridgeTaskExecRequest())
			if err != nil {
				t.Fatalf("ExecuteTask: %v", err)
			}
			if result.Status != domain.TaskRunFailed || result.ExitCode != 1 {
				t.Fatalf("result status/exit = %q/%d, want failed/1", result.Status, result.ExitCode)
			}
			if result.ErrorClass != "invalid_task_result" {
				t.Fatalf("error class = %q, want invalid_task_result", result.ErrorClass)
			}
			if len(result.ArtifactIDs) != 0 {
				t.Fatalf("artifact ids = %+v, want none persisted", result.ArtifactIDs)
			}
			if result.LogsRef != "" || result.ArtifactsRef != "" {
				t.Fatalf("result refs = logs:%q artifacts:%q, want none persisted", result.LogsRef, result.ArtifactsRef)
			}
			// The worktree must be untouched: an invalid result never applies the
			// patch it tried to smuggle in.
			if repo.read("file.txt") != "old\n" {
				t.Fatalf("file content = %q, want unchanged (no patch applied)", repo.read("file.txt"))
			}
			// No artifacts of any kind reached the store.
			artifacts, err := st.ArtifactQueries().ListArtifactRecords(ctx, "WS", artifactsmodule.SearchFilter{})
			if err != nil {
				t.Fatalf("list artifacts: %v", err)
			}
			if len(artifacts) != 0 {
				t.Fatalf("store artifacts = %+v, want none persisted", artifacts)
			}
		})
	}
}

// The Go-side validator mirrors the launcher JS: empty/non-terminal status and
// completed+nonzero exit are invalid; terminal results are valid.
func TestValidateBridgeTaskRunnerResult(t *testing.T) {
	exit := func(v int) *int { return &v }
	cases := []struct {
		name   string
		result bridgeTaskRunnerResult
		wantOK bool
	}{
		{name: "empty", result: bridgeTaskRunnerResult{}, wantOK: false},
		{name: "unknown status", result: bridgeTaskRunnerResult{Status: "weird"}, wantOK: false},
		{name: "non-terminal", result: bridgeTaskRunnerResult{Status: domain.TaskRunRunning}, wantOK: false},
		{name: "completed nonzero", result: bridgeTaskRunnerResult{Status: domain.TaskRunCompleted, ExitCode: exit(2)}, wantOK: false},
		{name: "completed zero", result: bridgeTaskRunnerResult{Status: domain.TaskRunCompleted, ExitCode: exit(0)}, wantOK: true},
		{name: "completed unset exit", result: bridgeTaskRunnerResult{Status: domain.TaskRunCompleted}, wantOK: true},
		{name: "failed", result: bridgeTaskRunnerResult{Status: domain.TaskRunFailed}, wantOK: true},
		{name: "cancelled", result: bridgeTaskRunnerResult{Status: domain.TaskRunCancelled}, wantOK: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := validateBridgeTaskRunnerResult(tc.result); ok != tc.wantOK {
				t.Fatalf("validateBridgeTaskRunnerResult ok = %v, want %v", ok, tc.wantOK)
			}
		})
	}
}

// requireTerminalStatus rejects completed+nonzero as invalid_task_result (§4.2).
func TestRequireTerminalStatusRejectsCompletedNonZeroExit(t *testing.T) {
	completion := normalizeTaskExecCompletion(TaskExecResult{Status: domain.TaskRunCompleted, ExitCode: 5}, nil)
	if completion.Status != domain.TaskRunFailed || completion.ExitCode != 5 || completion.ErrorClass != "invalid_task_result" {
		t.Fatalf("completion = %+v, want failed/exit5/invalid_task_result", completion)
	}
	ok := normalizeTaskExecCompletion(TaskExecResult{Status: domain.TaskRunCompleted, ExitCode: 0}, nil)
	if ok.Status != domain.TaskRunCompleted || ok.ErrorClass != "" {
		t.Fatalf("completion = %+v, want completed with no error class", ok)
	}
}

// The noop provider gate (§4.5) fails closed by default and only enables with
// LOOM_DRIVER_ENABLE_TEST_NOOP_PROVIDER=1, in BOTH preflight and execute.
func TestNoopProviderGate(t *testing.T) {
	ctx := context.Background()
	req := TaskExecRequest{WorkspaceKey: "WS", TaskRunID: "task-run-1", ProviderProfile: "local-noop"}
	opts := TaskRunRequestOptions{WorkspaceKey: "WS", DriverRunID: "run-1", TaskID: "T-1", ProviderProfile: "local-noop"}

	t.Run("disabled fails closed", func(t *testing.T) {
		t.Setenv(NoopTaskProviderEnvVar, "")
		result, err := LocalTaskExecutor{}.ExecuteTask(ctx, req)
		if err != nil {
			t.Fatalf("ExecuteTask: %v", err)
		}
		if result.Status != domain.TaskRunFailed || result.ErrorClass != "provider_unsupported" {
			t.Fatalf("disabled noop execute = %+v, want failed/provider_unsupported", result)
		}
		if _, perr := (LocalTaskExecutor{}).PreflightTaskProvider(ctx, opts); !errors.Is(perr, domain.ErrInvalid) {
			t.Fatalf("disabled noop preflight err = %v, want ErrInvalid", perr)
		}
	})

	t.Run("enabled completes", func(t *testing.T) {
		t.Setenv(NoopTaskProviderEnvVar, "1")
		result, err := LocalTaskExecutor{}.ExecuteTask(ctx, req)
		if err != nil {
			t.Fatalf("ExecuteTask: %v", err)
		}
		if result.Status != domain.TaskRunCompleted || result.ExitCode != 0 {
			t.Fatalf("enabled noop execute = %+v, want completed/0", result)
		}
		if _, perr := (LocalTaskExecutor{}).PreflightTaskProvider(ctx, opts); perr != nil {
			t.Fatalf("enabled noop preflight err = %v, want nil", perr)
		}
	})
}

// The trusted-local env widening (§4.3) is gated strictly by the local task
// runner entrypoint: the local runner inherits provider creds, while
// Daytona/remote runners keep the strict filter.
func TestLocalTaskRunnerBaseEnvWideningGatedByEntrypoint(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"HOME=/home/loom",
		"ANTHROPIC_API_KEY=anthropic-secret",
		"OPENAI_API_KEY=openai-secret",
		"CODEX_API_KEY=codex-secret",
		"CODEX_HOME=/home/loom/.codex",
		"GEMINI_API_KEY=gemini-secret",
		"GOOGLE_API_KEY=google-secret",
		"GOOGLE_APPLICATION_CREDENTIALS=/secrets/google.json",
		"CURSOR_API_KEY=cursor-secret",
		"GITHUB_TOKEN=github-secret",
		"GH_TOKEN=gh-secret",
		"LOOM_FLEET_DB_API_KEY=fleet-secret",
	}

	local := TaskExecRequest{RunnerEntrypoint: LocalTaskRunnerEntrypoint}
	localEnv := envMap(taskRunnerBaseEnvForRequest(local, env))
	for _, key := range []string{
		"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "CODEX_API_KEY", "CODEX_HOME",
		"GEMINI_API_KEY", "GOOGLE_API_KEY", "GOOGLE_APPLICATION_CREDENTIALS", "CURSOR_API_KEY",
	} {
		if _, ok := localEnv[key]; !ok {
			t.Fatalf("local runner env missing trusted credential %s: %+v", key, localEnv)
		}
	}
	if _, ok := localEnv["PATH"]; !ok {
		t.Fatalf("local runner env missing PATH: %+v", localEnv)
	}
	// The local runner never inherits forge or control-plane credentials.
	for _, key := range []string{"GITHUB_TOKEN", "GH_TOKEN", "LOOM_FLEET_DB_API_KEY"} {
		if _, ok := localEnv[key]; ok {
			t.Fatalf("local runner env leaked non-provider secret %s: %+v", key, localEnv)
		}
	}

	daytona := TaskExecRequest{RunnerEntrypoint: "daytona-task-runner"}
	daytonaEnv := envMap(taskRunnerBaseEnvForRequest(daytona, env))
	for _, key := range []string{
		"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "CODEX_API_KEY", "CODEX_HOME",
		"GEMINI_API_KEY", "GOOGLE_API_KEY", "GOOGLE_APPLICATION_CREDENTIALS", "CURSOR_API_KEY",
		// GitHub tokens MUST still be denied to Daytona/remote runners.
		"GITHUB_TOKEN", "GH_TOKEN", "LOOM_FLEET_DB_API_KEY",
	} {
		if _, ok := daytonaEnv[key]; ok {
			t.Fatalf("daytona runner env leaked credential %s (strict filter must hold): %+v", key, daytonaEnv)
		}
	}
}

// The local task runner gets the resolved backend env (§4.3/§4.5); other
// runners never do.
func TestLocalTaskRunnerBackendEnvResolution(t *testing.T) {
	ctx := context.Background()

	t.Run("defaults to codex without a profile", func(t *testing.T) {
		st := memstore.New()
		if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "ws"}); err != nil {
			t.Fatalf("create workspace: %v", err)
		}
		req := hostBridgeTaskExecRequest()
		req.RunnerEntrypoint = LocalTaskRunnerEntrypoint
		env := envMap(HostBridgeTaskExecutor{Store: st}.taskRunnerEnv(req, "{}"))
		if env[TaskRunnerBackendEnv] != "codex" {
			t.Fatalf("%s = %q, want codex default", TaskRunnerBackendEnv, env[TaskRunnerBackendEnv])
		}
	})

	t.Run("uses workspace local runtime provider", func(t *testing.T) {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		st := memstore.New()
		if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "ws"}); err != nil {
			t.Fatalf("create workspace: %v", err)
		}
		if err := localnodeconfig.SetRuntimeProvider("WS", "claude"); err != nil {
			t.Fatalf("set runtime provider: %v", err)
		}
		req := hostBridgeTaskExecRequest()
		req.RunnerEntrypoint = LocalTaskRunnerEntrypoint
		env := envMap(HostBridgeTaskExecutor{Store: st}.taskRunnerEnv(req, "{}"))
		if env[TaskRunnerBackendEnv] != "claude" {
			t.Fatalf("%s = %q, want claude from local node config", TaskRunnerBackendEnv, env[TaskRunnerBackendEnv])
		}
	})

	t.Run("retired supervised-agent override is ignored", func(t *testing.T) {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		st := memstore.New()
		if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "ws"}); err != nil {
			t.Fatalf("create workspace: %v", err)
		}
		if err := localnodeconfig.SetRuntimeProvider("WS", "claude"); err != nil {
			t.Fatalf("set runtime provider: %v", err)
		}
		req := hostBridgeTaskExecRequest()
		req.RunnerEntrypoint = LocalTaskRunnerEntrypoint
		req.WorkerProfileID = "worker-a"
		env := envMap(HostBridgeTaskExecutor{Store: st}.taskRunnerEnv(req, "{}"))
		if env[TaskRunnerBackendEnv] != "claude" {
			t.Fatalf("%s = %q, want claude from local node config", TaskRunnerBackendEnv, env[TaskRunnerBackendEnv])
		}
	})

	t.Run("prompt agent role config follows immutable request policy", func(t *testing.T) {
		st := memstore.New()
		if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "ws"}); err != nil {
			t.Fatalf("create workspace: %v", err)
		}
		budget := 3.25
		if _, err := st.Roles().Create(ctx, store.RoleCreate{
			WorkspaceKey: "WS", Name: "reviewer", Backend: "opencode", Model: "openai/gpt-5.6-terra",
			Effort: "high", ReadOnly: true, AllowedTools: []string{"read", "grep"},
			DeniedTools: []string{"write"}, MaxBudgetUSD: &budget,
		}); err != nil {
			t.Fatalf("create role: %v", err)
		}
		if _, err := st.AgentServices().Create(ctx, store.AgentServiceCreate{
			WorkspaceKey: "WS", ServiceID: "agt-reviewer", Name: "reviewer",
			Kind: domain.AgentServiceKindEvent, DesiredState: domain.AgentServiceDesiredRunning, RoleName: "reviewer",
		}); err != nil {
			t.Fatalf("create agent service: %v", err)
		}
		if _, err := st.Drivers().Create(ctx, store.DriverCreate{
			WorkspaceKey: "WS", DriverID: "prompt-agent", Name: "prompt-agent",
			OwnerType: workflowcatalog.DriverOwnerSystem, Status: workflowcatalog.DriverStatusActive,
		}); err != nil {
			t.Fatalf("create driver: %v", err)
		}
		if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
			WorkspaceKey: "WS", VersionID: "prompt-agent-v1", DriverID: "prompt-agent", Version: 1,
			SourceDigest: "sha256:source", BundleDigest: "sha256:bundle",
			ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
		}); err != nil {
			t.Fatalf("create driver version: %v", err)
		}
		if _, err := st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
			WorkspaceKey: "WS", BindingID: "agt-reviewer-1", Name: "reviewer",
			SourceKind: "internal", DriverID: "prompt-agent", DriverVersionID: "prompt-agent-v1",
			TargetAgentServiceID: "agt-reviewer", Enabled: true,
		}); err != nil {
			t.Fatalf("create trigger binding: %v", err)
		}
		if _, err := st.DriverRuns().Create(ctx, store.DriverRunCreate{
			WorkspaceKey: "WS", RunID: "driver-run-1", DriverID: "prompt-agent",
			DriverVersionID: "prompt-agent-v1", TriggerBindingID: "agt-reviewer-1",
		}); err != nil {
			t.Fatalf("create driver run: %v", err)
		}

		req := hostBridgeTaskExecRequest()
		req.RunnerEntrypoint = LocalTaskRunnerEntrypoint
		req.Input = []byte(`{
			"prompt": "review this card",
			"loomAgentPolicy": {
				"version": 1,
				"agentServiceId": "agt-reviewer",
				"roleName": "reviewer",
				"roleUpdatedAt": "2026-07-22T12:00:00Z",
				"backend": "opencode",
				"model": "openai/gpt-5.6-terra",
				"effort": "high",
				"readOnly": true,
				"allowedTools": ["read", "grep"],
				"deniedTools": ["write"],
				"maxBudgetUsd": 3.25
			}
		}`)
		env := envMap(HostBridgeTaskExecutor{Store: st}.taskRunnerEnv(req, "{}"))
		for key, want := range map[string]string{
			TaskRunnerBackendEnv:  "opencode",
			"LOOM_OPENCODE_MODEL": "openai/gpt-5.6-terra",
			"LOOM_AGENT_EFFORT":   "high",
			"LOOM_CLAUDE_EFFORT":  "high",
			"LOOM_READ_ONLY":      "1",
			"LOOM_ALLOWED_TOOLS":  "read,grep",
			"LOOM_DENIED_TOOLS":   "write",
			"LOOM_MAX_BUDGET_USD": "3.25",
		} {
			if env[key] != want {
				t.Fatalf("%s = %q, want %q; env=%+v", key, env[key], want, env)
			}
		}
		metadata := map[string]string{"backend": "cursor"}
		if _, err := st.AgentServices().Update(ctx, "WS", "agt-reviewer", store.AgentServiceUpdate{Metadata: &metadata}); err != nil {
			t.Fatalf("update per-agent backend: %v", err)
		}
		env = envMap(HostBridgeTaskExecutor{Store: st}.taskRunnerEnv(req, "{}"))
		if env[TaskRunnerBackendEnv] != "opencode" {
			t.Fatalf("%s = %q, want immutable opencode snapshot after live agent edit", TaskRunnerBackendEnv, env[TaskRunnerBackendEnv])
		}
		if env["LOOM_OPENCODE_MODEL"] != "openai/gpt-5.6-terra" {
			t.Fatalf("immutable role model drifted after live agent edit: %+v", env)
		}
	})

	t.Run("non-local runner gets no backend env", func(t *testing.T) {
		st := memstore.New()
		req := hostBridgeTaskExecRequest()
		req.RunnerEntrypoint = "daytona-task-runner"
		env := envMap(HostBridgeTaskExecutor{Store: st}.taskRunnerEnv(req, "{}"))
		if _, ok := env[TaskRunnerBackendEnv]; ok {
			t.Fatalf("daytona runner unexpectedly got %s: %+v", TaskRunnerBackendEnv, env)
		}
	})
}

// validateDriverRunnerSpecs rejects malformed specs (§4.6) and accepts valid
// flue-workflow / node-module runners.
func TestValidateDriverRunnerSpecs(t *testing.T) {
	cases := []struct {
		name    string
		runners []DriverRunnerSpec
		wantErr bool
	}{
		{name: "valid flue + node", runners: []DriverRunnerSpec{
			{Name: "local-task-runner", Kind: RunnerKindFlueWorkflow, Entrypoint: "local-task-runner"},
			{Name: "node-runner", Kind: RunnerKindNodeModule, Entrypoint: "runners/node.mjs"},
		}},
		{name: "empty name", runners: []DriverRunnerSpec{{Name: "", Kind: RunnerKindFlueWorkflow, Entrypoint: "x"}}, wantErr: true},
		{name: "empty kind", runners: []DriverRunnerSpec{{Name: "x", Kind: "", Entrypoint: "x"}}, wantErr: true},
		{name: "empty entrypoint", runners: []DriverRunnerSpec{{Name: "x", Kind: RunnerKindFlueWorkflow, Entrypoint: ""}}, wantErr: true},
		{name: "unknown kind", runners: []DriverRunnerSpec{{Name: "x", Kind: "wasm", Entrypoint: "x"}}, wantErr: true},
		{name: "absolute entrypoint", runners: []DriverRunnerSpec{{Name: "x", Kind: RunnerKindNodeModule, Entrypoint: "/etc/passwd"}}, wantErr: true},
		{name: "traversal entrypoint", runners: []DriverRunnerSpec{{Name: "x", Kind: RunnerKindNodeModule, Entrypoint: "../escape.mjs"}}, wantErr: true},
		{name: "duplicate names", runners: []DriverRunnerSpec{
			{Name: "dup", Kind: RunnerKindFlueWorkflow, Entrypoint: "a"},
			{Name: "dup", Kind: RunnerKindFlueWorkflow, Entrypoint: "b"},
		}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDriverRunnerSpecs(tc.runners)
			if tc.wantErr && err == nil {
				t.Fatalf("validateDriverRunnerSpecs(%+v) = nil, want error", tc.runners)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateDriverRunnerSpecs(%+v) = %v, want nil", tc.runners, err)
			}
			if tc.wantErr && err != nil && !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("validateDriverRunnerSpecs error = %v, want ErrInvalid", err)
			}
		})
	}
}

// The OpenShell guard (§4.6/§4.5) fails resolution closed with the
// openshell_runner_unimplemented error class regardless of the manifest.
func TestResolveDriverRunnerGuardsOpenShell(t *testing.T) {
	version := &workflowcatalog.DriverVersion{
		VersionID: "v-1",
		DriverID:  "epic-runner",
		Manifest: map[string]string{
			"runners": `[{"name":"openshell-task-runner","kind":"flue-workflow","entrypoint":"openshell-task-runner"}]`,
		},
	}
	_, err := resolveDriverRunner(version, OpenShellRunnerName)
	if err == nil {
		t.Fatal("resolveDriverRunner(openshell) = nil, want error")
	}
	if !errors.Is(err, ErrOpenShellRunnerUnimplemented) {
		t.Fatalf("resolveDriverRunner error = %v, want ErrOpenShellRunnerUnimplemented", err)
	}
	if !strings.Contains(err.Error(), "openshell_runner_unimplemented") {
		t.Fatalf("error %q must carry the openshell_runner_unimplemented class", err.Error())
	}

	// applyResolvedRunner surfaces the same guard.
	_, err = applyResolvedRunner(TaskRunRequestOptions{Runner: OpenShellRunnerName}, nil, version)
	if !errors.Is(err, ErrOpenShellRunnerUnimplemented) {
		t.Fatalf("applyResolvedRunner openshell err = %v, want ErrOpenShellRunnerUnimplemented", err)
	}
}

// End-to-end: the built-in launcher driving a Flue server that returns an
// empty result must yield invalid_task_result (no fake completion) and persist
// nothing.
func TestRunBuiltInFlueWorkflowRejectsEmptyResult(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not available: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st := memstore.New()
	worktree := t.TempDir()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "ws"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS",
		DriverID:     "epic-runner",
		Name:         "epic-runner",
		OwnerType:    workflowcatalog.DriverOwnerUser,
		Status:       workflowcatalog.DriverStatusActive,
		TrustLevel:   workflowcatalog.DriverTrustTrusted,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	bundleRoot := filepath.Join(worktree, ".loom", "drivers", "epic-runner", "driver-version-1")
	if err := os.MkdirAll(filepath.Join(bundleRoot, "dist"), 0o755); err != nil {
		t.Fatalf("mkdir bundle: %v", err)
	}
	// Flue server that returns an EMPTY result frame.
	server := `
function send(message) {
  if (typeof process.send === "function") process.send({ version: 1, ...message });
}
process.on("message", (message) => {
  if (!message || message.type !== "invoke") return;
  send({ type: "result", requestId: message.requestId, result: {} });
});
send({ type: "ready" });
setInterval(() => {}, 1000);
`
	if err := os.WriteFile(filepath.Join(bundleRoot, "dist", "server.mjs"), []byte(server), 0o644); err != nil {
		t.Fatalf("write server: %v", err)
	}
	manifest := map[string]string{"server_ref": "dist/server.mjs", "workflow_name": "epic-runner"}
	manifestBytes, err := writeFlueBundleManifest(bundleRoot, manifest)
	if err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	bundleDigest, err := digestBundleTree(bundleRoot, manifestBytes)
	if err != nil {
		t.Fatalf("digest bundle: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey:     "WS",
		VersionID:        "driver-version-1",
		DriverID:         "epic-runner",
		Version:          1,
		SourceRef:        "test://driver",
		SourceDigest:     "sha256:source",
		BundleRef:        filepath.ToSlash(filepath.Join(".loom", "drivers", "epic-runner", "driver-version-1")),
		BundleDigest:     bundleDigest,
		Runtime:          RuntimeFlueNode,
		Manifest:         manifest,
		ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
		CreatedBy:        "tester",
	}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}

	req := hostBridgeTaskExecRequest()
	req.ProviderProfile = ""
	req.Runner = "local-task-runner"
	req.RunnerKind = RunnerKindFlueWorkflow
	req.RunnerEntrypoint = "local-task-runner"
	req.RunnerVersionID = "driver-version-1"
	req.RunnerTrustLevel = workflowcatalog.DriverTrustTrusted
	result, err := (HostBridgeTaskExecutor{Store: st, WorktreePath: worktree, APIBaseURL: testTaskRunAPIURL}).ExecuteTask(ctx, req)
	if err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}
	if result.Status != domain.TaskRunFailed || result.ExitCode != 1 || result.ErrorClass != "invalid_task_result" {
		t.Fatalf("empty result = %+v, want failed/1/invalid_task_result", result)
	}
	if len(result.ArtifactIDs) != 0 || result.LogsRef != "" {
		t.Fatalf("empty result persisted refs = %+v / %q, want none", result.ArtifactIDs, result.LogsRef)
	}
}

// End-to-end: the built-in launcher driving a Flue server that returns a
// completed status with a STRINGIZED nonzero exit code ("1") must NOT be
// laundered to completed/exit-0. The launcher JS coerces the exit and rejects
// completed+nonzero as invalid_task_result (§4.1), persisting nothing.
func TestRunBuiltInFlueWorkflowRejectsCompletedStringizedNonzeroExit(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not available: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st := memstore.New()
	worktree := t.TempDir()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "ws"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS",
		DriverID:     "epic-runner",
		Name:         "epic-runner",
		OwnerType:    workflowcatalog.DriverOwnerUser,
		Status:       workflowcatalog.DriverStatusActive,
		TrustLevel:   workflowcatalog.DriverTrustTrusted,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	bundleRoot := filepath.Join(worktree, ".loom", "drivers", "epic-runner", "driver-version-1")
	if err := os.MkdirAll(filepath.Join(bundleRoot, "dist"), 0o755); err != nil {
		t.Fatalf("mkdir bundle: %v", err)
	}
	// Flue server that returns a completed status with a stringized nonzero
	// exit code — the classic fake-success laundering vector.
	server := `
function send(message) {
  if (typeof process.send === "function") process.send({ version: 1, ...message });
}
process.on("message", (message) => {
  if (!message || message.type !== "invoke") return;
  send({ type: "result", requestId: message.requestId, result: { status: "completed", exit_code: "1" } });
});
send({ type: "ready" });
setInterval(() => {}, 1000);
`
	if err := os.WriteFile(filepath.Join(bundleRoot, "dist", "server.mjs"), []byte(server), 0o644); err != nil {
		t.Fatalf("write server: %v", err)
	}
	manifest := map[string]string{"server_ref": "dist/server.mjs", "workflow_name": "epic-runner"}
	manifestBytes, err := writeFlueBundleManifest(bundleRoot, manifest)
	if err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	bundleDigest, err := digestBundleTree(bundleRoot, manifestBytes)
	if err != nil {
		t.Fatalf("digest bundle: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey:     "WS",
		VersionID:        "driver-version-1",
		DriverID:         "epic-runner",
		Version:          1,
		SourceRef:        "test://driver",
		SourceDigest:     "sha256:source",
		BundleRef:        filepath.ToSlash(filepath.Join(".loom", "drivers", "epic-runner", "driver-version-1")),
		BundleDigest:     bundleDigest,
		Runtime:          RuntimeFlueNode,
		Manifest:         manifest,
		ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
		CreatedBy:        "tester",
	}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}

	req := hostBridgeTaskExecRequest()
	req.ProviderProfile = ""
	req.Runner = "local-task-runner"
	req.RunnerKind = RunnerKindFlueWorkflow
	req.RunnerEntrypoint = "local-task-runner"
	req.RunnerVersionID = "driver-version-1"
	req.RunnerTrustLevel = workflowcatalog.DriverTrustTrusted
	result, err := (HostBridgeTaskExecutor{Store: st, WorktreePath: worktree, APIBaseURL: testTaskRunAPIURL}).ExecuteTask(ctx, req)
	if err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}
	if result.Status == domain.TaskRunCompleted {
		t.Fatalf("stringized nonzero exit laundered to completed: %+v", result)
	}
	if result.Status != domain.TaskRunFailed || result.ErrorClass != "invalid_task_result" {
		t.Fatalf("stringized nonzero exit = %+v, want failed/invalid_task_result", result)
	}
	if len(result.ArtifactIDs) != 0 || result.LogsRef != "" {
		t.Fatalf("invalid result persisted refs = %+v / %q, want none", result.ArtifactIDs, result.LogsRef)
	}
}

// The embedded launcher JS must apply the strict §4.1 algorithm: it never
// defaults a status to completed and rejects empty results with
// invalid_task_result.
func TestFlueTaskRunnerLauncherStrictValidationContract(t *testing.T) {
	for _, banned := range []string{
		"out.status = out.status || 'completed'",
		"out.status || 'completed'",
	} {
		if strings.Contains(flueTaskRunnerLauncher, banned) {
			t.Fatalf("launcher still defaults status to completed: %q", banned)
		}
	}
	for _, want := range []string{
		"validateBridgeResult",
		"invalid_task_result",
		"TERMINAL_STATUSES",
	} {
		if !strings.Contains(flueTaskRunnerLauncher, want) {
			t.Fatalf("launcher missing strict-validation token %q", want)
		}
	}
}

// Native registration never fabricates runners (§4.6): without supplied specs
// the manifest declares no runners.
func TestNativeFlueManifestDoesNotFabricateRunners(t *testing.T) {
	manifest := nativeFlueManifest("epic-runner", "epic-runner", "epic-runner", "ref", "sha256:src", "sha256:art", nil, nil)
	if strings.TrimSpace(manifest["runners"]) != "" {
		t.Fatalf("manifest runners = %q, want empty (no fabrication)", manifest["runners"])
	}

	withSpecs := nativeFlueManifest("epic-runner", "epic-runner", "epic-runner", "ref", "sha256:src", "sha256:art", nil, []DriverRunnerSpec{
		{Name: "local-task-runner", Kind: RunnerKindFlueWorkflow, Entrypoint: "local-task-runner"},
	})
	if !strings.Contains(withSpecs["runners"], "local-task-runner") {
		t.Fatalf("manifest runners = %q, want supplied local-task-runner", withSpecs["runners"])
	}
	if strings.Contains(withSpecs["runners"], "openshell-task-runner") {
		t.Fatalf("manifest runners = %q, must not fabricate openshell", withSpecs["runners"])
	}
}
