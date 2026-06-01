package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
)

func taskWorkflowTestCmd(out *bytes.Buffer) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(out)
	return cmd
}

func resetTaskWorkflowGlobals(t *testing.T) {
	t.Helper()
	taskOutput = "text"
	taskSubmitDesign = ""
	taskSubmitAssignee = ""
	taskBlockReason = ""
	taskCloseReason = "completed"
	taskCloseSession = ""
	taskCloseForce = false
	taskReopenReason = ""
	taskDeferUntil = ""
	taskListStatus = ""
	taskListType = ""
	taskListParent = ""
	taskListPriority = 0
	taskListLimit = 0
	resetDefaultIssueBackend()
	t.Cleanup(resetDefaultIssueBackend)
}

func TestTaskCommandSurfaceUsesRunSubcommand(t *testing.T) {
	if taskCmd.Use != "task" {
		t.Fatalf("task command Use = %q, want task namespace", taskCmd.Use)
	}
	if taskRunCmd.Use != "run [worktree|workspace]" {
		t.Fatalf("task run Use = %q", taskRunCmd.Use)
	}
	if taskRunCmd.Run == nil {
		t.Fatal("task run must execute the implementation-agent runner")
	}
	if err := taskCmd.Args(taskCmd, []string{"falcon"}); err == nil {
		t.Fatal("bare loom task <worktree> must be rejected")
	}
}

func TestRunTaskClaimDurableOnlyWhenRuntimeUnsafe(t *testing.T) {
	resetTaskWorkflowGlobals(t)
	tmp := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	mock := NewMockIssueBackend()
	setDefaultIssueBackend(mock)

	var out bytes.Buffer
	if err := runTaskClaim(taskWorkflowTestCmd(&out), []string{"loom-123"}); err != nil {
		t.Fatalf("runTaskClaim: %v", err)
	}

	if got := mock.CallCount("ClaimIssue"); got != 1 {
		t.Fatalf("ClaimIssue calls = %d, want 1", got)
	}
	if got := mock.CallCount("Get"); got != 0 {
		t.Fatalf("Get calls = %d, want 0 when local runtime is unsafe", got)
	}
	if _, err := os.Stat(filepath.Join(tmp, LockFileName)); !os.IsNotExist(err) {
		t.Fatalf("local lock should not be created, stat err=%v", err)
	}
}

