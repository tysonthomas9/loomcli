package status

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
)

const MaxChangedEntries = 5000

type Entry struct {
	Kind       string `json:"kind"`
	Workspace  string `json:"workspace,omitempty"`
	Repo       string `json:"repo,omitempty"`
	ID         string `json:"id,omitempty"`
	Path       string `json:"path,omitempty"`
	State      string `json:"state"`
	Reason     string `json:"reason,omitempty"`
	NextAction string `json:"next_action,omitempty"`
	SHA        string `json:"sha,omitempty"`
	Drift      string `json:"drift,omitempty"`
	Behind     int    `json:"behind,omitempty"`
	Ahead      int    `json:"ahead,omitempty"`
	BaseDrift  string `json:"base_drift,omitempty"`
	BaseBehind int    `json:"base_behind,omitempty"`
	BaseAhead  int    `json:"base_ahead,omitempty"`
	// Enforcement is "unenforced" on cross-repo delivery the provider does not hold back.
	Enforcement string `json:"enforcement,omitempty"`
}

type ChangedEntry struct {
	Path    string `json:"path"`
	Tracked bool   `json:"tracked"`
}

type Snapshot struct {
	Entries      []Entry        `json:"entries"`
	Changed      []ChangedEntry `json:"changed"`
	ChangedTotal int            `json:"changed_total"`
	Truncated    bool           `json:"truncated"`
}

type Cache struct {
	mu         sync.Mutex
	ready      bool
	running    chan struct{}
	generation uint64
	value      Snapshot
	err        error
}

func (cache *Cache) Invalidate() {
	cache.mu.Lock()
	cache.ready = false
	cache.generation++
	cache.mu.Unlock()
}

func (cache *Cache) Read(ctx context.Context, scan func(context.Context) (Snapshot, error)) (Snapshot, error) {
	for {
		cache.mu.Lock()
		if cache.ready {
			value, err := cache.value, cache.err
			cache.mu.Unlock()
			return value, err
		}
		if cache.running != nil {
			done := cache.running
			cache.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return Snapshot{}, ctx.Err()
			}
		}
		cache.running = make(chan struct{})
		generation := cache.generation
		cache.mu.Unlock()
		value, err := scan(ctx)
		cache.mu.Lock()
		stale := cache.generation != generation
		if !stale {
			cache.value, cache.err, cache.ready = value, err, true
		}
		close(cache.running)
		cache.running = nil
		cache.mu.Unlock()
		if stale {
			continue
		}
		return value, err
	}
}

