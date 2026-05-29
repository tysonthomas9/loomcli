package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	workflowpkg "github.com/tysonthomas9/loomcli/internal/workflow"
)

var (
	workflowJSON     bool
	workflowRunInput string
	workflowRunWait  bool
	workflowRunOnce  bool
	workflowLogsJSON bool
	workflowShowJSON bool
	workflowListJSON bool

	workflowWithActiveWorkspace = cmdstore.WithActiveWorkspace
	workflowWriteJSON           = cmdstore.WriteJSON
)

var workflowCmd = &cobra.Command{
	Use:     "workflow",
	Short:   "Run and inspect workflow definitions",
	GroupID: "workspace",
}

var workflowListCmd = &cobra.Command{
	Use:   "list",
	Short: "List workflow definitions",
	Args:  cobra.NoArgs,
	RunE:  runWorkflowList,
}

var workflowRunCmd = &cobra.Command{
	Use:   "run <NAME>",
	Short: "Create or resume a workflow run",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowRun,
}

var workflowShowCmd = &cobra.Command{
	Use:   "show <RUN_ID>",
	Short: "Show a workflow run",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowShow,
}

var workflowLogsCmd = &cobra.Command{
	Use:   "logs <RUN_ID>",
	Short: "Show workflow run events",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowLogs,
}

var workflowCancelCmd = &cobra.Command{
	Use:   "cancel <RUN_ID>",
	Short: "Cancel a live workflow run",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowCancel,
}

func init() {
	workflowListCmd.Flags().BoolVar(&workflowListJSON, "json", false, "JSON output")
	workflowRunCmd.Flags().StringVar(&workflowRunInput, "input", "{}", "Workflow input JSON")
	workflowRunCmd.Flags().BoolVar(&workflowRunWait, "wait", false, "Poll until the workflow reaches a terminal state")
	workflowRunCmd.Flags().BoolVar(&workflowRunOnce, "once", true, "Run one reconcile pass for built-in workflows")
	workflowRunCmd.Flags().BoolVar(&workflowJSON, "json", false, "JSON output")
	workflowShowCmd.Flags().BoolVar(&workflowShowJSON, "json", false, "JSON output")
	workflowLogsCmd.Flags().BoolVar(&workflowLogsJSON, "json", false, "JSON output")

	workflowCmd.AddCommand(workflowListCmd, workflowRunCmd, workflowShowCmd, workflowLogsCmd, workflowCancelCmd)
	cli.RegisterCommand(workflowCmd)
}

func runWorkflowList(_ *cobra.Command, _ []string) error {
	return workflowWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		if err := workflowpkg.EnsureBuiltins(ctx, h.Store, ws); err != nil {
			return err
		}
		defs, err := h.Store.WorkflowDefinitions().List(ctx, ws, store.WorkflowDefinitionFilter{Status: domain.DefinitionStatusActive})
		if err != nil {
			return fmt.Errorf("list workflows: %w", err)
		}
		if workflowListJSON {
			return workflowWriteJSON(defs)
		}
		if len(defs) == 0 {
			fmt.Printf("No workflow definitions in workspace %s\n", ws)
			return nil
		}
		for _, def := range defs {
			fmt.Printf("%-28s %-12s %s\n", def.Name, def.Version, def.Description)
		}
		return nil
	})
}

func runWorkflowRun(_ *cobra.Command, args []string) error {
	input, err := parseInput(workflowRunInput)
	if err != nil {
		return err
	}
	return workflowWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		ib := cli.DefaultIssueBackend()
		if ib == nil {
			return errors.New("no issue backend available")
		}
		run, err := workflowpkg.CreateOrResumeRun(ctx, h.Store, ws, args[0], input, actorName())
		if err != nil {
			return fmt.Errorf("create workflow run: %w", err)
		}
		var result *workflowpkg.BuiltinRunResult
		if workflowRunOnce {
			result, err = workflowpkg.RunOnce(ctx, h.Store, ib, run)
			if err != nil {
				return fmt.Errorf("run workflow: %w", err)
			}
			run = result.Run
		}
		if workflowRunWait {
			run, err = waitWorkflow(ctx, h.Store, ws, run.RunID)
			if err != nil {
				return err
			}
		}
		if workflowJSON {
			if result != nil {
				return workflowWriteJSON(result)
			}
			return workflowWriteJSON(run)
		}
		fmt.Printf("Workflow run %s %s (%s)\n", run.RunID, run.Status, run.WorkflowName)
		if result != nil {
			fmt.Printf("ready=%d open=%d blocked=%d ensured=%d\n", result.ReadyCount, result.OpenCount, result.BlockedCount, len(result.TaskRuns))
		}
		return nil
	})
}

