// Package taskroot owns node-local composite roots for TaskRun execution.
package taskroot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const ManifestVersion = 1

var repositoryNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

var ErrStaleLease = errors.New("stale TaskRun Root lease")

// Manager is the lifecycle seam for one composite TaskRun Root.
type Manager interface {
	Prepare(ctx context.Context, spec RootSpec) (RootManifest, error)
	Release(ctx context.Context, lease RootLease, policy RetentionPolicy) error
	Reconcile(ctx context.Context) error
}

type RootSpec struct {
	TaskRunID    string           `json:"task_run_id"`
	Generation   int64            `json:"generation"`
	FencingToken int64            `json:"fencing_token"`
	Repositories []RepositorySpec `json:"repositories"`
}

type RepositorySpec struct {
	Name       string `json:"name"`
	SourcePath string `json:"source_path"`
	BranchName string `json:"branch_name"`
	BaseSHA    string `json:"base_sha"`
}

type RootManifest struct {
	Version      int                  `json:"version"`
	TaskRunID    string               `json:"task_run_id"`
	Generation   int64                `json:"generation"`
	FencingToken int64                `json:"fencing_token"`
	RootPath     string               `json:"root_path"`
	State        string               `json:"state"`
	Repositories []RepositoryManifest `json:"repositories"`
	CreatedAt    time.Time            `json:"created_at"`
}

type RepositoryManifest struct {
	Name       string `json:"name"`
	SourcePath string `json:"source_path"`
	Path       string `json:"path"`
	BranchName string `json:"branch_name"`
	BaseSHA    string `json:"base_sha"`
}

type RootLease struct {
	TaskRunID    string
	Generation   int64
	FencingToken int64
}

type RetentionPolicy struct {
	RetainUntil time.Time
}

type Inventory struct {
	Roots     int `json:"roots"`
	Worktrees int `json:"worktrees"`
}

type LocalGitManager struct {
	workspaceRoot string
}

var _ Manager = (*LocalGitManager)(nil)

func NewLocalGitManager(workspaceRoot string) *LocalGitManager {
	return &LocalGitManager{workspaceRoot: workspaceRoot}
}

