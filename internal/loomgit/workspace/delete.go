package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit/agentcapture"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/pool"
)

// DeleteItem is one piece of work the user must see before confirmation.
type DeleteItem struct {
	Repo   string `json:"repo"`
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Detail string `json:"detail,omitempty"`
	Size   int64  `json:"size,omitempty"`
}

type DeletePreview struct {
	Items       []DeleteItem `json:"items"`
	Fingerprint string       `json:"fingerprint"`
}

type deleteCopy struct {
	repo, path, source string
	clone              bool
}

// ErrUnsavedWork means a missing or stale confirmation. The preview is carried
// with the error so both CLI and HTTP callers can show the exact new list.
type ErrUnsavedWork struct{ Preview DeletePreview }

func (e *ErrUnsavedWork) Error() string {
	return "unsaved_work: workspace deletion requires confirmation of the current work list"
}

func (e *ErrUnsavedWork) UnsavedWork() bool { return true }

func deleteRunner(path string) (*gitexec.Runner, error) {
	return gitexec.New(path, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
}

func within(root, path string) bool {
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func samePath(a, b string) bool {
	if resolved, err := filepath.EvalSymlinks(a); err == nil {
		a = resolved
	}
	if resolved, err := filepath.EvalSymlinks(b); err == nil {
		b = resolved
	}
	return a == b
}

// DryRun inventories every registered worktree under the workspace root and
// every unpushed clone branch. It makes no capture or filesystem changes.
func DryRun(ctx context.Context, ws config.WorkspaceConfig) (DeletePreview, error) {
	preview, _, err := inventory(ctx, ws)
	return preview, err
}

func inventory(ctx context.Context, ws config.WorkspaceConfig) (DeletePreview, []deleteCopy, error) {
	items := make([]DeleteItem, 0)
	var copies []deleteCopy
	seen := map[string]bool{}
	for _, repo := range ws.Repos {
		repoItems, repoCopies, err := inspectDeleteRepo(ctx, ws.Path, repo, seen)
		if err != nil {
			return DeletePreview{}, nil, err
		}
		items = append(items, repoItems...)
		copies = append(copies, repoCopies...)
	}
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		return a.Repo+"\x00"+a.Path+"\x00"+a.Kind+"\x00"+a.Detail < b.Repo+"\x00"+b.Path+"\x00"+b.Kind+"\x00"+b.Detail
	})
	sort.Slice(copies, func(i, j int) bool {
		if len(copies[i].path) != len(copies[j].path) {
			return len(copies[i].path) > len(copies[j].path)
		}
		return copies[i].path < copies[j].path
	})
	encoded, err := json.Marshal(items)
	if err != nil {
		return DeletePreview{}, nil, err
	}
	sum := sha256.Sum256(encoded)
	return DeletePreview{Items: items, Fingerprint: hex.EncodeToString(sum[:])}, copies, nil
}

func inspectDeleteRepo(ctx context.Context, wsPath string, repo config.RepoConfig, seen map[string]bool) ([]DeleteItem, []deleteCopy, error) {
	path := repo.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(wsPath, path)
	}
	path = filepath.Clean(path)
	if !within(wsPath, path) {
		return nil, nil, fmt.Errorf("repo %q escapes workspace", repo.Name)
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	} else if err != nil {
		return nil, nil, err
	}
	runner, err := deleteRunner(path)
	if err != nil {
		return nil, nil, err
	}
	common, err := runner.Run(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, nil, err
	}
	commonPath := strings.TrimSpace(string(common))
	clone := within(path, commonPath)
	source := filepath.Dir(commonPath)
	if clone {
		source = path
	}
	items, copies, err := inspectRepoWorktrees(ctx, runner, wsPath, repo.Name, path, source, clone, seen)
	if err != nil {
		return nil, nil, err
	}
	if clone {
		branches, err := inspectCloneBranches(ctx, runner, repo.Name, path)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, branches...)
	}
	return items, copies, nil
}

func inspectRepoWorktrees(ctx context.Context, runner *gitexec.Runner, wsPath, repoName, path, source string, clone bool, seen map[string]bool) ([]DeleteItem, []deleteCopy, error) {
	listed, err := runner.Run(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, nil, err
	}
	var items []DeleteItem
	var copies []deleteCopy
	for _, line := range strings.Split(string(listed), "\n") {
		if !strings.HasPrefix(line, "worktree ") {
			continue
		}
		copyPath := filepath.Clean(strings.TrimPrefix(line, "worktree "))
		if clone && !within(wsPath, copyPath) {
			return nil, nil, fmt.Errorf("clone %q has a worktree outside this workspace: %s", repoName, copyPath)
		}
		if !within(wsPath, copyPath) || seen[copyPath] {
			continue
		}
		seen[copyPath] = true
		copies = append(copies, deleteCopy{repo: repoName, path: copyPath, source: source, clone: clone && samePath(copyPath, path)})
		copyItems, err := inspectDeleteCopy(ctx, repoName, copyPath)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, copyItems...)
	}
	return items, copies, nil
}

