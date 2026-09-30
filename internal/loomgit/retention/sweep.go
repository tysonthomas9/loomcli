package retention

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/pool"
	"github.com/tysonthomas9/loomcli/internal/loomgit/mirror"
)

type Result struct {
	Workspace, Change, Attempt, Path string
	Action, Reason                   string
}

type Sweep struct {
	Store     *journal.SQLite
	Now       func() time.Time
	Abandoned AbandonmentLookup
}

type AbandonmentLookup interface {
	AbandonedForRetention(context.Context, string, string) (bool, error)
}

func RunAt(ctx context.Context, path string, apply bool) ([]Result, error) {
	store, err := journal.OpenSQLite(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()
	return (Sweep{Store: store, Abandoned: store}).Run(ctx, apply)
}

func (s Sweep) Run(ctx context.Context, apply bool) ([]Result, error) {
	if s.Store == nil {
		return nil, errors.New("retention store is required")
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	rows, err := s.Store.RetainedCopies(ctx)
	if err != nil {
		return nil, err
	}
	results := make([]Result, 0, len(rows))
	for _, row := range rows {
		result, err := s.inspect(ctx, row, now().UTC(), apply)
		results = append(results, result)
		if err != nil {
			return results, err
		}
		if row.Removed && !row.CaptureRefRemoved {
			captureResult, captureErr := s.captureRef(ctx, row, now().UTC(), apply)
			results = append(results, captureResult)
			if captureErr != nil {
				return results, captureErr
			}
		}
	}
	tombstoneResults, err := s.tombstones(ctx, now().UTC(), apply)
	return append(results, tombstoneResults...), err
}

func (s Sweep) tombstones(ctx context.Context, now time.Time, apply bool) ([]Result, error) {
	tombstones, err := s.Store.WorkspaceTombstones(ctx)
	if err != nil {
		return nil, err
	}
	var results []Result
	for _, tombstone := range tombstones {
		result := Result{Workspace: tombstone.Workspace, Path: tombstone.RefPrefix, Action: "keep"}
		prefix, err := refname.WorkspacePrefix(tombstone.Workspace)
		if err != nil || prefix != tombstone.RefPrefix {
			result.Reason = "invalid tombstone prefix"
			results = append(results, result)
			continue
		}
		policy, err := s.Store.RetentionPolicy(ctx, tombstone.Workspace)
		if err != nil {
			return results, err
		}
		if now.Before(tombstone.CreatedAt.Add(policy.CaptureRefs)) {
			result.Reason = "tombstone retention window open"
			results = append(results, result)
			continue
		}
		result.Action = "delete provider refs"
		if !apply {
			result.Reason = "dry run"
			results = append(results, result)
			continue
		}
		if err := mirror.DeleteWorkspacePrefix(ctx, s.Store, prefix); err != nil {
			result.Action, result.Reason = "keep", err.Error()
			results = append(results, result)
			continue
		}
		if err := s.Store.DeleteWorkspaceTombstone(ctx, tombstone.Workspace); err != nil {
			return results, err
		}
		results = append(results, result)
	}
	return results, nil
}

func (s Sweep) captureRef(ctx context.Context, row journal.RetainedCopy, now time.Time, apply bool) (Result, error) {
	result := Result{Workspace: row.Workspace, Change: row.Change, Attempt: row.Attempt,
		Action: "keep", Reason: "capture ref retention window open"}
	policy, err := s.Store.RetentionPolicy(ctx, row.Workspace)
	if err != nil {
		return result, err
	}
	if row.EligibleAt.IsZero() || now.Before(row.EligibleAt.Add(policy.CaptureRefs)) {
		return result, nil
	}
	if row.SourceRepo == "" {
		result.Reason = "source repository not recorded"
		return result, nil
	}
	ref, err := refname.AttemptCapture(row.Workspace, row.Attempt)
	if err != nil {
		return result, err
	}
	expected, err := s.Store.CaptureSHA(ctx, row)
	if err != nil || expected == "" {
		result.Reason = "complete capture SHA unavailable"
		return result, nil
	}
	result.Path, result.Action = ref, "delete capture ref"
	if !apply {
		result.Reason = "dry run"
		return result, nil
	}
	repo, err := pool.New(s.Store).Admit(ctx, row.SourceRepo)
	if err != nil {
		result.Action, result.Reason = "keep", err.Error()
		return result, nil
	}
	err = repo.WithLock(ctx, func(ctx context.Context) error {
		sha, err := repo.Run(ctx, "show-ref", "--verify", "--hash", ref)
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(sha)) != expected {
			return errors.New("capture ref changed")
		}
		_, err = repo.Run(ctx, "update-ref", "-d", ref, strings.TrimSpace(string(sha)))
		return err
	})
	if err != nil {
		result.Action, result.Reason = "keep", err.Error()
		return result, nil
	}
	return result, s.Store.MarkCaptureRefRemoved(ctx, row)
}

func (s Sweep) inspect(ctx context.Context, row journal.RetainedCopy, now time.Time, apply bool) (Result, error) {
	result := Result{Workspace: row.Workspace, Change: row.Change, Attempt: row.Attempt, Path: row.Path, Action: "keep"}
	if row.Removed {
		result.Action, result.Reason = "removed", "already removed"
		return result, nil
	}
	state, reason, err := s.eligibility(ctx, row)
	if err != nil {
		return result, err
	}
	if reason != "" {
		result.Reason = reason
		return result, nil
	}
	policy, err := s.Store.RetentionPolicy(ctx, row.Workspace)
	if err != nil {
		return result, err
	}
	if row.EligibleAt.IsZero() {
		result.Reason = "retention starts when terminal state is observed"
		if apply {
			return result, s.Store.ObserveRetention(ctx, row, now)
		}
		return result, nil
	}
	window := policy.Landed
	if state == "abandoned" {
		window = policy.Abandoned
	}
	if now.Before(row.EligibleAt.Add(window)) {
		result.Reason = "retention window open"
		return result, nil
	}
	if err := safeCopy(ctx, s.Store, row); err != nil {
		result.Reason = err.Error()
		return result, nil
	}
	result.Reason = "eligible; deletion disabled until capture is lease-covered (P4.6b)"
	return result, nil
}

func (s Sweep) eligibility(ctx context.Context, row journal.RetainedCopy) (string, string, error) {
	if !row.Complete {
		return "", "capture incomplete", nil
	}
	landed, err := s.Store.IsLanded(ctx, row.Workspace, row.Change)
	if err != nil {
		return "", "", err
	}
	state := ""
	if landed {
		state = "landed"
	}
	if state == "" && s.Abandoned != nil {
		abandoned, err := s.Abandoned.AbandonedForRetention(ctx, row.Workspace, row.Change)
		if err != nil {
			return "", "", err
		}
		if abandoned {
			state = "abandoned"
		}
	}
	if state == "" {
		return "", "change is not landed or abandoned", nil
	}
	complete, err := s.Store.HasCompleteCapture(ctx, row)
	if err != nil {
		return "", "", err
	}
	if !complete {
		return state, "complete revision not recorded", nil
	}
	if row.SourceRepo == "" {
		return state, "source repository not recorded", nil
	}
	return state, "", nil
}

func safeCopy(ctx context.Context, store *journal.SQLite, row journal.RetainedCopy) error {
	if err := recordedAgentCopy(row); err != nil {
		return err
	}
	if !filepath.IsAbs(row.Path) || !filepath.IsAbs(row.SourceRepo) || row.Path == row.SourceRepo {
		return errors.New("copy or source path is not an absolute distinct path")
	}
	info, err := os.Lstat(row.Path)
	if err != nil {
		return fmt.Errorf("copy path unavailable: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("copy path is not a directory")
	}
	ref, err := refname.AttemptCapture(row.Workspace, row.Attempt)
	if err != nil {
		return err
	}
	options := gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}}
	runner, err := gitexec.New(row.SourceRepo, options)
	if err != nil {
		return err
	}
	expected, err := store.CaptureSHA(ctx, row)
	if err != nil || expected == "" {
		return errors.New("complete capture SHA unavailable")
	}
	sha, err := runner.Run(ctx, "show-ref", "--verify", "--hash", ref)
	if err != nil {
		return fmt.Errorf("source capture ref unavailable: %w", err)
	}
	if strings.TrimSpace(string(sha)) != expected {
		return errors.New("source capture ref changed")
	}
	copyRunner, err := gitexec.New(row.Path, options)
	if err != nil {
		return err
	}
	if err := checkCopyContent(ctx, copyRunner, expected); err != nil {
		return err
	}
	return removeCopy(ctx, row, runner, copyRunner, expected)
}

