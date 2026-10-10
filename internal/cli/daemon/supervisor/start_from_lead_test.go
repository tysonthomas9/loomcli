package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/olesho/harness-wrapper/pkg/discovery"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/loomgit/pull"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type rerunFixture struct {
	dir, area string
	s         *Supervisor
	ap        *AgentProcess
}

// newRerunFixture is a daemon agent whose long-lived checkout is reused by
// every attempt, with lead L's working area registered for the same repo. The
// agent was started by lead L's session.
func newRerunFixture(t *testing.T) rerunFixture {
	t.Helper()
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
	stubCheckBackend(t, func(name string) (discovery.Info, error) {
		return discovery.Info{Name: name, Binary: name, Installed: true}, nil
	})
	s := newBackendUnavailableSupervisor()
	s.WorkspaceID, s.Concurrency = "WS", NewConcurrencyTracker(nil)
	controlStore := memstore.New()
	if _, err := controlStore.AgentSessions().Create(context.Background(), store.AgentSessionCreate{WorkspaceKey: "WS",
		SessionID: "lead-session", AgentID: "L", Kind: domain.AgentSessionKindOrchestration, Status: domain.AgentSessionRunning}); err != nil {
		t.Fatal(err)
	}
	s.ControlStore = controlStore
	s.IssueBackend = clitest.NewMockIssueBackend()
	ap := &AgentProcess{Entry: config.AgentEntry{Worktree: "agent", Role: "task", Backend: "codex"},
		RoleConfig: config.RoleConfig{Description: "test"}, WorktreePath: dir, ParentSessionID: "lead-session"}
	return rerunFixture{dir: dir, area: area, s: s, ap: ap}
}