// Scan reads host-local records and Git state without changing either.
//
//nolint:funlen,gocognit // Inventory classification handles each durable object kind in one pass.
func Scan(ctx context.Context, integrity bool) (Snapshot, error) {
	out := Snapshot{Entries: []Entry{}, Changed: []ChangedEntry{}}
	local, err := bootstrap.LoadStateCache()
	if err != nil {
		return out, err
	}
	path := filepath.Join(config.GetConfigDir(), "loomgit", "store.db")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return scanWithoutJournal(ctx, local), nil
	} else if err != nil {
		return out, err
	}
	store, err := journal.OpenSQLiteReadOnly(path)
	if err != nil {
		return out, err
	}
	defer func() { _ = store.Close() }()
	rows, err := store.Inventory(ctx)
	if err != nil {
		return out, err
	}
	runners := make(map[string]*gitexec.Runner)
	trunks := make(map[string]string)
	knownPaths := make(map[string]bool)
	seenWorkspaces := make(map[string]bool)
	for _, state := range local.Workspaces {
		for _, agent := range state.Agents {
			if agent.Worktree != "" {
				knownPaths[canonicalPath(agent.Worktree)] = true
			}
		}
	}
	for _, repo := range rows.Repos {
		seenWorkspaces[repo.Workspace] = true
		trunks[repo.Workspace+"\x00"+repo.Repo] = repo.Trunk
		path := localworkspace.RepoPath(local.Workspaces[repo.Workspace], repo.Repo)
		item := Entry{Kind: "workspace", Workspace: repo.Workspace, Repo: repo.Repo, Path: path, State: "current", Reason: "recorded workspace repository", SHA: repo.BaseSHA}
		if path == "" {
			item.State, item.NextAction = "missing_checkout", "restore the workspace checkout"
		} else {
			runner, runErr := gitexec.New(path, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
			if runErr != nil {
				item.State, item.Reason = "unavailable", runErr.Error()
			} else {
				runners[repo.Workspace+"\x00"+repo.Repo] = runner
				setDrift(ctx, runner, repo.BaseSHA, repo.Trunk, &item)
				if err := collectChanged(ctx, runner, &out); err != nil {
					return out, err
				}
			}
		}
		out.Entries = append(out.Entries, item)
		if path != "" {
			knownPaths[canonicalPath(path)] = true
		}
	}
	for workspace, state := range local.Workspaces {
		if !seenWorkspaces[workspace] {
			out.Entries = append(out.Entries, Entry{Kind: "workspace", Workspace: workspace, Path: state.Path, State: "unverified", Reason: "local workspace has no Loom Git repository record"})
		}
	}
	appendAreas(rows.Areas, knownPaths, &out)
	appendRevisions(ctx, rows, runners, integrity, &out)
	appendPublications(ctx, rows.Publications, runners, integrity, &out)
	appendMirrorRefs(ctx, rows.Mirrors, integrity, &out)
	policies, err := store.LeadMergePolicies(ctx)
	if err != nil {
		return out, err
	}
	for _, policy := range policies {
		out.Entries = append(out.Entries, Entry{Kind: "policy", Workspace: policy.Workspace, ID: "lead_may_merge",
			State: "warning", Reason: "lead_may_merge=when_green set by " + policy.SetBy + "; " + journal.LeadMayMergeWarning,
			NextAction: "require reviews in the provider's branch protection, or turn lead_may_merge off"})
	}
	if err := appendDependencyChecks(ctx, store, &out); err != nil {
		return out, err
	}
	for workspace, state := range local.Workspaces {
		discoverCopies(ctx, workspace, state.Path, runners, trunks, knownPaths, integrity, &out)
	}
	for key, runner := range runners {
		discoverWorktrees(ctx, key, runner, knownPaths, &out)
	}
	sortEntries(&out)
	sortChanged(&out)
	return out, nil
}

