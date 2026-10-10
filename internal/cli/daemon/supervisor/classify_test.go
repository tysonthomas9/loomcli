package supervisor

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olesho/harness-wrapper/pkg/discovery"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/loomgit/agentcapture"
	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
	"github.com/tysonthomas9/loomcli/internal/store"
	gitweb "github.com/tysonthomas9/loomcli/internal/webui/handlers/git"
)

type releaseOrderLeaseStore struct {
	store.AgentLeaseStore
	onRelease func()
}

func (lease *releaseOrderLeaseStore) Release(ctx context.Context, workspace, leaseID, token string) (*domain.AgentLease, error) {
	lease.onRelease()
	return nil, nil
}

type releaseOrderIssueBackend struct {
	*clitest.MockIssueBackend
	onRelease func()
}

func (issue *releaseOrderIssueBackend) ReleaseIssueAsActor(context.Context, string, string) error {
	issue.onRelease()
	return nil
}

type apiTaskRevision struct {
	Number     int    `json:"number"`
	HeadSHA    string `json:"head_sha"`
	Verdict    string `json:"verdict"`
	Incomplete bool   `json:"incomplete"`
	NoChanges  bool   `json:"no_changes"`
}

func taskRevisionResponse(t *testing.T) []apiTaskRevision {
	t.Helper()
	mux := http.NewServeMux()
	gitweb.NewModule(nil, nil).Register(mux)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest("GET", "/api/workspaces/WS/issues/task-1/revisions", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("revisions API: %d %s", response.Code, response.Body.String())
	}
	var body struct {
		Data []apiTaskRevision `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Data
}

func TestSpawnAndWaitFreezesCompletedRevision(t *testing.T) {
	dir := captureRepo(t)
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	base := gitForCaptureTest(t, dir, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(dir, "main.txt"), []byte("completed work\n"), 0600); err != nil {
		t.Fatal(err)
	}
	writeLockFile(t, dir, &cli.LockInfo{PID: os.Getpid(), AgentName: "agent", TaskID: "task-1"})
	previous := loomExecutablePath
	loomExecutablePath = func() (string, error) { return exec.LookPath("true") }
	t.Cleanup(func() { loomExecutablePath = previous })
	s := newTestSupervisor()
	s.WorkspaceID = "WS"
	s.Shutdown = make(chan struct{})
	s.Concurrency = NewConcurrencyTracker(nil)
	s.Concurrency.Acquire("task")
	s.EmitEvent = func(events.Event) {}
	baseStore := memstore.New()
	leaseReleases, claimReleases := 0, 0
	assertRevision := func() {
		revisions := taskRevisionResponse(t)
		if len(revisions) != 1 || revisions[0].Number != 1 || revisions[0].HeadSHA == "" || revisions[0].Verdict != "" {
			t.Fatalf("revision before release = %+v, want one unapproved revision", revisions)
		}
	}
	s.ControlStore = &controlPlaneStoreOverrides{Store: baseStore, leases: &releaseOrderLeaseStore{
		AgentLeaseStore: baseStore.AgentLeases(), onRelease: func() { leaseReleases++; assertRevision() },
	}}
	issues := clitest.NewMockIssueBackend()
	// The agent closed its task past the daemon before the run froze its
	// work: once frozen with code, the task goes back to review (P1.26).
	reviewed := 0
	issues.GetFn = func(_ context.Context, id string) (*backend.IssueDetailData, error) {
		return &backend.IssueDetailData{IssueData: backend.IssueData{ID: id, Status: "closed"}}, nil
	}
	issues.UpdateFn = func(_ context.Context, _ string, p backend.UpdateParams) error {
		if p.Status != nil && *p.Status == "review" && backend.HasCodeReviewLabel(p.AddLabels) {
			reviewed++
		}
		return nil
	}
	s.IssueBackend = &releaseOrderIssueBackend{MockIssueBackend: issues, onRelease: func() { claimReleases++; assertRevision() }}
	ap := &AgentProcess{Entry: config.AgentEntry{Worktree: "agent", Role: "task", Backend: "codex"},
		WorktreePath: dir, BeforeRef: base, AgentSessionID: "session-1", AssignedTaskID: "task-1",
		AgentLeaseID: "lease-1", AgentLeaseToken: "token-1"}
	s.spawnAndWait(ap)
	assertRevision()
	if leaseReleases != 1 || claimReleases != 1 {
		t.Fatalf("release calls = lease %d, claim %d; want one each", leaseReleases, claimReleases)
	}
	if reviewed != 1 {
		t.Fatalf("closed task put back in code review %d times after the freeze, want 1", reviewed)
	}
	ap.BeforeRef = base
	ap.AgentSessionID = "session-1"
	ap.AssignedTaskID = "task-1"
	writeLockFile(t, dir, &cli.LockInfo{PID: os.Getpid(), AgentName: "agent", TaskID: "task-1"})
	s.Concurrency.Acquire("task")
	s.spawnAndWait(ap)
	assertRevision()
	if reviewed != 2 {
		t.Fatalf("an already-frozen attempt must also put its closed task back in code review (got %d)", reviewed)
	}
	if ap.CaptureRetained {
		t.Fatal("repeated exit unexpectedly retained the worktree")
	}
}

func TestTwoSupervisorExitsApplyWithoutRuntimeFileConflicts(t *testing.T) {
	dir := captureRepo(t)
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	base := gitForCaptureTest(t, dir, "rev-parse", "HEAD")
	area := filepath.Join(t.TempDir(), "lead")
	gitForCaptureTest(t, dir, "worktree", "add", "-b", "loom/ws/WS/interactive/L", area, base)
	setupCaptureApplyArea(t, configDir, dir, area, base)
	previous := loomExecutablePath
	loomExecutablePath = func() (string, error) { return exec.LookPath("true") }
	t.Cleanup(func() { loomExecutablePath = previous })
	s := newTestSupervisor()
	s.WorkspaceID, s.Shutdown, s.Concurrency = "WS", make(chan struct{}), NewConcurrencyTracker(nil)
	s.EmitEvent = func(events.Event) {}
	s.ControlStore = memstore.New()
	s.IssueBackend = clitest.NewMockIssueBackend()
	ap := &AgentProcess{Entry: config.AgentEntry{Worktree: "agent", Role: "task", Backend: "codex"},
		WorktreePath: dir, BeforeRef: base, AssignedTaskID: "task-1"}
	for index := 1; index <= 2; index++ {
		writeRuntimeExitFiles(t, dir, index)
		ap.AgentSessionID = fmt.Sprintf("session-%d", index)
		writeLockFile(t, dir, &cli.LockInfo{PID: os.Getpid(), AgentName: "agent", TaskID: "task-1", RunID: ap.AgentSessionID})
		s.Concurrency.Acquire("task")
		s.spawnAndWait(ap)
		revisions := taskRevisionResponse(t)
		if len(revisions) != index || revisions[0].Number != index || revisions[0].HeadSHA == "" {
			t.Fatalf("exit %d revisions = %+v", index, revisions)
		}
		assertCapturedRuntimePaths(t, dir, revisions[0].HeadSHA)
		approveAndApplyExit(t, revisions[0], area)
	}
}

func setupCaptureApplyArea(t *testing.T, configDir, source, area, base string) {
	t.Helper()
	handle, err := bootstrap.OpenStore(context.Background(), configDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if _, err := handle.Store.Workspaces().Create(context.Background(), store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatal(err)
	}
	if _, err := handle.Store.Repos().Create(context.Background(), store.RepoCreate{WorkspaceKey: "WS", Name: filepath.Base(source)}); err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.MutateStateCache(func(state *bootstrap.StateCache) error {
		state.Workspaces["WS"] = bootstrap.WorkspaceLocalState{Path: configDir, Repos: map[string]string{filepath.Base(source): source}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(configDir, "loomgit"), 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(configDir, "loomgit", "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS working_areas (workspace TEXT NOT NULL, lead TEXT NOT NULL, repo TEXT NOT NULL, path TEXT NOT NULL, branch TEXT NOT NULL, base_sha TEXT NOT NULL, mode TEXT NOT NULL, PRIMARY KEY(workspace, lead, repo))`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO working_areas VALUES (?,?,?,?,?,?,?)`, "WS", "L", filepath.Base(source), area, "loom/ws/WS/interactive/L", base, "worktree"); err != nil {
		t.Fatal(err)
	}
}

func writeRuntimeExitFiles(t *testing.T, dir string, index int) {
	t.Helper()
	for path, value := range map[string]string{
		fmt.Sprintf("work-%d.txt", index): "agent work", ".agent.checkpoint.json": fmt.Sprint(index),
		".agent.lock.flock": fmt.Sprint(index), ".codex/hooks.json": fmt.Sprint(index),
		".claude/settings.json": fmt.Sprintf(`{"hooks":{"UserPromptSubmit":[{"matcher":"%d","hooks":[{"command":"loom skill materialize"}]}]}}`, index),
		"agent.lock":            "user file", ".codex/config.toml": "user config",
	} {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func assertCapturedRuntimePaths(t *testing.T, dir, head string) {
	t.Helper()
	for _, path := range []string{".agent.checkpoint.json", ".agent.lock", ".agent.lock.flock", ".codex/hooks.json", ".claude/settings.json"} {
		if gitForCaptureTest(t, dir, "ls-tree", "-r", "--name-only", head, "--", path) != "" {
			t.Errorf("runtime file %s entered revision %s", path, head)
		}
	}
	for _, path := range []string{"agent.lock", ".codex/config.toml"} {
		if gitForCaptureTest(t, dir, "ls-tree", "-r", "--name-only", head, "--", path) != path {
			t.Errorf("user file %s missing from revision %s", path, head)
		}
	}
}

func approveAndApplyExit(t *testing.T, revision apiTaskRevision, area string) {
	t.Helper()
	reviewer, err := review.OpenLocal()
	if err != nil {
		t.Fatal(err)
	}
	defer reviewer.Close()
	items, err := reviewer.TaskRevisions(context.Background(), "WS", "task-1")
	if err != nil {
		t.Fatal(err)
	}
	change := ""
	for _, item := range items {
		if item.Number == revision.Number {
			change = item.ChangeID
		}
	}
	if change == "" {
		t.Fatalf("revision %d has no change", revision.Number)
	}
	if _, err := reviewer.SubmitForLead(context.Background(), "WS", change, revision.Number, revision.HeadSHA, "approve", "", review.Actor{Kind: "human", ID: "reviewer"}, "L"); err != nil {
		t.Fatal(err)
	}
	result, err := apply.ApplyLocal(context.Background(), apply.Request{Workspace: "WS", Lead: "L", Change: change, Revision: revision.Number, RequestID: fmt.Sprintf("apply-%d", revision.Number)})
	if err != nil || result.ConflictCommit != "" {
		t.Fatalf("apply revision %d: %+v, %v", revision.Number, result, err)
	}
	if gitForCaptureTest(t, area, "rev-parse", "HEAD") != result.HeadSHA {
		t.Fatal("working area did not advance")
	}
}

func TestReconcilePendingAgentFreeze(t *testing.T) {
	dir := captureRepo(t)
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	base := gitForCaptureTest(t, dir, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(dir, "main.txt"), []byte("recovered work\n"), 0600); err != nil {
		t.Fatal(err)
	}
	lockDir := cli.ResolveLockDir(dir)
	cp := &config.Checkpoint{AgentName: "agent", TaskID: "task-1", FreezeBase: base,
		FreezeID: "session-1", FreezeRepo: "repo", FreezeState: "completed"}
	if err := config.SaveCheckpoint(lockDir, cp); err != nil {
		t.Fatal(err)
	}
	stubCheckBackend(t, func(name string) (discovery.Info, error) {
		return discovery.Info{Name: name, Installed: false}, nil
	})
	s := newBackendUnavailableSupervisor()
	s.WorkspaceID = "WS"
	ap := &AgentProcess{Entry: config.AgentEntry{Worktree: "agent", Backend: "codex"}, WorktreePath: dir}
	if s.preFlightSetup(ap) {
		t.Fatal("backend gate unexpectedly allowed a new run")
	}
	if s.preFlightSetup(ap) {
		t.Fatal("backend gate unexpectedly allowed a second run")
	}
	revisions := taskRevisionResponse(t)
	if len(revisions) != 1 || revisions[0].Number != 1 || revisions[0].HeadSHA == "" || revisions[0].Verdict != "" {
		t.Fatalf("recovered revisions API = %+v, want one unapproved revision", revisions)
	}
}

func writeLockFile(t *testing.T, dir string, info *cli.LockInfo) {
	t.Helper()
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, cli.LockFileName), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func newTestSupervisor() *Supervisor {
	return &Supervisor{ConfigSnapshot: func() *config.DaemonConfig { return &config.DaemonConfig{} }}
}

func gitForCaptureTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec,gosec // Real temporary Git repository verifies capture objects.
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func captureRepo(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[user]\nname = Test\nemail = test@example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	gitForCaptureTest(t, dir, "init")
	if err := os.WriteFile(filepath.Join(dir, "main.txt"), []byte("initial\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitForCaptureTest(t, dir, "add", "main.txt")
	gitForCaptureTest(t, dir, "commit", "-m", "initial")
	return dir
}

func TestAgentExitCapturesLargeTrackedAndUntrackedWork(t *testing.T) {
	dir := captureRepo(t)
	tracked := strings.Repeat("tracked edit\n", 2000)
	if err := os.WriteFile(filepath.Join(dir, "main.txt"), []byte(tracked), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("untracked work\n"), 0600); err != nil {
		t.Fatal(err)
	}
	writeLockFile(t, dir, &cli.LockInfo{AgentName: "agent", TaskID: "task-1", TaskTitle: "Work"})
	s := newTestSupervisor()
	ap := &AgentProcess{Entry: config.AgentEntry{Worktree: "agent"}, WorktreePath: dir}
	s.handleAgentCheckpoint(ap, 1)
	cp, err := config.LoadCheckpoint(cli.ResolveLockDir(dir))
	if err != nil || cp == nil {
		t.Fatalf("checkpoint: %v, %+v", err, cp)
	}
	if cp.CaptureRef == "" || cp.Retained {
		t.Fatalf("capture checkpoint: %+v", cp)
	}
	if got := gitForCaptureTest(t, dir, "show", cp.CaptureRef+":main.txt"); got != strings.TrimSpace(tracked) {
		t.Fatal("tracked edit was not captured in full")
	}
	if got := gitForCaptureTest(t, dir, "show", cp.CaptureRef+":new.txt"); got != "untracked work" {
		t.Fatalf("untracked content: %q", got)
	}
	if got := gitForCaptureTest(t, dir, "status", "--porcelain"); !strings.Contains(got, "main.txt") || !strings.Contains(got, "new.txt") {
		t.Fatalf("worktree changed after capture: %q", got)
	}
}

func TestAgentExitCapturesAfterDrainRemovedYieldFile(t *testing.T) {
	dir := captureRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("yield work\n"), 0600); err != nil {
		t.Fatal(err)
	}
	writeLockFile(t, dir, &cli.LockInfo{AgentName: "agent", TaskID: "task-2"})
	s := newTestSupervisor()
	ap := &AgentProcess{Entry: config.AgentEntry{Worktree: "agent"}, WorktreePath: dir, YieldReason: "shutdown"}
	s.handleAgentCheckpoint(ap, 0)
	cp, err := config.LoadCheckpoint(cli.ResolveLockDir(dir))
	if err != nil || cp == nil || cp.CaptureRef == "" || cp.YieldReason != "shutdown" {
		t.Fatalf("yield checkpoint: %+v, %v", cp, err)
	}
	if got := gitForCaptureTest(t, dir, "show", cp.CaptureRef+":new.txt"); got != "yield work" {
		t.Fatalf("captured yield work: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.txt")); err != nil {
		t.Fatalf("yield work was removed: %v", err)
	}
}

func TestAgentExitCaptureFailureRetainsWork(t *testing.T) {
	for _, tc := range []struct {
		name        string
		exitCode    int
		yieldReason string
	}{
		{name: "failed exit", exitCode: 1},
		{name: "clean exit", exitCode: 0},
		{name: "yield", exitCode: 0, yieldReason: "shutdown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testAgentExitCaptureFailureRetainsWork(t, tc.exitCode, tc.yieldReason)
		})
	}
}

func testAgentExitCaptureFailureRetainsWork(t *testing.T, exitCode int, yieldReason string) {
	dir := t.TempDir()
	mock := clitest.NewMockIssueBackend()
	mock.GetResult = &backend.IssueDetailData{IssueData: backend.IssueData{ID: "task-3", Status: "in_progress"}}
	cli.SetDefaultIssueBackend(mock)
	t.Cleanup(cli.ResetDefaultIssueBackend)
	writeLockFile(t, dir, &cli.LockInfo{AgentName: "agent", TaskID: "task-3"})
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	s := newTestSupervisor()
	s.captureWorktree = func(context.Context, string, string, string, string, string) (agentcapture.Result, error) {
		return agentcapture.Result{}, errors.New("capture failed")
	}
	ap := &AgentProcess{Entry: config.AgentEntry{Worktree: "agent"}, WorktreePath: dir, YieldReason: yieldReason}
	s.handleAgentCheckpoint(ap, exitCode)
	if !ap.CaptureRetained {
		t.Fatal("capture failure must mark worktree retained")
	}
	cp, err := config.LoadCheckpoint(cli.ResolveLockDir(dir))
	if err != nil || cp == nil || !cp.Retained {
		t.Fatalf("retained checkpoint: %+v, %v", cp, err)
	}
	s.postMortemRecovery(ap, exitCode)
	if _, err := os.Stat(filepath.Join(dir, cli.LockFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("agent lock remains after capture failure: %v", err)
	}
	var released, reopened bool
	for _, call := range mock.Calls {
		switch call.Method {
		case "ReleaseIssueLock":
			released = true
		case "Update":
			if params, ok := call.Args[1].(backend.UpdateParams); ok && params.Status != nil && *params.Status == "open" {
				reopened = true
			}
		}
	}
	if !released || !reopened {
		t.Fatalf("ownership not released after capture failure: released=%v reopened=%v calls=%+v", released, reopened, mock.Calls)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "new.txt")); err != nil || string(data) != "keep" {
		t.Fatalf("worktree changed: %q, %v", data, err)
	}
}

func TestCleanExitStillCapturesAndClearsCheckpoint(t *testing.T) {
	dir := captureRepo(t)
	writeLockFile(t, dir, &cli.LockInfo{AgentName: "agent", TaskID: "task-4"})
	lockDir := cli.ResolveLockDir(dir)
	if err := config.SaveCheckpoint(lockDir, &config.Checkpoint{TaskID: "old", CaptureRef: "refs/loom/old"}); err != nil {
		t.Fatal(err)
	}
	s := newTestSupervisor()
	ap := &AgentProcess{Entry: config.AgentEntry{Worktree: "agent"}, WorktreePath: dir}
	s.handleAgentCheckpoint(ap, 0)
	if ap.CaptureRetained {
		t.Fatal("clean capture unexpectedly retained")
	}
	manifests, err := filepath.Glob(filepath.Join(dir, ".git", "loom", "capture", "agent-*.json"))
	if err != nil || len(manifests) != 1 {
		t.Fatalf("clean exit did not run capture: %v, %v", manifests, err)
	}
	cp, err := config.LoadCheckpoint(lockDir)
	if err != nil || cp != nil {
		t.Fatalf("clean checkpoint: %+v, %v", cp, err)
	}
}

// D18 on the successful daemon freeze: an untracked secret-pattern file is
// left out, the run still freezes a revision, marked incomplete, and the
// worktree is retained with the file.
func TestCleanExitWithUntrackedSecretFreezesIncompleteRevision(t *testing.T) {
	dir := captureRepo(t)
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	base := gitForCaptureTest(t, dir, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(dir, "main.txt"), []byte("completed work\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "server.pem"), []byte("non-secret test marker\n"), 0600); err != nil {
		t.Fatal(err)
	}
	writeLockFile(t, dir, &cli.LockInfo{AgentName: "agent", TaskID: "task-1"})
	s := newTestSupervisor()
	s.WorkspaceID = "WS"
	ap := &AgentProcess{Entry: config.AgentEntry{Worktree: "agent"}, WorktreePath: dir,
		BeforeRef: base, AgentSessionID: "session-1", AssignedTaskID: "task-1"}
	s.handleAgentCheckpoint(ap, 0)

	revisions := taskRevisionResponse(t)
	if len(revisions) != 1 || !revisions[0].Incomplete || revisions[0].NoChanges {
		t.Fatalf("revisions = %+v, want one incomplete revision", revisions)
	}
	assertSecretLeftOut(t, dir, revisions[0].HeadSHA)
	if !ap.CaptureRetained {
		t.Fatal("incomplete capture must keep the worktree retained")
	}
	cp, err := config.LoadCheckpoint(cli.ResolveLockDir(dir))
	if err != nil || cp == nil || !cp.Retained || cp.FreezeID != "" {
		t.Fatalf("checkpoint = %+v, %v; want retained with no pending freeze", cp, err)
	}
}

// D18 on the restart path: a pending freeze reconciled before the next run
// captures the worktree, leaves the untracked secret file out and records an
// incomplete revision.
func TestReconcilePendingFreezeWithUntrackedSecretIsIncomplete(t *testing.T) {
	dir := captureRepo(t)
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	base := gitForCaptureTest(t, dir, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(dir, "main.txt"), []byte("completed work\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "server.pem"), []byte("non-secret test marker\n"), 0600); err != nil {
		t.Fatal(err)
	}
	lockDir := cli.ResolveLockDir(dir)
	if err := config.SaveCheckpoint(lockDir, &config.Checkpoint{AgentName: "agent", TaskID: "task-1",
		FreezeBase: base, FreezeID: "session-1", FreezeRepo: "repo", FreezeState: "completed"}); err != nil {
		t.Fatal(err)
	}
	s := newTestSupervisor()
	s.WorkspaceID = "WS"
	ap := &AgentProcess{Entry: config.AgentEntry{Worktree: "agent"}, WorktreePath: dir}
	if err := s.reconcilePendingFreeze(ap); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	revisions := taskRevisionResponse(t)
	if len(revisions) != 1 || !revisions[0].Incomplete || revisions[0].NoChanges {
		t.Fatalf("revisions = %+v, want one incomplete revision", revisions)
	}
	assertSecretLeftOut(t, dir, revisions[0].HeadSHA)
}

func assertSecretLeftOut(t *testing.T, dir, head string) {
	t.Helper()
	tree := gitForCaptureTest(t, dir, "ls-tree", "-r", "--name-only", head)
	if strings.Contains(tree, "server.pem") || !strings.Contains(tree, "main.txt") {
		t.Fatalf("revision tree = %q", tree)
	}
	if got := gitForCaptureTest(t, dir, "show", head+":main.txt"); got != "completed work" {
		t.Fatalf("main.txt = %q", got)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "server.pem")); err != nil || string(data) != "non-secret test marker\n" {
		t.Fatalf("worktree lost server.pem: %q, %v", data, err)
	}
}
