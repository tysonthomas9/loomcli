package kv

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// OrphanedTask represents a task whose worker has gone stale.
type OrphanedTask struct {
	TaskID     string
	TaskTitle  string
	WorkerID   string
	StaleSince time.Time
}

// ReconcileResult holds the outcome of reconciling a single orphaned task.
type ReconcileResult struct {
	TaskID  string
	Success bool
	Error   error
}

// Reconciler resets orphaned tasks in beads so they become available for re-assignment.
type Reconciler struct {
	bdPath string // path to the bd binary (default: "bd")
}

// NewReconciler creates a new reconciler. If bdPath is empty, "bd" is used.
func NewReconciler(bdPath string) *Reconciler {
	if bdPath == "" {
		bdPath = "bd"
	}
	return &Reconciler{bdPath: bdPath}
}

// ResetOrphanedTasks resets each orphaned task to open status with no assignee.
func (r *Reconciler) ResetOrphanedTasks(ctx context.Context, tasks []OrphanedTask) []ReconcileResult {
	results := make([]ReconcileResult, len(tasks))
	for i, task := range tasks {
		results[i] = r.resetTask(ctx, task)
	}
	return results
}

func (r *Reconciler) resetTask(ctx context.Context, task OrphanedTask) ReconcileResult {
	if task.TaskID == "" {
		return ReconcileResult{
			TaskID:  task.TaskID,
			Success: false,
			Error:   errors.New("task ID cannot be empty"),
		}
	}
	if strings.ContainsAny(task.TaskID, ";\n\r\t `$|&") {
		return ReconcileResult{
			TaskID:  task.TaskID,
			Success: false,
			Error:   fmt.Errorf("task ID contains invalid characters: %q", task.TaskID),
		}
	}

	cmd := exec.CommandContext(ctx, r.bdPath, "update", task.TaskID, "--status", "open", "--assignee", "")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return ReconcileResult{
			TaskID:  task.TaskID,
			Success: false,
			Error:   fmt.Errorf("bd update failed: %w (output: %s)", err, string(output)),
		}
	}
	return ReconcileResult{
		TaskID:  task.TaskID,
		Success: true,
	}
}
