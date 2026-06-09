package driver

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestParseDriverRunPayload(t *testing.T) {
	payload, err := parseDriverRunPayload([]string{"provider=flue-daytona", "note=hello=world"}, "TEST-1")
	if err != nil {
		t.Fatalf("parseDriverRunPayload: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("payload JSON: %v", err)
	}
	if got["provider"] != "flue-daytona" || got["note"] != "hello=world" || got["epicId"] != "TEST-1" {
		t.Fatalf("payload = %+v, want parsed key-values", got)
	}
}

func TestParseDriverRunPayloadRejectsMissingKey(t *testing.T) {
	if _, err := parseDriverRunPayload([]string{"=value"}, ""); err == nil {
		t.Fatal("parseDriverRunPayload accepted empty key")
	}
	if _, err := parseDriverRunPayload([]string{"missing-equals"}, ""); err == nil {
		t.Fatal("parseDriverRunPayload accepted missing equals")
	}
}

func TestDriverCommandContainsSubcommands(t *testing.T) {
	if driverCmd.Commands() == nil {
		t.Fatal("driver command has no subcommands")
	}
	for _, name := range []string{"register", "run", "exec-task", "work-task-run", "claim-ready", "complete-task", "release-task", "recover-stale-tasks"} {
		found := false
		for _, cmd := range driverCmd.Commands() {
			if cmd.Name() == name {
				found = true
				if (name == "exec-task" || name == "work-task-run" || name == "claim-ready" || name == "complete-task" || name == "release-task" || name == "recover-stale-tasks") && !cmd.Hidden {
					t.Fatalf("%s command should stay hidden", name)
				}
				break
			}
		}
		if !found {
			t.Fatalf("driver command missing %q subcommand", name)
		}
	}
	if driverCompleteTaskCmd.Flags().Lookup("legacy-task-close") == nil {
		t.Fatal("complete-task command missing legacy-task-close flag")
	}
	if driverExecTaskCmd.Flags().Lookup("defer-completion") == nil {
		t.Fatal("exec-task command missing defer-completion flag")
	}
}

func TestCompleteDriverTaskRunUsesChildLeaseCredentials(t *testing.T) {
	taskRuns := &fakeDriverTaskRunStore{run: &domain.TaskRun{
		WorkspaceKey: "WS",
		TaskRunID:    "task-run-1",
		TaskID:       "task-1",
		Status:       domain.TaskRunRunning,
		NodeID:       "child-node",
		LeaseID:      "child-lease",
		FencingToken: 42,
		ArtifactsRef: "",
		ErrorClass:   "",
		ErrorMessage: "",
	}}

	result, err := completeDriverTaskRun(context.Background(), taskRuns, "WS", "task-run-1", driverTaskRunCompletionOptions{
		TaskID:       "task-1",
		CompletionID: "completion-1",
		LeaseToken:   "task-run-token",
		ArtifactIDs:  []string{"artifact-1"},
		LogsRef:      "logs://task-run-1",
		ArtifactsRef: "artifacts://task-run-1",
		Reason:       "done",
	})
	if err != nil {
		t.Fatalf("completeDriverTaskRun: %v", err)
	}
	if result.ID != "task-1" || result.Status != string(domain.TaskRunCompleted) || result.Reason != "done" {
		t.Fatalf("result = %+v, want completed task-1", result)
	}
	if taskRuns.completeCalls != 1 || taskRuns.completedWorkspace != "WS" || taskRuns.completedTaskRunID != "task-run-1" {
		t.Fatalf("complete call = %d %q/%q, want one call for WS/task-run-1", taskRuns.completeCalls, taskRuns.completedWorkspace, taskRuns.completedTaskRunID)
	}
	got := taskRuns.complete
	if got.NodeID != "child-node" || got.LeaseID != "child-lease" || got.FencingToken != 42 || got.LeaseToken != "task-run-token" {
		t.Fatalf("complete owner credentials = node:%q lease:%q fence:%d token:%q, want child credentials/token", got.NodeID, got.LeaseID, got.FencingToken, got.LeaseToken)
	}
	if got.CompletionID != "completion-1" || !got.CloseTask || !got.RequireArtifacts || got.LogsRef != "logs://task-run-1" || got.ArtifactsRef != "artifacts://task-run-1" {
		t.Fatalf("complete payload = %+v, want completion/close/artifact refs", got)
	}
}

func TestCompleteDriverTaskRunRejectsTaskIDMismatch(t *testing.T) {
	taskRuns := &fakeDriverTaskRunStore{run: &domain.TaskRun{
		WorkspaceKey: "WS",
		TaskRunID:    "task-run-1",
		TaskID:       "task-actual",
		Status:       domain.TaskRunRunning,
		NodeID:       "child-node",
		LeaseID:      "child-lease",
	}}

	_, err := completeDriverTaskRun(context.Background(), taskRuns, "WS", "task-run-1", driverTaskRunCompletionOptions{TaskID: "task-requested"})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("completeDriverTaskRun mismatch err = %v, want ErrInvalid", err)
	}
	if taskRuns.completeCalls != 0 {
		t.Fatalf("complete calls = %d, want none after task mismatch", taskRuns.completeCalls)
	}
}

