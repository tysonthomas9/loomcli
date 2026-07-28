//nolint:revive // Tests use the established driver package name to exercise unexported helpers.
package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	runtimesettings "github.com/tysonthomas9/loomcli/internal/localsettings"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// TestTaskRunnerEnvAPIBaseURL pins the serve-transport seam: when the
// executor carries an API base URL, runners get LOOM_TASK_RUN_API_URL (and
// the SDK drops its fleet-db requirement); without one, the env is exactly
// the legacy set so unflipped deployments are byte-identical.
func TestTaskRunnerEnvAPIBaseURL(t *testing.T) {
	req := hostBridgeTaskExecRequest()
	legacy := HostBridgeTaskExecutor{WorktreePath: "/wt"}.taskRunnerEnv(req, "{}")
	for _, entry := range legacy {
		if strings.HasPrefix(entry, "LOOM_TASK_RUN_API_URL=") {
			t.Fatalf("legacy env unexpectedly exports the serve API URL: %q", entry)
		}
	}
	withURL := HostBridgeTaskExecutor{WorktreePath: "/wt", APIBaseURL: " http://127.0.0.1:8080 "}.taskRunnerEnv(req, "{}")
	if len(withURL) != len(legacy)+1 || withURL[len(withURL)-1] != "LOOM_TASK_RUN_API_URL=http://127.0.0.1:8080" {
		t.Fatalf("env with APIBaseURL = %v, want legacy env plus trimmed LOOM_TASK_RUN_API_URL appended", withURL)
	}
	if !slices.Equal(withURL[:len(legacy)], legacy) {
		t.Fatalf("APIBaseURL changed the legacy env prefix:\n%v\n%v", withURL[:len(legacy)], legacy)
	}
}

func TestHostBridgeTaskExecutorRequiresTaskRunAPIURL(t *testing.T) {
	t.Run("preflight", func(t *testing.T) {
		_, err := (HostBridgeTaskExecutor{Command: []string{"unused"}}).PreflightTaskProvider(
			context.Background(),
			TaskRunRequestOptions{ProviderProfile: "codex-default"},
		)
		if !errors.Is(err, domain.ErrInvalid) || !strings.Contains(err.Error(), "task runner requires the loom serve task-run API URL") {
			t.Fatalf("PreflightTaskProvider error = %v, want missing task-run API URL", err)
		}
	})

	t.Run("execute", func(t *testing.T) {
		_, err := (HostBridgeTaskExecutor{Command: []string{"unused"}}).ExecuteTask(context.Background(), hostBridgeTaskExecRequest())
		if !errors.Is(err, domain.ErrInvalid) || !strings.Contains(err.Error(), "task runner requires the loom serve task-run API URL") {
			t.Fatalf("ExecuteTask error = %v, want missing task-run API URL", err)
		}
	})
}

func TestLocalTaskRunnerSettingsDoNotOverrideInheritedGitHubToken(t *testing.T) {
	settingsDir := t.TempDir()
	credential, err := runtimesettings.SealRuntimeCredential(settingsDir, runtimesettings.RuntimeCredentialProviderGitHub, "settings-token", time.Now())
	if err != nil {
		t.Fatalf("seal github credential: %v", err)
	}
	settings := runtimesettings.Default()
	settings.LocalTaskRunner.OpenCodeModel = "opencode/model"
	settings.RuntimeCredentials.GitHub = credential
	if err := runtimesettings.Save(settingsDir, settings); err != nil {
		t.Fatalf("save local settings: %v", err)
	}

	req := hostBridgeTaskExecRequest()
	req.RunnerEntrypoint = LocalTaskRunnerEntrypoint
	executor := HostBridgeTaskExecutor{WorktreePath: "/wt", LocalSettingsDir: settingsDir}

	env := executor.taskRunnerEnv(req, "{}", []string{"PATH=/bin", "GITHUB_TOKEN=host-token"})
	if envContains(env, "GITHUB_TOKEN=settings-token") {
		t.Fatalf("settings GitHub token overrode inherited GITHUB_TOKEN: %v", env)
	}
	if !envContains(env, "LOOM_OPENCODE_MODEL=opencode/model") {
		t.Fatalf("non-secret local task runner setting was not exported: %v", env)
	}

	env = executor.taskRunnerEnv(req, "{}", []string{"PATH=/bin", "GH_TOKEN=host-token"})
	if envContains(env, "GITHUB_TOKEN=settings-token") {
		t.Fatalf("settings GitHub token overrode inherited GH_TOKEN: %v", env)
	}

	env = executor.taskRunnerEnv(req, "{}", []string{"PATH=/bin"})
	if !envContains(env, "GITHUB_TOKEN=settings-token") {
		t.Fatalf("settings GitHub token was not exported when inherited env had no GitHub token: %v", env)
	}

	req.Input = json.RawMessage(`{
		"loomAgentPolicy": {
			"version": 1,
			"agentServiceId": "agent-1",
			"roleName": "reviewer",
			"backend": "opencode",
			"model": "role/model"
		}
	}`)
	env = executor.taskRunnerEnv(req, "{}", []string{"PATH=/bin"})
	if !envContains(env, "LOOM_OPENCODE_MODEL=role/model") {
		t.Fatalf("immutable role model was not exported: %v", env)
	}
	if envContains(env, "LOOM_OPENCODE_MODEL=opencode/model") {
		t.Fatalf("local settings overrode immutable role model: %v", env)
	}
}

func TestLocalTaskRunnerManagedPolicyParsingFailsClosed(t *testing.T) {
	valid := json.RawMessage(`{
		"loomAgentPolicy": {
			"version": 1,
			"agentServiceId": "agent-1",
			"roleName": "reviewer",
			"backend": "codex"
		}
	}`)
	policy, present, err := localTaskRunnerAgentPolicyFromInput(valid)
	if err != nil || !present {
		t.Fatalf("valid managed policy parse = (%+v, %v, %v), want present without error", policy, present, err)
	}
	if policy.AgentServiceID != "agent-1" || policy.RoleName != "reviewer" || policy.Backend != "codex" {
		t.Fatalf("valid managed policy = %+v, want stamped values", policy)
	}

	_, present, err = localTaskRunnerAgentPolicyFromInput(json.RawMessage(`{
		"loomAgentPolicy": {"version": 1, "backend": "codex"}
	}`))
	if !present || err == nil || !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("incomplete managed policy parse = (present=%v, err=%v), want invalid error", present, err)
	}

	_, present, err = localTaskRunnerAgentPolicyFromInput(json.RawMessage(`{"prompt":"legacy"}`))
	if err != nil || present {
		t.Fatalf("legacy input parse = (present=%v, err=%v), want absent without error", present, err)
	}
}

