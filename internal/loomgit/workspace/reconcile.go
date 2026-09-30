package workspace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

type Creation = journal.WorkspaceCreation

// Recovery holds the journal fence while serve adopts an interrupted creation.
type Recovery struct {
	*Session
	Plan      Creation
	unplanned bool
}

func (r *Recovery) Workspace() string {
	if r.entry.Operation == "attach_workspace_repos" {
		parts := strings.SplitN(strings.TrimPrefix(r.entry.RequestID, "workspace-add:"), ":", 2)
		return parts[0]
	}
	return strings.TrimPrefix(r.entry.RequestID, "workspace-create:")
}
func (r *Recovery) Phase() string   { return r.entry.Phase }
func (r *Recovery) IsAttach() bool  { return r.entry.Operation == "attach_workspace_repos" }
func (r *Recovery) Unplanned() bool { return r.unplanned }

// DiscardMissingAttachment clears an attachment journal after its workspace was
// deleted. A surviving checkout needs manual recovery and keeps journal ownership.
func (r *Recovery) DiscardMissingAttachment(ctx context.Context) error {
	if !r.IsAttach() {
		return errors.New("not an attachment recovery")
	}
	for _, repo := range r.Plan.Repos {
		if _, err := os.Lstat(repo.Path); err == nil {
			return fmt.Errorf("deleted workspace has an attached checkout requiring recovery: %s", repo.Path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return r.store.AbortWorkspace(ctx, r.entry)
}

func OpenCreations(ctx context.Context) ([]*Recovery, error) {
	path := filepath.Join(config.GetConfigDir(), "loomgit", "store.db")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	st, err := journal.OpenSQLite(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = st.Close() }()
	entries, err := st.OpenEntries(ctx)
	if err != nil {
		return nil, err
	}
	var recoveries []*Recovery
	for _, entry := range entries {
		if entry.Operation != "ensure_workspace" && entry.Operation != "attach_workspace_repos" {
			continue
		}
		recovery, err := openRecovery(ctx, st, path, entry)
		if err != nil {
			return nil, err
		}
		if recovery != nil {
			recoveries = append(recoveries, recovery)
		}
	}
	return recoveries, nil
}

// CheckOpenEntries refuses journal operations without a recovery handler. It
// leaves their fences and files untouched for explicit repair.
func CheckOpenEntries(ctx context.Context) error {
	path := filepath.Join(config.GetConfigDir(), "loomgit", "store.db")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	st, err := journal.OpenSQLite(path)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	entries, err := st.OpenEntries(ctx)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Operation != "ensure_workspace" && entry.Operation != "attach_workspace_repos" {
			return loomgit.NewError(loomgit.AttentionRequired,
				fmt.Sprintf("journal request %q has no recovery handler for %q", entry.RequestID, entry.Operation), nil)
		}
	}
	return nil
}

func openRecovery(ctx context.Context, st *journal.SQLite, path string, entry loomgit.JournalEntry) (*Recovery, error) {
	plan, err := st.WorkspaceCreation(ctx, entry)
	unplanned := errors.Is(err, sql.ErrNoRows)
	if err != nil && !unplanned {
		return nil, err
	}
	entry, err = st.Takeover(ctx, entry)
	if err != nil {
		return nil, err
	}
	owned, err := journal.OpenSQLite(path)
	if err != nil {
		return nil, err
	}
	repos := make([]loomgit.WorkspaceRepo, 0, len(plan.Repos))
	workspace := strings.TrimPrefix(entry.RequestID, "workspace-create:")
	if entry.Operation == "attach_workspace_repos" {
		workspace = strings.SplitN(strings.TrimPrefix(entry.RequestID, "workspace-add:"), ":", 2)[0]
	}
	if unplanned {
		// Older entries have no plan. Keep their journal ownership and ask for
		// repair; deleting them could strand a worktree created before a crash.
		plan.Name = workspace
	}
	for _, repo := range plan.Repos {
		repos = append(repos, loomgit.WorkspaceRepo{Workspace: workspace, Repo: repo.Name, Trunk: plan.Trunk, WorkspaceBranch: repo.Branch, BaseSHA: repo.BaseSHA})
	}
	return &Recovery{Session: &Session{store: owned, entry: entry, repos: repos}, Plan: plan, unplanned: unplanned}, nil
}

// ResumeCheckouts adopts verified linked worktrees or finishes missing ones.
// Once remote rows were written, a missing checkout requires attention instead.
func (r *Recovery) ResumeCheckouts(ctx context.Context) error {
	for _, repo := range r.Plan.Repos {
		if info, err := os.Lstat(repo.Path); err == nil {
			if !info.IsDir() {
				return fmt.Errorf("checkout path is not a directory: %s", repo.Path)
			}
			if err := verifyCheckout(repo); err != nil {
				return err
			}
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if r.entry.Phase == "rows_written" {
			return fmt.Errorf("workspace checkout missing after rows written: %s", repo.Path)
		}
		if repo.Path == repo.Source {
			return fmt.Errorf("cloned workspace checkout missing: %s", repo.Path)
		}
		if _, err := cli.RunGitCommand(repo.Source, "show-ref", "--verify", "refs/heads/"+repo.Branch); err == nil {
			return fmt.Errorf("workspace branch exists without checkout: %s", repo.Branch)
		}
		if err := os.MkdirAll(filepath.Dir(repo.Path), 0o755); err != nil {
			return err
		}
		if _, err := cli.RunGitCommand(repo.Source, "worktree", "add", repo.Path, "-b", repo.Branch, repo.BaseSHA); err != nil {
			return err
		}
	}
	if r.entry.Phase == "started" && !r.IsAttach() {
		return r.checkoutsAdded(ctx)
	}
	return nil
}

func verifyCheckout(repo journal.WorkspaceCreationRepo) error {
	gitPath := filepath.Join(repo.Path, ".git")
	if _, err := os.Lstat(gitPath); err != nil {
		return fmt.Errorf("workspace checkout has no git directory: %s: %w", repo.Path, err)
	}
	top, err := cli.RunGitCommand(repo.Path, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	resolvedTop, topErr := filepath.EvalSymlinks(strings.TrimSpace(top))
	resolvedPath, pathErr := filepath.EvalSymlinks(repo.Path)
	if topErr != nil || pathErr != nil || filepath.Clean(resolvedTop) != filepath.Clean(resolvedPath) {
		return fmt.Errorf("workspace checkout root mismatch: %s", repo.Path)
	}
	branch, err := cli.RunGitCommand(repo.Path, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || strings.TrimSpace(branch) != repo.Branch {
		return fmt.Errorf("workspace checkout branch mismatch: %s", repo.Path)
	}
	common, err := cli.RunGitCommand(repo.Path, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	sourceCommon, err := cli.RunGitCommand(repo.Source, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil || strings.TrimSpace(common) != strings.TrimSpace(sourceCommon) {
		return fmt.Errorf("workspace checkout source mismatch: %s", repo.Path)
	}
	return nil
}

// ReplayResult returns the committed workspace only for the same request ID.
func ReplayResult(ctx context.Context, workspace, requestID string) (Creation, bool, error) {
	if requestID == "" {
		return Creation{}, false, nil
	}
	path := filepath.Join(config.GetConfigDir(), "loomgit", "store.db")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return Creation{}, false, nil
	} else if err != nil {
		return Creation{}, false, err
	}
	st, err := journal.OpenSQLite(path)
	if err != nil {
		return Creation{}, false, err
	}
	defer func() { _ = st.Close() }()
	entry, err := st.Get(ctx, "workspace-create:"+workspace)
	if errors.Is(err, journal.ErrNotFound) {
		return Creation{}, false, nil
	}
	if err != nil {
		return Creation{}, false, err
	}
	plan, err := st.WorkspaceCreation(ctx, entry)
	if err != nil {
		return Creation{}, false, err
	}
	return plan, entry.Phase == "done" && plan.RequestID == requestID, nil
}
