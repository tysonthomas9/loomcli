package supervisor

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/agent"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/workspace"
)

// AgentPlacement is the (repo, worktree, repo config) triple an agent's current
// cycle runs against. It is immutable once published; a re-point publishes a
// NEW one rather than mutating the old, which is what lets readers take it
// without a lock (see AgentProcess.placement).
type AgentPlacement struct {
	Repo       string             // workspace repo name ("" when unresolved)
	WorkDir    string             // absolute worktree path
	RepoConfig *cfgpkg.RepoConfig // trunk + remote for that repo (may be nil)
}

// Placement returns the agent's effective placement for this cycle. Before any
// re-point it synthesizes the base placement from the fields NewAgent wrote, so
// an agent that never re-points behaves exactly as it did before routing existed.
func (ap *AgentProcess) Placement() AgentPlacement {
	if p := ap.placement.Load(); p != nil {
		return *p
	}
	return AgentPlacement{
		Repo:       ap.Entry.Repo,
		WorkDir:    ap.WorktreePath,
		RepoConfig: ap.RepoConfig,
	}
}

// WorkDir returns the worktree the current cycle actually runs in. This is the
// accessor every runtime reader uses; ap.WorktreePath is the base placement and
// is only correct before the first re-point.
func (ap *AgentProcess) WorkDir() string { return ap.Placement().WorkDir }

// effectivePlacement returns the repo and worktree the agent's current cycle
// runs against, falling back to the configured repo name when nothing has been
// re-pointed yet. Used for status snapshots.
func (ap *AgentProcess) effectivePlacement() (repo, workDir string) {
	p := ap.Placement()
	if p.Repo == "" {
		return ap.Entry.Repo, p.WorkDir
	}
	return p.Repo, p.WorkDir
}

// SetPlacement publishes a new effective placement for the agent.
func (ap *AgentProcess) SetPlacement(p AgentPlacement) { ap.placement.Store(&p) }

// DefaultResolveWorktree is the production ResolveWorktree: it resolves the
// per-repo, per-agent worktree and CREATES it when the repo checkout exists but
// the agent's worktree does not (that self-heal is wanted, not an error).
//
// It is exported so the daemon can wire it without importing
// internal/cli/workspace itself — that package sits at its import-fanout
// ceiling. Tests substitute their own function; a nil field disables routing.
func DefaultResolveWorktree(agentName, repo string) (string, error) {
	target, err := workspace.ResolveAgentTarget(agentName, repo)
	if err != nil {
		return "", err
	}
	return target.WorkDir, nil
}

// applyTaskPlacement re-points the cycle's worktree at the repo the freshly
// claimed task belongs to. It runs immediately after claimTask and before
// createAgentSession — the session captures BeforeRef from the worktree, so it
// must see the final one.
//
// Returns false when the placement cannot be established; the caller aborts the
// cycle. It NEVER falls back to the previous worktree on a resolution failure:
// running a task in another repo's checkout is the bug this exists to prevent.
func (s *Supervisor) applyTaskPlacement(ap *AgentProcess) bool {
	ap.Mu.Lock()
	taskID := ap.AssignedTaskID
	sourceRepo := strings.TrimSpace(ap.AssignedTaskRepo)
	ap.Mu.Unlock()

	if s.ResolveWorktree == nil {
		// Non-workspace mode, or a composite-literal Supervisor in a test:
		// placement routing is simply not wired. Behave exactly as before.
		return true
	}
	if ap.Entry.Worktree == "" {
		// ResolveAgentTarget would resolve an empty name to the workspace root.
		slog.Warn("task placement skipped: agent has no worktree name", "task_id", taskID)
		return true
	}
	if sourceRepo == "" {
		// DELIBERATE TOLERANCE. Most issues on a live fleet still arrive with no
		// source_repo (the API-backend create path drops it), and failing here
		// would halt dispatch fleet-wide. A missing source_repo is a different
		// bug from a WRONG mapping; only the latter fails loudly below.
		slog.Warn("claimed task carries no source_repo; keeping current worktree",
			"worktree", ap.Entry.Worktree, "task_id", taskID, "work_dir", ap.WorkDir())
		return true
	}

	repo, ok := resolveWorkspaceRepoName(sourceRepo, s.Repos)
	if !ok {
		s.failTaskPlacement(ap, taskID, fmt.Sprintf(
			"task %s source_repo %q is not a workspace repo (have: %s)",
			taskID, sourceRepo, strings.Join(workspaceRepoNames(s.Repos), ", ")))
		return false
	}

	current := ap.Placement()
	path, err := s.ResolveWorktree(ap.Entry.Worktree, repo)
	if err != nil {
		s.failTaskPlacement(ap, taskID, fmt.Sprintf(
			"task %s: cannot resolve worktree for repo %q (agent %s): %v",
			taskID, repo, ap.Entry.Worktree, err))
		return false
	}
	if path == current.WorkDir && repo == current.Repo {
		return true
	}

	ap.SetPlacement(AgentPlacement{Repo: repo, WorkDir: path, RepoConfig: s.FindRepoConfig(repo)})
	slog.Info("re-pointed agent worktree to the claimed task's repo",
		"worktree", ap.Entry.Worktree, "task_id", taskID,
		"from_repo", current.Repo, "from_path", current.WorkDir,
		"to_repo", repo, "to_path", path)

	// The cold-start recovery in preFlightSetup ran against the PREVIOUS
	// worktree. The newly adopted one may still be dirty from a cycle before
	// that, so clean it here; it is a no-op on a clean tree.
	//
	// Deliberately agent.CleanAdoptedWorktree and NOT recoverAgent: full
	// recovery releases and resets the agent's in_progress tasks, and the claim
	// this cycle just took is one of them — running it here would hand the work
	// straight back. See that function for what it does instead.
	if err := agent.CleanAdoptedWorktree(path); err != nil {
		slog.Warn("could not clean the re-pointed worktree; continuing",
			"worktree", ap.Entry.Worktree, "task_id", taskID, "work_dir", path, "err", err)
	}
	return true
}