func (m *LocalGitManager) Prepare(ctx context.Context, spec RootSpec) (RootManifest, error) {
	rootPath, repositories, err := m.validateAndResolve(spec)
	if err != nil {
		return RootManifest{}, err
	}
	if existing, found, err := matchingManifest(ctx, rootPath, spec, repositories); found || err != nil {
		return existing, err
	}
	if err := os.MkdirAll(rootPath, 0o700); err != nil {
		return RootManifest{}, fmt.Errorf("create TaskRun Root: %w", err)
	}

	manifest := RootManifest{
		Version:      ManifestVersion,
		TaskRunID:    spec.TaskRunID,
		Generation:   spec.Generation,
		FencingToken: spec.FencingToken,
		RootPath:     rootPath,
		State:        "ready",
		Repositories: make([]RepositoryManifest, 0, len(repositories)),
		CreatedAt:    time.Now().UTC(),
	}
	for _, repository := range repositories {
		manifest.Repositories = append(manifest.Repositories, RepositoryManifest{
			Name:       repository.Name,
			SourcePath: repository.SourcePath,
			Path:       filepath.Join(rootPath, repository.Name),
			BranchName: repository.BranchName,
			BaseSHA:    repository.BaseSHA,
		})
	}
	journal := manifest
	journal.State = "provisioning"
	if err := writeManifest(rootPath, ".provisioning.json", journal); err != nil {
		_ = os.RemoveAll(rootPath)
		return RootManifest{}, err
	}
	created := make([]RepositoryManifest, 0, len(repositories))
	rollback := func() {
		for i := len(created) - 1; i >= 0; i-- {
			_, _ = runGit(ctx, created[i].SourcePath, "worktree", "remove", "--force", created[i].Path)
			_, _ = runGit(ctx, created[i].SourcePath, "worktree", "prune")
		}
		_ = os.RemoveAll(rootPath)
	}

	for index, repository := range repositories {
		entry := manifest.Repositories[index]
		if err := addWorktree(ctx, repository, entry.Path); err != nil {
			rollback()
			return RootManifest{}, fmt.Errorf("provision repository %q: %w", repository.Name, err)
		}
		created = append(created, entry)
	}
	if err := publishManifest(rootPath, manifest); err != nil {
		rollback()
		return RootManifest{}, err
	}
	if err := os.Remove(filepath.Join(rootPath, ".provisioning.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		rollback()
		return RootManifest{}, fmt.Errorf("remove TaskRun Root provisioning journal: %w", err)
	}
	return manifest, nil
}

func matchingManifest(ctx context.Context, rootPath string, spec RootSpec, repositories []RepositorySpec) (RootManifest, bool, error) {
	data, err := os.ReadFile(filepath.Join(rootPath, "manifest.json"))
	if errors.Is(err, os.ErrNotExist) {
		return RootManifest{}, false, nil
	}
	if err != nil {
		return RootManifest{}, true, fmt.Errorf("read existing TaskRun Root Manifest: %w", err)
	}
	var manifest RootManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return RootManifest{}, true, fmt.Errorf("decode existing TaskRun Root Manifest: %w", err)
	}
	if manifest.Version != ManifestVersion || manifest.TaskRunID != spec.TaskRunID || manifest.Generation != spec.Generation || manifest.FencingToken != spec.FencingToken {
		return RootManifest{}, true, fmt.Errorf("existing TaskRun Root identity does not match requested lease")
	}
	if len(manifest.Repositories) != len(repositories) {
		return RootManifest{}, true, fmt.Errorf("existing TaskRun Root repository set does not match request")
	}
	for index, requested := range repositories {
		existing := manifest.Repositories[index]
		if existing.Name != requested.Name || existing.SourcePath != requested.SourcePath || existing.BranchName != requested.BranchName || existing.BaseSHA != requested.BaseSHA {
			return RootManifest{}, true, fmt.Errorf("existing TaskRun Root repository %q does not match request", requested.Name)
		}
		if existing.Path != filepath.Join(rootPath, requested.Name) || !pathContains(rootPath, existing.Path) {
			return RootManifest{}, true, fmt.Errorf("existing TaskRun Root repository %q path is invalid", requested.Name)
		}
		if _, err := os.Stat(filepath.Join(existing.Path, ".git")); err != nil {
			return RootManifest{}, true, fmt.Errorf("existing TaskRun Root repository %q is unavailable: %w", requested.Name, err)
		}
		branch, err := runGit(ctx, existing.Path, "branch", "--show-current")
		if err != nil || branch != requested.BranchName {
			return RootManifest{}, true, fmt.Errorf("existing TaskRun Root repository %q branch changed", requested.Name)
		}
	}
	return manifest, true, nil
}

func (m *LocalGitManager) Inventory(_ context.Context) (Inventory, error) {
	root, err := m.taskRootsPath()
	if err != nil {
		return Inventory{}, err
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return Inventory{}, nil
	}
	if err != nil {
		return Inventory{}, fmt.Errorf("read TaskRun Root inventory: %w", err)
	}
	var inventory Inventory
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, entry.Name(), "manifest.json"))
		if err != nil {
			continue
		}
		var manifest RootManifest
		if json.Unmarshal(data, &manifest) != nil {
			continue
		}
		inventory.Roots++
		inventory.Worktrees += len(manifest.Repositories)
	}
	return inventory, nil
}

