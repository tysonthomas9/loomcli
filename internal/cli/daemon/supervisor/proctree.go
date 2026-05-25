package supervisor

import (
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

// procInfo is a snapshot of one process for tree-walking.
type procInfo struct {
	PID  int
	PPID int
	PGID int
}

// processInspector returns the current snapshot of all processes (best-effort)
// and the cwd of a given pid. Set during package init by a build-tagged file;
// on platforms without an implementation it is left as zero-value stubs.
type processInspector struct {
	List func() ([]procInfo, error)
	CWD  func(pid int) (string, error)
}

var procInspector processInspector

// sweepOrphanedBackends kills any backend processes (codex/claude/cursor/etc)
// reparented to init by a previous daemon SIGKILL or crash, scoped to the
// daemon's managed worktrees. Logs a count when anything is killed.
// Without this, a hung-process kill that left codex reparented to init keeps
// holding its API connection and context window across restarts.
func (s *Supervisor) sweepOrphanedBackends() {
	paths := s.managedWorktreePaths()
	if len(paths) == 0 {
		return
	}
	if killed := s.killOrphanedWorktreeProcesses(paths); killed > 0 {
		slog.Info("killed orphaned backend processes on startup", "count", killed)
	}
}

// managedWorktreePaths returns the absolute filesystem paths the daemon is
// currently supervising (one per agent). Used to scope the startup orphan
// sweep so the daemon never signals processes that aren't ours.
func (s *Supervisor) managedWorktreePaths() []string {
	s.AgentsMu.RLock()
	defer s.AgentsMu.RUnlock()
	seen := map[string]struct{}{}
	paths := make([]string, 0, len(s.Agents))
	for _, ap := range s.Agents {
		if ap == nil || ap.WorktreePath == "" {
			continue
		}
		if _, dup := seen[ap.WorktreePath]; dup {
			continue
		}
		seen[ap.WorktreePath] = struct{}{}
		paths = append(paths, ap.WorktreePath)
	}
	return paths
}

// findDescendantPGIDs returns the set of unique process-group IDs spanned by
// rootPID's descendant subtree, excluding rootPID's own pgroup. The hung-process
// killer uses this to reach backends like codex that put themselves in a new
// pgroup via Setpgid:true — those are invisible to a single syscall.Kill(-pid).
//
// Best-effort: returns nil on platforms without a process inspector, or if
// listing fails. Skips PGIDs that are 0, 1, or the daemon's own pgroup so a
// scanner bug can never accidentally signal init or ourselves.
func findDescendantPGIDs(rootPID int, ownPGID int) map[int]struct{} {
	if procInspector.List == nil || rootPID <= 1 {
		return nil
	}
	procs, err := procInspector.List()
	if err != nil || len(procs) == 0 {
		return nil
	}

	byPPID := make(map[int][]procInfo, len(procs))
	var rootPGID int
	for _, p := range procs {
		byPPID[p.PPID] = append(byPPID[p.PPID], p)
		if p.PID == rootPID {
			rootPGID = p.PGID
		}
	}

	pgids := map[int]struct{}{}
	queue := []int{rootPID}
	seen := map[int]bool{rootPID: true}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		for _, child := range byPPID[pid] {
			if seen[child.PID] {
				continue
			}
			seen[child.PID] = true
			queue = append(queue, child.PID)
			pgid := child.PGID
			if pgid <= 1 || pgid == rootPGID || pgid == ownPGID {
				continue
			}
			pgids[pgid] = struct{}{}
		}
	}
	return pgids
}

// signalDescendantPGroups sends sig to every pgid in the set. Logs every
// delivery so failures and successes are both visible in the daemon log;
// callers pass pgids captured before the worker dies so a clean exit doesn't
// race the descendant-tree walk.
func signalDescendantPGroups(pgids map[int]struct{}, sig syscall.Signal, worktree string) {
	for pgid := range pgids {
		if err := syscall.Kill(-pgid, sig); err != nil {
			slog.Warn("failed to signal descendant process group",
				"worktree", worktree, "pgid", pgid, "signal", sig.String(), "err", err)
			continue
		}
		slog.Info("sent signal to descendant process group",
			"worktree", worktree, "pgid", pgid, "signal", sig.String())
	}
}