func appendMirrorRefs(ctx context.Context, mirrors []journal.MirrorRecord, integrity bool, out *Snapshot) {
	for _, record := range mirrors {
		if record.SHA == "" || record.Repo == "" {
			continue
		}
		workspace, _ := strings.CutPrefix(record.Ref, refname.WorkspaceRefPrefix)
		workspace, _, _ = strings.Cut(workspace, "/")
		item := Entry{Kind: "ref", Workspace: workspace, Path: record.Repo, ID: record.Ref, State: record.State, Reason: record.Reason, SHA: record.SHA}
		runner, err := gitexec.New(record.Repo, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
		if err != nil {
			item.State, item.Reason = "integrity_unverified", err.Error()
		} else if err := checkRef(ctx, runner, record.Ref, record.SHA, integrity); err != nil {
			item.State, item.Reason, item.NextAction = "integrity_missing", fmt.Sprintf("ref %s expected %s: %v", record.Ref, record.SHA, err), "restore the recorded ref"
		}
		out.Entries = append(out.Entries, item)
	}
}

func appendPublications(ctx context.Context, publications []journal.Publication, runners map[string]*gitexec.Runner, integrity bool, out *Snapshot) {
	for _, publication := range publications {
		item := Entry{Kind: "publication", Workspace: publication.Workspace, Repo: publication.Repo, ID: publication.Change, State: publication.Phase, Reason: "durable publication", SHA: publication.Head}
		if runner := runners[publication.Workspace+"\x00"+publication.Repo]; runner != nil {
			setDrift(ctx, runner, publication.Head, publication.Trunk, &item)
			if integrity && publication.Phase == "done" {
				if ref, err := refname.Publication(publication.Workspace, publication.Change); err == nil {
					if err := checkRef(ctx, runner, ref, publication.Head, true); err != nil {
						item.State, item.Reason, item.NextAction = "integrity_missing", fmt.Sprintf("publication ref %s expected %s: %v", ref, publication.Head, err), "restore the publication ref or object"
					}
				}
			}
		}
		if item.Drift == "diverged" {
			item.NextAction = "review and restack the PR"
		}
		out.Entries = append(out.Entries, item)
	}
}

// appendDependencyChecks shows loom/dependencies per change and whether each
// repository enforces it. A change whose repository does not require the check
// from the Loom app is flagged: only Loom's own merges keep the order there.
func appendDependencyChecks(ctx context.Context, store *journal.SQLite, out *Snapshot) error {
	checks, enforcement, err := store.DependencyChecks(ctx)
	if err != nil {
		return err
	}
	unenforced := map[string]string{}
	for _, row := range enforcement {
		item := Entry{Kind: "dependency_enforcement", Repo: row.Repo, ID: row.Branch, State: row.State, Reason: row.Reason}
		switch row.State {
		case "not_enforced", "unknown":
			item.NextAction = "ask a repo admin to require loom/dependencies from the Loom app in branch protection"
			unenforced[row.Repo] = "not_enforced"
		case "not_pinned":
			item.NextAction = "ask a repo admin to pin the required loom/dependencies check to the Loom app"
			unenforced[row.Repo] = "not_pinned"
		}
		out.Entries = append(out.Entries, item)
	}
	for _, check := range checks {
		item := Entry{Kind: "dependency", Workspace: check.Workspace, Repo: check.Repo, ID: check.Change, State: check.State, Reason: check.Reason}
		if state := unenforced[check.Repo]; state != "" {
			item.Enforcement = "unenforced"
			item.Reason += "; cross-repo order is " + strings.ReplaceAll(state, "_", " ") + " on " + check.Repo
		}
		out.Entries = append(out.Entries, item)
	}
	return nil
}

func scanWithoutJournal(ctx context.Context, local *bootstrap.StateCache) Snapshot {
	out := Snapshot{Entries: []Entry{}, Changed: []ChangedEntry{}}
	runners := make(map[string]*gitexec.Runner)
	known := make(map[string]bool)
	for workspace, state := range local.Workspaces {
		out.Entries = append(out.Entries, Entry{Kind: "workspace", Workspace: workspace, Path: state.Path, State: "unverified", Reason: "local workspace has no Loom Git journal"})
		for repo, path := range state.Repos {
			runner, err := gitexec.New(path, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
			if err == nil {
				runners[workspace+"\x00"+repo] = runner
			}
		}
		for _, agent := range state.Agents {
			if agent.Worktree != "" {
				known[canonicalPath(agent.Worktree)] = true
			}
		}
		discoverCopies(ctx, workspace, state.Path, runners, nil, known, false, &out)
	}
	for key, runner := range runners {
		discoverWorktrees(ctx, key, runner, known, &out)
	}
	sortEntries(&out)
	return out
}

func sortEntries(out *Snapshot) {
	sort.Slice(out.Entries, func(left, right int) bool {
		first, second := out.Entries[left], out.Entries[right]
		return cmp.Or(
			cmp.Compare(first.Workspace, second.Workspace),
			cmp.Compare(first.Kind, second.Kind),
			cmp.Compare(first.Repo, second.Repo),
			cmp.Compare(first.ID, second.ID),
			cmp.Compare(first.Path, second.Path),
		) < 0
	})
}

func appendAreas(areas []journal.WorkingArea, knownPaths map[string]bool, out *Snapshot) {
	for _, area := range areas {
		item := Entry{Kind: "working_area", Workspace: area.Workspace, Repo: area.Repo, ID: area.Lead, Path: area.Path, State: "kept", Reason: "recorded working area", SHA: area.BaseSHA}
		if _, err := os.Stat(area.Path); err != nil {
			item.State, item.NextAction = "missing_checkout", "restore the recorded working area"
		}
		out.Entries = append(out.Entries, item)
		knownPaths[canonicalPath(area.Path)] = true
	}
}

func sortChanged(out *Snapshot) {
	sort.SliceStable(out.Changed, func(i, j int) bool { return out.Changed[i].Tracked && !out.Changed[j].Tracked })
	if len(out.Changed) > MaxChangedEntries {
		out.Changed = out.Changed[:MaxChangedEntries]
		out.Truncated = true
	}
}

func appendRevisions(ctx context.Context, rows journal.Inventory, runners map[string]*gitexec.Runner, integrity bool, out *Snapshot) {
	for _, revision := range rows.Revisions {
		item := Entry{Kind: "change", Workspace: revision.Workspace, ID: fmt.Sprintf("%s/r%d", revision.Change, revision.Number), State: revision.Outcome, Reason: "durable revision", SHA: revision.HeadSHA}
		if !revision.Ready {
			item.State = "incomplete"
		}
		found := false
		checked := false
		missingReason := fmt.Sprintf("change %s has no matching local revision ref at %s", revision.Change, revision.HeadSHA)
		for _, repo := range rows.Repos {
			if repo.Workspace != revision.Workspace {
				continue
			}
			runner := runners[repo.Workspace+"\x00"+repo.Repo]
			if runner == nil {
				continue
			}
			checked = true
			ref, refErr := refname.RevisionHead(revision.Workspace, revision.Change, strconv.Itoa(revision.Number))
			if refErr != nil {
				continue
			}
			baseRef, baseErr := refname.RevisionBase(revision.Workspace, revision.Change, strconv.Itoa(revision.Number))
			if baseErr != nil {
				continue
			}
			if err := checkRef(ctx, runner, ref, revision.HeadSHA, integrity); err != nil {
				missingReason = fmt.Sprintf("change %s ref %s expected %s: %v", revision.Change, ref, revision.HeadSHA, err)
				continue
			}
			if err := checkRef(ctx, runner, baseRef, revision.BaseSHA, integrity); err != nil {
				missingReason = fmt.Sprintf("change %s ref %s expected %s: %v", revision.Change, baseRef, revision.BaseSHA, err)
				continue
			}
			item.Repo = repo.Repo
			setDrift(ctx, runner, revision.HeadSHA, repo.Trunk, &item)
			var base Entry
			setDrift(ctx, runner, revision.HeadSHA, revision.BaseSHA, &base)
			item.BaseDrift, item.BaseBehind, item.BaseAhead = base.Drift, base.Behind, base.Ahead
			found = true
			break
		}
		if revision.Ready && !checked {
			item.State, item.Reason, item.NextAction = "integrity_unverified", "no local repository is available to verify recorded refs", "restore the checkout and rerun doctor"
		}
		if revision.Ready && checked && !found {
			item.State, item.Reason, item.NextAction = "integrity_missing", missingReason, "inspect the missing ref and restore from the provider or backup"
		}
		out.Entries = append(out.Entries, item)
	}
}

func discoverCopies(ctx context.Context, workspace, root string, runners map[string]*gitexec.Runner, trunks map[string]string, knownPaths map[string]bool, integrity bool, out *Snapshot) {
	if root == "" {
		return
	}
	base := filepath.Join(root, ".loom", "task-copies")
	repos, _ := os.ReadDir(base)
	for _, repo := range repos {
		if !repo.IsDir() {
			continue
		}
		copies, _ := os.ReadDir(filepath.Join(base, repo.Name()))
		for _, copy := range copies {
			if !copy.IsDir() {
				continue
			}
			path := filepath.Join(base, repo.Name(), copy.Name())
			item := Entry{Kind: "task_copy", Workspace: workspace, Repo: repo.Name(), ID: copy.Name(), Path: path, State: "kept", Reason: "task copy remains available for recovery", NextAction: "review before removing"}
			ref, err := refname.AttemptBase(workspace, copy.Name())
			runner := runners[workspace+"\x00"+repo.Name()]
			if len(trunks) == 0 || err != nil || runner == nil {
				item.State, item.Reason = "unowned", "no matching Loom repository or attempt"
			} else if sha, err := runner.Run(ctx, "rev-parse", "--verify", ref+"^{commit}"); err != nil {
				item.State, item.Reason = "unowned", "no recorded attempt base ref"
			} else {
				item.SHA = strings.TrimSpace(string(sha))
				if err := checkRef(ctx, runner, ref, item.SHA, integrity); err != nil {
					item.State, item.Reason, item.NextAction = "integrity_missing", err.Error(), "restore the attempt base ref or object"
				} else {
					setDrift(ctx, runner, ref, trunks[workspace+"\x00"+repo.Name()], &item)
				}
			}
			out.Entries = append(out.Entries, item)
			knownPaths[canonicalPath(path)] = true
		}
	}
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), ".loom-local-runner-") {
			out.Entries = append(out.Entries, Entry{Kind: "checkout", Workspace: workspace, Path: filepath.Join(root, entry.Name()), State: "unowned", Reason: "no Loom manifest", NextAction: "inspect this checkout manually"})
		}
	}
}

