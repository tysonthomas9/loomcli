package cli

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// Pipeline YAML structures matching agentflow-go's loader expectations.
type pipelineYAML struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description,omitempty"`
	Steps       []pipelineStep `yaml:"steps"`
}

type pipelineStep struct {
	ID       string   `yaml:"id"`
	Type     string   `yaml:"type"`
	Command  string   `yaml:"command,omitempty"`
	After    []string `yaml:"after,omitempty"`
	GoalGate bool     `yaml:"goal_gate,omitempty"`
}

// Flag variables
var (
	pipelineGenerateOutput   string
	pipelineGenerateWorktree string
)

var pipelineCmd = &cobra.Command{
	Use:     "pipeline",
	Short:   "Pipeline generation and management",
	GroupID: "agents",
}

var pipelineGenerateCmd = &cobra.Command{
	Use:   "generate <epic-id>",
	Short: "Generate an agentflow pipeline from an epic's task DAG",
	Long: `Generate an agentflow pipeline YAML from the tasks under a beads epic.

Each open task produces one or two pipeline steps:
  - plan step (if task has no design or needs revision)
  - impl step (if task has an approved design)

Dependencies between tasks become 'after' edges in the pipeline.
A validation step (make gate) is appended after all implementation steps.

Examples:
  loom pipeline generate loomcli-abc --worktree falcon
  loom pipeline generate loomcli-abc --worktree falcon --output pipeline.yaml`,
	Args: cobra.ExactArgs(1),
	Run:  runPipelineGenerate,
}

func init() {
	pipelineGenerateCmd.Flags().StringVarP(&pipelineGenerateOutput, "output", "o", "", "Output file (default: stdout)")
	pipelineGenerateCmd.Flags().StringVar(&pipelineGenerateWorktree, "worktree", "", "Worktree for agent execution (required)")
	_ = pipelineGenerateCmd.MarkFlagRequired("worktree")
	pipelineCmd.AddCommand(pipelineGenerateCmd)
	rootCmd.AddCommand(pipelineCmd)
}

func runPipelineGenerate(cmd *cobra.Command, args []string) {
	epicID := args[0]
	ctx := context.Background()

	// Resolve worktree to absolute path so commands work regardless of agentflow's work-dir
	target, err := ResolveAgentTarget(pipelineGenerateWorktree, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: could not resolve worktree %q: %v\n", pipelineGenerateWorktree, err)
		os.Exit(1)
	}
	worktreeAbsPath := target.WorkDir

	// Validate epic exists
	epic, err := defaultTracker().GetIssue(ctx, epicID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: could not load epic %s: %v\n", epicID, err)
		os.Exit(1)
	}
	if epic.IssueType != "epic" {
		fmt.Fprintf(os.Stderr, "Error: %s is a %s, not an epic\n", epicID, epic.IssueType)
		os.Exit(1)
	}

	// Fetch all children
	children, err := defaultTracker().List(ctx, ListOpts{ParentID: epicID})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: could not list children of %s: %v\n", epicID, err)
		os.Exit(1)
	}

	if len(children) == 0 {
		fmt.Fprintf(os.Stderr, "Error: epic %s has no child tasks\n", epicID)
		os.Exit(1)
	}

	// Build DAG and generate steps
	blockedBy := buildBlocksDAG(children)
	steps := buildPipelineSteps(children, blockedBy, worktreeAbsPath, epicID)

	if len(steps) == 0 {
		fmt.Fprintf(os.Stderr, "No actionable tasks found in epic %s (all closed or in_progress)\n", epicID)
		os.Exit(0)
	}

	pipeline := pipelineYAML{
		Name:        "epic-run-" + sanitizeID(epicID),
		Description: fmt.Sprintf("Mission pipeline for epic %s: %s", epicID, epic.Title),
		Steps:       steps,
	}

	data, err := yaml.Marshal(&pipeline)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: YAML marshal failed: %v\n", err)
		os.Exit(1)
	}

	if pipelineGenerateOutput != "" {
		if err := os.WriteFile(pipelineGenerateOutput, data, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "Error: writing %s: %v\n", pipelineGenerateOutput, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Pipeline written to %s (%d steps)\n", pipelineGenerateOutput, len(steps))
	} else {
		fmt.Print(string(data))
	}
}