func TestCompleteDriverTaskRunAllowsTaskRunIDWithoutTaskID(t *testing.T) {
	taskRuns := &fakeDriverTaskRunStore{run: &domain.TaskRun{
		WorkspaceKey: "WS",
		TaskRunID:    "task-run-1",
		TaskID:       "task-1",
		Status:       domain.TaskRunRunning,
		NodeID:       "child-node",
		LeaseID:      "child-lease",
	}}

	result, err := completeDriverTaskRun(context.Background(), taskRuns, "WS", "task-run-1", driverTaskRunCompletionOptions{})
	if err != nil {
		t.Fatalf("completeDriverTaskRun: %v", err)
	}
	if result.ID != "task-1" || result.Reason != "completed by driver" {
		t.Fatalf("result = %+v, want task-run completion defaults", result)
	}
	if taskRuns.complete.CompletionID != "complete-task-run-1" || taskRuns.complete.CloseReason != "completed by driver" {
		t.Fatalf("complete defaults = %+v, want default completion id/reason", taskRuns.complete)
	}
}

func TestResolveDriverCompleteTaskLeaseToken(t *testing.T) {
	orig := driverCompleteTaskLeaseToken
	t.Cleanup(func() { driverCompleteTaskLeaseToken = orig })
	t.Setenv("LOOM_TASK_RUN_LEASE_TOKEN", "task-token")
	t.Setenv("LOOM_RUNNER_LEASE_TOKEN", "runner-token")

	driverCompleteTaskLeaseToken = ""
	if got := resolveDriverCompleteTaskLeaseToken(); got != "task-token" {
		t.Fatalf("resolve token from env = %q, want task-token", got)
	}
	driverCompleteTaskLeaseToken = "flag-token"
	if got := resolveDriverCompleteTaskLeaseToken(); got != "flag-token" {
		t.Fatalf("resolve token from flag = %q, want flag-token", got)
	}
}

func TestParseDriverRecoverStaleBefore(t *testing.T) {
	got, err := parseDriverRecoverStaleBefore("2026-06-06T12:34:56Z")
	if err != nil {
		t.Fatalf("parse stale before: %v", err)
	}
	want := time.Date(2026, 6, 6, 12, 34, 56, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("stale before = %s, want %s", got, want)
	}
	zero, err := parseDriverRecoverStaleBefore(" ")
	if err != nil {
		t.Fatalf("parse empty stale before: %v", err)
	}
	if !zero.IsZero() {
		t.Fatalf("empty stale before = %s, want zero", zero)
	}
	if _, err := parseDriverRecoverStaleBefore("not-a-time"); err == nil {
		t.Fatal("parse invalid stale before succeeded")
	}
}

type fakeDriverTaskRunStore struct {
	run                *domain.TaskRun
	completedWorkspace string
	completedTaskRunID string
	complete           store.TaskRunComplete
	completeCalls      int
}

func (s *fakeDriverTaskRunStore) Create(context.Context, store.TaskRunCreate) (*domain.TaskRun, error) {
	panic("unexpected Create")
}

func (s *fakeDriverTaskRunStore) ClaimQueued(context.Context, string, store.TaskRunClaim) (*domain.TaskRun, error) {
	panic("unexpected ClaimQueued")
}

func (s *fakeDriverTaskRunStore) Get(_ context.Context, workspaceKey, taskRunID string) (*domain.TaskRun, error) {
	if s.run == nil || s.run.WorkspaceKey != workspaceKey || s.run.TaskRunID != taskRunID {
		return nil, domain.ErrNotFound
	}
	run := *s.run
	return &run, nil
}

func (s *fakeDriverTaskRunStore) List(context.Context, string, store.TaskRunFilter) ([]*domain.TaskRun, error) {
	panic("unexpected List")
}

func (s *fakeDriverTaskRunStore) Heartbeat(context.Context, string, string, store.TaskRunHeartbeat) (*domain.TaskRun, error) {
	panic("unexpected Heartbeat")
}

func (s *fakeDriverTaskRunStore) Finish(context.Context, string, string, store.TaskRunFinish) (*domain.TaskRun, error) {
	panic("unexpected Finish")
}

func (s *fakeDriverTaskRunStore) Complete(_ context.Context, workspaceKey, taskRunID string, complete store.TaskRunComplete) (*domain.TaskRun, error) {
	s.completeCalls++
	s.completedWorkspace = workspaceKey
	s.completedTaskRunID = taskRunID
	s.complete = complete
	run := *s.run
	run.Status = complete.Status
	run.LogsRef = complete.LogsRef
	run.ArtifactsRef = complete.ArtifactsRef
	run.ErrorClass = complete.ErrorClass
	run.ErrorMessage = complete.ErrorMessage
	return &run, nil
}

func (s *fakeDriverTaskRunStore) AppendLog(context.Context, string, string, store.TaskRunLogAppend) (*domain.TaskRunLogEntry, error) {
	panic("unexpected AppendLog")
}

func (s *fakeDriverTaskRunStore) ListLogs(context.Context, string, string, store.TaskRunLogFilter) ([]*domain.TaskRunLogEntry, error) {
	panic("unexpected ListLogs")
}
