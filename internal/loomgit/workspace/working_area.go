package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

// WorkingAreaSource identifies a workspace repo and its local source checkout.
// Mode is "worktree" or "clone"; empty means worktree.
type WorkingAreaSource struct{ Name, Path, Mode string }

// EnsureWorkingArea opens one lasting checkout per selected repo for an
// interactive agent. Existing areas are reused; failure never returns a shared
// checkout for the caller to run in.
func EnsureWorkingArea(ctx context.Context, workspace, lead, wsDir string, sources []WorkingAreaSource) ([]journal.WorkingArea, error) {
	branch, err := loomgit.InteractiveBranch(workspace, lead)
	if err != nil {
		return nil, err
	}
	if len(sources) == 0 || wsDir == "" {
		return nil, errors.New("working area needs a workspace path and repos")
	}
	st, err := journal.OpenSQLite(filepath.Join(config.GetConfigDir(), "loomgit", "store.db"))
	if err != nil {
		return nil, err
	}
	defer func() { _ = st.Close() }()
	records, err := st.WorkspaceRepos(ctx, workspace)
	if err != nil {
		return nil, err
	}
	prior, err := st.WorkingAreas(ctx, workspace, lead)
	if err != nil {
		return nil, err
	}
	if len(prior) > 0 {
		return verifyWorkingAreas(prior, sources, branch)
	}
	byRepo := workspaceReposByName(records)
	areas := make([]journal.WorkingArea, 0, len(sources))
	created := make([]journal.WorkingArea, 0, len(sources))
	for _, src := range sources {
		record, ok := byRepo[src.Name]
		if !ok || src.Path == "" {
			rollbackWorkingAreas(created, sources)
			return nil, loomgit.NewError(loomgit.TaskCopyCreateFailed, "unknown working area repo "+src.Name, nil)
		}
		a, err := openWorkingArea(ctx, workspace, lead, branch, wsDir, src, record)
		if err != nil {
			rollbackWorkingAreas(created, sources)
			return nil, loomgit.NewError(loomgit.TaskCopyCreateFailed, fmt.Sprintf("create working area for %s", src.Name), err)
		}
		if a.Path != src.Path {
			created = append(created, a)
		}
		areas = append(areas, a)
	}
	if err := st.SaveWorkingAreas(ctx, areas); err != nil {
		rollbackWorkingAreas(created, sources)
		return nil, loomgit.NewError(loomgit.TaskCopyCreateFailed, "record working areas", err)
	}
	return areas, nil
}

func workspaceReposByName(records []loomgit.WorkspaceRepo) map[string]loomgit.WorkspaceRepo {
	byRepo := make(map[string]loomgit.WorkspaceRepo, len(records))
	for _, record := range records {
		byRepo[record.Repo] = record
	}
	return byRepo
}

func verifyWorkingAreas(prior []journal.WorkingArea, sources []WorkingAreaSource, branch string) ([]journal.WorkingArea, error) {
	if len(prior) != len(sources) {
		return nil, loomgit.NewError(loomgit.TaskCopyCreateFailed, "working area repo set changed", nil)
	}
	wanted := make(map[string]bool, len(sources))
	for _, src := range sources {
		if wanted[src.Name] {
			return nil, loomgit.NewError(loomgit.TaskCopyCreateFailed, "duplicate working area repo", nil)
		}
		wanted[src.Name] = true
	}
	for _, a := range prior {
		if !wanted[a.Repo] || a.Branch != branch {
			return nil, loomgit.NewError(loomgit.TaskCopyCreateFailed, "working area branch changed", nil)
		}
		if _, err := os.Stat(filepath.Join(a.Path, ".git")); err != nil {
			return nil, loomgit.NewError(loomgit.TaskCopyCreateFailed, "working area missing", err)
		}
	}
	return prior, nil
}

func openWorkingArea(ctx context.Context, workspace, lead, branch, wsDir string, src WorkingAreaSource, record loomgit.WorkspaceRepo) (journal.WorkingArea, error) {
	mode := src.Mode
	if mode == "" {
		mode = "worktree"
	}
	if mode != "worktree" && mode != "clone" {
		return journal.WorkingArea{}, errors.New("unsupported working area mode")
	}
	path := filepath.Join(wsDir, "worktrees", src.Name, lead)
	if lead == "lead" && record.WorkspaceBranch == branch {
		path = src.Path
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return journal.WorkingArea{}, err
	}
	a := journal.WorkingArea{Workspace: workspace, Lead: lead, Repo: src.Name, Path: path, Branch: branch, BaseSHA: record.BaseSHA, Mode: mode}
	if path == src.Path {
		return a, nil
	}
	r, err := gitexec.New(src.Path, workingAreaGitOptions())
	if err != nil {
		return journal.WorkingArea{}, err
	}
	if mode == "worktree" {
		_, err = r.Run(ctx, "worktree", "add", "-b", branch, path, record.BaseSHA)
	} else {
		_, err = r.Run(ctx, "clone", "--local", "--no-checkout", src.Path, path)
		if err == nil {
			var clone *gitexec.Runner
			clone, err = gitexec.New(path, workingAreaGitOptions())
			if err == nil {
				_, err = clone.Run(ctx, "checkout", "-b", branch, record.BaseSHA)
			}
		}
	}
	return a, err
}

func rollbackWorkingAreas(created []journal.WorkingArea, sources []WorkingAreaSource) {
	for i := len(created) - 1; i >= 0; i-- {
		a := created[i]
		if a.Mode != "worktree" {
			continue
		}
		for _, src := range sources {
			if src.Name == a.Repo {
				r, err := gitexec.New(src.Path, workingAreaGitOptions())
				if err == nil {
					_, _ = r.Run(context.Background(), "worktree", "remove", "--force", a.Path)
					_, _ = r.Run(context.Background(), "branch", "-D", a.Branch)
				}
				break
			}
		}
	}
}

func workingAreaGitOptions() gitexec.Options {
	return gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}}
}
