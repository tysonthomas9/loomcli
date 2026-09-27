package opsimpl

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/gitbranch"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/ops"
)

const (
	repairMethodNone      = "none"
	repairMethodRepair    = "repair"
	repairMethodRecreate  = "recreate"
	repairMethodProvision = "provision"
)

type repairCheckoutSpec struct {
	scope      string
	target     string
	repo       ops.WorkspaceRepo
	path       string
	branch     string
	baseBranch string
	label      string
}

var (
	repairProvisionCheckout           = provisionRepairCheckout
	repairProvisionCheckoutWithBranch = provisionRepairCheckoutWithBranch
)

// RepairCheckout repairs or provisions a known workspace checkout. All target
// paths are derived from workspace topology and validated under the workspace
// root; no request field is treated as a filesystem path.
func (g *GitOpsImpl) RepairCheckout(workspaceID, scope, target, repoName string, force bool) (ops.RepairResult, error) {
	ws, err := g.ResolveWorkspaceData(workspaceID)
	if err != nil {
		return ops.RepairResult{}, fmt.Errorf("load workspace data: %w", err)
	}
	wsRoot, err := g.ResolveWorkspaceRoot(workspaceID)
	if err != nil {
		return ops.RepairResult{}, fmt.Errorf("resolve workspace root: %w", err)
	}
	ws.Path = wsRoot

	spec, err := repairCheckoutTarget(ws, wsRoot, scope, target, repoName)
	if err != nil {
		return ops.RepairResult{}, err
	}

	exists, err := repairPathExists(spec.path)
	if err != nil {
		return ops.RepairResult{}, err
	}
	source, sourceOK := findRepairSource(ws, wsRoot, spec.repo, spec.path)
	var result ops.RepairResult
	if !exists {
		result, err = provisionMissingCheckout(source, sourceOK, spec)
	} else {
		result, err = repairExistingCheckout(source, sourceOK, spec, force)
	}
	if err != nil {
		return ops.RepairResult{}, err
	}
	// Checkouts created before loom installed its env-token credential helper
	// self-heal here. Best-effort on purpose: a helper that cannot be written
	// is not a reason to report an otherwise-successful repair as failed.
	if result.Repaired {
		_ = localworkspace.EnsureCredentialHelper(context.Background(), spec.path)
	}
	return result, nil
}

// provisionMissingCheckout creates a checkout whose working directory is absent.
func provisionMissingCheckout(source string, sourceOK bool, spec repairCheckoutSpec) (ops.RepairResult, error) {
	if !sourceOK {
		return repairNone("No healthy source checkout is available to provision " + spec.label), nil
	}
	recovery, err := repairProvisionCheckout(source, spec.path, spec.branch, spec.baseBranch)
	if err != nil {
		return ops.RepairResult{}, fmt.Errorf("provision checkout: %w", err)
	}
	if !repairCheckoutHealthy(spec.path) {
		return ops.RepairResult{}, fmt.Errorf("provisioned checkout did not become healthy")
	}
	return ops.RepairResult{
		Repaired: true,
		Method:   repairMethodProvision,
		Message:  repairMessageWithBranchRecovery("Provisioned "+spec.label, recovery),
	}, nil
}

// repairExistingCheckout repairs a checkout whose working directory is present.
// Non-destructive `worktree repair` is always attempted first; recreation (which
// preserves the existing directory as a timestamped backup) requires force.
func repairExistingCheckout(source string, sourceOK bool, spec repairCheckoutSpec, force bool) (ops.RepairResult, error) {
	if repairCheckoutHealthy(spec.path) {
		return ops.RepairResult{
			Repaired: true,
			Method:   repairMethodNone,
			Message:  spec.label + " is already healthy",
		}, nil
	}
	if !sourceOK {
		return repairNone("No healthy source checkout is available to repair " + spec.label), nil
	}

	_, _ = runRepairGit(source, "worktree", "prune")
	_, _ = runRepairGit(source, "worktree", "repair", spec.path)
	if repairCheckoutHealthy(spec.path) {
		return ops.RepairResult{
			Repaired: true,
			Method:   repairMethodRepair,
			Message:  "Repaired " + spec.label,
		}, nil
	}

	if !force {
		return ops.RepairResult{
			Repaired:      false,
			Method:        repairMethodNone,
			RequiresForce: true,
			Message:       spec.label + " must be recreated. Its current contents will be preserved in a timestamped backup folder before a fresh checkout is created.",
		}, nil
	}

	backupPath, recovery, err := recreateRepairCheckout(source, spec.path, spec.branch, spec.baseBranch)
	if err != nil {
		return ops.RepairResult{}, err
	}
	message := fmt.Sprintf("Recreated %s and preserved previous contents at %s", spec.label, backupPath)
	return ops.RepairResult{
		Repaired:   true,
		Method:     repairMethodRecreate,
		BackupPath: backupPath,
		Message:    repairMessageWithBranchRecovery(message, recovery),
	}, nil
}

