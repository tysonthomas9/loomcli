package supervisor

import (
	"context"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

func TestClaimTask_SelectsEligibleTaskAndClaims(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{
		{ID: "epic-1", IssueType: "epic", Status: "open", Priority: 0, Title: "Epic"},
		{ID: "task-2", IssueType: "task", Status: "open", Priority: 3, Title: "No design"},
		{ID: "task-1", IssueType: "task", Status: "open", Priority: 1, Title: "Ready", Design: "plan"},
	}
	s := &Supervisor{IssueBackend: mock}
	ap := &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"},
		RoleConfig: cfgpkg.RoleConfig{TaskFilter: "has_design"},
	}

	if !s.claimTask(ap, "parent-1") {
		t.Fatal("claimTask returned false")
	}
	if ap.AssignedTaskID != "task-1" {
		t.Fatalf("AssignedTaskID = %q, want task-1", ap.AssignedTaskID)
	}
	if len(mock.Calls) != 2 {
		t.Fatalf("calls = %#v, want Ready and ClaimIssue", mock.Calls)
	}
	opts := mock.Calls[0].Args[0].(backend.ReadyOpts)
	if opts.ParentID != "parent-1" || opts.Limit != claimReadyLimit {
		t.Fatalf("ReadyOpts = %#v", opts)
	}
	if mock.Calls[1].Method != "ClaimIssue" || mock.Calls[1].Args[0] != "task-1" {
		t.Fatalf("claim call = %#v", mock.Calls[1])
	}
	if ttl := mock.Calls[1].Args[1].(time.Duration); ttl != 0 {
		t.Fatalf("claim TTL = %v, want zero", ttl)
	}
}

func TestClaimTask_ClaimsRequestedTaskIgnoringRoleFilter(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{
		{ID: "task-1", IssueType: "task", Status: "open", Priority: 1, Title: "No design", Assignee: "falcon"},
		{ID: "task-2", IssueType: "task", Status: "open", Priority: 0, Title: "Ready", Design: "plan"},
	}
	s := &Supervisor{IssueBackend: mock}
	ap := &AgentProcess{
		Entry:           cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"},
		RoleConfig:      cfgpkg.RoleConfig{TaskFilter: "has_design"},
		RequestedTaskID: "task-1",
	}

	if !s.claimTask(ap, "") {
		t.Fatal("claimTask returned false")
	}
	if ap.AssignedTaskID != "task-1" {
		t.Fatalf("AssignedTaskID = %q, want task-1", ap.AssignedTaskID)
	}
	if ap.RequestedTaskID != "" {
		t.Fatalf("RequestedTaskID = %q, want empty", ap.RequestedTaskID)
	}
	if len(mock.Calls) != 2 {
		t.Fatalf("calls = %#v, want Ready and ClaimIssue", mock.Calls)
	}
	opts := mock.Calls[0].Args[0].(backend.ReadyOpts)
	if opts.Assignee != "falcon" {
		t.Fatalf("ReadyOpts.Assignee = %q, want falcon", opts.Assignee)
	}
	if mock.Calls[1].Method != "ClaimIssue" || mock.Calls[1].Args[0] != "task-1" {
		t.Fatalf("claim call = %#v", mock.Calls[1])
	}
}

func TestClaimTask_RequestedTaskNotReadyDoesNotClaimFallback(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{
		{ID: "task-2", IssueType: "task", Status: "open", Priority: 0, Title: "Ready", Design: "plan"},
	}
	s := &Supervisor{IssueBackend: mock}
	ap := &AgentProcess{
		Entry:           cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"},
		RoleConfig:      cfgpkg.RoleConfig{TaskFilter: "has_design"},
		RequestedTaskID: "task-1",
	}

	if s.claimTask(ap, "") {
		t.Fatal("claimTask returned true")
	}
	if ap.AssignedTaskID != "" {
		t.Fatalf("AssignedTaskID = %q, want empty", ap.AssignedTaskID)
	}
	if len(mock.Calls) != 1 {
		t.Fatalf("calls = %#v, want only requested-task Ready", mock.Calls)
	}
	if ap.LastError == nil || ap.LastError.Class != agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome) {
		t.Fatalf("LastError = %#v, want NoWork", ap.LastError)
	}
}

