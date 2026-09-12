// Package gitstate answers one question about a git worktree: is it sitting in
// the middle of a merge, rebase, cherry-pick, revert or bisect, since when, and
// how bad is it.
//
// It exists because a worktree left mid-merge is silently blocking: every later
// integrator run fails its first step against it, and nothing in loom reported
// the condition. Two callers share this one detector — the `merge_in_progress`
// doctor check and the supervisor's post-exit scan — so the git plumbing is
// written once.
//
// The package deliberately depends on nothing inside loom: it shells out to
// git and touches the filesystem, nothing more.
package gitstate

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Op names the in-progress git operation, or "" when the worktree is clean.
type Op string

const (
	OpNone       Op = ""
	OpMerge      Op = "merge"
	OpRebase     Op = "rebase"
	OpCherryPick Op = "cherry-pick"
	OpRevert     Op = "revert"
	OpBisect     Op = "bisect"
)

// State describes an in-progress git operation in one worktree.
type State struct {
	Path     string    // worktree path as given
	Op       Op        // "" when clean or not a repo
	Head     string    // MERGE_HEAD / REBASE_HEAD sha, "" when unavailable
	Unmerged int       // distinct paths reported by `git ls-files -u`
	Since    time.Time // mtime of the state file/dir; zero when unknown
	Branch   string    // current branch, "" when detached
}

// Age reports how long the operation has been in progress. A zero or future
// Since is "age unknown" and yields 0 — callers must treat that as unknown,
// never as "young enough to ignore".
func (s State) Age() time.Duration {
	if s.Since.IsZero() {
		return 0
	}
	age := time.Since(s.Since)
	if age < 0 {
		return 0
	}
	return age
}

// AgeKnown reports whether Since carries a usable timestamp.
func (s State) AgeKnown() bool {
	return !s.Since.IsZero() && !s.Since.After(time.Now())
}

// String renders the state for a log line or a doctor detail.
func (s State) String() string {
	if s.Op == OpNone {
		return fmt.Sprintf("%s: clean", s.Path)
	}
	age := "age=unknown"
	if s.AgeKnown() {
		age = "age=" + s.Age().Truncate(time.Second).String()
	}
	head := s.Head
	if head == "" {
		head = "unknown"
	}
	return fmt.Sprintf("%s: %s in progress (head=%s, unmerged=%d, %s)",
		s.Path, s.Op, head, s.Unmerged, age)
}