func TestHostBridgeTaskExecutorAppliesPatchUploadsAndFinalizesArtifact(t *testing.T) {
	ctx := context.Background()
	t.Setenv("LOOM_FLEET_DB_URL", "https://fleet.invalid")
	t.Setenv("LOOM_FLEET_DB_API_KEY", "broad-secret")
	t.Setenv("LOOM_TASK_RUN_LEASE_TOKEN", "task-run-token")
	t.Setenv("OPENAI_API_KEY", "model-secret")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "aws-secret")
	t.Setenv("GITHUB_TOKEN", "github-secret")
	st := memstore.New()
	repo := newPatchBackRepo(t)
	base := repo.commitFile("file.txt", "old\n", "base")
	patch := "diff --git a/file.txt b/file.txt\n--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+new\n"

	executor := HostBridgeTaskExecutor{
		Store:               st,
		Artifacts:           testArtifactsAPI(st),
		ArtifactAuthorities: taskWorkerTestAuthorities{},
		WorktreePath:        repo.dir,
		APIBaseURL:          testTaskRunAPIURL,
		Command:             hostBridgeHelperCommand(t, "success", base, patch),
	}
	result, err := executor.ExecuteTask(ctx, hostBridgeTaskExecRequest())
	if err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}
	if result.Status != domain.TaskRunCompleted || result.ExitCode != 0 {
		t.Fatalf("result status/exit = %q/%d, want completed and zero exit", result.Status, result.ExitCode)
	}
	if repo.read("file.txt") != "new\n" {
		t.Fatalf("file content = %q, want patched content", repo.read("file.txt"))
	}
	if len(result.ArtifactIDs) != 1 || result.ArtifactIDs[0] != "patch-task-run-1" {
		t.Fatalf("artifact ids = %+v, want patch-task-run-1", result.ArtifactIDs)
	}
	if result.RuntimeMetadata["patch_back_status"] != PatchBackApplied {
		t.Fatalf("metadata = %+v, want patch-back applied", result.RuntimeMetadata)
	}
	artifact, err := st.Artifacts().Get(ctx, "WS", "patch-task-run-1")
	if err != nil {
		t.Fatalf("get artifact: %v", err)
	}
	if artifact.OwnerType != "task_run" || artifact.OwnerID != "task-run-1" || artifact.TaskID != "TASK-1" || artifact.Type != "patch" {
		t.Fatalf("artifact ownership = %+v, want task-run patch artifact", artifact)
	}
	if artifact.DurableStatus != "finalized" || artifact.URI == "" || artifact.ContentHash == "" || artifact.Checksum == "" || artifact.FinalizedAt == nil {
		t.Fatalf("artifact = %+v, want finalized durable artifact", artifact)
	}
}

