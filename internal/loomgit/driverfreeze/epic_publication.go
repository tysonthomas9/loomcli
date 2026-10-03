package driverfreeze

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
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
	changes, empty, err := epicRunChanges(ctx, journalStore, run.WorkspaceKey, tasks)
	if err != nil {
		return err
	}
	if len(changes) == 0 && empty {
		return nil
	}
	if len(changes) == 0 {
		return errors.New("epic PR delivery has no frozen task revisions")
	}
	return journalStore.RecordEpicPublication(ctx, journal.EpicPublication{Workspace: run.WorkspaceKey,
		RunID: run.RunID, Lead: lead, Changes: changes})
}

// epicRunChanges lists the changes the epic's PRs carry. A task whose attempt
// changed nothing closed without review, so it is left out (it can never be
// applied); empty reports that at least one such task was skipped.
func epicRunChanges(ctx context.Context, journalStore *journal.SQLite, workspace string, tasks []*domain.TaskRun) ([]string, bool, error) {
	var changes []string
	empty := false
	for _, task := range tasks {
		if task.Status != domain.TaskRunCompleted {
			continue
		}
		attempt := strings.TrimSpace(task.RuntimeMetadata["attempt_id"])
		if attempt == "" && task.RuntimeMetadata["remote_capture_status"] == "frozen" {
			remote := strings.TrimSpace(task.RuntimeMetadata["remote_capture_attempt"])
			if epicRemoteAttempt(task.TaskRunID, remote) {
				attempt = remote
			}
		}
		if attempt == "" {
			return nil, false, fmt.Errorf("task run %s has no frozen attempt identity", task.TaskRunID)
		}
		revision, err := journalStore.RevisionByRequest(ctx, "driver:"+attempt)
		if err != nil {
			return nil, false, fmt.Errorf("task run %s has no frozen revision: %w", task.TaskRunID, err)
		}
		owner, err := journalStore.TaskForChange(ctx, workspace, revision.Change)
		if err != nil {
			return nil, false, err
		}
		if revision.Workspace != workspace || owner != task.TaskID || !revision.Ready {
			return nil, false, fmt.Errorf("task run %s frozen revision belongs to another task or is incomplete", task.TaskRunID)
		}
		if revision.NoChanges {
			empty = true
			continue
		}
		if !slices.Contains(changes, revision.Change) {
			changes = append(changes, revision.Change)
		}
	}
	return changes, empty, nil
}

func epicRemoteAttempt(runID, attempt string) bool {
	name := runID
	for _, character := range runID {
		if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' || character == '.') {
			name = "hex-" + hex.EncodeToString([]byte(runID))
			break
		}
	}
	number, err := strconv.Atoi(strings.TrimPrefix(attempt, name+"-a"))
	return strings.HasPrefix(attempt, name+"-a") && err == nil && number > 0
}