func repairNone(message string) ops.RepairResult {
	return ops.RepairResult{Repaired: false, Method: repairMethodNone, Message: message}
}

func repairCheckoutTarget(ws *ops.WorkspaceData, wsRoot, scope, target, repoName string) (repairCheckoutSpec, error) {
	scope = strings.TrimSpace(scope)
	target = strings.TrimSpace(target)
	repoName = strings.TrimSpace(repoName)
	switch scope {
	case "agent":
		return repairAgentCheckoutTarget(ws, wsRoot, target, repoName)
	case "repo":
		return repairRepoCheckoutTarget(ws, wsRoot, target, repoName)
	default:
		return repairCheckoutSpec{}, fmt.Errorf("%w: unsupported scope %q", ops.ErrCheckoutTargetNotAllowed, scope)
	}
}

func repairAgentCheckoutTarget(ws *ops.WorkspaceData, wsRoot, target, repoName string) (repairCheckoutSpec, error) {
	agent, err := findWorkspaceAgent(ws, ws.ID, target)
	if err != nil {
		return repairCheckoutSpec{}, fmt.Errorf("%w: agent %q is not known in workspace", ops.ErrCheckoutTargetNotAllowed, target)
	}
	var repo ops.WorkspaceRepo
	if repoName != "" {
		repo, err = selectAgentRepoByName(ws.Repos, *agent, repoName)
	} else {
		repo, err = selectAgentRepo(ws.Repos, *agent)
	}
	if err != nil {
		return repairCheckoutSpec{}, err
	}
	path, err := validateRepairTargetPath(wsRoot, localworkspace.AgentWorktreePath(wsRoot, repo.Name, target))
	if err != nil {
		return repairCheckoutSpec{}, err
	}
	return repairCheckoutSpec{
		scope:      "agent",
		target:     target,
		repo:       repo,
		path:       path,
		branch:     target,
		baseBranch: repairDefaultBranch(repo),
		label:      target,
	}, nil
}

func repairRepoCheckoutTarget(ws *ops.WorkspaceData, wsRoot, target, repoName string) (repairCheckoutSpec, error) {
	if repoName != "" && repoName != target {
		return repairCheckoutSpec{}, fmt.Errorf("%w: repo body value %q does not match target %q", ops.ErrCheckoutTargetNotAllowed, repoName, target)
	}
	repo, ok := findWorkspaceRepo(ws.Repos, target)
	if !ok {
		return repairCheckoutSpec{}, fmt.Errorf("%w: repo %q is not known in workspace", ops.ErrCheckoutTargetNotAllowed, target)
	}
	path, err := validateRepairTargetPath(wsRoot, repairRepoCheckoutPath(wsRoot, repo))
	if err != nil {
		return repairCheckoutSpec{}, err
	}
	branch := strings.TrimSpace(repo.DefaultBranch)
	if branch == "" {
		branch = "main"
	}
	return repairCheckoutSpec{
		scope:      "repo",
		target:     target,
		repo:       repo,
		path:       path,
		branch:     branch,
		baseBranch: branch,
		label:      target,
	}, nil
}

func repairDefaultBranch(repo ops.WorkspaceRepo) string {
	branch := strings.TrimSpace(repo.DefaultBranch)
	if branch == "" {
		return "main"
	}
	return branch
}

func repairRepoCheckoutPath(wsRoot string, repo ops.WorkspaceRepo) string {
	if repo.Path != "" {
		return repo.Path
	}
	return filepath.Join(wsRoot, repo.Name)
}

func validateRepairTargetPath(wsRoot, path string) (string, error) {
	absRoot, err := filepath.Abs(wsRoot)
	if err != nil {
		return "", fmt.Errorf("%w: resolve workspace root: %v", ops.ErrCheckoutTargetNotAllowed, err)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("%w: resolve checkout path: %v", ops.ErrCheckoutTargetNotAllowed, err)
	}
	if absPath == absRoot || !localworkspace.PathContains(absRoot, absPath) {
		return "", fmt.Errorf("%w: checkout path escapes workspace root", ops.ErrCheckoutTargetNotAllowed)
	}
	if err := validateNoRepairSymlinkComponents(absRoot, absPath); err != nil {
		return "", err
	}
	return absPath, nil
}