func TestLocalTaskRunnerPublishedDelivery(t *testing.T) {
	localReq := hostBridgeTaskExecRequest()
	localReq.RunnerEntrypoint = LocalTaskRunnerEntrypoint
	for _, tc := range []struct {
		name     string
		req      TaskExecRequest
		status   domain.TaskRunStatus
		delivery string
		want     bool
	}{
		{name: "local branch", req: localReq, status: domain.TaskRunCompleted, delivery: "local_branch", want: true},
		{name: "stack branch", req: localReq, status: domain.TaskRunCompleted, delivery: "stack_branch", want: true},
		{name: "pull request", req: localReq, status: domain.TaskRunCompleted, delivery: "pull_request", want: true},
		{name: "patch back", req: localReq, status: domain.TaskRunCompleted, delivery: "patch_back"},
		{name: "empty published unit", req: localReq, status: domain.TaskRunCompleted, delivery: "pull_request_skipped_no_changes"},
		{name: "failed result", req: localReq, status: domain.TaskRunFailed, delivery: "local_branch"},
		{name: "foreign runner cannot self assert", req: hostBridgeTaskExecRequest(), status: domain.TaskRunCompleted, delivery: "local_branch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := TaskExecResult{
				Status:          tc.status,
				RuntimeMetadata: map[string]string{"delivery": tc.delivery},
			}
			if got := localTaskRunnerPublishedDelivery(tc.req, result); got != tc.want {
				t.Fatalf("localTaskRunnerPublishedDelivery() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHostBridgeTaskExecutorPersistsPublishedPatchWithoutPatchBack(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	repo := newPatchBackRepo(t)
	base := repo.commitFile("file.txt", "old\n", "base")
	patch := "diff --git a/file.txt b/file.txt\n--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+published\n"
	req := hostBridgeTaskExecRequest()
	req.RunnerEntrypoint = LocalTaskRunnerEntrypoint
	req.RunnerTrustLevel = domain.DriverTrustTrusted

	executor := HostBridgeTaskExecutor{
		Store:               st,
		Artifacts:           testArtifactsAPI(st),
		ArtifactAuthorities: taskWorkerTestAuthorities{},
		WorktreePath:        repo.dir,
		APIBaseURL:          testTaskRunAPIURL,
		Command:             hostBridgeHelperCommand(t, "published", base, patch),
	}
	result, err := executor.ExecuteTask(ctx, req)
	if err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}
	if result.Status != domain.TaskRunCompleted || result.ExitCode != 0 {
		t.Fatalf("result = %+v, want completed and zero exit", result)
	}
	if got := repo.read("file.txt"); got != "old\n" {
		t.Fatalf("host file content = %q, want published delivery to leave it unchanged", got)
	}
	if got := result.RuntimeMetadata["patch_artifact_id"]; got != "patch-task-run-1" {
		t.Fatalf("patch artifact metadata = %q, want patch-task-run-1", got)
	}
	if _, ok := result.RuntimeMetadata["patch_back_status"]; ok {
		t.Fatalf("published delivery unexpectedly attempted patch-back: %+v", result.RuntimeMetadata)
	}
	artifact, err := st.Artifacts().Get(ctx, "WS", "patch-task-run-1")
	if err != nil {
		t.Fatalf("get patch artifact: %v", err)
	}
	if artifact.Type != "patch" || artifact.DurableStatus != "finalized" || artifact.ContentHash == "" {
		t.Fatalf("patch artifact = %+v, want finalized exact diff evidence", artifact)
	}
	contentReader, ok := st.Artifacts().(interface {
		ReadContent(context.Context, string, string) ([]byte, error)
	})
	if !ok {
		t.Fatal("artifact store does not expose ReadContent")
	}
	content, err := contentReader.ReadContent(ctx, "WS", "patch-task-run-1")
	if err != nil {
		t.Fatalf("read patch artifact content: %v", err)
	}
	if string(content) != patch {
		t.Fatalf("patch artifact content = %q, want exact runner patch %q", content, patch)
	}
}

func TestHostBridgeTaskExecutorPreservesFinalizedPatchArtifactOnConflict(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	repo := newPatchBackRepo(t)
	base := repo.commitFile("file.txt", "old\n", "base")
	repo.write("file.txt", "local edit\n")
	patch := "diff --git a/file.txt b/file.txt\n--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+new\n"

	executor := HostBridgeTaskExecutor{
		Store:               st,
		Artifacts:           testArtifactsAPI(st),
		ArtifactAuthorities: taskWorkerTestAuthorities{},
		WorktreePath:        repo.dir,
		APIBaseURL:          testTaskRunAPIURL,
		Command:             hostBridgeHelperCommand(t, "success", base, patch),
	}
	result, err := executor.ExecuteTask(ctx, hostBridgeTaskExecRequest())
	if err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}
	if result.Status != domain.TaskRunFailed || result.ErrorClass != PatchBackConflict {
		t.Fatalf("result = %+v, want failed patch conflict", result)
	}
	if repo.read("file.txt") != "local edit\n" {
		t.Fatalf("file content = %q, want local edit preserved", repo.read("file.txt"))
	}
	if len(result.ArtifactIDs) != 1 || result.ArtifactIDs[0] != "patch-task-run-1" {
		t.Fatalf("artifact ids = %+v, want preserved patch artifact id", result.ArtifactIDs)
	}
	if result.RuntimeMetadata["patch_preserved"] != "true" || result.RuntimeMetadata["patch_back_status"] != PatchBackConflict {
		t.Fatalf("metadata = %+v, want preserved patch conflict", result.RuntimeMetadata)
	}
	artifact, err := st.Artifacts().Get(ctx, "WS", "patch-task-run-1")
	if err != nil {
		t.Fatalf("get artifact: %v", err)
	}
	if artifact.DurableStatus != "finalized" || artifact.ContentHash == "" {
		t.Fatalf("artifact = %+v, want finalized patch artifact despite conflict", artifact)
	}
}

func TestHostBridgeTaskExecutorThroughRequestTaskRun(t *testing.T) {
	ctx, st, run := setupRunningDriverRun(t)
	repo := newPatchBackRepo(t)
	base := repo.commitFile("file.txt", "old\n", "base")
	patch := "diff --git a/file.txt b/file.txt\n--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+new\n"
	executor := HostBridgeTaskExecutor{
		Store:               st,
		Artifacts:           testArtifactsAPI(st),
		ArtifactAuthorities: taskWorkerTestAuthorities{},
		WorktreePath:        repo.dir,
		APIBaseURL:          testTaskRunAPIURL,
		Command:             hostBridgeHelperCommand(t, "success", base, patch),
	}

	outcome, err := RequestTaskRunWithResult(ctx, st, TaskRunRequestOptions{
		WorkspaceKey:    "TEST",
		DriverRunID:     run.RunID,
		TaskRunID:       "task-run-1",
		TaskID:          "TEST-1",
		ProviderProfile: "flue-daytona",
		ParentNodeID:    run.NodeID,
		ParentLeaseID:   run.LeaseID,
		ParentFence:     run.FencingToken,
		DeferCompletion: true,
	}, executor)
	if err != nil {
		t.Fatalf("RequestTaskRunWithResult: %v", err)
	}
	if outcome.Run.Status != domain.TaskRunCompleted || outcome.Run.ArtifactsRef != "artifacts://task-run-1" {
		t.Fatalf("run = %+v, want completed with artifacts ref", outcome.Run)
	}
	stored, err := st.TaskRuns().Get(ctx, "TEST", "task-run-1")
	if err != nil {
		t.Fatalf("get stored task run: %v", err)
	}
	if stored.Status != domain.TaskRunRunning || stored.ArtifactsRef != "artifacts://task-run-1" {
		t.Fatalf("stored run = %+v, want running with pending artifacts ref", stored)
	}
	if len(outcome.ArtifactIDs) != 1 || outcome.ArtifactIDs[0] != "patch-task-run-1" {
		t.Fatalf("outcome artifact ids = %+v, want patch artifact", outcome.ArtifactIDs)
	}
	artifact, err := st.Artifacts().Get(ctx, "TEST", "patch-task-run-1")
	if err != nil {
		t.Fatalf("get artifact: %v", err)
	}
	if artifact.DurableStatus != "finalized" || artifact.OwnerID != "task-run-1" {
		t.Fatalf("artifact = %+v, want finalized child artifact", artifact)
	}
	exitCode := 0
	completed, err := st.TaskRuns().Complete(ctx, "TEST", "task-run-1", store.TaskRunComplete{
		CompletionID:        "complete-task-run-1",
		NodeID:              stored.NodeID,
		LeaseID:             stored.LeaseID,
		LeaseToken:          outcome.LeaseToken,
		FencingToken:        stored.FencingToken,
		Status:              domain.TaskRunCompleted,
		ExitCode:            &exitCode,
		LogsRef:             outcome.Run.LogsRef,
		ArtifactsRef:        outcome.Run.ArtifactsRef,
		RequiredArtifactIDs: outcome.ArtifactIDs,
		RequireArtifacts:    true,
		CloseTask:           true,
		CloseReason:         "patched",
	})
	if err != nil {
		t.Fatalf("complete deferred task run: %v", err)
	}
	if completed.Status != domain.TaskRunCompleted {
		t.Fatalf("completed status = %s, want completed", completed.Status)
	}
}

func TestHostBridgeTaskExecutorRegistersFinalizedRunnerArtifacts(t *testing.T) {
	ctx, st, run := setupRunningDriverRun(t)
	executor := HostBridgeTaskExecutor{
		Store:               st,
		Artifacts:           testArtifactsAPI(st),
		ArtifactAuthorities: taskWorkerTestAuthorities{},
		WorktreePath:        t.TempDir(),
		APIBaseURL:          testTaskRunAPIURL,
		Command:             hostBridgeHelperCommand(t, "artifact", "unused-base", "unused-patch"),
	}

	outcome, err := RequestTaskRunWithResult(ctx, st, TaskRunRequestOptions{
		WorkspaceKey:    "TEST",
		DriverRunID:     run.RunID,
		TaskRunID:       "task-run-1",
		TaskID:          "TEST-1",
		ProviderProfile: "flue-daytona",
		ParentNodeID:    run.NodeID,
		ParentLeaseID:   run.LeaseID,
		ParentFence:     run.FencingToken,
		DeferCompletion: true,
	}, executor)
	if err != nil {
		t.Fatalf("RequestTaskRunWithResult: %v", err)
	}
	if outcome.Run.Status != domain.TaskRunCompleted {
		t.Fatalf("run status = %s error=%s, want completed", outcome.Run.Status, outcome.Run.ErrorMessage)
	}
	if len(outcome.ArtifactIDs) != 1 || outcome.ArtifactIDs[0] != "artifact-task-run-1" {
		t.Fatalf("outcome artifact ids = %+v, want finalized runner artifact", outcome.ArtifactIDs)
	}
	if outcome.Run.ArtifactsRef != "artifacts://task-run-1" {
		t.Fatalf("artifacts ref = %q, want default task-run artifact ref", outcome.Run.ArtifactsRef)
	}
	artifact, err := st.Artifacts().Get(ctx, "TEST", "artifact-task-run-1")
	if err != nil {
		t.Fatalf("get runner artifact: %v", err)
	}
	if artifact.DurableStatus != "finalized" || artifact.URI != "artifact://artifact-task-run-1" || artifact.ContentHash != "sha256:remote-artifact" || artifact.OwnerID != "task-run-1" {
		t.Fatalf("artifact = %+v, want finalized server-visible task-run artifact", artifact)
	}
	stored, err := st.TaskRuns().Get(ctx, "TEST", "task-run-1")
	if err != nil {
		t.Fatalf("get stored task run: %v", err)
	}
	exitCode := 0
	if _, err := st.TaskRuns().Complete(ctx, "TEST", "task-run-1", store.TaskRunComplete{
		CompletionID:        "complete-finalized-artifact-task-run-1",
		NodeID:              stored.NodeID,
		LeaseID:             stored.LeaseID,
		LeaseToken:          outcome.LeaseToken,
		FencingToken:        stored.FencingToken,
		Status:              domain.TaskRunCompleted,
		ExitCode:            &exitCode,
		ArtifactsRef:        outcome.Run.ArtifactsRef,
		RequiredArtifactIDs: outcome.ArtifactIDs,
		RequireArtifacts:    true,
		CloseTask:           true,
		CloseReason:         "runner finalized artifact",
	}); err != nil {
		t.Fatalf("complete task run with finalized runner artifact: %v", err)
	}
}

func TestHostBridgeTaskExecutorMapsFlueSessionAndTranscript(t *testing.T) {
	ctx, st, run := setupRunningDriverRun(t)
	executor := HostBridgeTaskExecutor{
		Store:               st,
		Artifacts:           testArtifactsAPI(st),
		ArtifactAuthorities: taskWorkerTestAuthorities{},
		WorktreePath:        t.TempDir(),
		APIBaseURL:          testTaskRunAPIURL,
		Command:             hostBridgeHelperCommand(t, "flue-transcript", "unused-base", "unused-patch"),
	}

	outcome, err := RequestTaskRunWithResult(ctx, st, TaskRunRequestOptions{
		WorkspaceKey:    "TEST",
		DriverRunID:     run.RunID,
		TaskRunID:       "task-run-1",
		TaskID:          "TEST-1",
		Runner:          "local-task-runner",
		ParentSessionID: "lead-session-1",
		ParentNodeID:    run.NodeID,
		ParentLeaseID:   run.LeaseID,
		ParentFence:     run.FencingToken,
		DeferCompletion: true,
	}, executor)
	if err != nil {
		t.Fatalf("RequestTaskRunWithResult: %v", err)
	}
	if outcome.Run.Status != domain.TaskRunCompleted {
		t.Fatalf("run status = %s error=%s, want completed", outcome.Run.Status, outcome.Run.ErrorMessage)
	}
	if len(outcome.ArtifactIDs) != 2 || outcome.ArtifactIDs[0] != "transcript-task-run-1" || outcome.ArtifactIDs[1] != "logs-task-run-1" {
		t.Fatalf("artifact ids = %+v, want transcript and logs artifacts", outcome.ArtifactIDs)
	}
	session, err := st.AgentSessions().Get(ctx, "TEST", "flue-task-run-1")
	if err != nil {
		t.Fatalf("get flue agent session: %v", err)
	}
	if session.Kind != domain.AgentSessionKindTask || session.TaskID != "TEST-1" || session.ParentSessionID != "lead-session-1" {
		t.Fatalf("session identity = %+v, want task session under lead-session-1", session)
	}
	if session.Status != domain.AgentSessionCompleted || session.FinishedAt == nil {
		t.Fatalf("session status = %s finished=%v, want completed", session.Status, session.FinishedAt)
	}
	if session.Metadata["runtime"] != "flue" || session.Metadata["flue_session"] != "flue-task-run-1" || session.Metadata["task_run_id"] != "task-run-1" {
		t.Fatalf("session metadata = %+v, want flue task-run metadata", session.Metadata)
	}
	if session.Metadata["transcript_ref"] != "artifact://transcript-task-run-1" || session.Metadata["logs_ref"] != "artifact://logs-task-run-1" {
		t.Fatalf("session refs = %+v, want transcript/log artifact refs", session.Metadata)
	}
	transcriptArtifact, err := st.Artifacts().Get(ctx, "TEST", "transcript-task-run-1")
	if err != nil {
		t.Fatalf("get transcript artifact: %v", err)
	}
	if transcriptArtifact.DurableStatus != "finalized" || transcriptArtifact.SessionID != "flue-task-run-1" || transcriptArtifact.Type != "transcript" {
		t.Fatalf("transcript artifact = %+v, want finalized transcript owned by session", transcriptArtifact)
	}
	contentReader, ok := st.Artifacts().(interface {
		ReadContent(context.Context, string, string) ([]byte, error)
	})
	if !ok {
		t.Fatal("artifact store does not expose ReadContent")
	}
	content, err := contentReader.ReadContent(ctx, "TEST", "transcript-task-run-1")
	if err != nil {
		t.Fatalf("read transcript artifact content: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 3 {
		t.Fatalf("transcript lines = %d, want 3: %s", len(lines), content)
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("parse first transcript line: %v", err)
	}
	if first["seq"] == nil || first["timestamp"] == nil || first["role"] != "user" || first["type"] != "text" {
		t.Fatalf("first transcript line = %+v, want canonical user text", first)
	}
	if first["type"] == "turn_request" {
		t.Fatalf("first transcript line looks like raw event: %+v", first)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(lines[2]), &result); err != nil {
		t.Fatalf("parse tool result transcript line: %v", err)
	}
	if result["role"] != "tool" || result["type"] != "tool_result" || result["tool_use_id"] != "tool-1" || result["output"] != "ok" {
		t.Fatalf("tool result line = %+v, want canonical tool result output", result)
	}
	if _, hasText := result["text"]; hasText {
		t.Fatalf("tool result line should use output, not text: %+v", result)
	}
}

func TestHostBridgeTaskExecutorReusesOutputArtifactsOnRetry(t *testing.T) {
	ctx, st, _ := setupRunningDriverRun(t)
	executor := HostBridgeTaskExecutor{
		Store:               st,
		Artifacts:           testArtifactsAPI(st),
		ArtifactAuthorities: taskWorkerTestAuthorities{},
		WorktreePath:        t.TempDir(),
		APIBaseURL:          testTaskRunAPIURL,
		Command:             hostBridgeHelperCommand(t, "flue-transcript", "unused-base", "unused-patch"),
	}
	req := hostBridgeTaskExecRequest()
	req.RunnerKind = RunnerKindFlueWorkflow
	req.RunnerTrustLevel = domain.DriverTrustTrusted

	first, err := executor.ExecuteTask(ctx, req)
	if err != nil {
		t.Fatalf("first ExecuteTask: %v", err)
	}
	if first.Status != domain.TaskRunCompleted {
		t.Fatalf("first status = %s, want completed", first.Status)
	}
	second, err := executor.ExecuteTask(ctx, req)
	if err != nil {
		t.Fatalf("second ExecuteTask: %v", err)
	}
	if second.Status != domain.TaskRunCompleted {
		t.Fatalf("second status = %s, want completed", second.Status)
	}
	want := []string{"transcript-task-run-1", "logs-task-run-1"}
	if !slices.Equal(second.ArtifactIDs, want) {
		t.Fatalf("second artifact ids = %+v, want %+v", second.ArtifactIDs, want)
	}
}

func TestHostBridgeTaskExecutorRunsNodeModuleThroughGenericInvoker(t *testing.T) {
	ctx := context.Background()
	worktree := t.TempDir()
	if err := os.MkdirAll(filepath.Join(worktree, "runners"), 0o755); err != nil {
		t.Fatalf("mkdir runners: %v", err)
	}
	module := `export async function run(ctx) {
  if (ctx.request.task_run_id !== "task-run-1") throw new Error("missing request");
  if (ctx.env.LOOM_TASK_RUNNER_KIND !== "node-module") throw new Error("missing runner kind env");
  return {
    status: "completed",
    exitCode: 0,
    logsRef: "logs://" + ctx.request.task_run_id,
    runtimeMetadata: {
      module_runner: "ok",
      input_message: (ctx.input && ctx.input.message) || ""
    }
  };
}
`
	if err := os.WriteFile(filepath.Join(worktree, "runners", "local-command-runner.mjs"), []byte(module), 0o644); err != nil {
		t.Fatalf("write runner module: %v", err)
	}
	req := hostBridgeTaskExecRequest()
	req.ProviderProfile = ""
	req.Runner = "local-command-runner"
	req.RunnerKind = RunnerKindNodeModule
	req.RunnerEntrypoint = "runners/local-command-runner.mjs"
	req.RunnerTrustLevel = domain.DriverTrustTrusted
	req.Input = json.RawMessage(`{"message":"hello"}`)
	executor := HostBridgeTaskExecutor{
		WorktreePath: worktree,
		APIBaseURL:   testTaskRunAPIURL,
		Command:      []string{"node", genericTaskRunnerInvokerPath(t)},
	}
	result, err := executor.ExecuteTask(ctx, req)
	if err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}
	if result.Status != domain.TaskRunCompleted || result.ExitCode != 0 || result.LogsRef != "logs://task-run-1" {
		t.Fatalf("result = %+v, want completed node-module result", result)
	}
	if result.RuntimeMetadata["module_runner"] != "ok" ||
		result.RuntimeMetadata["input_message"] != "hello" ||
		result.RuntimeMetadata["task_runner_invoker"] != "loom-task-runner-invoker" ||
		result.RuntimeMetadata["runner"] != "local-command-runner" ||
		result.RuntimeMetadata["runner_kind"] != RunnerKindNodeModule ||
		result.RuntimeMetadata["runner_entrypoint"] != "runners/local-command-runner.mjs" {
		t.Fatalf("runtime metadata = %+v, want node-module runner metadata", result.RuntimeMetadata)
	}
}

func TestStackScriptsUseGenericTaskRunnerInvoker(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		forbidden string
	}{
		{name: "slack", path: "../../smoke-test/smoke-test-slack-epic-runner-stack.sh", forbidden: "flue-task-agent-runner.mjs"},
		{name: "github-review", path: "../../scripts/run-github-review-codex-stack.sh", forbidden: "codex-review-runner.mjs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := os.ReadFile(tt.path)
			if err != nil {
				t.Fatalf("read %s: %v", tt.path, err)
			}
			source := string(data)
			if !strings.Contains(source, "scripts/loom-task-runner-invoker.mjs") ||
				!strings.Contains(source, "LOOM_DRIVER_TASK_RUNNER_CMD_JSON") ||
				!strings.Contains(source, "loom-task-runner-invoker.mjs") {
				t.Fatalf("%s does not wire the generic task runner invoker", tt.path)
			}
			if strings.Contains(source, tt.forbidden) {
				t.Fatalf("%s still references retired runner %q", tt.path, tt.forbidden)
			}
		})
	}
}

