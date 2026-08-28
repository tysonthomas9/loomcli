package supervisor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
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
	if opts.Assignee != "" {
		t.Fatalf("ReadyOpts.Assignee = %q, want empty for requested task lookup", opts.Assignee)
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

func TestClaimTask_EphemeralRequiresRequestedTask(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{
		{ID: "task-1", IssueType: "task", Status: "open", Priority: 1, Title: "Ready", Design: "plan"},
	}
	s := &Supervisor{IssueBackend: mock}
	ap := &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "worker", Role: "task", Mode: domain.AgentModeEphemeral},
		RoleConfig: cfgpkg.RoleConfig{TaskFilter: "has_design"},
	}

	if s.claimTask(ap, "EPIC-1") {
		t.Fatal("claimTask returned true, want false for ephemeral worker without requested task")
	}
	if ap.AssignedTaskID != "" {
		t.Fatalf("AssignedTaskID = %q, want empty", ap.AssignedTaskID)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("calls = %#v, want no ready/claim calls", mock.Calls)
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

// TestClaimTask_SimultaneousClaimsElectOneWinner reproduces the 2026-08-27
// PUPPET-201 incident: every agent of a cold-started daemon claims in the same
// millisecond. With a backend that happily accepts all of them, exactly one
// must still end up holding the issue and the losers must move on.
func TestClaimTask_SimultaneousClaimsElectOneWinner(t *testing.T) {
	const agents = 4
	mock := clitest.NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{
		{ID: "task-hot", IssueType: "task", Status: "open", Priority: 0, Title: "Contended", Design: "plan"},
	}
	// The broken backend under test: no mutual exclusion at all.
	mock.ClaimIssueFn = func(_ context.Context, _ string, _ time.Duration) error { return nil }

	s := &Supervisor{IssueBackend: mock}
	aps := make([]*AgentProcess, agents)
	for i := range aps {
		aps[i] = &AgentProcess{
			Entry:      cfgpkg.AgentEntry{Worktree: "worker-" + string(rune('a'+i)), Role: "task"},
			RoleConfig: cfgpkg.RoleConfig{TaskFilter: "has_design"},
		}
	}

	start := make(chan struct{})
	results := make(chan bool, agents)
	for _, ap := range aps {
		go func(ap *AgentProcess) {
			<-start
			results <- s.claimTask(ap, "")
		}(ap)
	}
	close(start)

	winners := 0
	for i := 0; i < agents; i++ {
		if <-results {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d, want exactly 1", winners)
	}

	holders := 0
	for _, ap := range aps {
		ap.Mu.Lock()
		assigned := ap.AssignedTaskID
		lastErr := ap.LastError
		ap.Mu.Unlock()
		if assigned == "task-hot" {
			holders++
			continue
		}
		if assigned != "" {
			t.Fatalf("loser %s assigned %q, want empty", ap.Entry.Worktree, assigned)
		}
		if lastErr == nil || lastErr.Class != agenterr.OutcomeFromDomain(agenterr.LockConflictOutcome) {
			t.Fatalf("loser %s LastError = %#v, want LockConflict", ap.Entry.Worktree, lastErr)
		}
	}
	if holders != 1 {
		t.Fatalf("agents holding task-hot = %d, want 1", holders)
	}
}

// TestClaimTask_SimultaneousWorktreelessClaimsElectOneWinner is the sibling of
// the test above for the one configuration the ledger used to let through: an
// agent configured without a worktree. Those agents once shared the identity
// "role:<role>", so the second one hit reserve's "re-reserving your own task is
// a no-op" path and reached the backend alongside the first.
func TestClaimTask_SimultaneousWorktreelessClaimsElectOneWinner(t *testing.T) {
	const agents = 4
	mock := clitest.NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{
		{ID: "task-hot", IssueType: "task", Status: "open", Priority: 0, Title: "Contended", Design: "plan"},
	}
	// The broken backend under test: no mutual exclusion at all.
	mock.ClaimIssueFn = func(_ context.Context, _ string, _ time.Duration) error { return nil }

	s := &Supervisor{IssueBackend: mock}
	aps := make([]*AgentProcess, agents)
	for i := range aps {
		aps[i] = &AgentProcess{
			Entry:      cfgpkg.AgentEntry{Worktree: "", Role: "task"},
			RoleConfig: cfgpkg.RoleConfig{TaskFilter: "has_design"},
		}
	}

	start := make(chan struct{})
	results := make(chan bool, agents)
	for _, ap := range aps {
		go func(ap *AgentProcess) {
			<-start
			results <- s.claimTask(ap, "")
		}(ap)
	}
	close(start)

	winners := 0
	for i := 0; i < agents; i++ {
		if <-results {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d, want exactly 1", winners)
	}

	// Agents are identified by index here: Entry.Worktree is empty for all of
	// them, which is the whole point of the case.
	holders := 0
	for i, ap := range aps {
		ap.Mu.Lock()
		assigned := ap.AssignedTaskID
		lastErr := ap.LastError
		ap.Mu.Unlock()
		if assigned == "task-hot" {
			holders++
			continue
		}
		if assigned != "" {
			t.Fatalf("loser %d assigned %q, want empty", i, assigned)
		}
		if lastErr == nil || lastErr.Class != agenterr.OutcomeFromDomain(agenterr.LockConflictOutcome) {
			t.Fatalf("loser %d LastError = %#v, want LockConflict", i, lastErr)
		}
	}
	if holders != 1 {
		t.Fatalf("agents holding task-hot = %d, want 1", holders)
	}
}

// TestClaimantID_WorktreelessAgentsAreDistinct names the invariant directly, so
// a future refactor cannot quietly collapse two worktree-less agents back onto
// one identity.
func TestClaimantID_WorktreelessAgentsAreDistinct(t *testing.T) {
	withWorktree := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "worker-1", Role: "task"}}
	if got := claimantID(withWorktree); got != "worker-1" {
		t.Fatalf("claimantID(worktree agent) = %q, want %q", got, "worker-1")
	}

	a := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "", Role: "task"}}
	b := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "", Role: "task"}}

	idA := claimantID(a)
	idB := claimantID(b)
	if idA == "" || idB == "" {
		t.Fatalf("worktree-less identities must be non-empty: %q, %q", idA, idB)
	}
	if idA == idB {
		t.Fatalf("two worktree-less agents of one role share identity %q", idA)
	}
	if again := claimantID(a); again != idA {
		t.Fatalf("claimantID not stable: %q then %q", idA, again)
	}
}

