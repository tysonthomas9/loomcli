package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

var (
	taskSubmitDesign   string
	taskSubmitAssignee string
	taskBlockReason    string
	taskCloseReason    string
	taskCloseSession   string
	taskCloseForce     bool
	taskReopenReason   string
	taskDeferUntil     string
	taskListStatus     string
	taskListType       string
	taskListParent     string
	taskListPriority   int
	taskListLimit      int
)

var taskClaimCmd = &cobra.Command{
	Use:   "claim <issue-id>",
	Short: "Claim a task and bind the local agent lock when safe",
	Args:  cobra.ExactArgs(1),
	RunE:  runTaskClaim,
}

var taskSubmitCmd = &cobra.Command{
	Use:   "submit <issue-id>",
	Short: "Submit a task for review",
	Args:  cobra.ExactArgs(1),
	RunE:  runTaskSubmit,
}

var taskBlockCmd = &cobra.Command{
	Use:   "block <issue-id> --reason <text>",
	Short: "Mark a task blocked",
	Args:  cobra.ExactArgs(1),
	RunE:  runTaskBlock,
}

var taskReleaseCmd = &cobra.Command{
	Use:   "release <issue-id>",
	Short: "Release active work back to the open queue",
	Args:  cobra.ExactArgs(1),
	RunE:  runTaskRelease,
}

var taskCloseCmd = &cobra.Command{
	Use:   "close <issue-id>",
	Short: "Close a completed task",
	Args:  cobra.ExactArgs(1),
	RunE:  runTaskClose,
}

var taskReopenCmd = &cobra.Command{
	Use:   "reopen <issue-id>",
	Short: "Reopen a closed task",
	Args:  cobra.ExactArgs(1),
	RunE:  runTaskReopen,
}

var taskDeferCmd = &cobra.Command{
	Use:   "defer <issue-id> --until <time>",
	Short: "Defer a task until a future time",
	Args:  cobra.ExactArgs(1),
	RunE:  runTaskDefer,
}

var taskUndeferCmd = &cobra.Command{
	Use:   "undefer <issue-id>",
	Short: "Resume deferred work",
	Args:  cobra.ExactArgs(1),
	RunE:  runTaskUndefer,
}

var taskShowCmd = &cobra.Command{
	Use:   "show <issue-id>",
	Short: "Show a task",
	Args:  cobra.ExactArgs(1),
	RunE:  runTaskShow,
}

var taskListCmd = &cobra.Command{
	Use:   "list",
	Short: "List tasks",
	Args:  cobra.NoArgs,
	RunE:  runTaskList,
}

func init() {
	taskSubmitCmd.Flags().StringVar(&taskSubmitDesign, "design", "", "Set design text while submitting")
	taskSubmitCmd.Flags().StringVar(&taskSubmitAssignee, "assignee", "", "Set assignee instead of clearing it")

	taskBlockCmd.Flags().StringVar(&taskBlockReason, "reason", "", "Reason the task is blocked")
	_ = taskBlockCmd.MarkFlagRequired("reason")

	taskCloseCmd.Flags().StringVar(&taskCloseReason, "reason", "completed", "Reason for closing")
	taskCloseCmd.Flags().StringVar(&taskCloseSession, "session", "", "Session identifier to attach to the close event")
	taskCloseCmd.Flags().BoolVar(&taskCloseForce, "force", false, "Force close even if blocked or dependencies open")

	taskReopenCmd.Flags().StringVar(&taskReopenReason, "reason", "", "Reason for reopening")

	taskDeferCmd.Flags().StringVar(&taskDeferUntil, "until", "", "RFC3339 timestamp or YYYY-MM-DD date")
	_ = taskDeferCmd.MarkFlagRequired("until")

	taskListCmd.Flags().StringVar(&taskListStatus, "status", "", "Filter by status")
	taskListCmd.Flags().StringVar(&taskListType, "type", "", "Filter by issue type")
	taskListCmd.Flags().StringVar(&taskListParent, "parent", "", "Filter by parent issue ID")
	taskListCmd.Flags().IntVar(&taskListPriority, "priority", 0, "Filter by priority")
	taskListCmd.Flags().IntVar(&taskListLimit, "limit", 0, "Maximum number of results")
}

func runTaskClaim(cmd *cobra.Command, args []string) error {
	if err := validateTaskOutput(); err != nil {
		return err
	}
	ctx := cmd.Context()
	id := args[0]
	ib := cli.DefaultIssueBackend()
	runtimeCtx := detectTaskAgentRuntime()

	if err := claimTask(ctx, ib, id); err != nil {
		return err
	}

	var title string
	if runtimeCtx != nil {
		title = fetchTaskTitle(ctx, ib, id)
		if err := bindLocalTask(runtimeCtx, id, title); err != nil {
			return err
		}
		emitTaskClaimedEvent(id, title)
	}

	return printTaskMessage(cmd.OutOrStdout(), "claimed "+id, taskOutput)
}

