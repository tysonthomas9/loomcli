// Package apply installs an approved revision in a lead's working area.
package apply

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/changeset"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/pool"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/replay"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

type Store interface {
	loomgit.RevisionStore
	review.Store
	SetRevisionAuthor(context.Context, loomgit.Revision, string, string) error
	SaveApplied(context.Context, loomgit.AppliedLayer) error
	AdvanceApplied(context.Context, string, string, string) error
	AppliedLog(context.Context, string, string) ([]loomgit.AppliedLayer, error)
	WorkingAreas(context.Context, string, string) ([]journal.WorkingArea, error)
	OpenApplied(context.Context, string, string) ([]loomgit.AppliedLayer, error)
}

type Request struct {
	Workspace, Lead, Change, RequestID string
	Revision                           int
}

type Result struct {
	HeadSHA               string
	Derived               loomgit.Revision
	ConflictCommit        string
	Paths, DroppedCommits []string
}

type Service struct {
	store            Store
	repo             *pool.LocalRepo
	runner           *gitexec.Runner
	beforeIndexLock  func()
	onIndexLocked    func()
	beforeRecoverCAS func()
}

func New(store Store, repo *pool.LocalRepo, runner *gitexec.Runner) *Service {
	return &Service{store: store, repo: repo, runner: runner}
}