// TestClaimantID_ConcurrentCallersAgree pins the memoization down under -race:
// several goroutines asking one agent for its identity must all get the same
// answer.
func TestClaimantID_ConcurrentCallersAgree(t *testing.T) {
	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "", Role: "task"}}
	const callers = 8
	ids := make(chan string, callers)
	start := make(chan struct{})
	for i := 0; i < callers; i++ {
		go func() {
			<-start
			ids <- claimantID(ap)
		}()
	}
	close(start)

	first := <-ids
	for i := 1; i < callers; i++ {
		if got := <-ids; got != first {
			t.Fatalf("concurrent claimantID disagreed: %q vs %q", first, got)
		}
	}
}

// TestClaimTask_WorktreelessResumeReReservesOwnTask guards the resume flow: the
// memoized identity must still let an agent re-reserve the task it already
// holds, which reserve treats as a no-op.
func TestClaimTask_WorktreelessResumeReReservesOwnTask(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{
		{ID: "task-1", IssueType: "task", Status: "open", Priority: 0, Title: "Ready", Design: "plan"},
	}
	s := &Supervisor{IssueBackend: mock}
	ap := &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "", Role: "task"},
		RoleConfig: cfgpkg.RoleConfig{TaskFilter: "has_design"},
	}
	if !s.claimTask(ap, "") {
		t.Fatalf("claimTask = false, want true")
	}
	if ap.AssignedTaskID != "task-1" {
		t.Fatalf("AssignedTaskID = %q, want task-1", ap.AssignedTaskID)
	}
	if err := s.claims.reserve("task-1", claimantID(ap)); err != nil {
		t.Fatalf("re-reserving own task = %v, want nil", err)
	}
}