// runGit runs a git command in path and returns trimmed stdout.
func runGit(path string, args ...string) (string, error) {
	full := append([]string{"-C", path}, args...)
	// G204: the subcommand is a package-local constant list; only the worktree
	// path and git-owned arguments vary.
	out, err := exec.Command("git", full...).Output() //nolint:gosec
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// gitDir resolves the git directory for path. For a linked worktree this is
// <common>/worktrees/<name>, NOT <path>/.git — the whole incident this package
// exists for happened in a linked worktree, so resolving by hand would catch
// nothing.
func gitDir(path string) (string, error) {
	out, err := runGit(path, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", err
	}
	if out == "" {
		return "", fmt.Errorf("empty git dir for %s", path)
	}
	return out, nil
}

// opProbe pairs a state marker inside the git dir with the operation it means.
// Order matters: rebase markers are checked before MERGE_HEAD, because an
// interactive rebase resolving a conflict has both.
var opProbes = []struct {
	entry string
	op    Op
}{
	{"rebase-merge", OpRebase},
	{"rebase-apply", OpRebase},
	{"MERGE_HEAD", OpMerge},
	{"CHERRY_PICK_HEAD", OpCherryPick},
	{"REVERT_HEAD", OpRevert},
	{"BISECT_LOG", OpBisect},
}

// Inspect reports the in-progress operation in the worktree at path.
//
// A path that does not exist, is not a git repository, or is clean returns
// Op == OpNone and a nil error: callers scan paths from configuration that may
// legitimately have gone away, and a stale entry must never make a health
// check fail.
func Inspect(path string) (State, error) {
	st := State{Path: path}
	if path == "" {
		return st, nil
	}
	if _, err := os.Stat(path); err != nil {
		return st, nil
	}
	dir, err := gitDir(path)
	if err != nil {
		return st, nil // not a repo
	}

	for _, probe := range opProbes {
		marker := filepath.Join(dir, probe.entry)
		info, statErr := os.Stat(marker)
		if statErr != nil {
			continue
		}
		st.Op = probe.op
		st.Since = info.ModTime()
		break
	}
	if st.Op == OpNone {
		return st, nil
	}

	st.Head = readOpHead(path, dir, st.Op)
	st.Unmerged = countUnmerged(path)
	if branch, berr := runGit(path, "branch", "--show-current"); berr == nil {
		st.Branch = branch
	}
	return st, nil
}

// readOpHead returns the sha the operation is applying, best effort.
func readOpHead(path, dir string, op Op) string {
	var candidates []string
	switch op {
	case OpMerge:
		candidates = []string{"MERGE_HEAD"}
	case OpCherryPick:
		candidates = []string{"CHERRY_PICK_HEAD"}
	case OpRevert:
		candidates = []string{"REVERT_HEAD"}
	case OpRebase:
		candidates = []string{"REBASE_HEAD", "rebase-merge/orig-head", "rebase-apply/original-commit"}
	case OpBisect:
		candidates = []string{"BISECT_START"}
	case OpNone:
		return ""
	}
	for _, name := range candidates {
		data, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // G304: name is a package constant, dir comes from git rev-parse
		if err != nil {
			continue
		}
		if line := firstLine(string(data)); line != "" {
			return line
		}
	}
	// Fall back to asking git, which knows about pseudo-refs we do not.
	if op == OpMerge {
		if out, err := runGit(path, "rev-parse", "--verify", "--quiet", "MERGE_HEAD"); err == nil {
			return out
		}
	}
	return ""
}

func firstLine(s string) string {
	if idx := strings.IndexAny(s, "\r\n"); idx >= 0 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}

// countUnmerged counts distinct conflicted paths. Zero unmerged alongside a
// live MERGE_HEAD is still a real, blocking state (an auto-merged but
// uncommitted merge), so callers must not use this as the presence test.
func countUnmerged(path string) int {
	out, err := runGit(path, "ls-files", "-u")
	if err != nil || out == "" {
		return 0
	}
	seen := map[string]struct{}{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		// Format: <mode> <sha> <stage>\t<path>
		tab := strings.IndexByte(line, '\t')
		if tab < 0 || tab+1 >= len(line) {
			continue
		}
		seen[line[tab+1:]] = struct{}{}
	}
	return len(seen)
}

// abortVerbs maps an operation to the git invocation that undoes it.
var abortVerbs = map[Op][]string{
	OpMerge:      {"merge", "--abort"},
	OpRebase:     {"rebase", "--abort"},
	OpCherryPick: {"cherry-pick", "--abort"},
	OpRevert:     {"revert", "--abort"},
	OpBisect:     {"bisect", "reset"},
}

// Abort undoes the in-progress operation. It is destructive — it throws away
// every conflict resolution in the worktree — so it is only ever reached from
// an explicit operator action.
func Abort(path string, op Op) error {
	args, ok := abortVerbs[op]
	if !ok {
		return fmt.Errorf("no abort for op %q", op)
	}
	full := append([]string{"-C", path}, args...)
	out, err := exec.Command("git", full...).CombinedOutput() //nolint:gosec // G204: abort verbs come from abortVerbs, not from input
	if err != nil {
		return fmt.Errorf("git %s in %s: %w: %s", strings.Join(args, " "), path, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Snapshot writes what the aborted state contained into destDir, so an abort
// is recoverable by a human afterwards.
//
// Best effort by design: a failure on any one artifact is recorded in
// README.txt and does not fail the call. A snapshot that cannot be written must
// never block the abort an operator explicitly asked for.
func Snapshot(path, destDir string) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("create snapshot dir %s: %w", destDir, err)
	}

	var problems []string
	dir, err := gitDir(path)
	if err == nil {
		for _, name := range []string{"MERGE_HEAD", "MERGE_MSG", "CHERRY_PICK_HEAD", "REVERT_HEAD", "REBASE_HEAD"} {
			data, readErr := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // G304: name is a package constant, dir comes from git rev-parse
			if readErr != nil {
				continue
			}
			if writeErr := os.WriteFile(filepath.Join(destDir, name), data, 0o644); writeErr != nil {
				problems = append(problems, fmt.Sprintf("%s: %v", name, writeErr))
			}
		}
	} else {
		problems = append(problems, fmt.Sprintf("git dir: %v", err))
	}

	captures := []struct {
		file string
		args []string
	}{
		{"status.txt", []string{"status", "--porcelain=v2", "--branch"}},
		{"worktree.diff", []string{"diff", "HEAD"}},
		{"unmerged.txt", []string{"ls-files", "-u"}},
	}
	for _, c := range captures {
		out, runErr := runGit(path, c.args...)
		if runErr != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", c.file, runErr))
			continue
		}
		if writeErr := os.WriteFile(filepath.Join(destDir, c.file), []byte(out+"\n"), 0o644); writeErr != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", c.file, writeErr))
		}
	}

	st, _ := Inspect(path)
	readme := fmt.Sprintf("loom gitstate snapshot\npath: %s\nop: %s\nhead: %s\nunmerged: %d\nbranch: %s\ntaken: %s\n",
		path, st.Op, st.Head, st.Unmerged, st.Branch, time.Now().Format(time.RFC3339))
	if len(problems) > 0 {
		readme += "\nincomplete artifacts:\n  " + strings.Join(problems, "\n  ") + "\n"
	}
	if writeErr := os.WriteFile(filepath.Join(destDir, "README.txt"), []byte(readme), 0o644); writeErr != nil {
		return fmt.Errorf("write snapshot README: %w", writeErr)
	}
	return nil
}