func validateNoRepairSymlinkComponents(root, target string) error {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return fmt.Errorf("%w: resolve checkout relative path: %v", ops.ErrCheckoutTargetNotAllowed, err)
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("%w: inspect checkout path: %v", ops.ErrCheckoutTargetNotAllowed, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: checkout path contains a symlink", ops.ErrCheckoutTargetNotAllowed)
		}
	}
	return nil
}

func repairPathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("inspect checkout path: %w", err)
}

func findRepairSource(ws *ops.WorkspaceData, wsRoot string, repo ops.WorkspaceRepo, targetPath string) (string, bool) {
	for _, candidate := range repairSourceCandidates(ws, wsRoot, repo, targetPath) {
		if candidate == "" || sameRepairPath(candidate, targetPath) {
			continue
		}
		if _, err := validateRepairTargetPath(wsRoot, candidate); err != nil {
			continue
		}
		if repairGitUsable(candidate) {
			return candidate, true
		}
	}
	return "", false
}

func repairSourceCandidates(ws *ops.WorkspaceData, wsRoot string, repo ops.WorkspaceRepo, targetPath string) []string {
	candidates := []string{repairRepoCheckoutPath(wsRoot, repo)}
	worktreesRoot := filepath.Join(wsRoot, "worktrees", repo.Name)
	entries, err := os.ReadDir(worktreesRoot)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				candidates = append(candidates, filepath.Join(worktreesRoot, entry.Name()))
			}
		}
	}
	if ws != nil {
		for _, agent := range ws.Agents {
			candidates = append(candidates, localworkspace.AgentWorktreePath(wsRoot, repo.Name, agent.Name))
		}
	}
	candidates = append(candidates, targetPath)
	return dedupeRepairPaths(candidates)
}

func dedupeRepairPaths(paths []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		abs, err := filepath.Abs(path)
		if err != nil {
			continue
		}
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		out = append(out, abs)
	}
	return out
}

func sameRepairPath(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	return errA == nil && errB == nil && aa == bb
}

func repairGitUsable(path string) bool {
	_, err := runRepairGit(path, "rev-parse", "--git-dir")
	return err == nil
}

func repairCheckoutHealthy(path string) bool {
	_, err := runRepairGit(path, "status", "--porcelain")
	return err == nil
}

func provisionRepairCheckout(sourceRepo, targetPath, branch, baseBranch string) (gitbranch.Recovery, error) {
	info, err := gitbranch.Inspect(sourceRepo, branch)
	if err != nil {
		return info, err
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return info, fmt.Errorf("create checkout parent: %w", err)
	}
	return repairProvisionCheckoutWithBranch(sourceRepo, targetPath, branch, baseBranch, info)
}

func provisionRepairCheckoutWithBranch(
	sourceRepo, targetPath, branch, baseBranch string,
	info gitbranch.Recovery,
) (gitbranch.Recovery, error) {
	if info.State == gitbranch.StateHealthy {
		return info, provisionHealthyRepairCheckout(sourceRepo, targetPath, branch)
	}
	recovery, err := gitbranch.Recover(sourceRepo, branch, baseBranch, info)
	if err != nil {
		return recovery, err
	}
	_, err = runRepairGit(sourceRepo, "worktree", "add", targetPath, "-b", branch, recovery.BaseSHA)
	return recovery, err
}

func provisionHealthyRepairCheckout(sourceRepo, targetPath, branch string) error {
	if _, err := runRepairGit(sourceRepo, "worktree", "add", targetPath, "-b", branch); err == nil {
		return nil
	} else if !repairBranchAlreadyExists(err) {
		return err
	}
	_, err := runRepairGit(sourceRepo, "worktree", "add", targetPath, branch)
	return err
}

func repairBranchAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "already exists") ||
		strings.Contains(msg, "already a worktree") ||
		strings.Contains(msg, "already checked out")
}

func repairMessageWithBranchRecovery(message string, recovery gitbranch.Recovery) string {
	if recovery.State == gitbranch.StateHealthy {
		return message
	}
	return message + ". " + repairBranchRecoveryMessage(recovery)
}

func repairBranchRecoveryMessage(recovery gitbranch.Recovery) string {
	state := "missing"
	if recovery.State == gitbranch.StateBroken {
		state = "corrupt"
	}
	return fmt.Sprintf(
		"Branch ref %q was %s and was recreated from %s at %s",
		recovery.Branch,
		state,
		recovery.Base,
		shortRepairSHA(recovery.BaseSHA),
	)
}