func TestHostBridgeTaskExecutorPreflightsBuiltInFlueWorkflowWithoutCommand(t *testing.T) {
	executor := HostBridgeTaskExecutor{APIBaseURL: testTaskRunAPIURL}
	if _, err := executor.PreflightTaskProvider(context.Background(), TaskRunRequestOptions{
		Runner:           "daytona-task-runner",
		RunnerKind:       RunnerKindFlueWorkflow,
		RunnerTrustLevel: domain.DriverTrustTrusted,
	}); err != nil {
		t.Fatalf("PreflightTaskProvider flue-workflow err = %v, want nil", err)
	}
	if _, err := executor.PreflightTaskProvider(context.Background(), TaskRunRequestOptions{
		Runner:           "node-runner",
		RunnerKind:       RunnerKindNodeModule,
		RunnerTrustLevel: domain.DriverTrustTrusted,
	}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("PreflightTaskProvider node-module err = %v, want ErrInvalid", err)
	}
}

func TestHostBridgeTaskExecutorRefusesUntrustedNamedRunner(t *testing.T) {
	ranPath := filepath.Join(t.TempDir(), "ran")
	executor := HostBridgeTaskExecutor{
		APIBaseURL: testTaskRunAPIURL,
		Command:    []string{"sh", "-c", "printf ran > \"$1\"; printf '%s\n' '{\"status\":\"completed\",\"exit_code\":0}'", "sh", ranPath},
	}
	req := hostBridgeTaskExecRequest()
	req.Runner = "local-task-runner"
	req.RunnerKind = RunnerKindFlueWorkflow
	req.RunnerEntrypoint = "local-task-runner"
	req.RunnerVersionID = "driver-version-untrusted"
	req.RunnerTrustLevel = domain.DriverTrustUntrusted

	result, err := executor.ExecuteTask(context.Background(), req)
	if err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}
	if result.Status != domain.TaskRunFailed || result.ErrorClass != ErrorClassSandboxRequired || result.ExitCode != 1 {
		t.Fatalf("result = %+v, want failed %s", result, ErrorClassSandboxRequired)
	}
	if result.RuntimeMetadata[ErrorCodeOutputKey] != ErrorClassSandboxRequired ||
		result.RuntimeMetadata[SandboxLauncherOutputKey] != SandboxProviderProcess ||
		result.RuntimeMetadata["runner_trust_level"] != string(domain.DriverTrustUntrusted) {
		t.Fatalf("runtime metadata = %+v, want sandbox refusal audit", result.RuntimeMetadata)
	}
	if _, err := os.Stat(ranPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("host bridge command ran despite untrusted runner refusal; stat err=%v", err)
	}
	if _, err := executor.PreflightTaskProvider(context.Background(), TaskRunRequestOptions{
		Runner:           "local-task-runner",
		RunnerKind:       RunnerKindFlueWorkflow,
		RunnerTrustLevel: domain.DriverTrustUntrusted,
	}); !errors.Is(err, domain.ErrInvalid) || !strings.Contains(err.Error(), ErrorClassSandboxRequired) {
		t.Fatalf("PreflightTaskProvider err = %v, want ErrInvalid containing %s", err, ErrorClassSandboxRequired)
	}
}