// attempt runs one cold-started daemon attempt in which the agent writes and
// commits feature.txt, and returns the revision its exit froze.
func (f rerunFixture) attempt(t *testing.T, index int, content string) apiTaskRevision {
	t.Helper()
	f.s.IssueBackend = nil // claimTask: no queue; the task is assigned below.
	if !f.s.preFlightSetup(f.ap) {
		t.Fatalf("attempt %d did not start: %+v", index, f.ap.LastError)
	}
	leadHead := gitForCaptureTest(t, f.area, "rev-parse", "HEAD")
	if f.ap.BeforeRef != leadHead {
		t.Fatalf("attempt %d base = %s, want the lead's current head %s", index, f.ap.BeforeRef, leadHead)
	}
	if err := os.WriteFile(filepath.Join(f.dir, "feature.txt"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	gitForCaptureTest(t, f.dir, "add", "feature.txt")
	gitForCaptureTest(t, f.dir, "commit", "-m", fmt.Sprintf("attempt %d", index))
	f.ap.AssignedTaskID = "task-1"
	writeLockFile(t, f.dir, &cli.LockInfo{PID: os.Getpid(), AgentName: "agent", TaskID: "task-1", RunID: f.ap.AgentSessionID})
	f.s.IssueBackend = clitest.NewMockIssueBackend()
	f.s.Concurrency.Acquire("task")
	f.s.spawnAndWait(f.ap)
	revisions := taskRevisionResponse(t)
	if len(revisions) != index || revisions[0].Number != index || revisions[0].HeadSHA == "" {
		t.Fatalf("attempt %d revisions = %+v", index, revisions)
	}
	return revisions[0]
}

func unapplyTaskChange(t *testing.T, area string) {
	t.Helper()
	reviewer, err := review.OpenLocal()
	if err != nil {
		t.Fatal(err)
	}
	defer reviewer.Close()
	items, err := reviewer.TaskRevisions(context.Background(), "WS", "task-1")
	if err != nil || len(items) == 0 {
		t.Fatalf("task revisions: %+v, %v", items, err)
	}
	if _, err := pull.UnapplyLocal(context.Background(), area, items[0].ChangeID, "unapply-1"); err != nil {
		t.Fatalf("unapply: %v", err)
	}
}

// The F7 walk-through repro: approve a task, Unapply it, rerun the task and
// approve the new attempt. The rerun must start from the lead's current head,
// not the agent's leftover commit, so approving it applies without a conflict.
func TestRerunAfterUnapplyStartsFromLeadHead(t *testing.T) {
	f := newRerunFixture(t)
	base := gitForCaptureTest(t, f.area, "rev-parse", "HEAD")
	first := f.attempt(t, 1, "attempt one\n")
	approveAndApplyExit(t, first, f.area)
	unapplyTaskChange(t, f.area)
	if head := gitForCaptureTest(t, f.area, "rev-parse", "HEAD"); head != base {
		t.Fatalf("lead after unapply = %s, want %s", head, base)
	}
	second := f.attempt(t, 2, "attempt two\n")
	approveAndApplyExit(t, second, f.area)
	if got := gitForCaptureTest(t, f.area, "show", "HEAD:feature.txt"); got != "attempt two" {
		t.Fatalf("lead feature.txt = %q, want the rerun's whole file", got)
	}
}

// A reused checkout with uncommitted work is never moved or cleaned: the
// attempt does not start, and the agent reports why.
func TestColdStartRefusesCheckoutWithUnsavedWork(t *testing.T) {
	f := newRerunFixture(t)
	if err := os.WriteFile(filepath.Join(f.area, "lead.txt"), []byte("lead work\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitForCaptureTest(t, f.area, "add", "lead.txt")
	gitForCaptureTest(t, f.area, "commit", "-m", "lead work")
	head := gitForCaptureTest(t, f.dir, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(f.dir, "notes.txt"), []byte("unsaved\n"), 0600); err != nil {
		t.Fatal(err)
	}
	f.s.IssueBackend = nil
	if f.s.preFlightSetup(f.ap) {
		t.Fatal("an attempt started on a checkout holding unsaved work")
	}
	if f.ap.LastError == nil || f.ap.LastError.Class != agenterr.OutcomeFromDomain(agenterr.SpawnFailureOutcome) {
		t.Fatalf("LastError = %+v, want a spawn failure naming the unsaved work", f.ap.LastError)
	}
	if got := gitForCaptureTest(t, f.dir, "rev-parse", "HEAD"); got != head {
		t.Fatalf("refused start moved HEAD from %s to %s", head, got)
	}
	if data, err := os.ReadFile(filepath.Join(f.dir, "notes.txt")); err != nil || string(data) != "unsaved\n" {
		t.Fatalf("unsaved file changed: %q, %v", data, err)
	}
}

// A checkout already at the lead's head still refuses a cold start, before any
// claim, while it holds uncommitted files.
func TestColdStartRefusesUncommittedFilesAtLeadHead(t *testing.T) {
	f := newRerunFixture(t)
	if err := os.WriteFile(filepath.Join(f.dir, "notes.txt"), []byte("unsaved\n"), 0600); err != nil {
		t.Fatal(err)
	}
	issues := clitest.NewMockIssueBackend()
	issues.ReadyResult = []backend.IssueData{{ID: "task-1", Status: "open", IssueType: "task", HasDesign: true}}
	f.s.IssueBackend = issues
	if f.s.preFlightSetup(f.ap) {
		t.Fatal("an attempt started on a checkout holding uncommitted files")
	}
	if f.ap.LastError == nil || f.ap.LastError.Class != agenterr.OutcomeFromDomain(agenterr.SpawnFailureOutcome) {
		t.Fatalf("LastError = %+v, want a spawn failure naming the unsaved work", f.ap.LastError)
	}
	if issues.Called("ClaimIssue") {
		t.Fatal("refused start claimed a task")
	}
	if data, err := os.ReadFile(filepath.Join(f.dir, "notes.txt")); err != nil || string(data) != "unsaved\n" {
		t.Fatalf("unsaved file changed: %q, %v", data, err)
	}
}

// claimDependent sets the fixture up to claim task-dep, a dependent task whose
// base DependentBase resolves.
func (f rerunFixture) claimDependent(resolve func() (string, bool, error)) *clitest.MockIssueBackend {
	issues := clitest.NewMockIssueBackend()
	issues.ReadyResult = []backend.IssueData{{ID: "task-dep", Status: "open", IssueType: "task", HasDesign: true}}
	f.s.IssueBackend = issues
	f.s.DependentBase = func(_ context.Context, workspace, repo, _, task string) (string, bool, error) {
		if workspace != "WS" || task != "task-dep" {
			return "", false, fmt.Errorf("unexpected %s/%s/%s", workspace, repo, task)
		}
		return resolve()
	}
	return issues
}

// A dependent task starts from its blocker's frozen revision, not the lead's
// head (Tyson's 2026-10-09 dependent-task decision).
func TestDependentTaskStartsFromBlockerRevision(t *testing.T) {
	f := newRerunFixture(t)
	base := gitForCaptureTest(t, f.dir, "rev-parse", "HEAD")
	gitForCaptureTest(t, f.dir, "checkout", "-q", "-b", "blocker")
	if err := os.WriteFile(filepath.Join(f.dir, "blocker.txt"), []byte("blocker\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitForCaptureTest(t, f.dir, "add", "blocker.txt")
	gitForCaptureTest(t, f.dir, "commit", "-m", "blocker revision")
	blocker := gitForCaptureTest(t, f.dir, "rev-parse", "HEAD")
	gitForCaptureTest(t, f.dir, "update-ref", "refs/loom/ws/WS/change/C/1/head", blocker)
	gitForCaptureTest(t, f.dir, "checkout", "-q", "--detach", base)
	f.claimDependent(func() (string, bool, error) { return blocker, true, nil })
	if !f.s.preFlightSetup(f.ap) {
		t.Fatalf("dependent attempt did not start: %+v", f.ap.LastError)
	}
	if f.ap.BeforeRef != blocker {
		t.Fatalf("dependent attempt base = %s, want the blocker revision %s", f.ap.BeforeRef, blocker)
	}
}

// A dependent base that cannot be resolved refuses the start and hands the
// claim back, so the task is not stranded in progress.
func TestDependentBaseFailureHandsBackClaim(t *testing.T) {
	f := newRerunFixture(t)
	issues := f.claimDependent(func() (string, bool, error) { return "", false, errors.New("predecessor has no ready revision") })
	if f.s.preFlightSetup(f.ap) {
		t.Fatal("an attempt started without its dependent base")
	}
	if f.ap.LastError == nil || f.ap.LastError.Class != agenterr.OutcomeFromDomain(agenterr.SpawnFailureOutcome) {
		t.Fatalf("LastError = %+v, want a spawn failure; calls %+v", f.ap.LastError, issues.Calls)
	}
	if !issues.Called("ClaimIssue") {
		t.Fatal("task was never claimed")
	}
	reopened := false
	for _, call := range issues.Calls {
		if call.Method == "Update" {
			if params, ok := call.Args[1].(backend.UpdateParams); ok && params.Status != nil && *params.Status == "open" {
				reopened = true
			}
		}
	}
	if !reopened || f.ap.AssignedTaskID != "" {
		t.Fatalf("claim not handed back: reopened=%v assigned=%q", reopened, f.ap.AssignedTaskID)
	}
}