func runTaskSubmit(cmd *cobra.Command, args []string) error {
	if err := validateTaskOutput(); err != nil {
		return err
	}
	status := "review"
	assignee := ""
	params := backend.UpdateParams{
		Status:   &status,
		Assignee: &assignee,
	}
	if cmd.Flags().Changed("design") {
		params.Design = &taskSubmitDesign
	}
	if cmd.Flags().Changed("assignee") {
		params.Assignee = &taskSubmitAssignee
	}
	if err := cli.DefaultIssueBackend().Update(cmd.Context(), args[0], params); err != nil {
		return err
	}
	return printTaskMessage(cmd.OutOrStdout(), "submitted "+args[0]+" for review", taskOutput)
}

func runTaskBlock(cmd *cobra.Command, args []string) error {
	if err := validateTaskOutput(); err != nil {
		return err
	}
	id := args[0]
	status := "blocked"
	notes := taskBlockReason
	if !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(notes)), "BLOCKED:") {
		notes = "BLOCKED: " + notes
	}
	params := backend.UpdateParams{
		Status: &status,
		Notes:  &notes,
	}
	ib := cli.DefaultIssueBackend()
	if err := ib.Update(cmd.Context(), id, params); err != nil {
		return err
	}
	if _, err := ib.AddComment(cmd.Context(), backend.CommentAddParams{IssueID: id, Text: notes}); err != nil {
		return err
	}
	return printTaskMessage(cmd.OutOrStdout(), "blocked "+id, taskOutput)
}

func runTaskRelease(cmd *cobra.Command, args []string) error {
	if err := validateTaskOutput(); err != nil {
		return err
	}
	status := "open"
	assignee := ""
	params := backend.UpdateParams{
		Status:   &status,
		Assignee: &assignee,
	}
	if err := cli.DefaultIssueBackend().Update(cmd.Context(), args[0], params); err != nil {
		return err
	}
	return printTaskMessage(cmd.OutOrStdout(), "released "+args[0], taskOutput)
}

func runTaskClose(cmd *cobra.Command, args []string) error {
	if err := validateTaskOutput(); err != nil {
		return err
	}
	params := backend.CloseParams{
		Reason:  taskCloseReason,
		Session: taskCloseSession,
		Force:   taskCloseForce,
	}
	if _, err := cli.DefaultIssueBackend().Close(cmd.Context(), args[0], params); err != nil {
		return err
	}
	return printTaskMessage(cmd.OutOrStdout(), "closed "+args[0], taskOutput)
}

func runTaskReopen(cmd *cobra.Command, args []string) error {
	if err := validateTaskOutput(); err != nil {
		return err
	}
	params := backend.ReopenParams{Reason: taskReopenReason}
	if err := cli.DefaultIssueBackend().Reopen(cmd.Context(), args[0], params); err != nil {
		return err
	}
	return printTaskMessage(cmd.OutOrStdout(), "reopened "+args[0], taskOutput)
}

func runTaskDefer(cmd *cobra.Command, args []string) error {
	if err := validateTaskOutput(); err != nil {
		return err
	}
	until, err := parseTaskUntil(taskDeferUntil)
	if err != nil {
		return err
	}
	if err := cli.DefaultIssueBackend().DeferIssue(cmd.Context(), args[0], until); err != nil {
		return err
	}
	return printTaskMessage(cmd.OutOrStdout(), "deferred "+args[0], taskOutput)
}

func runTaskUndefer(cmd *cobra.Command, args []string) error {
	if err := validateTaskOutput(); err != nil {
		return err
	}
	if err := cli.DefaultIssueBackend().UndeferIssue(cmd.Context(), args[0]); err != nil {
		return err
	}
	return printTaskMessage(cmd.OutOrStdout(), "undeferred "+args[0], taskOutput)
}

func runTaskShow(cmd *cobra.Command, args []string) error {
	if err := validateTaskOutput(); err != nil {
		return err
	}
	detail, err := cli.DefaultIssueBackend().Get(cmd.Context(), args[0])
	if err != nil {
		return err
	}
	if detail == nil {
		return backend.ErrNotFound("task show", "issue not found")
	}
	return printTaskDetail(cmd.OutOrStdout(), detail, taskOutput)
}

func runTaskList(cmd *cobra.Command, args []string) error {
	if err := validateTaskOutput(); err != nil {
		return err
	}
	opts := backend.ListOpts{
		Status:    taskListStatus,
		IssueType: taskListType,
		ParentID:  taskListParent,
		Limit:     taskListLimit,
	}
	if cmd.Flags().Changed("priority") {
		p := taskListPriority
		opts.Priority = &p
	}
	items, err := cli.DefaultIssueBackend().List(cmd.Context(), opts)
	if err != nil {
		return err
	}
	return printTaskList(cmd.OutOrStdout(), items, taskOutput)
}