func shortRepairSHA(sha string) string {
	if len(sha) <= 12 {
		return sha
	}
	return sha[:12]
}

func recreateRepairCheckout(sourceRepo, targetPath, branch, baseBranch string) (string, gitbranch.Recovery, error) {
	info, err := gitbranch.Inspect(sourceRepo, branch)
	if err != nil {
		return "", info, err
	}
	backupPath := uniqueRepairBackupPath(targetPath, "broken")
	if err := os.Rename(targetPath, backupPath); err != nil {
		return "", info, fmt.Errorf("preserve broken checkout: %w", err)
	}
	recovery, err := finishRecreateRepairCheckout(sourceRepo, targetPath, backupPath, branch, baseBranch, info)
	if err != nil {
		return backupPath, recovery, rollbackRepairRecreate(targetPath, backupPath, err)
	}
	return backupPath, recovery, nil
}

func finishRecreateRepairCheckout(
	sourceRepo, targetPath, backupPath, branch, baseBranch string,
	info gitbranch.Recovery,
) (gitbranch.Recovery, error) {
	if _, err := runRepairGit(sourceRepo, "worktree", "prune"); err != nil {
		return info, fmt.Errorf("prune stale worktree metadata: %w", err)
	}
	recovery, err := repairProvisionCheckoutWithBranch(sourceRepo, targetPath, branch, baseBranch, info)
	if err != nil {
		return recovery, fmt.Errorf("create fresh checkout after preserving %s: %w", backupPath, err)
	}
	if err := copyCheckoutContents(backupPath, targetPath); err != nil {
		return recovery, fmt.Errorf("restore preserved checkout contents: %w", err)
	}
	if !repairCheckoutHealthy(targetPath) {
		return recovery, fmt.Errorf("recreated checkout did not become healthy")
	}
	return recovery, nil
}

func rollbackRepairRecreate(targetPath, backupPath string, cause error) error {
	if err := renameExistingRepairPath(targetPath, "failed"); err != nil {
		return fmt.Errorf("repair failed after preserving checkout at %s; rollback could not clear failed target %s: %w; original error: %v", backupPath, targetPath, err, cause)
	}
	if err := os.Rename(backupPath, targetPath); err != nil {
		return fmt.Errorf("repair failed and rollback failed; preserved checkout remains at %s: %w; original error: %v", backupPath, err, cause)
	}
	return fmt.Errorf("%w; rolled back checkout to %s; nothing was changed", cause, targetPath)
}

func uniqueRepairBackupPath(path, tag string) string {
	base := fmt.Sprintf("%s.%s-%d", path, tag, time.Now().Unix())
	candidate := base
	for i := 2; ; i++ {
		if _, err := os.Lstat(candidate); err != nil {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
}

func copyCheckoutContents(srcDir, dstDir string) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == ".git" {
			continue
		}
		if err := copyCheckoutPath(filepath.Join(srcDir, entry.Name()), filepath.Join(dstDir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func copyCheckoutPath(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		return copyRepairSymlink(src, dst)
	case info.IsDir():
		return copyRepairDir(src, dst, info.Mode().Perm())
	case info.Mode().IsRegular():
		return copyRepairFile(src, dst, info.Mode().Perm())
	default:
		return fmt.Errorf("unsupported preserved checkout entry: %s", src)
	}
}

func copyRepairSymlink(src, dst string) error {
	target, err := os.Readlink(src)
	if err != nil {
		return err
	}
	if err := renameExistingRepairPath(dst, "fresh"); err != nil {
		return err
	}
	return os.Symlink(target, dst)
}

func copyRepairDir(src, dst string, perm os.FileMode) error {
	if info, err := os.Lstat(dst); err == nil && !info.IsDir() {
		if err := renameExistingRepairPath(dst, "fresh"); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(dst, perm); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := copyCheckoutPath(filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name())); err != nil {
			return err
		}
	}
	return os.Chmod(dst, perm)
}

func copyRepairFile(src, dst string, perm os.FileMode) error {
	if info, err := os.Lstat(dst); err == nil && (info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		if err := renameExistingRepairPath(dst, "fresh"); err != nil {
			return err
		}
	}
	in, err := os.Open(src) //nolint:gosec // src is a preserved checkout path derived from workspace state.
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm) //nolint:gosec // dst is a fresh checkout path derived from workspace state.
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func renameExistingRepairPath(path, tag string) error {
	if _, err := os.Lstat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return os.Rename(path, uniqueRepairBackupPath(path, tag))
}

func runRepairGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...) //nolint:gosec // fixed git binary; args are controlled by checkout repair code.
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git -C %s %s: %w: %s", dir, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
