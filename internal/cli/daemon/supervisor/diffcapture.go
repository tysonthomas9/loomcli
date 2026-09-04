package supervisor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// --- Checkpoint git diff capture ---
//
// The checkpoint diff is the ONLY thing that carries an interrupted run's
// work-in-progress into the next attempt once --resume is unavailable or
// exhausted (see resume_recovery.go). It therefore has to cover what an agent
// actually produces — mostly NEW files — and it has to look where the agent
// actually worked, which is its own worktree, not the workspace's base clones.

const (
	// maxUntrackedFiles caps how many untracked files a single source may
	// contribute, so a node_modules in a repo with no .gitignore cannot turn
	// the capture into a directory listing.
	maxUntrackedFiles = 20
	// maxUntrackedFileBytes is the per-file ceiling; larger files get a marker
	// line instead of their contents.
	maxUntrackedFileBytes = 64 * 1024
)

// Discovery indirection, so capture can be tested without a workspace config.
var (
	discoverWorktreesFn      = cli.DiscoverWorktrees
	discoverAgentWorktreesFn = cli.DiscoverAgentWorktrees
)

// checkpointExtraPathsEnv names out-of-workspace clones (deploy trees) that no
// resolver can discover. OS-list-separated, same shape as PATH.
const checkpointExtraPathsEnv = "LOOM_CHECKPOINT_EXTRA_PATHS"

// diffSource is one git tree to scan, with a short label for the diff header.
type diffSource struct {
	Name string
	Path string
}

// captureGitDiff collects the uncommitted work an agent left behind, across
// every tree it plausibly wrote to. It returns the (truncated) diff and the
// list of paths actually scanned — the second value is what makes an empty
// diff diagnosable rather than silent.
func captureGitDiff(worktreePath, agentName string, maxBytes int) (string, []string) {
	sources := checkpointDiffSources(worktreePath, agentName)

	var sb strings.Builder
	scanned := make([]string, 0, len(sources))
	remaining := maxBytes

	for i, src := range sources {
		if remaining <= 0 {
			sb.WriteString(fmt.Sprintf(
				"# ... (%d further sources not scanned: diff budget exhausted)\n", len(sources)-i))
			break
		}
		scanned = append(scanned, src.Path)
		out := captureRepoDiff(src.Path, remaining)
		if out == "" {
			continue
		}
		block := fmt.Sprintf("--- repo: %s ---\n%s\n", src.Name, out)
		sb.WriteString(block)
		remaining -= len(block)
	}

	return config.TruncateDiff(strings.TrimSpace(sb.String()), maxBytes), scanned
}

// checkpointDiffSources builds the ordered, de-duplicated scan list. Order is
// load-bearing: the agent's own worktree is first so a large clone diff can
// never starve it out of the byte budget.
func checkpointDiffSources(worktreePath, agentName string) []diffSource {
	set := &diffSourceSet{seen: make(map[string]struct{})}

	// 1. The agent's own worktree — always first, always considered.
	set.add("self", worktreePath)

	// 2. Same-named agent worktrees in OTHER repos. A task whose source_repo
	//    differs from the agent's assigned worktree repo leaves its work here.
	if wts, err := discoverAgentWorktreesFn(); err == nil {
		for _, wt := range wts {
			if wt.Name != agentName {
				continue
			}
			set.add(agentSourceName(wt), wt.Path)
		}
	}

	// 3. Workspace repo clones. Work there is possible but rare, so they come
	//    last and get only what the budget has left.
	if wts, err := discoverWorktreesFn(); err == nil {
		for _, wt := range wts {
			set.add(wt.Name, wt.Path)
		}
	}

	// 4. Explicitly declared out-of-workspace clones.
	for _, p := range extraCheckpointPaths() {
		set.add("extra:"+filepath.Base(p), p)
	}

	return set.sources
}

// diffSourceSet accumulates sources, dropping duplicates and non-git paths.
type diffSourceSet struct {
	seen    map[string]struct{}
	sources []diffSource
}

// add normalizes path, skips it when it is not a git tree or was already
// added, and otherwise appends it. Symlinks are resolved best-effort so
// /var and /private/var do not count twice; note EvalSymlinks does NOT fold
// case, so this is not a case-insensitive comparison.
func (s *diffSourceSet) add(name, path string) {
	if path == "" {
		return
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return
	}
	abs = filepath.Clean(abs)
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	if _, dup := s.seen[abs]; dup {
		return
	}
	// A linked worktree's .git is a file, not a directory — Stat covers both.
	if _, err := os.Stat(filepath.Join(abs, ".git")); err != nil {
		return
	}
	s.seen[abs] = struct{}{}
	s.sources = append(s.sources, diffSource{Name: name, Path: abs})
}