// failTaskPlacement records the loud failure and gives the claim back. The
// release is best-effort — it no-ops on backends without actor-scoped release,
// in which case the claim lock leaks until its TTL. That is still strictly
// better than holding the claim AND blocking the agent for the same window;
// fixing release itself is out of this change's scope.
func (s *Supervisor) failTaskPlacement(ap *AgentProcess, taskID, msg string) {
	slog.Error("task placement failed", "worktree", ap.Entry.Worktree, "task_id", taskID, "err", msg)
	s.releaseAssignedTaskClaim(ap, taskID)
	ap.Mu.Lock()
	ap.AssignedTaskID = ""
	ap.AssignedTaskRepo = ""
	ap.Mu.Unlock()
	s.setPreflightError(ap, agenterr.OutcomeFromDomain(agenterr.WorktreeUnavailableOutcome), msg)
}

// adoptCarriedWorktree restores the effective placement after a daemon restart.
// ap.placement starts nil, so detectRecovery would look for a crash remnant in
// the BASE worktree and miss one left in a re-pointed one — silently converting
// a resumable run into a cold start and stranding a stale lock in a directory
// nothing sweeps. Best-effort: any failure leaves the base placement in place.
func (s *Supervisor) adoptCarriedWorktree(ap *AgentProcess) {
	if ap.placement.Load() != nil || ap.Entry.Worktree == "" || len(s.Repos) == 0 {
		return
	}
	root := workspaceRootFromWorktree(ap.WorktreePath)
	if root == "" {
		return
	}
	var (
		bestRepo string
		bestPath string
		bestAt   time.Time
	)
	for _, repo := range s.Repos {
		if repo.Name == "" {
			continue
		}
		path := filepath.Join(root, "worktrees", repo.Name, ap.Entry.Worktree)
		info, err := cli.ReadLockFile(path)
		if err != nil || info == nil || info.TaskID == "" {
			continue
		}
		if info.AgentName != "" && info.AgentName != ap.Entry.Worktree {
			continue
		}
		if bestPath != "" && !info.TaskStartedAt.After(bestAt) {
			continue
		}
		bestRepo, bestPath, bestAt = repo.Name, path, info.TaskStartedAt
	}
	if bestPath == "" || bestPath == ap.WorktreePath {
		return
	}
	ap.SetPlacement(AgentPlacement{Repo: bestRepo, WorkDir: bestPath, RepoConfig: s.FindRepoConfig(bestRepo)})
	slog.Info("adopted carried worktree from a prior daemon run",
		"worktree", ap.Entry.Worktree, "repo", bestRepo, "work_dir", bestPath)
}

// workspaceRootFromWorktree recovers "<ws>" from "<ws>/worktrees/<repo>/<agent>".
// Returns "" when the path does not have that shape (non-workspace mode, or an
// absolute worktree configured by hand), which disables adoption rather than
// guessing at a root.
func workspaceRootFromWorktree(path string) string {
	if path == "" {
		return ""
	}
	repoDir := filepath.Dir(path)
	worktreesDir := filepath.Dir(repoDir)
	if filepath.Base(worktreesDir) != "worktrees" {
		return ""
	}
	return filepath.Dir(worktreesDir)
}

// resolveWorkspaceRepoName maps an issue's source_repo — which may be a repo
// name, a source_repo_id, or a remote URL — onto a workspace repo name.
//
// NOTE: this is a deliberate copy of findRepoBySelector / normalizedRepoToken /
// repoBasename in internal/driver/task_worktree_resolver.go. internal/driver and
// this package share no dependency today, and ~40 lines of string munging is not
// worth creating one. Keep the two in sync if the matching rules change.
func resolveWorkspaceRepoName(selector string, repos []cfgpkg.RepoConfig) (string, bool) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return "", false
	}
	want := normalizedRepoToken(selector)
	wantBase := normalizedRepoToken(repoBasename(selector))
	for _, repo := range repos {
		if repo.Name == "" {
			continue
		}
		candidates := []string{repo.Name, repo.SourceRepoID, repoBasename(repo.Path)}
		for _, candidate := range candidates {
			got := normalizedRepoToken(candidate)
			if got != "" && (got == want || got == wantBase) {
				return repo.Name, true
			}
		}
	}
	return "", false
}

func normalizedRepoToken(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.TrimSuffix(value, ".git")
	value = strings.Trim(value, "/")
	return value
}

func repoBasename(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(value, ".git")
	value = strings.TrimRight(value, "/")
	if value == "" {
		return ""
	}
	if idx := strings.LastIndexAny(value, "/:"); idx >= 0 && idx+1 < len(value) {
		return value[idx+1:]
	}
	return value
}

// workspaceRepoNames lists the configured repo names for the "have: ..." half of
// the unknown-repo error, sorted so the message is stable across daemon runs.
func workspaceRepoNames(repos []cfgpkg.RepoConfig) []string {
	names := make([]string, 0, len(repos))
	for _, repo := range repos {
		if repo.Name != "" {
			names = append(names, repo.Name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return []string{"none"}
	}
	return names
}