func TestHostBridgeTaskExecutorRunsFlueWorkflowThroughGenericInvoker(t *testing.T) {
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
		OwnerType:    domain.DriverOwnerUser,
		Status:       domain.DriverStatusActive,
		TrustLevel:   domain.DriverTrustTrusted,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	bundleRoot := filepath.Join(worktree, ".loom", "drivers", "epic-runner", "driver-version-1")
	if err := os.MkdirAll(filepath.Join(bundleRoot, "dist"), 0o755); err != nil {
		t.Fatalf("mkdir bundle: %v", err)
	}
	server := `
function send(message) {
  if (typeof process.send === "function") process.send({ version: 1, ...message });
}
process.on("message", (message) => {
  if (!message || message.type !== "invoke") return;
  send({
    type: "result",
    requestId: message.requestId,
    result: {
      status: "completed",
      exitCode: 0,
      logsRef: "logs://" + message.payload.task_run_id,
      runtimeMetadata: {
        workflow: process.env.FLUE_CLI_NAME,
        request_task_run: message.payload.task_run_id
      }
    }
  });
});
send({ type: "ready" });
setInterval(() => {}, 1000);
`
	if err := os.WriteFile(filepath.Join(bundleRoot, "dist", "server.mjs"), []byte(server), 0o644); err != nil {
		t.Fatalf("write server: %v", err)
	}
	manifest := map[string]string{
		"server_ref":    "dist/server.mjs",
		"workflow_name": "epic-runner",
	}
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
		ValidationStatus: domain.DriverVersionValidationPassed,
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
	req.RunnerTrustLevel = domain.DriverTrustTrusted
	executor := HostBridgeTaskExecutor{
		Store:        st,
		WorktreePath: worktree,
		APIBaseURL:   testTaskRunAPIURL,
		Command:      []string{"node", genericTaskRunnerInvokerPath(t)},
	}
	result, err := executor.ExecuteTask(ctx, req)
	if err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}
	if result.Status != domain.TaskRunCompleted || result.ExitCode != 0 || result.LogsRef != "logs://task-run-1" {
		t.Fatalf("result = %+v, want completed flue-workflow result", result)
	}
	if result.RuntimeMetadata["workflow"] != "local-task-runner" ||
		result.RuntimeMetadata["task_runner_invoker"] != "loom-task-runner-invoker" ||
		result.RuntimeMetadata["runner"] != "local-task-runner" ||
		result.RuntimeMetadata["runner_kind"] != RunnerKindFlueWorkflow ||
		result.RuntimeMetadata["runner_entrypoint"] != "local-task-runner" {
		t.Fatalf("runtime metadata = %+v, want flue workflow metadata", result.RuntimeMetadata)
	}

	directReq := req
	directReq.TaskRunID = "task-run-2"
	directReq.TaskID = "TASK-2"
	directReq.LeaseToken = "scoped-task-token-2"
	directResult, err := (HostBridgeTaskExecutor{
		Store:        st,
		WorktreePath: worktree,
		APIBaseURL:   testTaskRunAPIURL,
	}).ExecuteTask(ctx, directReq)
	if err != nil {
		t.Fatalf("direct ExecuteTask: %v", err)
	}
	if directResult.Status != domain.TaskRunCompleted || directResult.ExitCode != 0 || directResult.LogsRef != "logs://task-run-2" {
		t.Fatalf("direct result = %+v, want completed flue-workflow result", directResult)
	}
	if directResult.RuntimeMetadata["workflow"] != "local-task-runner" ||
		directResult.RuntimeMetadata["task_runner_invoker"] != "loom-builtin-flue-runner" ||
		directResult.RuntimeMetadata["runner"] != "local-task-runner" ||
		directResult.RuntimeMetadata["runner_kind"] != RunnerKindFlueWorkflow ||
		directResult.RuntimeMetadata["runner_entrypoint"] != "local-task-runner" {
		t.Fatalf("direct runtime metadata = %+v, want built-in flue workflow metadata", directResult.RuntimeMetadata)
	}
}