// agentSourceName labels a sibling agent worktree as <repo>/<agent>.
func agentSourceName(wt cli.WorktreeInfo) string {
	if wt.Repo != nil && wt.Repo.Name != "" {
		return wt.Repo.Name + "/" + wt.Name
	}
	return wt.Name
}

// extraCheckpointPaths reads LOOM_CHECKPOINT_EXTRA_PATHS.
func extraCheckpointPaths() []string {
	raw := os.Getenv(checkpointExtraPathsEnv)
	if raw == "" {
		return nil
	}
	var out []string
	for _, p := range filepath.SplitList(raw) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// captureRepoDiff renders one source: tracked modifications plus untracked
// files. A repo with no commits fails `git diff HEAD`; that is not fatal, the
// untracked half still carries the work.
func captureRepoDiff(path string, maxBytes int) string {
	var parts []string

	if tracked, err := cli.RunGitCommand(path, "diff", "HEAD"); err == nil {
		if t := strings.TrimSpace(tracked); t != "" {
			parts = append(parts, t)
		}
	}

	used := 0
	for _, p := range parts {
		used += len(p) + 1
	}
	if untracked := untrackedDiff(path, maxBytes-used); untracked != "" {
		parts = append(parts, untracked)
	}

	return strings.Join(parts, "\n")
}

// untrackedDiff renders new files as a real unified diff. It lists them with
// `ls-files --others --exclude-standard` (so .gitignore is honored) and NUL
// separation (so filenames with spaces or newlines survive), then diffs each
// against /dev/null.
func untrackedDiff(path string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	out, err := cli.RunGitCommand(path, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return ""
	}

	var candidates []string
	for _, rel := range strings.Split(out, "\x00") {
		if rel == "" || isLoomRuntimePath(rel) {
			continue
		}
		candidates = append(candidates, rel)
	}

	var sb strings.Builder
	for i, rel := range candidates {
		if i >= maxUntrackedFiles {
			fmt.Fprintf(&sb, "# ... and %d more untracked files\n", len(candidates)-i)
			break
		}
		block := untrackedFileBlock(path, rel)
		if block == "" {
			continue
		}
		// Write first, check after: a single block larger than the remaining
		// budget must still contribute its opening lines rather than vanish,
		// and the caller's final TruncateDiff enforces the real ceiling.
		sb.WriteString(block)
		if sb.Len() >= maxBytes {
			if i < len(candidates)-1 {
				sb.WriteString("# ... (untracked capture truncated: diff budget exhausted)\n")
			}
			break
		}
	}
	return strings.TrimSpace(sb.String())
}

// untrackedFileBlock diffs one untracked file against /dev/null.
//
// `git diff --no-index` is deliberate: `git add -N` would mutate the index of
// a LIVE worktree, and a crash mid-capture would leave it dirty for the next
// run. Capture must be strictly read-only. --no-index also prints
// "Binary files ... differ" for binaries, which is exactly what we want.
// It exits 1 whenever the files differ — the normal case — so the output is
// taken on its own merits and the error is ignored.
func untrackedFileBlock(root, rel string) string {
	full := filepath.Join(root, rel)
	info, err := os.Lstat(full)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	if info.Size() > maxUntrackedFileBytes {
		return fmt.Sprintf("# skipped %s (%d bytes)\n", rel, info.Size())
	}

	out, _ := cli.RunGitCommandRaw(root, "diff", "--no-index", "--", os.DevNull, rel)
	if out = strings.TrimSpace(out); out == "" {
		return ""
	}
	return out + "\n"
}

// loomRuntimeDirs are directories loom and its harness create inside a live
// worktree. They are never the agent's work.
var loomRuntimeDirs = []string{".claude", ".loom"}

// isLoomRuntimePath reports whether rel (relative to a source root) is loom's
// own runtime noise rather than agent work.
//
// This is a correctness requirement, not a nicety: an idle agent worktree's
// ONLY untracked content is .agent.lock, .agent.lock.flock,
// .agent.checkpoint.json and .claude/, so without this every checkpoint would
// come back non-empty and tell the next agent "the previous attempt made these
// uncommitted changes."
//
// The lock/checkpoint/yield family is matched by its shared ".agent." prefix
// rather than by name, so the .tmp siblings and any future sidecar are covered
// by default (lockSidecarName in internal/cli/lock.go is unexported anyway).
func isLoomRuntimePath(rel string) bool {
	first := rel
	if i := strings.IndexAny(rel, `/\`); i >= 0 {
		first = rel[:i]
	}
	if strings.HasPrefix(first, ".agent.") {
		return true
	}
	for _, d := range loomRuntimeDirs {
		if first == d {
			return true
		}
	}
	return false
}