func TestClaimTask_PrefersAssignedTasksBeforeGeneralQueue(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.ReadyFn = func(_ context.Context, opts backend.ReadyOpts) ([]backend.IssueData, error) {
		if opts.Assignee == "falcon" {
			return []backend.IssueData{
				{ID: "task-assigned", IssueType: "task", Status: "open", Priority: 3, Title: "Assigned", Design: "plan", Assignee: "falcon"},
			}, nil
		}
		return []backend.IssueData{
			{ID: "task-general", IssueType: "task", Status: "open", Priority: 0, Title: "General", Design: "plan"},
		}, nil
	}
	s := &Supervisor{IssueBackend: mock}
	ap := &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"},
		RoleConfig: cfgpkg.RoleConfig{TaskFilter: "has_design"},
	}

	if !s.claimTask(ap, "") {
		t.Fatal("claimTask returned false")
	}
	if ap.AssignedTaskID != "task-assigned" {
		t.Fatalf("AssignedTaskID = %q, want task-assigned", ap.AssignedTaskID)
	}
	if len(mock.Calls) != 2 {
		t.Fatalf("calls = %#v, want assigned Ready and ClaimIssue", mock.Calls)
	}
	opts := mock.Calls[0].Args[0].(backend.ReadyOpts)
	if opts.Assignee != "falcon" {
		t.Fatalf("ReadyOpts.Assignee = %q, want falcon", opts.Assignee)
	}
}

func TestClaimTask_SkipsConflictedCandidate(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{
		{ID: "task-1", IssueType: "task", Status: "open", Priority: 1, Title: "First", Design: "plan"},
		{ID: "task-2", IssueType: "task", Status: "open", Priority: 2, Title: "Second", Design: "plan"},
	}
	mock.ClaimIssueFn = func(_ context.Context, id string, _ time.Duration) error {
		if id == "task-1" {
			return backend.ErrConflict("ClaimIssue", "claimed")
		}
		return nil
	}
	s := &Supervisor{IssueBackend: mock}
	ap := &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"},
		RoleConfig: cfgpkg.RoleConfig{TaskFilter: "has_design"},
	}

	if !s.claimTask(ap, "") {
		t.Fatal("claimTask returned false")
	}
	if ap.AssignedTaskID != "task-2" {
		t.Fatalf("AssignedTaskID = %q, want task-2", ap.AssignedTaskID)
	}
}

func TestClaimTask_CapsConflictRetries(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	for i := 0; i < claimConflictRetryLimit+5; i++ {
		mock.ReadyResult = append(mock.ReadyResult, backend.IssueData{
			ID:        "task-" + string(rune('a'+i)),
			IssueType: "task",
			Status:    "open",
			Priority:  1,
			Title:     "Conflicted",
			Design:    "plan",
		})
	}
	claimCalls := 0
	mock.ClaimIssueFn = func(_ context.Context, _ string, _ time.Duration) error {
		claimCalls++
		return backend.ErrConflict("ClaimIssue", "claimed")
	}
	s := &Supervisor{IssueBackend: mock}
	ap := &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"},
		RoleConfig: cfgpkg.RoleConfig{TaskFilter: "has_design"},
	}

	if s.claimTask(ap, "") {
		t.Fatal("claimTask returned true after all candidates conflicted")
	}
	if claimCalls != claimConflictRetryLimit {
		t.Fatalf("claim calls = %d, want capped at %d", claimCalls, claimConflictRetryLimit)
	}
	if ap.LastError == nil || ap.LastError.Class != agenterr.OutcomeFromDomain(agenterr.LockConflictOutcome) {
		t.Fatalf("LastError = %#v, want LockConflict after conflict retry cap", ap.LastError)
	}
}

func TestClaimTask_NoMatchSetsNoWork(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{
		{ID: "epic-1", IssueType: "epic", Status: "open", Priority: 1, Title: "Epic"},
	}
	s := &Supervisor{IssueBackend: mock}
	ap := &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"},
		RoleConfig: cfgpkg.RoleConfig{TaskFilter: "has_design"},
	}

	if s.claimTask(ap, "") {
		t.Fatal("claimTask returned true, want false")
	}
	if ap.LastError == nil || ap.LastError.Class != agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome) {
		t.Fatalf("LastError = %#v, want NoWork", ap.LastError)
	}
}