func TestTaskRunnerEnvIncludesFlueBundleForRunnerVersion(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	worktree := t.TempDir()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "ws"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS",
		DriverID:     "epic-runner",
		Name:         "epic-runner",
		OwnerType:    domain.DriverOwnerUser,
		Status:       domain.DriverStatusActive,
		TrustLevel:   domain.DriverTrustTrusted,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	bundleRoot := filepath.Join(worktree, ".loom", "drivers", "epic-runner", "driver-version-1")
	if err := os.MkdirAll(filepath.Join(bundleRoot, "dist"), 0o755); err != nil {
		t.Fatalf("mkdir bundle: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bundleRoot, "dist", "server.mjs"), []byte("export {};\n"), 0o644); err != nil {
		t.Fatalf("write server: %v", err)
	}
	manifest := map[string]string{
		"server_ref":    "dist/server.mjs",
		"workflow_name": "epic-runner",
	}
	manifestBytes, err := writeFlueBundleManifest(bundleRoot, manifest)
	if err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	digest, err := digestBundleTree(bundleRoot, manifestBytes)
	if err != nil {
		t.Fatalf("digest bundle: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey:     "WS",
		VersionID:        "driver-version-1",
		DriverID:         "epic-runner",
		Version:          1,
		SourceDigest:     "sha256:source",
		BundleRef:        filepath.ToSlash(filepath.Join(".loom", "drivers", "epic-runner", "driver-version-1")),
		BundleDigest:     digest,
		Runtime:          RuntimeFlueNode,
		Manifest:         manifest,
		ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}
	req := hostBridgeTaskExecRequest()
	req.RunnerKind = RunnerKindFlueWorkflow
	req.RunnerVersionID = "driver-version-1"
	req.RunnerTrustLevel = domain.DriverTrustTrusted
	env := HostBridgeTaskExecutor{Store: st, WorktreePath: worktree}.taskRunnerEnv(req, "{}")
	if !envContains(env, "LOOM_TASK_RUNNER_BUNDLE_ROOT="+bundleRoot) {
		t.Fatalf("env missing bundle root: %v", env)
	}
	if !envContains(env, "LOOM_TASK_RUNNER_SERVER_PATH="+filepath.Join(bundleRoot, "dist", "server.mjs")) {
		t.Fatalf("env missing server path: %v", env)
	}
	if !envContainsPrefix(env, "LOOM_TASK_RUNNER_MANIFEST_JSON=") {
		t.Fatalf("env missing manifest JSON: %v", env)
	}
}

func TestBridgeTaskRunnerResultDecodesFinalizedArtifacts(t *testing.T) {
	var result bridgeTaskRunnerResult
	if err := json.Unmarshal([]byte(`{"artifacts":[{"artifact_id":"artifact-1","uri":"artifact://artifact-1","content_hash":"sha256:x"}]}`), &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	artifacts := result.finalizedArtifacts()
	if len(artifacts) != 1 || artifacts[0].ArtifactID != "artifact-1" || artifacts[0].URI != "artifact://artifact-1" || artifacts[0].ContentHash != "sha256:x" {
		t.Fatalf("decoded artifacts = %+v, want artifact descriptor", artifacts)
	}
}

func TestHostBridgeTaskExecutorHelperProcess(t *testing.T) {
	if os.Getenv("LOOM_HOST_BRIDGE_HELPER") != "1" {
		return
	}
	for _, key := range []string{"LOOM_FLEET_DB_URL", "LOOM_FLEET_DB_API_KEY", "LOOM_FLEET_DB_ACTOR", "LOOM_RUNNER_LEASE_TOKEN", "OPENAI_API_KEY", "AWS_SECRET_ACCESS_KEY", "GITHUB_TOKEN"} {
		if os.Getenv(key) != "" {
			t.Fatalf("%s leaked into task runner env", key)
		}
	}
	stdin, err := io.ReadAll(os.Stdin)
	if err != nil {
		t.Fatalf("read stdin: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(stdin, &raw); err != nil {
		t.Fatalf("decode raw request: %v", err)
	}
	if _, ok := raw["task_run_id"]; !ok {
		t.Fatalf("request JSON missing task_run_id: %s", stdin)
	}
	if _, ok := raw["TaskRunID"]; ok {
		t.Fatalf("request JSON contains Go field name: %s", stdin)
	}
	var req TaskExecRequest
	if err := json.Unmarshal(stdin, &req); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if req.TaskRunID == "" || os.Getenv("LOOM_TASK_RUN_ID") != req.TaskRunID {
		t.Fatalf("request/env task run mismatch: %+v env=%q", req, os.Getenv("LOOM_TASK_RUN_ID"))
	}
	if req.LeaseToken == "" || os.Getenv("LOOM_TASK_RUN_LEASE_TOKEN") != req.LeaseToken {
		t.Fatalf("request/env task run lease token mismatch: req=%q env=%q", req.LeaseToken, os.Getenv("LOOM_TASK_RUN_LEASE_TOKEN"))
	}
	if req.ParentSessionID != "" && (os.Getenv("LOOM_PARENT_SESSION_ID") != req.ParentSessionID || os.Getenv("LOOM_TASK_RUN_PARENT_SESSION_ID") != req.ParentSessionID) {
		t.Fatalf("request/env parent session mismatch: req=%q parent=%q task_run_parent=%q", req.ParentSessionID, os.Getenv("LOOM_PARENT_SESSION_ID"), os.Getenv("LOOM_TASK_RUN_PARENT_SESSION_ID"))
	}
	if os.Getenv("LOOM_TASK_RUN_PROVIDER_PROFILE") != req.ProviderProfile {
		t.Fatalf("provider env mismatch: req=%q task_run=%q", req.ProviderProfile, os.Getenv("LOOM_TASK_RUN_PROVIDER_PROFILE"))
	}
	if os.Getenv("LOOM_TASK_PROVIDER_PROFILE") != "" {
		t.Fatalf("legacy provider env should not be set: %q", os.Getenv("LOOM_TASK_PROVIDER_PROFILE"))
	}
	if os.Getenv("LOOM_WORKTREE_PATH") == "" {
		t.Fatal("LOOM_WORKTREE_PATH missing")
	}
	args := os.Args
	mode := args[len(args)-3]
	base := args[len(args)-2]
	patch := args[len(args)-1]
	switch mode {
	case "success":
		result := map[string]any{
			"status":         "completed",
			"exit_code":      0,
			"patch":          patch,
			"patch_base_ref": base,
			"logs_ref":       "logs://" + req.TaskRunID,
			"runtime_metadata": map[string]string{
				"helper": "host_bridge",
			},
		}
		if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
			t.Fatalf("encode result: %v", err)
		}
	case "published":
		result := map[string]any{
			"status":         "completed",
			"exitCode":       0,
			"patch":          patch,
			"patch_base_ref": base,
			"logsRef":        "logs://" + req.TaskRunID,
			"runtimeMetadata": map[string]string{
				"helper":   "host_bridge",
				"delivery": "local_branch",
			},
		}
		if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
			t.Fatalf("encode result: %v", err)
		}
	case "artifact":
		result := map[string]any{
			"status":    "completed",
			"exit_code": 0,
			"artifacts": []map[string]any{{
				"artifact_id":  "artifact-" + req.TaskRunID,
				"type":         "patch",
				"uri":          "artifact://artifact-" + req.TaskRunID,
				"content_hash": "sha256:remote-artifact",
				"checksum":     "sha256:remote-artifact",
				"mime_type":    "text/x-diff",
				"size_bytes":   123,
				"summary":      "remote patch",
				"metadata": map[string]string{
					"source": "remote-runner",
				},
			}},
			"runtime_metadata": map[string]string{
				"helper": "host_bridge",
			},
		}
		if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
			t.Fatalf("encode result: %v", err)
		}
	case "flue-transcript":
		result := map[string]any{
			"status":    "completed",
			"exit_code": 0,
			"logs":      "flue runner log\n",
			"transcript_entries": []map[string]any{
				{
					"seq":       1,
					"timestamp": "2026-06-09T12:00:00Z",
					"role":      "user",
					"type":      "text",
					"text":      "Implement TEST-1",
				},
				{
					"seq":         2,
					"timestamp":   "2026-06-09T12:00:01Z",
					"role":        "assistant",
					"type":        "tool_use",
					"tool_name":   "bash",
					"tool_use_id": "tool-1",
					"tool_input":  map[string]any{"command": "npm test"},
				},
				{
					"seq":         3,
					"timestamp":   "2026-06-09T12:00:02Z",
					"role":        "tool",
					"type":        "tool_result",
					"tool_name":   "bash",
					"tool_use_id": "tool-1",
					"output":      "ok",
				},
			},
			"runtime_metadata": map[string]string{
				"helper":       "host_bridge",
				"flue_harness": "task-agent",
			},
		}
		if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
			t.Fatalf("encode result: %v", err)
		}
	case "flue-retry-transcript":
		attempt := req.TaskRunAttempt
		if attempt < 1 {
			attempt = 1
		}
		status := "failed"
		exitCode := 1
		errorClass := "retry_fixture"
		errorMessage := "attempt 1 failed"
		if attempt > 1 {
			status = "completed"
			exitCode = 0
			errorClass = ""
			errorMessage = ""
		}
		result := map[string]any{
			"status":        status,
			"exit_code":     exitCode,
			"error_class":   errorClass,
			"error_message": errorMessage,
			"logs":          "logs for attempt " + strconv.Itoa(attempt) + "\n",
			"transcript_entries": []map[string]any{{
				"seq":       1,
				"timestamp": "2026-07-23T01:00:00Z",
				"role":      "assistant",
				"type":      "text",
				"text":      "transcript for attempt " + strconv.Itoa(attempt),
			}},
			"runtime_metadata": map[string]string{
				"fixture_attempt": strconv.Itoa(attempt),
			},
		}
		if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
			t.Fatalf("encode retry transcript result: %v", err)
		}
	case "invalid-empty":
		os.Stdout.WriteString("{}\n")
	case "invalid-null":
		os.Stdout.WriteString("null\n")
	case "invalid-missing-status":
		// Carries patch + artifacts that MUST NOT be persisted.
		os.Stdout.WriteString(`{"exit_code":0,"patch":"` + invalidHelperPatch + `","patch_base_ref":"` + base + `","artifacts":[{"artifact_id":"artifact-` + req.TaskRunID + `","uri":"artifact://x","content_hash":"sha256:x"}],"logs":"should not persist\n"}` + "\n")
	case "invalid-unknown-status":
		os.Stdout.WriteString(`{"status":"weird","exit_code":0,"patch":"` + invalidHelperPatch + `","patch_base_ref":"` + base + `"}` + "\n")
	case "invalid-nonterminal":
		os.Stdout.WriteString(`{"status":"running","patch":"` + invalidHelperPatch + `","patch_base_ref":"` + base + `"}` + "\n")
	case "invalid-completed-nonzero":
		os.Stdout.WriteString(`{"status":"completed","exit_code":3,"patch":"` + invalidHelperPatch + `","patch_base_ref":"` + base + `","logs":"should not persist\n"}` + "\n")
	default:
		t.Fatalf("unknown helper mode %q", mode)
	}
	os.Exit(0)
}

