package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olesho/harness-wrapper/pkg/discovery"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/loomgit/agentcapture"
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
	Number  int    `json:"number"`
	HeadSHA string `json:"head_sha"`
	Verdict string `json:"verdict"`
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
	s.IssueBackend = &releaseOrderIssueBackend{MockIssueBackend: clitest.NewMockIssueBackend(), onRelease: func() { claimReleases++; assertRevision() }}
	ap := &AgentProcess{Entry: config.AgentEntry{Worktree: "agent", Role: "task", Backend: "codex"},
		WorktreePath: dir, BeforeRef: base, AgentSessionID: "session-1", AssignedTaskID: "task-1",
		AgentLeaseID: "lease-1", AgentLeaseToken: "token-1"}
	s.spawnAndWait(ap)
	assertRevision()
	if leaseReleases != 1 || claimReleases != 1 {
		t.Fatalf("release calls = lease %d, claim %d; want one each", leaseReleases, claimReleases)
	}
	ap.BeforeRef = base
	ap.AgentSessionID = "session-1"
	ap.AssignedTaskID = "task-1"
	writeLockFile(t, dir, &cli.LockInfo{PID: os.Getpid(), AgentName: "agent", TaskID: "task-1"})
	s.Concurrency.Acquire("task")
	s.spawnAndWait(ap)
	assertRevision()
	if ap.CaptureRetained {
		t.Fatal("repeated exit unexpectedly retained the worktree")
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