func (m *LocalGitManager) Release(ctx context.Context, lease RootLease, policy RetentionPolicy) error {
	root, err := m.taskRootsPath()
	if err != nil {
		return err
	}
	if !repositoryNamePattern.MatchString(lease.TaskRunID) {
		return fmt.Errorf("task_run_id %q is not a safe path segment", lease.TaskRunID)
	}
	rootPath := filepath.Join(root, lease.TaskRunID)
	data, err := os.ReadFile(filepath.Join(rootPath, "manifest.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read TaskRun Root Manifest for release: %w", err)
	}
	var manifest RootManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("decode TaskRun Root Manifest for release: %w", err)
	}
	if manifest.TaskRunID != lease.TaskRunID || manifest.Generation != lease.Generation || manifest.FencingToken != lease.FencingToken {
		return ErrStaleLease
	}
	if policy.RetainUntil.After(time.Now()) {
		return nil
	}
	for index := len(manifest.Repositories) - 1; index >= 0; index-- {
		repository := manifest.Repositories[index]
		if !pathContains(rootPath, repository.Path) || repository.Path == rootPath {
			return fmt.Errorf("repository %q path escapes TaskRun Root", repository.Name)
		}
		if _, err := runGit(ctx, repository.SourcePath, "worktree", "remove", "--force", repository.Path); err != nil {
			if _, statErr := os.Stat(repository.Path); !errors.Is(statErr, os.ErrNotExist) {
				return fmt.Errorf("release repository %q: %w", repository.Name, err)
			}
		}
		if _, err := runGit(ctx, repository.SourcePath, "worktree", "prune"); err != nil {
			return fmt.Errorf("prune repository %q worktrees: %w", repository.Name, err)
		}
	}
	if err := os.RemoveAll(rootPath); err != nil {
		return fmt.Errorf("remove TaskRun Root: %w", err)
	}
	return nil
}

func (m *LocalGitManager) Reconcile(ctx context.Context) error {
	root, err := m.taskRootsPath()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read TaskRun Roots for reconciliation: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || !repositoryNamePattern.MatchString(entry.Name()) {
			continue
		}
		rootPath := filepath.Join(root, entry.Name())
		manifestPath := filepath.Join(rootPath, "manifest.json")
		if _, err := os.Stat(manifestPath); err == nil {
			manifest, err := readManifest(manifestPath)
			if err != nil {
				return err
			}
			for _, repository := range manifest.Repositories {
				if _, err := runGit(ctx, repository.SourcePath, "worktree", "prune"); err != nil {
					return fmt.Errorf("reconcile repository %q registrations: %w", repository.Name, err)
				}
			}
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		journalPath := filepath.Join(rootPath, ".provisioning.json")
		journal, err := readManifest(journalPath)
		if errors.Is(err, os.ErrNotExist) {
			if removeErr := os.Remove(rootPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				return fmt.Errorf("remove empty orphan TaskRun Root: %w", removeErr)
			}
			continue
		}
		if err != nil {
			return err
		}
		for index := len(journal.Repositories) - 1; index >= 0; index-- {
			repository := journal.Repositories[index]
			if !pathContains(rootPath, repository.Path) || repository.Path == rootPath {
				return fmt.Errorf("orphan repository %q path escapes TaskRun Root", repository.Name)
			}
			_, _ = runGit(ctx, repository.SourcePath, "worktree", "remove", "--force", repository.Path)
			if _, err := runGit(ctx, repository.SourcePath, "worktree", "prune"); err != nil {
				return fmt.Errorf("prune orphan repository %q registration: %w", repository.Name, err)
			}
		}
		if err := os.RemoveAll(rootPath); err != nil {
			return fmt.Errorf("remove orphan TaskRun Root: %w", err)
		}
	}
	return nil
}