func TestClaimTask_LoserFallsThroughToAnotherTask(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{
		{ID: "task-hot", IssueType: "task", Status: "open", Priority: 0, Title: "Contended", Design: "plan"},
		{ID: "task-cold", IssueType: "task", Status: "open", Priority: 5, Title: "Spare", Design: "plan"},
	}
	s := &Supervisor{IssueBackend: mock}
	winner := &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "worker-1", Role: "task"},
		RoleConfig: cfgpkg.RoleConfig{TaskFilter: "has_design"},
	}
	loser := &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "worker-2", Role: "task"},
		RoleConfig: cfgpkg.RoleConfig{TaskFilter: "has_design"},
	}

	if !s.claimTask(winner, "") || winner.AssignedTaskID != "task-hot" {
		t.Fatalf("winner AssignedTaskID = %q, want task-hot", winner.AssignedTaskID)
	}
	if !s.claimTask(loser, "") {
		t.Fatal("loser claimTask returned false, want fallthrough to the spare task")
	}
	if loser.AssignedTaskID != "task-cold" {
		t.Fatalf("loser AssignedTaskID = %q, want task-cold", loser.AssignedTaskID)
	}
}

func TestReleaseAssignedTaskClaim_FreesReservationForPeer(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{
		{ID: "task-hot", IssueType: "task", Status: "open", Priority: 0, Title: "Contended", Design: "plan"},
	}
	s := &Supervisor{IssueBackend: mock}
	first := &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "worker-1", Role: "task"},
		RoleConfig: cfgpkg.RoleConfig{TaskFilter: "has_design"},
	}
	second := &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "worker-2", Role: "task"},
		RoleConfig: cfgpkg.RoleConfig{TaskFilter: "has_design"},
	}

	if !s.claimTask(first, "") {
		t.Fatal("first claimTask returned false")
	}
	if s.claimTask(second, "") {
		t.Fatal("second claimTask returned true while the first still holds the task")
	}
	s.releaseAssignedTaskClaim(first, "task-hot")
	if !s.claimTask(second, "") || second.AssignedTaskID != "task-hot" {
		t.Fatalf("after release, second AssignedTaskID = %q, want task-hot", second.AssignedTaskID)
	}
}

// A claim that fails at the backend must not leave its reservation behind.
func TestClaimIssueForAgent_FailedClaimReleasesReservation(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.ClaimIssueFn = func(_ context.Context, _ string, _ time.Duration) error {
		return backend.ErrConflict("ClaimIssue", "claimed")
	}
	s := &Supervisor{IssueBackend: mock}
	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "worker-1", Role: "task"}}

	if err := s.claimIssueForAgent(ap, "task-1", "test"); err == nil {
		t.Fatal("claimIssueForAgent returned nil, want conflict")
	}
	if err := s.claims.reserve("task-1", "worker-2"); err != nil {
		t.Fatalf("claims.reserve after failed claim = %v, want nil (reservation leaked)", err)
	}
}

// The candidate list empties through conflicts well before the retry limit;
// the report must carry the conflict detail rather than the generic no-work
// message, so lock contention is distinguishable from an empty board.
func TestClaimTask_ExhaustedCandidatesReportConflictDetail(t *testing.T) {
	mock := clitest.NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{
		{ID: "task-1", IssueType: "task", Status: "open", Priority: 1, Title: "One", Design: "plan"},
		{ID: "task-2", IssueType: "task", Status: "open", Priority: 2, Title: "Two", Design: "plan"},
	}
	mock.ClaimIssueFn = func(_ context.Context, _ string, _ time.Duration) error {
		return &backend.BackendError{
			Kind: backend.KindConflict, Op: "ClaimIssue", Message: "claimed",
			Meta: map[string]string{"existing_owner": "peer-agent"},
		}
	}
	s := &Supervisor{IssueBackend: mock}
	ap := &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"},
		RoleConfig: cfgpkg.RoleConfig{TaskFilter: "has_design"},
	}

	if s.claimTask(ap, "") {
		t.Fatal("claimTask returned true after every candidate conflicted")
	}
	if ap.LastError == nil || ap.LastError.Class != agenterr.OutcomeFromDomain(agenterr.LockConflictOutcome) {
		t.Fatalf("LastError = %#v, want LockConflict", ap.LastError)
	}
	if !strings.Contains(ap.LastError.Message, "conflicts") || !strings.Contains(ap.LastError.Message, "peer-agent") {
		t.Fatalf("LastError.Message = %q, want conflict count and holder", ap.LastError.Message)
	}
}
