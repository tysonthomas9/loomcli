package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
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
	lock     *os.File
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

// RowsWritten is called after FleetDB and local workspace state are durable.
func (s *Session) RowsWritten(ctx context.Context) error {
	entry, err := s.store.Advance(ctx, s.entry, "rows_written", nil, nil)
	if err == nil {
		s.entry = entry
	}
	return err
}

func (s *Session) checkoutsAdded(ctx context.Context) error {
	entry, err := s.store.Advance(ctx, s.entry, "checkouts_added", nil, nil)
	if err == nil {
		s.entry = entry
	}
	return err
}

func (s *Session) Rollback(ctx context.Context) error {
	if s.complete {
		return errors.New("committed workspace cannot be rolled back")
	}
	if s.entry.Phase == "rows_written" {
		return errors.New("workspace rows are durable; reconcile instead of removing checkouts")
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

func (s *Session) Close() error {
	err := s.store.Close()
	if s.lock != nil {
		err = errors.Join(err, releaseCreationLock(s.lock))
		s.lock = nil
	}
	return err
}

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
			return loomgit.NewError(loomgit.WorkspaceUnsupported, "created before v2, recreate it", nil)
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
		return loomgit.NewError(loomgit.WorkspaceUnsupported, "created before v2, recreate it", nil)
	}
	return nil
}

// Ensure journals the intended source paths, checks every source, and creates
// each lead checkout. The caller commits after FleetDB registration.
func Ensure(ctx context.Context, workspace, trunk, wsDir string, sources []Source) (*Session, error) {
	return EnsureRequest(ctx, workspace, workspace, "", trunk, wsDir, sources)
}