// AppliedLog interleaves applied task layers with contiguous lead-owned runs.
func (s *Service) AppliedLog(ctx context.Context, workspace, lead string) ([]loomgit.AppliedLayer, error) {
	tasks, err := s.store.AppliedLog(ctx, workspace, lead)
	if err != nil {
		return nil, err
	}
	areas, err := s.store.WorkingAreas(ctx, workspace, lead)
	if err != nil {
		return nil, err
	}
	base := ""
	if len(areas) > 0 {
		base = areas[0].BaseSHA
	} else if len(tasks) > 0 {
		base = tasks[0].OldTip
	} else {
		return tasks, nil
	}
	if s.runner == nil {
		return nil, errors.New("applied log requires working-area Git runner")
	}
	head, err := git(ctx, s.runner, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	commits, err := git(ctx, s.runner, "rev-list", "--first-parent", "--reverse", base+".."+head)
	if err != nil {
		return nil, err
	}
	return s.interleaveLayers(ctx, workspace, lead, base, strings.Fields(commits), tasks)
}

// Apply uses one retry if a terminal commit moves HEAD before the index lock.
func (s *Service) Apply(ctx context.Context, in Request) (Result, error) {
	if in.Workspace == "" || in.Lead == "" || in.Change == "" || in.RequestID == "" || in.Revision < 1 {
		return Result{}, errors.New("workspace, lead, change, request ID and revision are required")
	}
	source, err := s.store.GetRevision(ctx, in.Workspace, in.Change, in.Revision)
	if err != nil {
		return Result{}, err
	}
	if err := review.RequireVerdict(ctx, s.store, in.Workspace, in.Change, in.Revision, source.HeadSHA, "apply", in.Lead); err != nil {
		return Result{}, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		old, err := git(ctx, s.runner, "rev-parse", "HEAD")
		if err != nil {
			return Result{}, err
		}
		trial, err := replay.New(s.repo).TrialMerge(ctx, source.BaseSHA, source.HeadSHA, old)
		if err != nil {
			return Result{}, err
		}
		if trial.ConflictCommit != "" {
			return Result{ConflictCommit: trial.ConflictCommit, Paths: trial.ConflictingPaths},
				loomgit.NewError(loomgit.Conflict, strings.Join(trial.ConflictingPaths, ", "), nil)
		}
		var result Result
		err = s.repo.WithLock(ctx, func(ctx context.Context) error {
			var swapErr error
			result, swapErr = s.swap(ctx, in, source, old, trial)
			return swapErr
		})
		if errors.Is(err, errHeadMoved) && attempt == 0 {
			continue
		}
		if errors.Is(err, errHeadMoved) {
			err = loomgit.NewError(loomgit.Stale, "working area moved twice", err)
		}
		return result, err
	}
	return Result{}, loomgit.NewError(loomgit.Stale, "working area moved twice", nil)
}

var errHeadMoved = errors.New("working area HEAD moved")

func (s *Service) swap(ctx context.Context, in Request, source loomgit.Revision, old string, trial replay.Result) (Result, error) {
	branch, err := git(ctx, s.runner, "symbolic-ref", "HEAD")
	if err != nil {
		return Result{}, err
	}
	want, err := refname.InteractiveBranch(in.Workspace, in.Lead)
	if err != nil {
		return Result{}, err
	}
	if branch != "refs/heads/"+want {
		return Result{}, loomgit.NewError(loomgit.Stale, "working area branch differs from lead", nil)
	}
	indexPath, err := git(ctx, s.runner, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return Result{}, err
	}
	if s.beforeIndexLock != nil {
		s.beforeIndexLock()
	}
	lock, err := os.OpenFile(indexPath+".lock", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600) //nolint:gosec // Git resolves the checkout index; O_EXCL reserves its lock.
	if errors.Is(err, os.ErrExist) {
		return Result{}, loomgit.NewError(loomgit.SwapHeld, "working area index is locked", err)
	}
	if err != nil {
		return Result{}, err
	}
	lockOwned := true
	keepLock := false
	defer func() {
		_ = lock.Close()
		if lockOwned && !keepLock {
			_ = os.Remove(indexPath + ".lock")
		}
	}()
	if s.onIndexLocked != nil {
		s.onIndexLocked()
	}
	result, swapErr := s.swapLocked(ctx, in, source, old, trial, branch, indexPath, &lockOwned, &keepLock)
	return result, swapErr
}

func (s *Service) swapLocked(ctx context.Context, in Request, source loomgit.Revision, old string,
	trial replay.Result, branch, indexPath string, lockOwned, keepLock *bool) (Result, error) {
	actual, err := git(ctx, s.runner, "rev-parse", "HEAD")
	if err != nil || actual != old {
		return Result{}, errors.Join(errHeadMoved, err)
	}
	paths, err := s.pendingPaths(ctx, old, trial.HeadSHA)
	if err != nil {
		return Result{}, err
	}
	if len(paths) != 0 {
		return Result{Paths: paths}, loomgit.NewError(loomgit.ApplyPending, strings.Join(paths, ", "), nil)
	}
	result := Result{HeadSHA: trial.HeadSHA, DroppedCommits: trial.DroppedCommits}
	if old != source.BaseSHA {
		result.Derived, err = changeset.RecordDerived(ctx, s.store, s.runner, changeset.DerivedInput{
			Workspace: in.Workspace, Change: in.Change, RequestID: in.RequestID + ":derived",
			FromNumber: source.Number, Operation: "apply", BaseSHA: old,
			HeadSHA: trial.HeadSHA, Outcome: source.Outcome,
		})
		if err != nil {
			return Result{}, err
		}
	}
	if err := s.recordLayer(ctx, in, source, old, trial, result.Derived); err != nil {
		return Result{}, err
	}
	if err := s.install(ctx, branch, indexPath, old, trial.HeadSHA, in.RequestID, lockOwned, keepLock); err != nil {
		return result, err
	}
	if result.Derived.Number != 0 && len(trial.DroppedCommits) == 0 {
		_, _, err = review.CarryForward(ctx, s.store, s.runner, source, result.Derived, trial)
	}
	return result, err
}

func (s *Service) recordLayer(ctx context.Context, in Request, source loomgit.Revision, old string,
	trial replay.Result, derived loomgit.Revision) error {
	commits, err := git(ctx, s.runner, "rev-list", "--reverse", "--first-parent", old+".."+trial.HeadSHA)
	if err != nil {
		return err
	}
	layer := loomgit.AppliedLayer{RequestID: in.RequestID, Workspace: in.Workspace, Lead: in.Lead,
		Change: in.Change, Revision: source.Number, OldTip: old, NewTip: trial.HeadSHA,
		Commits: strings.Fields(commits), DroppedCommits: trial.DroppedCommits}
	if derived.Number != 0 {
		layer.Revision = derived.Number
	}
	for _, sha := range layer.Commits {
		attribution, err := s.attribute(ctx, sha, in.Change)
		if err != nil {
			return err
		}
		layer.CommitDetails = append(layer.CommitDetails, attribution)
	}
	return s.store.SaveApplied(ctx, layer)
}

func (s *Service) attribute(ctx context.Context, sha, change string) (loomgit.AppliedCommit, error) {
	message, err := git(ctx, s.runner, "show", "-s", "--format=%B", sha)
	if err != nil {
		return loomgit.AppliedCommit{}, err
	}
	a := loomgit.AppliedCommit{SHA: sha, Change: change}
	for _, line := range strings.Split(message, "\n") {
		key, value, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		switch key {
		case "Loom-Change-Id":
			a.Change = value
		case "Loom-Revision":
			a.Revision = value
		case "Loom-Task":
			a.Task = value
		case "Loom-Attempt":
			a.Attempt = value
		}
	}
	return a, nil
}

func (s *Service) pendingPaths(ctx context.Context, old, next string) ([]string, error) {
	changed, err := s.runner.Run(ctx, "diff", "--name-only", "-z", old, next)
	if err != nil {
		return nil, err
	}
	dirty, err := s.runner.Run(ctx, "diff", "--name-only", "-z", "HEAD")
	if err != nil {
		return nil, err
	}
	untracked, err := s.runner.Run(ctx, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	want := splitPaths(changed)
	var conflict []string
	for _, path := range append(splitPaths(dirty), splitPaths(untracked)...) {
		for _, incoming := range want {
			if path == incoming || strings.HasPrefix(path, incoming+"/") || strings.HasPrefix(incoming, path+"/") {
				conflict = append(conflict, path)
				break
			}
		}
	}
	slices.Sort(conflict)
	return slices.Compact(conflict), nil
}

func splitPaths(data []byte) []string {
	return strings.FieldsFunc(string(data), func(r rune) bool { return r == 0 })
}

func git(ctx context.Context, runner *gitexec.Runner, args ...string) (string, error) {
	out, err := runner.Run(ctx, args...)
	return strings.TrimSpace(string(out)), err
}

func (s *Service) install(ctx context.Context, branch, indexPath, old, next, requestID string, lockOwned, keepLock *bool) error {
	if old == next {
		return s.store.AdvanceApplied(ctx, requestID, "prepared", "done")
	}
	tmpPath, ownerPath := recoveryPaths(indexPath, requestID)
	if err := prepareIndex(indexPath, tmpPath, ownerPath); err != nil {
		return err
	}
	defer func() {
		if !*keepLock {
			_ = os.Remove(tmpPath)
			_ = os.Remove(ownerPath)
		}
	}()
	if err := s.store.AdvanceApplied(ctx, requestID, "prepared", "installing"); err != nil {
		return err
	}
	*keepLock = true
	if _, err := s.runner.RunWithEnv(ctx, map[string]string{"GIT_INDEX_FILE": tmpPath}, "read-tree", "-m", "-u", old, next); err != nil {
		return fmt.Errorf("install working files: %w", err)
	}
	if err := s.store.AdvanceApplied(ctx, requestID, "installing", "files_updated"); err != nil {
		return err
	}
	if err := s.runner.UpdateRef(ctx, branch, next, old); err != nil {
		return err
	}
	if err := s.store.AdvanceApplied(ctx, requestID, "files_updated", "ref_updated"); err != nil {
		return err
	}
	if err := commitIndex(indexPath+".lock", indexPath, tmpPath); err != nil {
		return err
	}
	*lockOwned = false
	if err := s.store.AdvanceApplied(ctx, requestID, "ref_updated", "done"); err != nil {
		return err
	}
	*keepLock = false
	return nil
}

func prepareIndex(indexPath, tmpPath, ownerPath string) error {
	tmp, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600) //nolint:gosec // Path is derived from Git's index path and the request hash.
	if err != nil {
		return err
	}
	_ = tmp.Close()
	prepared := false
	defer func() {
		if !prepared {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := os.Link(indexPath+".lock", ownerPath); err != nil {
		return err
	}
	data, err := os.ReadFile(indexPath) //nolint:gosec // Git resolves the active checkout index path.
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(ownerPath)
		return err
	}
	if err := os.WriteFile(tmpPath, data, 0600); err != nil { //nolint:gosec // The file was created by this operation.
		_ = os.Remove(ownerPath)
		return err
	}
	prepared = true
	return nil
}

func recoveryPaths(indexPath, requestID string) (string, string) {
	key := sha256.Sum256([]byte(requestID))
	base := filepath.Join(filepath.Dir(indexPath), fmt.Sprintf("loom-apply-%x", key[:12]))
	return base + ".index", base + ".owner"
}