// invalidHelperPatch is a syntactically valid patch the invalid-result helper
// modes emit so the test can prove an invalid result never applies it.
const invalidHelperPatch = "diff --git a/file.txt b/file.txt\\n--- a/file.txt\\n+++ b/file.txt\\n@@ -1 +1 @@\\n-old\\n+new\\n"

func hostBridgeTaskExecRequest() TaskExecRequest {
	return TaskExecRequest{
		WorkspaceKey:    "WS",
		DriverRunID:     "driver-run-1",
		TaskRunID:       "task-run-1",
		TaskID:          "TASK-1",
		ProviderProfile: "flue-daytona",
		ParentSessionID: "lead-session-1",
		NodeID:          "node-1",
		LeaseID:         "lease-1",
		LeaseToken:      "scoped-task-token",
		FencingToken:    42,
	}
}

func hostBridgeHelperCommand(t *testing.T, mode, base, patch string) []string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	t.Setenv("LOOM_HOST_BRIDGE_HELPER", "1")
	return []string{exe, "-test.run=TestHostBridgeTaskExecutorHelperProcess", "--", mode, base, patch}
}

func genericTaskRunnerInvokerPath(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "scripts", "loom-task-runner-invoker.mjs"))
	if err != nil {
		t.Fatalf("resolve generic invoker path: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat generic invoker: %v", err)
	}
	return path
}

func envContains(env []string, want string) bool {
	for _, entry := range env {
		if entry == want {
			return true
		}
	}
	return false
}

func envContainsPrefix(env []string, prefix string) bool {
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return true
		}
	}
	return false
}