func inspectDeleteCopy(ctx context.Context, repo, path string) ([]DeleteItem, error) {
	var items []DeleteItem
	if _, err := os.Stat(filepath.Join(path, ".agent.lock")); err == nil {
		items = append(items, DeleteItem{Repo: repo, Path: path, Kind: "running_agent"})
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	runner, err := deleteRunner(path)
	if err != nil {
		return nil, err
	}
	status, err := runner.Run(ctx, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	for _, row := range strings.Split(strings.TrimSuffix(string(status), "\n"), "\n") {
		if row != "" {
			items = append(items, DeleteItem{Repo: repo, Path: path, Kind: "file", Detail: row})
		}
	}
	ignored, err := agentcapture.ListIgnored(ctx, path)
	if err != nil {
		return nil, err
	}
	for _, entry := range ignored {
		items = append(items, DeleteItem{Repo: repo, Path: filepath.Join(path, entry.Path), Kind: "ignored", Detail: entry.Class, Size: entry.Size})
	}
	head, err := runner.Run(ctx, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	items = append(items, DeleteItem{Repo: repo, Path: path, Kind: "head", Detail: strings.TrimSpace(string(head))})
	unpushed, err := runner.Run(ctx, "rev-list", "--count", "HEAD", "--not", "--remotes")
	if err != nil {
		return nil, err
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(unpushed)))
	if err != nil {
		return nil, err
	}
	if count > 0 {
		items = append(items, DeleteItem{Repo: repo, Path: path, Kind: "unpushed", Detail: strconv.Itoa(count)})
	}
	return items, nil
}

func inspectCloneBranches(ctx context.Context, runner *gitexec.Runner, repo, path string) ([]DeleteItem, error) {
	branches, err := runner.Run(ctx, "for-each-ref", "--format=%(refname:short) %(objectname)", "refs/heads")
	if err != nil {
		return nil, err
	}
	var items []DeleteItem
	for _, row := range strings.Split(strings.TrimSpace(string(branches)), "\n") {
		parts := strings.Fields(row)
		if len(parts) != 2 {
			continue
		}
		unpublished, err := runner.Run(ctx, "rev-list", "--count", parts[1], "--not", "--remotes")
		if err != nil {
			return nil, err
		}
		n, err := strconv.Atoi(strings.TrimSpace(string(unpublished)))
		if err != nil {
			return nil, err
		}
		if n > 0 {
			items = append(items, DeleteItem{Repo: repo, Path: path, Kind: "clone_branch", Detail: row})
		}
	}
	return items, nil
}

// DeleteWorkspace refuses a missing or stale fingerprint, captures each copy,
// and calls deleteRows only after all copies and local records are removed.
func DeleteWorkspace(ctx context.Context, ws config.WorkspaceConfig, fingerprint string, deleteRows func(context.Context, string) error) error {
	preview, copies, err := inventory(ctx, ws)
	if err != nil {
		return err
	}
	if fingerprint == "" || fingerprint != preview.Fingerprint {
		return &ErrUnsavedWork{Preview: preview}
	}
	for _, item := range preview.Items {
		if item.Kind == "running_agent" {
			return fmt.Errorf("workspace has running agents at %s", item.Path)
		}
	}
	var failures []error
	for _, copy := range copies {
		if err := deleteOneCopy(ctx, ws.ID, copy); err != nil {
			failures = append(failures, err)
		}
	}
	if len(failures) > 0 {
		return errors.Join(failures...)
	}
	if err := finishLocalDeletion(ctx, ws.ID); err != nil {
		return err
	}
	return deleteRows(ctx, ws.ID)
}

func deleteOneCopy(ctx context.Context, workspace string, copy deleteCopy) error {
	result, err := agentcapture.CaptureWorkingArea(ctx, copy.path, workspace, "lead")
	if err != nil {
		return fmt.Errorf("capture %s: %w", copy.path, err)
	}
	if !result.Complete {
		return fmt.Errorf("incomplete capture at %s", copy.path)
	}
	if copy.clone {
		return removeCapturedClone(ctx, workspace, copy)
	}
	st, err := journal.OpenSQLite(filepath.Join(config.GetConfigDir(), "loomgit", "store.db"))
	if err != nil {
		return err
	}
	p := pool.New(st, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
	repo, err := p.Admit(ctx, copy.source)
	if err == nil {
		err = repo.LinkedWorktree(copy.path).Remove(ctx, pool.CompleteCapture(result.Ref))
	}
	closeErr := st.Close()
	if err != nil {
		return fmt.Errorf("remove %s: %w", copy.path, err)
	}
	return closeErr
}

func removeCapturedClone(ctx context.Context, workspace string, copy deleteCopy) error {
	// A clone owns its Git objects. Preserve all branches and WIP refs in a
	// verified bundle outside the directory before removing the clone.
	if filepath.Base(copy.repo) != copy.repo {
		return fmt.Errorf("invalid repo name %q", copy.repo)
	}
	bundleDir := filepath.Join(config.GetConfigDir(), "loomgit", "deletion-captures", workspace)
	if err := os.MkdirAll(bundleDir, 0700); err != nil {
		return err
	}
	runner, err := deleteRunner(copy.path)
	if err != nil {
		return err
	}
	bundle := filepath.Join(bundleDir, copy.repo+".bundle")
	if _, err := runner.Run(ctx, "bundle", "create", bundle, "--all"); err != nil {
		return err
	}
	if _, err := runner.Run(ctx, "bundle", "verify", bundle); err != nil {
		return err
	}
	return os.RemoveAll(copy.path)
}

func finishLocalDeletion(ctx context.Context, workspace string) error {
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
	err = st.FinishWorkspaceDeletion(ctx, workspace)
	closeErr := st.Close()
	if err != nil {
		return err
	}
	return closeErr
}