func (m *LocalGitManager) validateAndResolve(spec RootSpec) (string, []RepositorySpec, error) {
	if !repositoryNamePattern.MatchString(spec.TaskRunID) {
		return "", nil, fmt.Errorf("task_run_id %q is not a safe path segment", spec.TaskRunID)
	}
	if spec.Generation < 1 {
		return "", nil, fmt.Errorf("root generation must be positive")
	}
	if spec.FencingToken < 1 {
		return "", nil, fmt.Errorf("root fencing token must be positive")
	}
	if len(spec.Repositories) == 0 {
		return "", nil, fmt.Errorf("repository set must not be empty")
	}
	root, err := m.taskRootsPath()
	if err != nil {
		return "", nil, err
	}
	rootPath := filepath.Join(root, spec.TaskRunID)
	if !pathContains(root, rootPath) {
		return "", nil, fmt.Errorf("TaskRun Root escapes workspace")
	}
	repositories := append([]RepositorySpec(nil), spec.Repositories...)
	sort.Slice(repositories, func(i, j int) bool { return repositories[i].Name < repositories[j].Name })
	seen := make(map[string]struct{}, len(repositories))
	for _, repository := range repositories {
		if !repositoryNamePattern.MatchString(repository.Name) {
			return "", nil, fmt.Errorf("repository name %q is invalid", repository.Name)
		}
		if _, duplicate := seen[repository.Name]; duplicate {
			return "", nil, fmt.Errorf("repository name %q is duplicated", repository.Name)
		}
		seen[repository.Name] = struct{}{}
		if strings.TrimSpace(repository.SourcePath) == "" || strings.TrimSpace(repository.BranchName) == "" || strings.TrimSpace(repository.BaseSHA) == "" {
			return "", nil, fmt.Errorf("repository %q source_path, branch_name, and base_sha are required", repository.Name)
		}
	}
	return rootPath, repositories, nil
}

func (m *LocalGitManager) taskRootsPath() (string, error) {
	workspace, err := filepath.Abs(strings.TrimSpace(m.workspaceRoot))
	if err != nil || strings.TrimSpace(m.workspaceRoot) == "" {
		return "", fmt.Errorf("workspace root is required")
	}
	return filepath.Join(workspace, ".loom", "task-roots"), nil
}

func addWorktree(ctx context.Context, repository RepositorySpec, targetPath string) error {
	if _, err := os.Stat(targetPath); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return fmt.Errorf("target already exists: %s", targetPath)
		}
		return err
	}
	if _, err := runGit(ctx, repository.SourcePath, "rev-parse", "--verify", repository.BaseSHA+"^{commit}"); err != nil {
		return fmt.Errorf("resolve base %s: %w", repository.BaseSHA, err)
	}
	_, branchErr := runGit(ctx, repository.SourcePath, "show-ref", "--verify", "--quiet", "refs/heads/"+repository.BranchName)
	args := []string{"worktree", "add"}
	if branchErr == nil {
		args = append(args, targetPath, repository.BranchName)
	} else {
		args = append(args, "-b", repository.BranchName, targetPath, repository.BaseSHA)
	}
	if _, err := runGit(ctx, repository.SourcePath, args...); err != nil {
		return err
	}
	return nil
}

func publishManifest(rootPath string, manifest RootManifest) error {
	return writeManifest(rootPath, "manifest.json", manifest)
}

func writeManifest(rootPath, name string, manifest RootManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode TaskRun Root Manifest: %w", err)
	}
	data = append(data, '\n')
	temporary, err := os.CreateTemp(rootPath, ".manifest-*.json")
	if err != nil {
		return fmt.Errorf("create temporary TaskRun Root Manifest: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, filepath.Join(rootPath, name)); err != nil {
		return fmt.Errorf("publish TaskRun Root Manifest: %w", err)
	}
	return nil
}

func readManifest(path string) (RootManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return RootManifest{}, err
	}
	var manifest RootManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return RootManifest{}, fmt.Errorf("decode TaskRun Root Manifest %s: %w", path, err)
	}
	return manifest, nil
}

func runGit(ctx context.Context, directory string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", args...) //nolint:gosec // arguments are passed directly without a shell.
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

func pathContains(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
