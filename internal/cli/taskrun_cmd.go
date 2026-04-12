package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

var (
	taskRunTaskID   string
	taskRunRole     string
	taskRunWorktree string
	taskRunParentID string
	taskRunJSON     bool
	taskRunStyle    string // "epic-run" or "fleet" (default: "epic-run")
)

// taskRunResult is the JSON output written to stdout when --json is set.
// NOTE: --json is currently unreliable because the backend's non-interactive
// invoker writes agent display output to stdout, contaminating the JSON stream.
// The pipeline uses exit codes for pass/fail, not JSON parsing. The --json flag
// is kept for future use once backend output is redirected to stderr.
type taskRunResult struct {
	TaskID       string  `json:"task_id"`
	Role         string  `json:"role"`
	AgentName    string  `json:"agent_name"`
	ExitCode     int     `json:"exit_code"`
	DurationSec  float64 `json:"duration_sec"`
	FilesChanged int     `json:"files_changed"`
	LinesAdded   int     `json:"lines_added"`
	LinesRemoved int     `json:"lines_removed"`
	SessionID    string  `json:"session_id,omitempty"`
	Error        string  `json:"error,omitempty"`
}

var taskRunCmd = &cobra.Command{
	Use:     "task-run",
	Short:   "Run a single agent for a specific task (non-interactive, for pipelines)",
	GroupID: "agents",
	Long: `Run a single agent for a pre-assigned task ID.

This is a non-interactive, single-shot command designed for use in
agentflow pipelines. Unlike 'loom plan' and 'loom task' (which pick
tasks from the backlog), task-run is given the task ID explicitly.

Required Flags:
  --task    Task ID (e.g. bd-abc.3)
  --role    Agent role: plan or task

Exit Codes:
  0   Agent completed successfully
  1   Agent failed

Examples:
  loom task-run --task bd-abc.3 --role task --worktree falcon
  loom task-run --task bd-abc.3 --role plan --worktree falcon --json`,
	Args: cobra.NoArgs,
	Run:  runTaskRun,
}

func init() {
	taskRunCmd.Flags().StringVar(&taskRunTaskID, "task", "", "Task ID to work on (required)")
	taskRunCmd.Flags().StringVar(&taskRunRole, "role", "", "Agent role: plan or task (required)")
	taskRunCmd.Flags().StringVar(&taskRunWorktree, "worktree", "", "Worktree name or path")
	taskRunCmd.Flags().StringVar(&taskRunParentID, "parent", "", "Epic ID for session tracking")
	taskRunCmd.Flags().BoolVar(&taskRunJSON, "json", false, "Write JSON result to stdout")
	taskRunCmd.Flags().StringVar(&taskRunStyle, "style", "epic-run", "Prompt style: epic-run or fleet")
	_ = taskRunCmd.MarkFlagRequired("task")
	_ = taskRunCmd.MarkFlagRequired("role")
	rootCmd.AddCommand(taskRunCmd)
}

func runTaskRun(cmd *cobra.Command, args []string) {
	if taskRunRole != "plan" && taskRunRole != "task" {
		fmt.Fprintf(os.Stderr, "Error: --role must be 'plan' or 'task', got %q\n", taskRunRole)
		os.Exit(1)
	}

	// Resolve worktree
	target, err := ResolveAgentTarget(taskRunWorktree, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: resolve worktree: %v\n", err)
		os.Exit(1)
	}
	worktreePath := target.WorkDir
	agentName := target.AgentName

	// Acquire lock
	if err := AcquireLock(worktreePath, "task-run:"+taskRunRole, agentName); err != nil {
		fmt.Fprintf(os.Stderr, "Error: acquire lock: %v\n", err)
		os.Exit(1)
	}
	// defer ensures lock release even on panic. For the os.Exit path at the
	// bottom, we call ReleaseLock explicitly before os.Exit (since defers don't
	// run on os.Exit).
	defer func() { _ = ReleaseLock(worktreePath) }()

	if err := UpdateLockState(worktreePath, StateActive); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not update lock state: %v\n", err)
	}

	prompt := generateTaskRunPrompt(agentName, taskRunTaskID, taskRunRole, taskRunStyle)
	result := executeTaskRun(worktreePath, agentName, prompt)

	if taskRunJSON {
		_ = json.NewEncoder(os.Stdout).Encode(result)
	}

	if result.ExitCode != 0 {
		fmt.Fprintf(os.Stderr, "Error: agent failed: %s\n", result.Error)
		_ = ReleaseLock(worktreePath) // explicit — defer won't run after os.Exit
		os.Exit(result.ExitCode)
	}
}