func TestRunTaskClaimBindsLocalLockWhenRuntimeMatches(t *testing.T) {
	resetTaskWorkflowGlobals(t)
	tmp := t.TempDir()
	tmp, _ = filepath.EvalSymlinks(tmp)
	t.Setenv("LOOM_WORKTREE_PATH", tmp)
	t.Setenv("LOOM_AGENT_NAME", "nova")
	t.Setenv("LOOM_EVENTS_DIR", filepath.Join(tmp, "events"))
	origDir, _ := os.Getwd()
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	lockData, err := json.Marshal(LockInfo{
		PID:       os.Getpid(),
		Command:   "task",
		AgentName: "nova",
		StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, LockFileName), lockData, 0o600); err != nil {
		t.Fatal(err)
	}

	mock := NewMockIssueBackend()
	mock.GetResult = &backend.IssueDetailData{IssueData: backend.IssueData{ID: "loom-123", Title: "Test task"}}
	setDefaultIssueBackend(mock)

	var out bytes.Buffer
	if err := runTaskClaim(taskWorkflowTestCmd(&out), []string{"loom-123"}); err != nil {
		t.Fatalf("runTaskClaim: %v", err)
	}

	info, err := cli.ReadLockFile(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if info.TaskID != "loom-123" {
		t.Fatalf("TaskID = %q, want loom-123", info.TaskID)
	}
	if info.TaskTitle != "Test task" {
		t.Fatalf("TaskTitle = %q, want Test task", info.TaskTitle)
	}
	if got := mock.CallCount("ClaimIssue"); got != 1 {
		t.Fatalf("ClaimIssue calls = %d, want 1", got)
	}
	if got := mock.CallCount("Get"); got != 1 {
		t.Fatalf("Get calls = %d, want 1 for local title binding", got)
	}
}

func TestRunTaskClaimSkipsLocalBindingOnMismatchedLock(t *testing.T) {
	resetTaskWorkflowGlobals(t)
	tmp := t.TempDir()
	tmp, _ = filepath.EvalSymlinks(tmp)
	t.Setenv("LOOM_WORKTREE_PATH", tmp)
	t.Setenv("LOOM_AGENT_NAME", "nova")
	origDir, _ := os.Getwd()
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	lockData, err := json.Marshal(LockInfo{
		PID:       os.Getpid(),
		Command:   "task",
		AgentName: "other-agent",
		StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, LockFileName), lockData, 0o600); err != nil {
		t.Fatal(err)
	}

	mock := NewMockIssueBackend()
	setDefaultIssueBackend(mock)

	var out bytes.Buffer
	if err := runTaskClaim(taskWorkflowTestCmd(&out), []string{"loom-123"}); err != nil {
		t.Fatalf("runTaskClaim: %v", err)
	}

	info, err := cli.ReadLockFile(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if info.TaskID != "" {
		t.Fatalf("TaskID = %q, want no local binding", info.TaskID)
	}
	if got := mock.CallCount("Get"); got != 0 {
		t.Fatalf("Get calls = %d, want 0 when lock owner mismatches", got)
	}
}

func TestRunTaskClaimTreatsAlreadyClaimedBySameActorAsSuccess(t *testing.T) {
	resetTaskWorkflowGlobals(t)
	t.Setenv("LOOM_AGENT_NAME", "nova")
	mock := NewMockIssueBackend()
	mock.ClaimIssueErr = backend.ErrConflict("ClaimIssue", "already claimed")
	mock.GetResult = &backend.IssueDetailData{IssueData: backend.IssueData{
		ID:       "loom-123",
		Status:   "in_progress",
		Assignee: "nova",
	}}
	setDefaultIssueBackend(mock)

	var out bytes.Buffer
	if err := runTaskClaim(taskWorkflowTestCmd(&out), []string{"loom-123"}); err != nil {
		t.Fatalf("runTaskClaim should tolerate same-actor conflict: %v", err)
	}
	if got := mock.CallCount("ClaimIssue"); got != 1 {
		t.Fatalf("ClaimIssue calls = %d, want 1", got)
	}
	if got := mock.CallCount("Get"); got != 1 {
		t.Fatalf("Get calls = %d, want 1 to verify same actor", got)
	}
}

func TestRunTaskSubmitSetsReviewClearsAssigneeAndOptionalDesign(t *testing.T) {
	resetTaskWorkflowGlobals(t)
	var captured backend.UpdateParams
	mock := NewMockIssueBackend()
	mock.UpdateFn = func(ctx context.Context, id string, params backend.UpdateParams) error {
		if id != "loom-123" {
			t.Fatalf("id = %q, want loom-123", id)
		}
		captured = params
		return nil
	}
	setDefaultIssueBackend(mock)

	var out bytes.Buffer
	cmd := taskWorkflowTestCmd(&out)
	cmd.Flags().StringVar(&taskSubmitDesign, "design", "", "")
	cmd.Flags().StringVar(&taskSubmitAssignee, "assignee", "", "")
	if err := cmd.Flags().Set("design", "implementation plan"); err != nil {
		t.Fatal(err)
	}
	if err := runTaskSubmit(cmd, []string{"loom-123"}); err != nil {
		t.Fatalf("runTaskSubmit: %v", err)
	}

	if captured.Status == nil || *captured.Status != "review" {
		t.Fatalf("Status = %v, want review", captured.Status)
	}
	if captured.Assignee == nil || *captured.Assignee != "" {
		t.Fatalf("Assignee = %v, want explicit clear", captured.Assignee)
	}
	if captured.Design == nil || *captured.Design != "implementation plan" {
		t.Fatalf("Design = %v, want implementation plan", captured.Design)
	}
}

func TestRunTaskBlockReleaseCloseTransitions(t *testing.T) {
	resetTaskWorkflowGlobals(t)
	mock := NewMockIssueBackend()
	setDefaultIssueBackend(mock)

	var updates []backend.UpdateParams
	mock.UpdateFn = func(ctx context.Context, id string, params backend.UpdateParams) error {
		if id != "loom-123" {
			t.Fatalf("id = %q, want loom-123", id)
		}
		updates = append(updates, params)
		return nil
	}
	var closeParams backend.CloseParams
	mock.CloseFn = func(ctx context.Context, id string, params backend.CloseParams) (*backend.CloseResult, error) {
		if id != "loom-123" {
			t.Fatalf("close id = %q, want loom-123", id)
		}
		closeParams = params
		return &backend.CloseResult{}, nil
	}

	var out bytes.Buffer
	taskBlockReason = "waiting on upstream API"
	var blockComment backend.CommentAddParams
	mock.AddCommentFn = func(ctx context.Context, params backend.CommentAddParams) (*backend.CommentData, error) {
		blockComment = params
		return &backend.CommentData{IssueID: params.IssueID, Text: params.Text}, nil
	}
	if err := runTaskBlock(taskWorkflowTestCmd(&out), []string{"loom-123"}); err != nil {
		t.Fatalf("runTaskBlock: %v", err)
	}
	if err := runTaskRelease(taskWorkflowTestCmd(&out), []string{"loom-123"}); err != nil {
		t.Fatalf("runTaskRelease: %v", err)
	}
	taskCloseReason = "Completed with tests"
	if err := runTaskClose(taskWorkflowTestCmd(&out), []string{"loom-123"}); err != nil {
		t.Fatalf("runTaskClose: %v", err)
	}

	if len(updates) != 2 {
		t.Fatalf("Update calls = %d, want 2", len(updates))
	}
	if updates[0].Status == nil || *updates[0].Status != "blocked" {
		t.Fatalf("block Status = %v, want blocked", updates[0].Status)
	}
	if updates[0].Notes == nil || *updates[0].Notes != "BLOCKED: waiting on upstream API" {
		t.Fatalf("block Notes = %v", updates[0].Notes)
	}
	if updates[0].Assignee != nil {
		t.Fatalf("block Assignee = %v, want nil", updates[0].Assignee)
	}
	if blockComment.IssueID != "loom-123" || blockComment.Text != "BLOCKED: waiting on upstream API" {
		t.Fatalf("block comment = %+v", blockComment)
	}
	if updates[1].Status == nil || *updates[1].Status != "open" {
		t.Fatalf("release Status = %v, want open", updates[1].Status)
	}
	if updates[1].Assignee == nil || *updates[1].Assignee != "" {
		t.Fatalf("release Assignee = %v, want explicit clear", updates[1].Assignee)
	}
	if closeParams.Reason != "Completed with tests" {
		t.Fatalf("close reason = %q", closeParams.Reason)
	}
}

func TestRunTaskReadAndSchedulingCommands(t *testing.T) {
	resetTaskWorkflowGlobals(t)
	mock := NewMockIssueBackend()
	setDefaultIssueBackend(mock)

	mock.GetResult = &backend.IssueDetailData{IssueData: backend.IssueData{
		ID:       "loom-123",
		Title:    "Review CLI layer",
		Status:   "review",
		Priority: 1,
	}}
	mock.ListResult = []backend.IssueData{{
		ID:       "loom-123",
		Title:    "Review CLI layer",
		Status:   "review",
		Priority: 1,
	}}

	var reopenParams backend.ReopenParams
	mock.ReopenFn = func(ctx context.Context, id string, params backend.ReopenParams) error {
		if id != "loom-123" {
			t.Fatalf("reopen id = %q, want loom-123", id)
		}
		reopenParams = params
		return nil
	}
	var deferUntil time.Time
	mock.DeferIssueFn = func(ctx context.Context, id string, until time.Time) error {
		if id != "loom-123" {
			t.Fatalf("defer id = %q, want loom-123", id)
		}
		deferUntil = until
		return nil
	}
	var undeferID string
	mock.UndeferIssueFn = func(ctx context.Context, id string) error {
		undeferID = id
		return nil
	}
	var listOpts backend.ListOpts
	mock.ListFn = func(ctx context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
		listOpts = opts
		return mock.ListResult, nil
	}

	var out bytes.Buffer
	taskReopenReason = "approved after rework"
	if err := runTaskReopen(taskWorkflowTestCmd(&out), []string{"loom-123"}); err != nil {
		t.Fatalf("runTaskReopen: %v", err)
	}
	taskDeferUntil = "2026-06-01T00:00:00Z"
	if err := runTaskDefer(taskWorkflowTestCmd(&out), []string{"loom-123"}); err != nil {
		t.Fatalf("runTaskDefer: %v", err)
	}
	if err := runTaskUndefer(taskWorkflowTestCmd(&out), []string{"loom-123"}); err != nil {
		t.Fatalf("runTaskUndefer: %v", err)
	}
	taskOutput = "json"
	if err := runTaskShow(taskWorkflowTestCmd(&out), []string{"loom-123"}); err != nil {
		t.Fatalf("runTaskShow: %v", err)
	}
	taskListStatus = "review"
	taskListType = "task"
	taskListParent = "epic-1"
	taskListLimit = 25
	if err := runTaskList(taskWorkflowTestCmd(&out), nil); err != nil {
		t.Fatalf("runTaskList: %v", err)
	}

	if reopenParams.Reason != "approved after rework" {
		t.Fatalf("reopen reason = %q", reopenParams.Reason)
	}
	if deferUntil.Format(time.RFC3339) != "2026-06-01T00:00:00Z" {
		t.Fatalf("defer until = %s", deferUntil.Format(time.RFC3339))
	}
	if undeferID != "loom-123" {
		t.Fatalf("undefer id = %q", undeferID)
	}
	if listOpts.Status != "review" || listOpts.IssueType != "task" || listOpts.ParentID != "epic-1" || listOpts.Limit != 25 {
		t.Fatalf("list opts = %+v", listOpts)
	}
}