func discoverWorktrees(ctx context.Context, key string, runner *gitexec.Runner, knownPaths map[string]bool, out *Snapshot) {
	data, err := runner.Run(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return
	}
	parts := strings.SplitN(key, "\x00", 2)
	if len(parts) != 2 {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		path, ok := strings.CutPrefix(line, "worktree ")
		if !ok || knownPaths[canonicalPath(path)] {
			continue
		}
		if !strings.Contains(filepath.ToSlash(path), "/worktrees/") && !strings.Contains(filepath.ToSlash(path), "/pr-worktrees/") {
			continue
		}
		out.Entries = append(out.Entries, Entry{Kind: "checkout", Workspace: parts[0], Repo: parts[1], Path: canonicalPath(path), State: "unowned", Reason: "worktree has no recorded Loom owner", NextAction: "inspect the checkout manually"})
	}
}

func canonicalPath(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

func checkRef(ctx context.Context, runner *gitexec.Runner, ref, want string, integrity bool) error {
	if want == "" {
		return fmt.Errorf("missing recorded SHA")
	}
	got, err := runner.Run(ctx, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(got)) != want {
		return fmt.Errorf("ref SHA differs")
	}
	if integrity {
		_, err = runner.Run(ctx, "fsck", "--no-reflogs", "--connectivity-only", want)
	}
	return err
}

func setDrift(ctx context.Context, runner *gitexec.Runner, base, trunk string, item *Entry) {
	if base == "" || trunk == "" {
		return
	}
	counts, err := runner.Run(ctx, "rev-list", "--left-right", "--count", base+"..."+trunk)
	if err != nil {
		item.Drift = "unavailable"
		return
	}
	fields := strings.Fields(string(counts))
	if len(fields) != 2 {
		item.Drift = "unavailable"
		return
	}
	item.Ahead, _ = strconv.Atoi(fields[0])
	item.Behind, _ = strconv.Atoi(fields[1])
	switch {
	case item.Ahead > 0 && item.Behind > 0:
		item.Drift = "diverged"
	case item.Behind > 0:
		item.Drift = "behind"
	default:
		item.Drift = "current"
	}
}

func collectChanged(ctx context.Context, runner *gitexec.Runner, out *Snapshot) error {
	data, err := runner.RunWithEnv(ctx, map[string]string{"GIT_OPTIONAL_LOCKS": "0"}, "status", "--porcelain=v1", "-z", "--no-renames", "--untracked-files=all")
	if err != nil {
		return err
	}
	var tracked, untracked []ChangedEntry
	for _, record := range strings.Split(string(data), "\x00") {
		if len(record) < 4 {
			continue
		}
		entry := ChangedEntry{Path: record[3:], Tracked: record[:2] != "??"}
		if entry.Tracked {
			tracked = append(tracked, entry)
		} else {
			untracked = append(untracked, entry)
		}
	}
	sort.Slice(tracked, func(i, j int) bool { return tracked[i].Path < tracked[j].Path })
	sort.Slice(untracked, func(i, j int) bool { return untracked[i].Path < untracked[j].Path })
	out.ChangedTotal += len(tracked) + len(untracked)
	out.Changed = append(out.Changed, tracked...)
	out.Changed = append(out.Changed, untracked...)
	return nil
}