// buildBlocksDAG returns a map of taskID -> []taskIDs that block it.
// Only considers "blocks" dependency type (not parent-child, related, etc.).
func buildBlocksDAG(tasks []BdIssue) map[string][]string {
	blockedBy := make(map[string][]string)
	taskIDs := make(map[string]bool, len(tasks))
	for _, t := range tasks {
		taskIDs[t.ID] = true
	}

	for _, task := range tasks {
		for _, dep := range task.Dependencies {
			// dep means "task is blocked by dep.DependsOnID"
			// Use isDirectBlocker to match taskfilter.go semantics (blocks, conditional-blocks, waits-for)
			if isDirectBlocker(dep.Type) && taskIDs[dep.DependsOnID] {
				blockedBy[task.ID] = append(blockedBy[task.ID], dep.DependsOnID)
			}
		}
	}
	return blockedBy
}

// buildPipelineSteps generates the pipeline steps from tasks and their dependency graph.
// Uses a two-pass approach: first determine which tasks will emit steps (and what kind),
// then build steps with correct after-edge references regardless of sort order.
func buildPipelineSteps(tasks []BdIssue, blockedBy map[string][]string, worktree, epicID string) []pipelineStep {
	// Sort by priority (ascending = higher priority first), then by ID for determinism
	sorted := make([]BdIssue, len(tasks))
	copy(sorted, tasks)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Priority != sorted[j].Priority {
			return sorted[i].Priority < sorted[j].Priority
		}
		return sorted[i].ID < sorted[j].ID
	})

	// Pass 1: classify each task → determine which step IDs will exist
	type taskAction struct {
		task      BdIssue
		needsPlan bool // will emit a plan step
	}
	var actionable []taskAction
	willHavePlan := make(map[string]bool) // keyed by task ID
	willHaveImpl := make(map[string]bool)

	for _, task := range sorted {
		if task.Status != "open" || task.IssueType == "epic" {
			continue
		}
		np := NeedsPlan(task)
		actionable = append(actionable, taskAction{task: task, needsPlan: np})
		if np {
			willHavePlan[task.ID] = true
		}
		willHaveImpl[task.ID] = true
	}

	// Pass 2: build steps with correct after-edge references
	var steps []pipelineStep
	var implStepIDs []string

	for _, a := range actionable {
		sid := sanitizeID(a.task.ID)

		// Plan step (if task needs planning)
		if a.needsPlan {
			planID := "plan-" + sid
			var after []string
			for _, depID := range blockedBy[a.task.ID] {
				depSID := sanitizeID(depID)
				if willHavePlan[depID] {
					// Blocker also needs planning — wait for its plan
					after = append(after, "plan-"+depSID)
				} else if willHaveImpl[depID] {
					// Blocker already has a design — wait for its impl
					after = append(after, "impl-"+depSID)
				}
			}
			steps = append(steps, pipelineStep{
				ID:      planID,
				Type:    "shell",
				Command: buildTaskRunCommand(a.task.ID, "plan", worktree, epicID),
				After:   after,
			})
		}

		// Impl step
		implID := "impl-" + sid
		var after []string

		// Impl depends on its own plan (if plan was emitted)
		if willHavePlan[a.task.ID] {
			after = append(after, "plan-"+sid)
		}

		// Impl depends on blocker's impl (if blocker will have an impl step)
		for _, depID := range blockedBy[a.task.ID] {
			if willHaveImpl[depID] {
				after = append(after, "impl-"+sanitizeID(depID))
			}
		}

		steps = append(steps, pipelineStep{
			ID:      implID,
			Type:    "shell",
			Command: buildTaskRunCommand(a.task.ID, "task", worktree, epicID),
			After:   after,
		})
		implStepIDs = append(implStepIDs, implID)
	}

	// Validation step after all implementations.
	if len(implStepIDs) > 0 {
		steps = append(steps, pipelineStep{
			ID:       "validate",
			Type:     "shell",
			Command:  "make gate",
			After:    implStepIDs,
			GoalGate: true,
		})
	}

	return steps
}

// buildTaskRunCommand builds the shell command for a loom task-run step.
// Arguments are quoted to handle IDs or paths with special characters.
func buildTaskRunCommand(taskID, role, worktree, epicID string) string {
	cmd := fmt.Sprintf("loom task-run --task %q --role %s --worktree %q", taskID, role, worktree)
	if epicID != "" {
		cmd += fmt.Sprintf(" --parent %q", epicID)
	}
	return cmd
}

// sanitizeID makes a beads ID safe for use as an agentflow step ID.
// Replaces any character that isn't alphanumeric, underscore, or hyphen with a hyphen.
func sanitizeID(id string) string {
	var b strings.Builder
	b.Grow(len(id))
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}
