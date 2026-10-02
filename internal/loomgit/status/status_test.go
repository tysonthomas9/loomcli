package status

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

func TestCacheSingleFlightAndInvalidation(t *testing.T) {
	var cache Cache
	var scans atomic.Int32
	scan := func(context.Context) (Snapshot, error) {
		scans.Add(1)
		time.Sleep(10 * time.Millisecond)
		return Snapshot{ChangedTotal: 1}, nil
	}
	var workers sync.WaitGroup
	for range 20 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			result, err := cache.Read(context.Background(), scan)
			if err != nil || result.ChangedTotal != 1 {
				t.Errorf("read: %+v, %v", result, err)
			}
		}()
	}
	workers.Wait()
	if scans.Load() != 1 {
		t.Fatalf("scanned %d times", scans.Load())
	}
	cache.Invalidate()
	if _, err := cache.Read(context.Background(), scan); err != nil {
		t.Fatal(err)
	}
	if scans.Load() != 2 {
		t.Fatalf("invalidation scanned %d times", scans.Load())
	}
}

func TestChangedEntriesTrackBeforeTruncation(t *testing.T) {
	repo := t.TempDir()
	command := exec.Command("git", "init", "-q") //nolint:norawexec // Real repository acceptance test.
	command.Dir = repo
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", output, err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tracked"), []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	command = exec.Command("git", "add", "tracked") //nolint:norawexec // Real repository acceptance test.
	command.Dir = repo
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git add: %s: %v", output, err)
	}
	for number := 0; number < 5000; number++ {
		if err := os.WriteFile(filepath.Join(repo, fmt.Sprintf("u-%04d", number)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runner, err := gitexec.New(repo, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Test", Email: "test@example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.RunWithEnv(context.Background(), map[string]string{"GIT_OPTIONAL_LOCKS": "0"}, "status", "--porcelain=v1", "-z", "--no-renames", "--untracked-files=all"); err != nil {
		t.Fatal(err)
	}
	var snapshot Snapshot
	if err := collectChanged(context.Background(), runner, &snapshot); err != nil {
		t.Fatal(err)
	}
	sortChanged(&snapshot)
	if snapshot.ChangedTotal != 5001 || !snapshot.Truncated || len(snapshot.Changed) != 5000 || !snapshot.Changed[0].Tracked || snapshot.Changed[0].Path != "tracked" {
		t.Fatalf("bad changed cap: total=%d truncated=%v first=%+v len=%d", snapshot.ChangedTotal, snapshot.Truncated, snapshot.Changed[0], len(snapshot.Changed))
	}
}

func TestScanShowsBehindAndMissingRef(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ctx := context.Background()
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", args...) //nolint:norawexec // Real repository acceptance test.
		command.Dir = repo
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s: %v", args, output, err)
		}
		return strings.TrimSpace(string(output))
	}
	git("init", "-q")
	git("config", "user.name", "Test")
	git("config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(repo, "file"), []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "file")
	git("commit", "-qm", "one")
	base := git("rev-parse", "HEAD")
	git("branch", "trunk")
	if err := os.WriteFile(filepath.Join(repo, "file"), []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("commit", "-qam", "two")
	git("branch", "-f", "trunk")
	path := filepath.Join(os.Getenv("LOOM_CONFIG_DIR"), "loomgit", "store.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := journal.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`INSERT INTO workspace_repos VALUES ('W1','repo','trunk','branch',?)`, base); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO change_revisions(workspace,change_id,number,request_id,kind,operation,outcome,base_sha,head_sha,tree_hash,source_head_sha,ready) VALUES ('W1','C1',1,'R1','source','capture','captured',?,?,?, ?,1)`, base, base, "tree", base); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE mirror_refs(repo TEXT,ref TEXT,remote TEXT,sha TEXT,state TEXT,reason TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO mirror_refs VALUES (?,'refs/loom/ws/W1/attempt/A1/capture','',?,'mirrored','')`, repo, base); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	copyPath := filepath.Join(root, ".loom", "task-copies", "repo", "T1")
	runnerPath := filepath.Join(root, ".loom-local-runner-orphan")
	for _, path := range []string{copyPath, runnerPath} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	prPath := filepath.Join(root, ".loom", "pr-worktrees", "repo", "pr-1")
	if err := os.MkdirAll(filepath.Dir(prPath), 0o700); err != nil {
		t.Fatal(err)
	}
	git("worktree", "add", "-q", "-b", "pr-1", prPath)
	if err := bootstrap.MutateWorkspaceLocalState("W1", func(state *bootstrap.WorkspaceLocalState) error {
		state.Path = root
		state.Repos = map[string]string{"repo": repo}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := Scan(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	var behind, missing, captureMissing bool
	unowned := make(map[string]bool)
	for _, entry := range snapshot.Entries {
		if entry.Kind == "workspace" && entry.Drift == "behind" && entry.Behind == 1 {
			behind = true
		}
		if entry.Kind == "change" && entry.State == "integrity_missing" && entry.SHA == base {
			missing = true
		}
		if entry.Kind == "ref" && strings.Contains(entry.ID, "/capture") && entry.State == "integrity_missing" && entry.SHA == base {
			captureMissing = true
		}
		if entry.State == "unowned" {
			unowned[entry.Path] = true
		}
	}
	if !behind || !missing || !captureMissing || !unowned[copyPath] || !unowned[runnerPath] || !unowned[canonicalPath(prPath)] {
		t.Fatalf("behind=%v missing=%v captureMissing=%v unowned=%v entries=%+v", behind, missing, captureMissing, unowned, snapshot.Entries)
	}
	git("update-ref", "refs/loom/ws/W1/attempt/T1/base", base)
	snapshot, err = Scan(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	copyBehind := false
	for _, entry := range snapshot.Entries {
		if entry.Kind == "task_copy" && entry.ID == "T1" && entry.State == "kept" && entry.Drift == "behind" && entry.Behind == 1 {
			copyBehind = true
		}
	}
	if !copyBehind {
		t.Fatalf("kept copy behind trunk missing: %+v", snapshot.Entries)
	}
	git("update-ref", "refs/loom/ws/W1/change/C1/1/head", base)
	git("update-ref", "refs/loom/ws/W1/change/C1/1/base", base)
	snapshot, err = Scan(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range snapshot.Entries {
		if entry.Kind == "change" && entry.State == "integrity_missing" {
			t.Fatalf("healthy ref failed integrity check: %+v", entry)
		}
		if entry.Kind == "change" && (entry.Drift != "behind" || entry.Behind != 1 || entry.BaseDrift != "current") {
			t.Fatalf("change drift omitted: %+v", entry)
		}
	}
	if _, err := db.Exec(`INSERT INTO change_publications(workspace,change_id,repo,branch,trunk,slug,head_sha,phase) VALUES ('W1','C1','repo','pr','trunk','c1',?,'done')`, base); err != nil {
		t.Fatal(err)
	}
	git("checkout", "-q", "-b", "pr", base)
	if err := os.WriteFile(filepath.Join(repo, "pr-only"), []byte("review"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "pr-only")
	git("commit", "-qm", "pr")
	prSHA := git("rev-parse", "HEAD")
	if _, err := db.Exec(`UPDATE change_publications SET head_sha=? WHERE workspace='W1' AND change_id='C1'`, prSHA); err != nil {
		t.Fatal(err)
	}
	snapshot, err = Scan(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	diverged := false
	for _, entry := range snapshot.Entries {
		if entry.Kind == "publication" && entry.Drift == "diverged" && entry.NextAction != "" {
			diverged = true
		}
	}
	if !diverged {
		t.Fatalf("diverged PR missing next action: %+v", snapshot.Entries)
	}
}

func TestScanReportsMissingRecordedRevision(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	path := filepath.Join(os.Getenv("LOOM_CONFIG_DIR"), "loomgit", "store.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := journal.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	revision, err := store.ReserveRevision(ctx, loomgit.Revision{Workspace: "W1", Change: "C1", RequestID: "capture-1", Kind: "source", Outcome: "captured", BaseSHA: "abc", TreeHash: "tree", SourceHeadSHA: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	revision.HeadSHA = strings.Repeat("a", 40)
	if err := store.FinishRevision(ctx, revision); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := Scan(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Entries) != 1 || snapshot.Entries[0].State != "integrity_unverified" || snapshot.Entries[0].SHA != revision.HeadSHA {
		t.Fatalf("missing integrity finding: %+v", snapshot)
	}
}

func TestScanReportsUnownedWithoutJournal(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	path := filepath.Join(root, ".loom-local-runner-orphan")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.MutateWorkspaceLocalState("W1", func(state *bootstrap.WorkspaceLocalState) error { state.Path = root; return nil }); err != nil {
		t.Fatal(err)
	}
	snapshot, err := Scan(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Entries) != 2 || snapshot.Entries[0].State != "unowned" || snapshot.Entries[0].Path != path {
		t.Fatalf("unowned checkout hidden without journal: %+v", snapshot)
	}
}

func TestScanWarnsWhileLeadMayMergeWhenGreen(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	path := filepath.Join(os.Getenv("LOOM_CONFIG_DIR"), "loomgit", "store.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := journal.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	for _, value := range []string{"when_green", "off"} {
		if err := store.SetLeadMayMerge(ctx, "W1", value, "tyson"); err != nil {
			t.Fatal(err)
		}
		snapshot, err := Scan(ctx, false)
		if err != nil {
			t.Fatal(err)
		}
		var warnings []Entry
		for _, entry := range snapshot.Entries {
			if entry.Kind == "policy" {
				warnings = append(warnings, entry)
			}
		}
		if value == "off" {
			if len(warnings) != 0 {
				t.Fatalf("warning after policy off: %+v", warnings)
			}
			continue
		}
		if len(warnings) != 1 || warnings[0].Workspace != "W1" || warnings[0].State != "warning" ||
			!strings.Contains(warnings[0].Reason, "no required review") || !strings.Contains(warnings[0].Reason, "set by tyson") {
			t.Fatalf("missing no-required-review warning: %+v", snapshot.Entries)
		}
	}
}

func TestScanShowsUnenforcedCrossRepoDelivery(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	path := filepath.Join(os.Getenv("LOOM_CONFIG_DIR"), "loomgit", "store.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := journal.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, row := range []journal.DependencyEnforcement{
		{Repo: "owner/repo2", Branch: "main", State: "not_enforced", Reason: "not enforced: branch protection does not require loom/dependencies"},
		{Repo: "owner/repo3", Branch: "main", State: "not_pinned", Reason: "loom/dependencies is required but not pinned to the Loom app: any collaborator could post it"},
		{Repo: "owner/repo4", Branch: "main", State: "wrong_app", Reason: "loom/dependencies is pinned to an app Loom does not post as"},
	} {
		if err := store.RecordDependencyEnforcement(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.RecordDependencyCheck(ctx, journal.DependencyCheck{Workspace: "W", Change: "C2", Repo: "owner/repo2",
		State: "pending", Reason: "Waiting for owner/repo1#1 to land"}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordDependencyCheck(ctx, journal.DependencyCheck{Workspace: "W", Change: "C4", Repo: "owner/repo4",
		State: "pending", Reason: "Waiting for owner/repo1#1 to land"}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordDependencyCheck(ctx, journal.DependencyCheck{Workspace: "W", Change: "C5", Repo: "owner/repo5",
		State: "pending", Reason: "Waiting for owner/repo1#1 to land"}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDependencySynced(ctx, "W", "C5", false); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := Scan(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]Entry{}
	for _, entry := range snapshot.Entries {
		found[entry.Kind+":"+entry.Repo] = entry
	}
	repo2, repo3, change := found["dependency_enforcement:owner/repo2"], found["dependency_enforcement:owner/repo3"], found["dependency:owner/repo2"]
	if repo2.State != "not_enforced" || !strings.Contains(repo2.Reason, "not enforced") || repo2.NextAction == "" {
		t.Fatalf("repo2 enforcement = %+v", repo2)
	}
	if repo3.State != "not_pinned" || !strings.Contains(repo3.Reason, "any collaborator could post") {
		t.Fatalf("repo3 enforcement = %+v", repo3)
	}
	if repo4 := found["dependency_enforcement:owner/repo4"]; repo4.State != "wrong_app" || repo4.NextAction == "" {
		t.Fatalf("repo4 enforcement = %+v", repo4)
	}
	if change5 := found["dependency:owner/repo5"]; change5.State != "not_synced" ||
		!strings.Contains(change5.Reason, "dependency status not synced") || !strings.Contains(change5.Reason, "owner/repo1#1") {
		t.Fatalf("unsynced dependency status = %+v", change5)
	}
	if change.State != "pending" {
		t.Fatalf("synced dependency shown as %+v", change)
	}
	if change4 := found["dependency:owner/repo4"]; change4.Enforcement != "unenforced" {
		t.Fatalf("delivery on a repo pinned to another app not flagged: %+v", change4)
	}
	if change.ID != "C2" || change.State != "pending" || change.Enforcement != "unenforced" ||
		!strings.Contains(change.Reason, "owner/repo1#1") || !strings.Contains(change.Reason, "not enforced on owner/repo2") {
		t.Fatalf("dependent delivery = %+v", change)
	}
}

func TestScanShowsLandingAttention(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	path := filepath.Join(os.Getenv("LOOM_CONFIG_DIR"), "loomgit", "store.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := journal.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	for _, workspace := range []string{"W1", "W2"} {
		publication := journal.Publication{Workspace: workspace, Change: "A", Repo: "repo", Branch: "loom/ws/" + workspace + "/change/A",
			Trunk: "main", Slug: "owner/repo", Head: strings.Repeat("a", 40)}
		if err := store.BeginPublication(ctx, publication); err != nil {
			t.Fatal(err)
		}
		publication.Phase, publication.PRNumber = "done", 4
		if err := store.AdvancePublication(ctx, publication); err != nil {
			t.Fatal(err)
		}
	}
	reason := "publication mismatch: owned PR 4 does not match published change A"
	if _, err := store.RecordLandingAttention(ctx, "W1", "A", reason); err != nil {
		t.Fatal(err)
	}
	snapshot, err := Scan(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]Entry{}
	for _, entry := range snapshot.Entries {
		if entry.Kind == "publication" {
			states[entry.Workspace] = entry
		}
	}
	if got := states["W1"]; got.State != "attention_required" || got.Reason != reason || got.NextAction == "" {
		t.Fatalf("mismatched publication entry = %+v", got)
	}
	if got := states["W2"]; got.State != "done" {
		t.Fatalf("healthy publication entry = %+v", got)
	}
}