type taskAgentRuntime struct {
	worktreePath string
}

func detectTaskAgentRuntime() *taskAgentRuntime {
	worktreePath := os.Getenv("LOOM_WORKTREE_PATH")
	agentName := os.Getenv("LOOM_AGENT_NAME")
	if worktreePath == "" || agentName == "" {
		return nil
	}
	worktreePath, err := canonicalPath(worktreePath)
	if err != nil {
		return nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	cwd, err = canonicalPath(cwd)
	if err != nil {
		return nil
	}
	if !isPathInside(worktreePath, cwd) {
		return nil
	}
	info, err := cli.ReadLockFile(worktreePath)
	if err != nil || info == nil {
		return nil
	}
	if info.AgentName != agentName {
		return nil
	}
	return &taskAgentRuntime{worktreePath: worktreePath}
}

func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

func isPathInside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func claimTask(ctx context.Context, ib backend.IssueBackend, id string) error {
	err := ib.ClaimIssue(ctx, id, 0)
	if err == nil {
		return nil
	}
	if !backend.IsKind(err, backend.KindConflict) {
		return err
	}
	actor := currentTaskActor()
	if actor == "" {
		return err
	}
	detail, getErr := ib.Get(ctx, id)
	if getErr != nil || detail == nil {
		return err
	}
	if detail.Status == "in_progress" && detail.Assignee == actor {
		return nil
	}
	return err
}

func currentTaskActor() string {
	for _, key := range []string{"LOOM_AGENT_NAME", "LOOM_FLEET_DB_ACTOR", "LOOM_FLEET_ACTOR", "USER"} {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return ""
}

func fetchTaskTitle(ctx context.Context, ib backend.IssueBackend, id string) string {
	detail, err := ib.Get(ctx, id)
	if err != nil || detail == nil {
		return ""
	}
	return detail.Title
}

func bindLocalTask(runtimeCtx *taskAgentRuntime, id, title string) error {
	if err := cli.UpdateLockTask(runtimeCtx.worktreePath, id, title); err != nil {
		return err
	}
	lockDir := cli.ResolveLockDir(runtimeCtx.worktreePath)
	if cp, err := config.LoadCheckpoint(lockDir); err == nil && cp != nil && cp.TaskID != id {
		_ = config.ClearCheckpoint(lockDir)
	}
	return nil
}

func parseTaskUntil(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, fmt.Errorf("--until must not be empty")
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("--until must be RFC3339 or YYYY-MM-DD")
}

func validateTaskOutput() error {
	switch taskOutput {
	case "", "text", "json":
		return nil
	default:
		return fmt.Errorf("unsupported output format %q", taskOutput)
	}
}

func printTaskMessage(w io.Writer, message, format string) error {
	if format == "json" {
		return json.NewEncoder(w).Encode(map[string]string{"message": message})
	}
	_, err := fmt.Fprintln(w, message)
	return err
}

func printTaskDetail(w io.Writer, detail *backend.IssueDetailData, format string) error {
	if format == "json" {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(detail)
	}
	fmt.Fprintf(w, "ID: %s\n", detail.ID)
	fmt.Fprintf(w, "Title: %s\n", detail.Title)
	fmt.Fprintf(w, "Status: %s\n", detail.Status)
	fmt.Fprintf(w, "Priority: %d\n", detail.Priority)
	if detail.IssueType != "" {
		fmt.Fprintf(w, "Type: %s\n", detail.IssueType)
	}
	if detail.Assignee != "" {
		fmt.Fprintf(w, "Assignee: %s\n", detail.Assignee)
	}
	if detail.Parent != "" {
		fmt.Fprintf(w, "Parent: %s\n", detail.Parent)
	}
	if len(detail.Labels) > 0 {
		fmt.Fprintf(w, "Labels: %s\n", strings.Join(detail.Labels, ", "))
	}
	if detail.Description != "" {
		fmt.Fprintf(w, "\nDescription:\n%s\n", detail.Description)
	}
	if detail.Design != "" {
		fmt.Fprintf(w, "\nDesign:\n%s\n", detail.Design)
	}
	if detail.Notes != "" {
		fmt.Fprintf(w, "\nNotes:\n%s\n", detail.Notes)
	}
	return nil
}

func printTaskList(w io.Writer, items []backend.IssueData, format string) error {
	if format == "json" {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(items)
	}
	if len(items) == 0 {
		_, err := fmt.Fprintln(w, "No tasks found.")
		return err
	}
	for _, item := range items {
		if item.IssueType != "" {
			fmt.Fprintf(w, "%s\t%s\tP%d\t%s\t%s\n", item.ID, item.Status, item.Priority, item.IssueType, item.Title)
		} else {
			fmt.Fprintf(w, "%s\t%s\tP%d\t%s\n", item.ID, item.Status, item.Priority, item.Title)
		}
	}
	return nil
}