func removeCopy(ctx context.Context, row journal.RetainedCopy,
	runner, copyRunner *gitexec.Runner, expected string) error {
	common, err := copyRunner.Run(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	sourceCommon, err := runner.Run(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(common)) == strings.TrimSpace(string(sourceCommon)) {
		return nil
	}
	if err := cloneRefsCaptured(ctx, runner, copyRunner, expected); err != nil {
		return err
	}
	if filepath.Base(row.Path) != row.Attempt {
		return errors.New("clone path does not match recorded attempt")
	}
	return nil
}

func checkCopyContent(ctx context.Context, runner *gitexec.Runner, expected string) error {
	head, err := runner.Run(ctx, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if _, err := runner.Run(ctx, "merge-base", "--is-ancestor", strings.TrimSpace(string(head)), expected); err != nil {
		return errors.New("copy HEAD is outside complete capture")
	}
	status, err := runner.Run(ctx, "status", "--porcelain", "--untracked-files=all", "--ignored=matching")
	if err != nil {
		return err
	}
	if len(status) != 0 {
		return errors.New("copy contains work outside its complete capture")
	}
	return nil
}

func cloneRefsCaptured(ctx context.Context, sourceRunner, copyRunner *gitexec.Runner, expected string) error {
	refs, err := copyRunner.Run(ctx, "for-each-ref", "--format=%(refname) %(objectname)", "refs")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(refs)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return errors.New("unexpected copy ref listing")
		}
		if strings.HasPrefix(fields[0], refname.WorkspaceRefPrefix) {
			sourceSHA, err := sourceRunner.Run(ctx, "show-ref", "--verify", "--hash", fields[0])
			if err != nil || strings.TrimSpace(string(sourceSHA)) != fields[1] {
				return fmt.Errorf("copy ref %s is not captured in source", fields[0])
			}
			continue
		}
		if _, err := copyRunner.Run(ctx, "merge-base", "--is-ancestor", fields[1], expected); err != nil {
			return fmt.Errorf("copy branch %s is outside complete capture: %w", fields[0], err)
		}
	}
	return nil
}

func recordedAgentCopy(row journal.RetainedCopy) error {
	if !strings.Contains(filepath.ToSlash(row.Path), "/.loom/task-copies/") {
		return nil
	}
	data, err := os.ReadFile(bootstrap.StateFilePath())
	if err != nil {
		return err
	}
	var cache struct {
		Workspaces map[string]struct {
			Agents map[string]struct {
				Worktrees   map[string]string `json:"worktrees"`
				TaskCopyIDs map[string]string `json:"task_copy_ids"`
			} `json:"agents"`
		} `json:"workspaces"`
	}
	if err := json.Unmarshal(data, &cache); err != nil {
		return err
	}
	for _, agent := range cache.Workspaces[row.Workspace].Agents {
		for repoName, path := range agent.Worktrees {
			if filepath.Clean(path) == filepath.Clean(row.Path) && agent.TaskCopyIDs[repoName] == filepath.Base(row.Path) {
				return nil
			}
		}
	}
	return errors.New("task copy ID and path are not recorded together")
}