func runWorkflowShow(_ *cobra.Command, args []string) error {
	return workflowWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		run, err := h.Store.WorkflowRuns().Get(ctx, ws, args[0])
		if err != nil {
			return fmt.Errorf("get workflow run: %w", err)
		}
		if workflowShowJSON {
			return workflowWriteJSON(run)
		}
		fmt.Printf("Run:        %s\n", run.RunID)
		fmt.Printf("Workflow:   %s@%s\n", run.WorkflowName, run.WorkflowVersion)
		fmt.Printf("Status:     %s\n", run.Status)
		if run.IdempotencyKey != "" {
			fmt.Printf("Singleton:  %s\n", run.IdempotencyKey)
		}
		if run.WaitCondition != "" {
			fmt.Printf("Waiting:    %s\n", run.WaitCondition)
		}
		if run.ErrorMessage != "" {
			fmt.Printf("Error:      %s\n", run.ErrorMessage)
		}
		return nil
	})
}

func runWorkflowLogs(_ *cobra.Command, args []string) error {
	return workflowWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		events, err := h.Store.RunEvents().List(ctx, ws, store.RunEventFilter{WorkflowRunID: args[0]})
		if err != nil {
			return fmt.Errorf("list workflow events: %w", err)
		}
		if workflowLogsJSON {
			return workflowWriteJSON(events)
		}
		for _, ev := range events {
			fmt.Printf("%03d %-24s %s\n", ev.EventIndex, ev.Type, ev.Message)
		}
		return nil
	})
}

func runWorkflowCancel(_ *cobra.Command, args []string) error {
	return workflowWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		now := time.Now().UTC()
		finishedAt := &now
		status := domain.WorkflowRunCancelled
		run, err := h.Store.WorkflowRuns().Update(ctx, ws, args[0], store.WorkflowRunUpdate{
			Status:     &status,
			FinishedAt: &finishedAt,
		})
		if err != nil {
			return fmt.Errorf("cancel workflow run: %w", err)
		}
		_, _ = h.Store.RunEvents().Append(ctx, store.RunEventAppend{
			WorkspaceKey:  ws,
			WorkflowRunID: run.RunID,
			Type:          "workflow_cancelled",
			Message:       "workflow run cancelled",
		})
		fmt.Printf("Cancelled workflow run %s\n", run.RunID)
		return nil
	})
}

func parseInput(s string) (json.RawMessage, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		s = "{}"
	}
	var tmp any
	if err := json.Unmarshal([]byte(s), &tmp); err != nil {
		return nil, fmt.Errorf("--input must be valid JSON: %w", err)
	}
	return json.RawMessage(s), nil
}

func waitWorkflow(ctx context.Context, st store.Store, ws, runID string) (*domain.WorkflowRun, error) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		run, err := st.WorkflowRuns().Get(ctx, ws, runID)
		if err != nil {
			return nil, err
		}
		if !domain.WorkflowRunStatusLive(run.Status) {
			return run, nil
		}
		select {
		case <-ctx.Done():
			return run, ctx.Err()
		case <-ticker.C:
		}
	}
}

func actorName() string {
	if actor := strings.TrimSpace(os.Getenv("LOOM_ACTOR")); actor != "" {
		return actor
	}
	if actor := strings.TrimSpace(os.Getenv("USER")); actor != "" {
		return actor
	}
	return "loom"
}