// EnsureRequest persists the intended paths and request identity before base
// resolution, which may fetch and write source repository refs.
func EnsureRequest(ctx context.Context, workspace, name, requestID, trunk, wsDir string, sources []Source) (*Session, error) {
	branch, err := loomgit.InteractiveBranch(workspace, "lead")
	if err != nil {
		return nil, err
	}
	plan := journal.WorkspaceCreation{RequestID: requestID, Kind: "empty", Name: name, Path: wsDir, Trunk: trunk}
	for _, src := range sources {
		plan.Repos = append(plan.Repos, journal.WorkspaceCreationRepo{Name: src.Name, Source: src.Path, Path: filepath.Join(wsDir, src.Name), Branch: branch, Mode: "worktree"})
	}
	s, err := beginSession(ctx, workspace, nil, plan)
	if err != nil {
		return nil, err
	}
	repos, err := s.prepareSources(ctx, workspace, trunk, branch, sources, &plan)
	if err != nil {
		_ = s.Rollback(context.Background())
		_ = s.Close()
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
		if err := addLeadWorktree(ctx, src.Path, path, branch, repos[i].BaseSHA); err != nil {
			// This branch was absent before the add. Git can leave it behind if add
			// fails after creating the ref.
			_, _ = cli.RunGitCommand(src.Path, "branch", "-D", branch)
			_ = s.Rollback(context.Background())
			_ = s.Close()
			return nil, fmt.Errorf("checkout repo %q: %w", src.Name, err)
		}
		s.created = append(s.created, checkout{source: src.Path, path: path, branch: branch})
	}
	if err := s.checkoutsAdded(ctx); err != nil {
		_ = s.Rollback(context.Background())
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

func (s *Session) prepareSources(ctx context.Context, workspace, trunk, branch string, sources []Source, plan *journal.WorkspaceCreation) ([]loomgit.WorkspaceRepo, error) {
	repos := make([]loomgit.WorkspaceRepo, 0, len(sources))
	for i, src := range sources {
		base, err := localworkspace.PrepareWorkspaceBase(src.Path, workspace, "origin", trunk)
		if err != nil {
			return nil, fmt.Errorf("prepare repo %q: %w", src.Name, err)
		}
		repos = append(repos, loomgit.WorkspaceRepo{Workspace: workspace, Repo: src.Name, Trunk: trunk, WorkspaceBranch: branch, BaseSHA: base})
		plan.Repos[i].BaseSHA = base
	}
	if err := s.store.ReplaceWorkspaceCreation(ctx, s.entry, *plan); err != nil {
		return nil, err
	}
	s.repos = repos
	return repos, nil
}

// BeginCloneRequest owns the clone directories before they are created.
func BeginCloneRequest(ctx context.Context, workspace, name, requestID, trunk, wsDir string) (*Session, error) {
	plan := journal.WorkspaceCreation{RequestID: requestID, Kind: "clone", Name: name, Path: wsDir, Trunk: trunk}
	return beginSession(ctx, workspace, nil, plan)
}

// PlanClones records intended clone paths before a clone process can write them.
func (s *Session) PlanClones(ctx context.Context, sources []Source) error {
	plan, err := s.store.WorkspaceCreation(ctx, s.entry)
	if err != nil {
		return err
	}
	workspace := strings.TrimPrefix(s.entry.RequestID, "workspace-create:")
	branch, err := loomgit.InteractiveBranch(workspace, "lead")
	if err != nil {
		return err
	}
	for _, src := range sources {
		plan.Repos = append(plan.Repos, journal.WorkspaceCreationRepo{Name: src.Name, Source: src.Path, Path: src.Path, Branch: branch, Mode: "clone"})
	}
	return s.store.ReplaceWorkspaceCreation(ctx, s.entry, plan)
}

// AdoptClones installs the lead branch after clones are present. The updated
// plan is durable before the first checkout changes a cloned repository.
func (s *Session) AdoptClones(ctx context.Context, sources []Source) error {
	plan, err := s.store.WorkspaceCreation(ctx, s.entry)
	if err != nil {
		return err
	}
	workspace, trunk := strings.TrimPrefix(s.entry.RequestID, "workspace-create:"), plan.Trunk
	branch, err := loomgit.InteractiveBranch(workspace, "lead")
	if err != nil {
		return err
	}
	repos := make([]loomgit.WorkspaceRepo, 0, len(sources))
	for _, src := range sources {
		base, err := localworkspace.PrepareWorkspaceBase(src.Path, workspace, "origin", trunk)
		if err != nil {
			return fmt.Errorf("prepare cloned repo %q: %w", src.Name, err)
		}
		repos = append(repos, loomgit.WorkspaceRepo{Workspace: workspace, Repo: src.Name, Trunk: trunk, WorkspaceBranch: branch, BaseSHA: base})
	}
	for i, src := range sources {
		found := false
		for j := range plan.Repos {
			if plan.Repos[j].Name == src.Name && plan.Repos[j].Mode == "clone" {
				plan.Repos[j].BaseSHA = repos[i].BaseSHA
				found = true
				break
			}
		}
		if !found {
			plan.Repos = append(plan.Repos, journal.WorkspaceCreationRepo{Name: src.Name, Source: src.Path, Path: src.Path, Branch: branch, BaseSHA: repos[i].BaseSHA, Mode: "clone"})
		}
	}
	if err := s.store.ReplaceWorkspaceCreation(ctx, s.entry, plan); err != nil {
		return err
	}
	s.repos = append(s.repos, repos...)
	for i, src := range sources {
		if _, err := cli.RunGitCommand(src.Path, "checkout", "-b", branch, repos[i].BaseSHA); err != nil {
			return fmt.Errorf("open cloned repo %q: %w", src.Name, err)
		}
	}
	if err := s.checkoutsAdded(ctx); err != nil {
		return err
	}
	return nil
}

// AddWorktrees extends the same creation journal with local repository checkouts.
// The full plan is durable before the first worktree is created.
func (s *Session) AddWorktrees(ctx context.Context, workspace, trunk, wsDir string, sources []Source) error {
	if len(sources) == 0 {
		return nil
	}
	plan, err := s.store.WorkspaceCreation(ctx, s.entry)
	if err != nil {
		return err
	}
	branch, err := loomgit.InteractiveBranch(workspace, "lead")
	if err != nil {
		return err
	}
	for _, src := range sources {
		base, err := localworkspace.PrepareWorkspaceBase(src.Path, workspace, "origin", trunk)
		if err != nil {
			return fmt.Errorf("prepare repo %q: %w", src.Name, err)
		}
		s.repos = append(s.repos, loomgit.WorkspaceRepo{Workspace: workspace, Repo: src.Name, Trunk: trunk, WorkspaceBranch: branch, BaseSHA: base})
		plan.Repos = append(plan.Repos, journal.WorkspaceCreationRepo{Name: src.Name, Source: src.Path, Path: filepath.Join(wsDir, src.Name), Branch: branch, BaseSHA: base, Mode: "worktree"})
	}
	if err := s.store.ReplaceWorkspaceCreation(ctx, s.entry, plan); err != nil {
		return err
	}
	for i, src := range sources {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(wsDir, src.Name)
		base := s.repos[len(s.repos)-len(sources)+i].BaseSHA
		if err := addLeadWorktree(ctx, src.Path, path, branch, base); err != nil {
			_, _ = cli.RunGitCommand(src.Path, "branch", "-D", branch)
			return fmt.Errorf("checkout repo %q: %w", src.Name, err)
		}
		s.created = append(s.created, checkout{source: src.Path, path: path, branch: branch})
	}
	return nil
}

func addLeadWorktree(ctx context.Context, source, path, branch, base string) error {
	r, err := gitexec.New(source, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
	if err != nil {
		return err
	}
	_, err = r.Run(ctx, "worktree", "add", path, "-b", branch, base)
	return err
}

// BeginAttach journals new repo records for an existing workspace. The caller
// owns its newly created checkouts until Commit succeeds and aborts on failure.
func BeginAttach(ctx context.Context, workspace, wsDir string, repos []loomgit.WorkspaceRepo) (*Session, error) {
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
	plan := journal.WorkspaceCreation{Name: workspace, Path: wsDir, Trunk: repos[0].Trunk}
	for _, repo := range repos {
		path := filepath.Join(wsDir, repo.Repo)
		plan.Repos = append(plan.Repos, journal.WorkspaceCreationRepo{Name: repo.Repo, Source: path, Path: path, Branch: repo.WorkspaceBranch, BaseSHA: repo.BaseSHA})
	}
	if err := st.SaveWorkspaceCreation(ctx, entry, plan); err != nil {
		_ = st.AbortWorkspace(context.Background(), entry)
		_ = st.Close()
		return nil, err
	}
	return &Session{store: st, entry: entry, repos: repos}, nil
}

func beginSession(ctx context.Context, workspace string, repos []loomgit.WorkspaceRepo, plan journal.WorkspaceCreation) (*Session, error) {
	if err := os.MkdirAll(filepath.Join(config.GetConfigDir(), "loomgit"), 0o700); err != nil {
		return nil, err
	}
	lock, err := acquireCreationLock(workspace)
	if err != nil {
		return nil, err
	}
	st, err := journal.OpenSQLite(filepath.Join(config.GetConfigDir(), "loomgit", "store.db"))
	if err != nil {
		_ = releaseCreationLock(lock)
		return nil, err
	}
	entry, created, err := st.Begin(ctx, "workspace-create:"+workspace, "ensure_workspace")
	if err != nil || !created {
		_ = st.Close()
		_ = releaseCreationLock(lock)
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("workspace %q has an existing creation journal entry", workspace)
	}
	if err := st.SaveWorkspaceCreation(ctx, entry, plan); err != nil {
		_ = st.AbortWorkspace(context.Background(), entry)
		_ = st.Close()
		_ = releaseCreationLock(lock)
		return nil, err
	}
	return &Session{store: st, lock: lock, entry: entry, repos: repos}, nil
}