// generateTaskRunPrompt builds the prompt for the given role and style.
func generateTaskRunPrompt(agentName, taskID, role, style string) string {
	workspace, _ := ResolveActiveWorkspace()
	backendName := ResolveBackendName()
	if style == "fleet" {
		if role == "plan" {
			return GenerateFleetPlanningPrompt(agentName, taskID, workspace)
		}
		return GenerateFleetTaskPrompt(agentName, taskID, workspace, backendName)
	}
	// Default: epic-run style
	if role == "plan" {
		return GenerateEpicRunPlanningPrompt(agentName, taskID, workspace)
	}
	return GenerateEpicRunTaskPrompt(agentName, taskID, workspace, backendName)
}

// executeTaskRun invokes the agent and collects results.
func executeTaskRun(worktreePath, agentName, prompt string) taskRunResult {
	backendName := ResolveBackendName()
	phase := "implementation"
	if taskRunRole == "plan" {
		phase = "planning"
	}

	// Session tracking
	sessStore, sessErr := sessions.NewStore(GetBeadsDir())
	if sessErr != nil {
		log.Printf("[task-run] Warning: session store unavailable: %v", sessErr)
	}
	var sess *sessions.Session
	if sessStore != nil {
		sess, _ = sessStore.CreateSession(sessions.CreateOptions{
			AgentName: agentName,
			Backend:   backendName,
			EpicID:    taskRunParentID,
			Prompt:    prompt,
			Phase:     phase,
		})
		if sess != nil {
			SetActiveSessionEnv(GetBeadsDir(), sess.SessionID())
			go sessions.NotifyWebUI(context.Background(), resolveWebUIURL(), "", sess.SessionID(), sessions.StatusRunning)
		}
	}

	// Usage tracking
	usageStore, _ := usage.NewStore(GetBeadsDir())
	collector := usage.NewCollector(backendName, agentName)

	shutdown := SetupSignalHandler()
	beforeRef := captureHEADRef(worktreePath)
	startedAt := time.Now()

	invokeErr := InvokeAgentNonInteractive(worktreePath, prompt, agentName, shutdown, collector)

	endedAt := time.Now()
	recordSessionUsage(usageStore, collector, worktreePath, agentName, taskRunParentID, startedAt, endedAt, invokeErr, nil)

	// Compute results
	diffStats := ComputeDiffStats(worktreePath, beforeRef)
	exitCode := 0
	var errMsg string
	if invokeErr != nil {
		exitCode = 1
		errMsg = invokeErr.Error()
		var exitErr *exec.ExitError
		if errors.As(invokeErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}

	claimedTaskID := taskRunTaskID
	if info, lockErr := ReadLockFile(worktreePath); lockErr == nil && info.TaskID != "" {
		claimedTaskID = info.TaskID
	}

	// Finalize session
	if sess != nil {
		_ = sess.Finalize(sessions.FinalizeOptions{
			TaskID:   claimedTaskID,
			ExitCode: exitCode,
			DiffStats: sessions.DiffStats{
				FilesChanged: diffStats.FilesChanged,
				LinesAdded:   diffStats.LinesAdded,
				LinesRemoved: diffStats.LinesRemoved,
			},
		})
		ClearActiveSessionEnv()
		go sessions.NotifyWebUI(context.Background(), resolveWebUIURL(), claimedTaskID, sess.SessionID(), sess.Meta.Status)
	}

	sessionID := ""
	if sess != nil {
		sessionID = sess.SessionID()
	}
	return taskRunResult{
		TaskID:       claimedTaskID,
		Role:         taskRunRole,
		AgentName:    agentName,
		ExitCode:     exitCode,
		DurationSec:  endedAt.Sub(startedAt).Seconds(),
		FilesChanged: diffStats.FilesChanged,
		LinesAdded:   diffStats.LinesAdded,
		LinesRemoved: diffStats.LinesRemoved,
		SessionID:    sessionID,
		Error:        errMsg,
	}
}