func TestClaimTask_SkipsLongLivedRoleWithoutTaskFilter(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	s := &Supervisor{IssueBackend: mock}
	ap := &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "sentinel", Role: "oncall"},
		RoleConfig: cfgpkg.RoleConfig{},
	}

	if !s.claimTask(ap, "") {
		t.Fatal("claimTask returned false")
	}
	if ap.AssignedTaskID != "" {
		t.Fatalf("AssignedTaskID = %q, want empty", ap.AssignedTaskID)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("backend calls = %#v, want none", mock.Calls)
	}
}

func TestClaimTask_CancelsReadyOnShutdown(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	readyStarted := make(chan struct{})
	mock.ReadyFn = func(ctx context.Context, _ backend.ReadyOpts) ([]backend.IssueData, error) {
		close(readyStarted)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
			return nil, context.DeadlineExceeded
		}
	}
	shutdown := make(chan struct{})
	s := &Supervisor{IssueBackend: mock, Shutdown: shutdown}
	ap := &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"},
		RoleConfig: cfgpkg.RoleConfig{TaskFilter: "has_design"},
	}

	done := make(chan bool, 1)
	go func() {
		done <- s.claimTask(ap, "")
	}()

	<-readyStarted
	close(shutdown)

	select {
	case ok := <-done:
		if ok {
			t.Fatal("claimTask returned true after shutdown")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("claimTask did not cancel Ready promptly after shutdown")
	}
}

func TestTaskIDForLifecycle_UsesAssignedFallback(t *testing.T) {
	s := &Supervisor{}
	ap := &AgentProcess{AssignedTaskID: "task-assigned"}

	if got := s.taskIDForLifecycle(ap, nil); got != "task-assigned" {
		t.Fatalf("taskIDForLifecycle(nil) = %q, want task-assigned", got)
	}
	if got := s.taskIDForLifecycle(ap, &cli.LockInfo{TaskID: "task-lock"}); got != "task-lock" {
		t.Fatalf("taskIDForLifecycle(lock) = %q, want task-lock", got)
	}
}

// --- claimResumeTask / reclaimOwnLockedTask ---

// ownConflictErr builds the KindConflict error a fleet backend returns when an
// issue's claim lock is held, carrying the holder identity in Meta the way
// fleet-db's 409 envelope populates existing_owner.
func ownConflictErr(holder string) error {
	return &backend.BackendError{
		Kind:    backend.KindConflict,
		Op:      "ClaimIssue",
		Message: "already claimed",
		Meta:    map[string]string{"existing_owner": holder},
	}
}

// actorReleaseMock wraps MockIssueBackend with the actorReleaseBackend
// interface (ReleaseIssueAsActor) so tests can observe the supervisor's
// lock-only self-release. The mock's ReleaseIssueLock method has a different
// signature role and does NOT satisfy actorReleaseBackend.
type actorReleaseMock struct {
	*clitest.MockIssueBackend
	releases []string // recorded as "taskID/actor"
}

func (m *actorReleaseMock) ReleaseIssueAsActor(_ context.Context, id string, actor string) error {
	m.releases = append(m.releases, id+"/"+actor)
	return nil
}

func resumeAgentProcess(taskID string) *AgentProcess {
	return &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"},
		RoleConfig:   cfgpkg.RoleConfig{TaskFilter: "has_design"},
		ResumeTaskID: taskID,
	}
}

// The regression fence for WEB-EXTRACTOR-NEW-49: an own-lock conflict on the
// resume re-claim must release the stale lock and claim fresh, so the server
// re-asserts status=in_progress + assignee instead of resuming a task the
// board shows as open/unassigned.
func TestClaimTask_ResumeOwnConflict_ReleasesAndReclaims(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	claims := 0
	mock.ClaimIssueFn = func(_ context.Context, _ string, _ time.Duration) error {
		claims++
		if claims == 1 {
			return ownConflictErr("falcon")
		}
		return nil
	}
	rm := &actorReleaseMock{MockIssueBackend: mock}
	s := &Supervisor{IssueBackend: rm}
	ap := resumeAgentProcess("T-1")

	if !s.claimTask(ap, "") {
		t.Fatal("claimTask returned false")
	}
	if ap.AssignedTaskID != "T-1" {
		t.Fatalf("AssignedTaskID = %q, want T-1", ap.AssignedTaskID)
	}
	if claims != 2 {
		t.Fatalf("claim attempts = %d, want 2 (conflict, then fresh re-claim)", claims)
	}
	if len(rm.releases) != 1 || rm.releases[0] != "T-1/falcon" {
		t.Fatalf("releases = %#v, want exactly [T-1/falcon]", rm.releases)
	}
}