func TestLastJSONLine(t *testing.T) {
	line, err := lastJSONLine([]byte("log line\n{\"ok\":true}\n"))
	if err != nil {
		t.Fatalf("lastJSONLine: %v", err)
	}
	if !bytes.Equal(line, []byte(`{"ok":true}`)) {
		t.Fatalf("line = %s, want JSON object", line)
	}
}

func TestServeHostedTaskSessionHeartbeatAdvancesUntilCanceled(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "ws"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "task-session",
		AgentID:      "task-agent",
		Kind:         domain.AgentSessionKindTask,
		Status:       domain.AgentSessionRunning,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	before, err := st.AgentSessions().Get(ctx, "WS", "task-session")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}

	hbCtx, cancel := context.WithCancel(context.Background())
	go heartbeatFlueTaskSession(hbCtx, st, "WS", "task-session", time.Millisecond)
	t.Cleanup(cancel)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		after, getErr := st.AgentSessions().Get(ctx, "WS", "task-session")
		if getErr != nil {
			t.Fatalf("get heartbeat session: %v", getErr)
		}
		if after.LastHeartbeat.After(before.LastHeartbeat) {
			cancel()
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("serve-hosted task heartbeat did not advance the session")
}

func TestFinishFlueTaskSessionDrainsInFlightStaleHeartbeat(t *testing.T) {
	ctx := t.Context()
	base := memstore.New()
	if _, err := base.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "ws"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := base.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "task-session-race",
		AgentID:      "task-agent",
		Kind:         domain.AgentSessionKindTask,
		Status:       domain.AgentSessionRunning,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	staleSessions := &staleTaskHeartbeatAgentSessionStore{
		AgentSessionStore: base.AgentSessions(),
		read:              make(chan struct{}),
		release:           make(chan struct{}),
	}
	controlStore := &taskHeartbeatTestStore{Store: base, sessions: staleSessions}
	hbCtx, cancel := context.WithCancel(context.Background())
	session := &flueTaskSession{
		SessionID:     "task-session-race",
		Metadata:      map[string]string{},
		cancel:        cancel,
		heartbeatDone: startFlueTaskSessionHeartbeat(hbCtx, controlStore, "WS", "task-session-race", time.Millisecond),
	}
	select {
	case <-staleSessions.read:
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not capture the running task session")
	}

	finishDone := make(chan error, 1)
	go func() {
		finishDone <- (HostBridgeTaskExecutor{Store: controlStore}).finishFlueTaskSession(
			context.Background(),
			TaskExecRequest{WorkspaceKey: "WS"},
			session,
			TaskExecResult{Status: domain.TaskRunCompleted},
			nil,
			nil,
		)
	}()
	select {
	case err := <-finishDone:
		t.Fatalf("finish returned before heartbeat drain: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	close(staleSessions.release)
	select {
	case err := <-finishDone:
		if err != nil {
			t.Fatalf("finish task session: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("finish did not resume after heartbeat drain")
	}
	final, err := base.AgentSessions().Get(ctx, "WS", "task-session-race")
	if err != nil {
		t.Fatalf("get finalized task session: %v", err)
	}
	if final.Status != domain.AgentSessionCompleted || final.FinishedAt == nil {
		t.Fatalf("final session = status %q finished_at %v, want completed terminal record", final.Status, final.FinishedAt)
	}
}

func TestFinishFlueTaskSessionFinalizesAfterCallerCancellation(t *testing.T) {
	base := memstore.New()
	ctx := context.Background()
	if _, err := base.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "ws"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := base.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "task-session-canceled",
		AgentID:      "task-agent",
		Kind:         domain.AgentSessionKindTask,
		Status:       domain.AgentSessionRunning,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	err := (HostBridgeTaskExecutor{Store: base}).finishFlueTaskSession(
		canceledCtx,
		TaskExecRequest{WorkspaceKey: "WS"},
		&flueTaskSession{SessionID: "task-session-canceled", Metadata: map[string]string{}},
		TaskExecResult{Status: domain.TaskRunCancelled},
		nil,
		context.Canceled,
	)
	if err != nil {
		t.Fatalf("finish task session with canceled caller: %v", err)
	}
	final, err := base.AgentSessions().Get(ctx, "WS", "task-session-canceled")
	if err != nil {
		t.Fatalf("get finalized task session: %v", err)
	}
	if final.Status != domain.AgentSessionFailed || final.FinishedAt == nil {
		t.Fatalf("final session = status %q finished_at %v, want failed terminal record", final.Status, final.FinishedAt)
	}
}

func TestFinishFlueTaskSessionPreservesHostOwnedIdentityMetadata(t *testing.T) {
	ctx := t.Context()
	st := memstore.New()
	req := TaskExecRequest{
		WorkspaceKey: "WS",
		TaskID:       "TASK-1",
		TaskRunID:    "task-run-1",
		DriverRunID:  "driver-run-1",
		Runner:       "prompt-agent",
		RunnerKind:   RunnerKindFlueWorkflow,
	}
	sessionID := flueTaskSessionID(req)
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    sessionID,
		AgentID:      "task-agent",
		Kind:         domain.AgentSessionKindTask,
		TaskID:       req.TaskID,
		Status:       domain.AgentSessionRunning,
		Metadata:     flueTaskSessionMetadata(req, sessionID),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	err := (HostBridgeTaskExecutor{Store: st}).finishFlueTaskSession(
		ctx,
		req,
		&flueTaskSession{SessionID: sessionID, Metadata: flueTaskSessionMetadata(req, sessionID)},
		TaskExecResult{
			Status: domain.TaskRunCompleted,
			RuntimeMetadata: map[string]string{
				"task_id":       "TASK-OTHER",
				"task_run_id":   "task-run-other",
				"driver_run_id": "driver-run-other",
				"runner":        "forged-runner",
				"custom":        "retained",
			},
		},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("finish session: %v", err)
	}
	final, err := st.AgentSessions().Get(ctx, "WS", sessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if final.Metadata["task_id"] != req.TaskID ||
		final.Metadata["task_run_id"] != req.TaskRunID ||
		final.Metadata["driver_run_id"] != req.DriverRunID ||
		final.Metadata["runner"] != req.Runner {
		t.Fatalf("host-owned metadata was overwritten: %+v", final.Metadata)
	}
	if final.Metadata["custom"] != "retained" {
		t.Fatalf("ordinary runtime metadata = %+v, want custom field retained", final.Metadata)
	}
}

type taskHeartbeatTestStore struct {
	store.Store
	sessions store.AgentSessionStore
}

func (s *taskHeartbeatTestStore) AgentSessions() store.AgentSessionStore { return s.sessions }

type staleTaskHeartbeatAgentSessionStore struct {
	store.AgentSessionStore
	read    chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *staleTaskHeartbeatAgentSessionStore) Heartbeat(
	ctx context.Context,
	workspaceKey string,
	sessionID string,
) (*domain.AgentSession, error) {
	captured, err := s.AgentSessionStore.Get(ctx, workspaceKey, sessionID)
	if err != nil {
		return nil, err
	}
	s.once.Do(func() { close(s.read) })
	// Deliberately ignore cancellation after the read. This models a Redis
	// transaction that has already begun and can still commit its stale record.
	<-s.release
	now := time.Now().UTC()
	return s.AgentSessionStore.Update(context.Background(), workspaceKey, sessionID, store.AgentSessionUpdate{
		Status:        &captured.Status,
		LastHeartbeat: &now,
	})
}
