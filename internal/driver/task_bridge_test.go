package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

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
		Store:        st,
		WorktreePath: repo.dir,
		Command:      hostBridgeHelperCommand(t, "success", base, patch),
	}
	result, err := executor.ExecuteTask(ctx, hostBridgeTaskExecRequest())
	if err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}
	if result.Status != "" || result.ExitCode != 0 {
		t.Fatalf("result status/exit = %q/%d, want implicit completed and zero exit", result.Status, result.ExitCode)
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

func TestHostBridgeTaskExecutorPreservesFinalizedPatchArtifactOnConflict(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	repo := newPatchBackRepo(t)
	base := repo.commitFile("file.txt", "old\n", "base")
	repo.write("file.txt", "local edit\n")
	patch := "diff --git a/file.txt b/file.txt\n--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+new\n"

	executor := HostBridgeTaskExecutor{
		Store:        st,
		WorktreePath: repo.dir,
		Command:      hostBridgeHelperCommand(t, "success", base, patch),
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
		Store:        st,
		WorktreePath: repo.dir,
		Command:      hostBridgeHelperCommand(t, "success", base, patch),
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
		Store:        st,
		WorktreePath: t.TempDir(),
		Command:      hostBridgeHelperCommand(t, "artifact", "unused-base", "unused-patch"),
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
		Store:        st,
		WorktreePath: t.TempDir(),
		Command:      hostBridgeHelperCommand(t, "flue-transcript", "unused-base", "unused-patch"),
	}

	outcome, err := RequestTaskRunWithResult(ctx, st, TaskRunRequestOptions{
		WorkspaceKey:    "TEST",
		DriverRunID:     run.RunID,
		TaskRunID:       "task-run-1",
		TaskID:          "TEST-1",
		ProviderProfile: "flue-daytona",
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
	case "artifact":
		result := map[string]any{
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
			"transcript_entries": []map[string]any{{
				"seq":       1,
				"timestamp": "2026-06-09T12:00:00Z",
				"role":      "assistant",
				"type":      "text",
				"text":      "implemented task",
			}},
			"runtime_metadata": map[string]string{
				"helper":       "host_bridge",
				"flue_harness": "task-agent",
			},
		}
		if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
			t.Fatalf("encode result: %v", err)
		}
	default:
		t.Fatalf("unknown helper mode %q", mode)
	}
	os.Exit(0)
}

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

func TestLastJSONLine(t *testing.T) {
	line, err := lastJSONLine([]byte("log line\n{\"ok\":true}\n"))
	if err != nil {
		t.Fatalf("lastJSONLine: %v", err)
	}
	if !bytes.Equal(line, []byte(`{"ok":true}`)) {
		t.Fatalf("line = %s, want JSON object", line)
	}
}
