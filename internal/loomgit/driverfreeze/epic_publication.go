package driverfreeze

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func EpicPRRequested(payload json.RawMessage) (string, bool) {
	var input struct {
		LeadName            string `json:"leadName"`
		OpenPullRequest     bool   `json:"openPullRequest"`
		StackedPullRequests bool   `json:"stackedPullRequests"`
		DryRun              bool   `json:"dryRun"`
	}
	if json.Unmarshal(payload, &input) != nil {
		return "", false
	}
	return strings.TrimSpace(input.LeadName), !input.DryRun && (input.OpenPullRequest || input.StackedPullRequests)
}

func RecordEpicRun(ctx context.Context, runs store.TaskRunStore, run *domain.DriverRun) error {
	lead, requested := EpicPRRequested(run.Payload)
	if !requested || run.Status != domain.DriverRunCompleted {
		return nil
	}
	if lead == "" {
		return errors.New("epic PR delivery requires a lead")
	}
	journalStore, err := journal.OpenSQLite(filepath.Join(config.GetConfigDir(), "loomgit", "store.db"))
	if err != nil {
		return err
	}
	defer func() { _ = journalStore.Close() }()
	tasks, err := runs.List(ctx, run.WorkspaceKey, store.TaskRunFilter{DriverRunID: run.RunID, Limit: 10000})
	if err != nil {
		return err
	}
	if len(tasks) >= 10000 {
		return errors.New("epic PR delivery task list exceeds limit")
	}
	changes, err := epicRunChanges(ctx, journalStore, run.WorkspaceKey, tasks)
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		return errors.New("epic PR delivery has no frozen task revisions")
	}
	return journalStore.RecordEpicPublication(ctx, journal.EpicPublication{Workspace: run.WorkspaceKey,
		RunID: run.RunID, Lead: lead, Changes: changes})
}

func epicRunChanges(ctx context.Context, journalStore *journal.SQLite, workspace string, tasks []*domain.TaskRun) ([]string, error) {
	var changes []string
	for _, task := range tasks {
		if task.Status != domain.TaskRunCompleted {
			continue
		}
		attempt := strings.TrimSpace(task.RuntimeMetadata["attempt_id"])
		if attempt == "" {
			return nil, fmt.Errorf("task run %s has no frozen attempt identity", task.TaskRunID)
		}
		revision, err := journalStore.RevisionByRequest(ctx, "driver:"+attempt)
		if err != nil {
			return nil, fmt.Errorf("task run %s has no frozen revision: %w", task.TaskRunID, err)
		}
		owner, err := journalStore.TaskForChange(ctx, workspace, revision.Change)
		if err != nil {
			return nil, err
		}
		if revision.Workspace != workspace || owner != task.TaskID || !revision.Ready {
			return nil, fmt.Errorf("task run %s frozen revision belongs to another task or is incomplete", task.TaskRunID)
		}
		if !slices.Contains(changes, revision.Change) {
			changes = append(changes, revision.Change)
		}
	}
	return changes, nil
}
