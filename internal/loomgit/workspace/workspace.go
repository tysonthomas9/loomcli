package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

type Source struct {
	Name, Path string
}

type checkout struct {
	source, path, branch string
}

// Session owns the new checkouts until their records are committed with the
// journal entry. The caller rolls it back if FleetDB registration fails.
type Session struct {
	store    *journal.SQLite
	entry    loomgit.JournalEntry
	created  []checkout
	repos    []loomgit.WorkspaceRepo
	complete bool
}

func (s *Session) Repos() []loomgit.WorkspaceRepo {
	return append([]loomgit.WorkspaceRepo(nil), s.repos...)
}

func (s *Session) Commit(ctx context.Context) error {
	if err := s.store.CommitWorkspace(ctx, s.entry, s.repos); err != nil {
		return err
	}
	s.complete = true
	return nil
}

func (s *Session) Rollback(ctx context.Context) error {
	if s.complete {
		return errors.New("committed workspace cannot be rolled back")
	}
	var errs []error
	for i := len(s.created) - 1; i >= 0; i-- {
		c := s.created[i]
		if _, err := cli.RunGitCommand(c.source, "worktree", "remove", c.path); err != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", c.path, err))
		}
		if _, err := cli.RunGitCommand(c.source, "branch", "-D", c.branch); err != nil {
			errs = append(errs, fmt.Errorf("remove branch %s: %w", c.branch, err))
		}
	}
	if err := s.store.AbortWorkspace(ctx, s.entry); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (s *Session) Close() error { return s.store.Close() }

// Records returns the committed local repo records for a workspace.
func Records(ctx context.Context, workspace string) ([]loomgit.WorkspaceRepo, error) {
	path := filepath.Join(config.GetConfigDir(), "loomgit", "store.db")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	st, err := journal.OpenSQLite(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = st.Close() }()
	return st.WorkspaceRepos(ctx, workspace)
}

// CheckSupported refuses a workspace created before the v2 journal and
// working-area records existed. It performs no write on this path.
func CheckSupported(ctx context.Context, workspace string) error {
	path := filepath.Join(config.GetConfigDir(), "loomgit", "store.db")
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return loomgit.NewError(loomgit.Code("workspace_unsupported"), "created before v2, recreate it", nil)
		}
		return err
	}
	st, err := journal.OpenSQLite(path)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	entry, err := st.Get(ctx, "workspace-create:"+workspace)
	if err != nil || entry.Phase != "done" {
		return loomgit.NewError(loomgit.Code("workspace_unsupported"), "created before v2, recreate it", nil)
	}
	return nil
}

// Ensure checks every source before creating a checkout. It then records a
// started journal entry, creates each lead checkout, and returns a session for
// the caller to commit after registering the workspace in FleetDB.
func Ensure(ctx context.Context, workspace, trunk, wsDir string, sources []Source) (*Session, error) {
	branch, err := loomgit.InteractiveBranch(workspace, "lead")
	if err != nil {
		return nil, err
	}
	repos := make([]loomgit.WorkspaceRepo, 0, len(sources))
	for _, src := range sources {
		base, err := localworkspace.PrepareWorkspaceBase(src.Path, workspace, "origin", trunk)
		if err != nil {
			return nil, fmt.Errorf("prepare repo %q: %w", src.Name, err)
		}
		repos = append(repos, loomgit.WorkspaceRepo{Workspace: workspace, Repo: src.Name, Trunk: trunk, WorkspaceBranch: branch, BaseSHA: base})
	}
	s, err := beginSession(ctx, workspace, repos)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		_ = s.Rollback(context.Background())
		_ = s.Close()
		return nil, err
	}
	for i, src := range sources {
		if err := ctx.Err(); err != nil {
			_ = s.Rollback(context.Background())
			_ = s.Close()
			return nil, err
		}
		path := filepath.Join(wsDir, src.Name)
		if _, err := cli.RunGitCommand(src.Path, "worktree", "add", path, "-b", branch, repos[i].BaseSHA); err != nil {
			// This branch was absent before the add. Git can leave it behind if add
			// fails after creating the ref.
			_, _ = cli.RunGitCommand(src.Path, "branch", "-D", branch)
			_ = s.Rollback(context.Background())
			_ = s.Close()
			return nil, fmt.Errorf("checkout repo %q: %w", src.Name, err)
		}
		s.created = append(s.created, checkout{source: src.Path, path: path, branch: branch})
	}
	return s, nil
}

// AdoptClones opens the default lead's branch inside freshly cloned repos.
// The caller owns clone directories and removes them if this returns an error.
func AdoptClones(ctx context.Context, workspace, trunk string, sources []Source) (*Session, error) {
	branch, err := loomgit.InteractiveBranch(workspace, "lead")
	if err != nil {
		return nil, err
	}
	repos := make([]loomgit.WorkspaceRepo, 0, len(sources))
	for _, src := range sources {
		base, err := localworkspace.PrepareWorkspaceBase(src.Path, workspace, "origin", trunk)
		if err != nil {
			return nil, fmt.Errorf("prepare cloned repo %q: %w", src.Name, err)
		}
		repos = append(repos, loomgit.WorkspaceRepo{Workspace: workspace, Repo: src.Name, Trunk: trunk, WorkspaceBranch: branch, BaseSHA: base})
	}
	s, err := beginSession(ctx, workspace, repos)
	if err != nil {
		return nil, err
	}
	for i, src := range sources {
		if _, err := cli.RunGitCommand(src.Path, "checkout", "-b", branch, repos[i].BaseSHA); err != nil {
			_ = s.Rollback(context.Background())
			_ = s.Close()
			return nil, fmt.Errorf("open cloned repo %q: %w", src.Name, err)
		}
	}
	return s, nil
}

// BeginAttach journals new repo records for an existing workspace. The caller
// owns its newly created checkouts until Commit succeeds and aborts on failure.
func BeginAttach(ctx context.Context, workspace string, repos []loomgit.WorkspaceRepo) (*Session, error) {
	if len(repos) == 0 {
		return nil, errors.New("attach requires at least one repo")
	}
	if err := os.MkdirAll(filepath.Join(config.GetConfigDir(), "loomgit"), 0o700); err != nil {
		return nil, err
	}
	st, err := journal.OpenSQLite(filepath.Join(config.GetConfigDir(), "loomgit", "store.db"))
	if err != nil {
		return nil, err
	}
	// Repo names are unique within a workspace, so the first repo identifies
	// this attachment even when the request is retried after an abort.
	requestID := "workspace-add:" + workspace + ":" + repos[0].Repo
	entry, created, err := st.Begin(ctx, requestID, "attach_workspace_repos")
	if err != nil || !created {
		_ = st.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("workspace repo %q has an existing attachment journal entry", repos[0].Repo)
	}
	return &Session{store: st, entry: entry, repos: repos}, nil
}

func beginSession(ctx context.Context, workspace string, repos []loomgit.WorkspaceRepo) (*Session, error) {
	if err := os.MkdirAll(filepath.Join(config.GetConfigDir(), "loomgit"), 0o700); err != nil {
		return nil, err
	}
	st, err := journal.OpenSQLite(filepath.Join(config.GetConfigDir(), "loomgit", "store.db"))
	if err != nil {
		return nil, err
	}
	entry, created, err := st.Begin(ctx, "workspace-create:"+workspace, "ensure_workspace")
	if err != nil || !created {
		_ = st.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("workspace %q has an existing creation journal entry", workspace)
	}
	return &Session{store: st, entry: entry, repos: repos}, nil
}