// orphanCandidate is a process that may be a leftover backend from a previous
// daemon run: PPID==1 (reparented to init) and CWD lives under a managed
// worktree path.
type orphanCandidate struct {
	PID      int
	PGID     int
	CWD      string
	Worktree string
}

// findWorktreeOrphans scans the process table for processes whose parent is
// init (PPID==1) and whose CWD is one of the given worktree paths (or a
// descendant of one). Returns one candidate per matching process. Best-effort:
// returns nil on platforms without an inspector.
func findWorktreeOrphans(worktreePaths []string) []orphanCandidate {
	if procInspector.List == nil || procInspector.CWD == nil {
		return nil
	}
	if len(worktreePaths) == 0 {
		return nil
	}
	procs, err := procInspector.List()
	if err != nil {
		return nil
	}
	// Normalize worktree paths so prefix matching is robust to trailing slashes
	// and macOS `/var` → `/private/var` symlink redirection (lsof reports the
	// resolved path; t.TempDir and many configs use the symlinked one).
	norm := make([]string, 0, len(worktreePaths))
	for _, w := range worktreePaths {
		w = strings.TrimRight(w, "/")
		if w == "" {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(w); err == nil && resolved != "" {
			w = strings.TrimRight(resolved, "/")
		}
		norm = append(norm, w)
	}
	if len(norm) == 0 {
		return nil
	}

	var out []orphanCandidate
	for _, p := range procs {
		if p.PPID != 1 {
			continue
		}
		if p.PID == 1 {
			continue
		}
		cwd, err := procInspector.CWD(p.PID)
		if err != nil || cwd == "" {
			continue
		}
		cwd = strings.TrimRight(cwd, "/")
		for _, w := range norm {
			if cwd == w || strings.HasPrefix(cwd, w+"/") {
				out = append(out, orphanCandidate{PID: p.PID, PGID: p.PGID, CWD: cwd, Worktree: w})
				break
			}
		}
	}
	return out
}

// signalableOrphans drops candidates whose pgroup must never receive a
// syscall.Kill(-pgid): pgroup 0/1 (kernel/init) and the daemon's own process
// group (ownPGID), which would take down the daemon itself. The hung-process
// descendant killer applies the same own-pgroup guard via findDescendantPGIDs;
// the startup sweep needs it too because a backend that stayed in the daemon's
// pgroup, got reparented to init, and has a worktree cwd would otherwise match.
func signalableOrphans(orphans []orphanCandidate, ownPGID int) []orphanCandidate {
	return slices.DeleteFunc(orphans, func(o orphanCandidate) bool {
		return o.PGID <= 1 || o.PGID == ownPGID
	})
}

// killOrphanedWorktreeProcesses sweeps any processes orphaned by a previous
// daemon run (PPID==1) whose CWD points into one of worktreePaths, and signals
// each one's process group with SIGTERM followed by SIGKILL after grace.
// Designed to run once on supervisor startup as the "didn't shut down cleanly
// last time" safety net.
func (s *Supervisor) killOrphanedWorktreeProcesses(worktreePaths []string) int {
	orphans := signalableOrphans(findWorktreeOrphans(worktreePaths), syscall.Getpgrp())
	if len(orphans) == 0 {
		return 0
	}
	// Group by PGID so we send one signal per pgroup, not per pid.
	byPGID := map[int][]orphanCandidate{}
	for _, o := range orphans {
		byPGID[o.PGID] = append(byPGID[o.PGID], o)
	}
	for pgid, members := range byPGID {
		first := members[0]
		slog.Warn("killing orphaned backend from previous daemon run",
			"pgid", pgid, "pid", first.PID, "cwd", first.CWD, "worktree", first.Worktree, "members", len(members))
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
	}
	// Brief grace period before SIGKILL escalation. Long-orphaned backends
	// almost never respond to SIGTERM (they've been parentless for some time
	// and aren't handling signals cleanly), so we keep the window short and
	// poll early-exit when the orphans clear. This keeps Start() from
	// stalling agent spawn for more than ~half a second per cold boot.
	const gracePolls = 5 // 5 * 100ms = 500ms
	for range gracePolls {
		anyAlive := false
		for _, o := range orphans {
			if syscall.Kill(o.PID, 0) == nil {
				anyAlive = true
				break
			}
		}
		if !anyAlive {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	for pgid := range byPGID {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
	return len(orphans)
}