func TestClaimTask_ResumeOwnConflict_NoReleaseSupport_FallsBackToResume(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	claims := 0
	mock.ClaimIssueFn = func(_ context.Context, _ string, _ time.Duration) error {
		claims++
		return ownConflictErr("falcon")
	}
	s := &Supervisor{IssueBackend: mock} // bare mock: no actorReleaseBackend
	ap := resumeAgentProcess("T-1")

	if !s.claimTask(ap, "") {
		t.Fatal("claimTask returned false, want resume-in-place fallback")
	}
	if ap.AssignedTaskID != "T-1" {
		t.Fatalf("AssignedTaskID = %q, want T-1", ap.AssignedTaskID)
	}
	if claims != 2 {
		t.Fatalf("claim attempts = %d, want exactly 2 (no retry loop)", claims)
	}
}

func TestClaimTask_ResumeOwnConflict_ReclaimStolen_ColdStarts(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	claims := 0
	mock.ClaimIssueFn = func(_ context.Context, _ string, _ time.Duration) error {
		claims++
		if claims == 1 {
			return ownConflictErr("falcon")
		}
		return ownConflictErr("hawk") // another worktree won the released lock
	}
	rm := &actorReleaseMock{MockIssueBackend: mock}
	s := &Supervisor{IssueBackend: rm}
	ap := resumeAgentProcess("T-1")

	if s.claimTask(ap, "") {
		t.Fatal("claimTask returned true, want cold-start fallthrough with empty ready queue")
	}
	if ap.AssignedTaskID != "" {
		t.Fatalf("AssignedTaskID = %q, want empty", ap.AssignedTaskID)
	}
	if ap.ResumeTaskID != "" {
		t.Fatalf("ResumeTaskID = %q, want cleared after failed resume", ap.ResumeTaskID)
	}
	if claims != 2 {
		t.Fatalf("claim attempts = %d, want 2", claims)
	}
}

func TestClaimTask_ResumeForeignConflict_NoRelease_ColdStarts(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	claims := 0
	mock.ClaimIssueFn = func(_ context.Context, _ string, _ time.Duration) error {
		claims++
		return ownConflictErr("hawk")
	}
	rm := &actorReleaseMock{MockIssueBackend: mock}
	s := &Supervisor{IssueBackend: rm}
	ap := resumeAgentProcess("T-1")

	if s.claimTask(ap, "") {
		t.Fatal("claimTask returned true, want false")
	}
	if claims != 1 {
		t.Fatalf("claim attempts = %d, want 1 (foreign holder, no self-release re-claim)", claims)
	}
	if len(rm.releases) != 0 {
		t.Fatalf("releases = %#v, want none for a foreign holder", rm.releases)
	}
}

func TestClaimTask_ResumeFreshClaimSuccess(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	rm := &actorReleaseMock{MockIssueBackend: mock}
	s := &Supervisor{IssueBackend: rm}
	ap := resumeAgentProcess("T-1")

	if !s.claimTask(ap, "") {
		t.Fatal("claimTask returned false")
	}
	if ap.AssignedTaskID != "T-1" {
		t.Fatalf("AssignedTaskID = %q, want T-1", ap.AssignedTaskID)
	}
	if len(rm.releases) != 0 {
		t.Fatalf("releases = %#v, want none on a clean re-claim", rm.releases)
	}
	if len(mock.Calls) != 1 || mock.Calls[0].Method != "ClaimIssue" {
		t.Fatalf("calls = %#v, want a single ClaimIssue", mock.Calls)
	}
}

func TestClaimTask_ResumeOwnConflict_EmptyMeta_ColdStarts(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	claims := 0
	mock.ClaimIssueFn = func(_ context.Context, _ string, _ time.Duration) error {
		claims++
		return backend.ErrConflict("ClaimIssue", "already claimed") // no Meta: holder unknown
	}
	rm := &actorReleaseMock{MockIssueBackend: mock}
	s := &Supervisor{IssueBackend: rm}
	ap := resumeAgentProcess("T-1")

	if s.claimTask(ap, "") {
		t.Fatal("claimTask returned true, want false when the holder is unknown")
	}
	if claims != 1 {
		t.Fatalf("claim attempts = %d, want 1", claims)
	}
	if len(rm.releases) != 0 {
		t.Fatalf("releases = %#v, want none when the holder is unknown", rm.releases)
	}
}
