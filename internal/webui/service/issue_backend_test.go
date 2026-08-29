package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/types"
)

// fakeIssueBackend is a webui-local stub for backend.IssueBackend used by
// service-layer tests. It records calls and returns canned values so each
// test can assert behavior without depending on the cli/clitest mock (which
// would create an import cycle).
type fakeIssueBackend struct {
	mu sync.Mutex

	// Per-method canned returns and capture slots.
	getResult           *backend.IssueDetailData
	getErr              error
	getCalls            []string
	listResult          []backend.IssueData
	listErr             error
	listCalls           []backend.ListOpts
	readyResult         []backend.IssueData
	readyErr            error
	readyCalls          []backend.ReadyOpts
	blockedResult       []backend.IssueData
	blockedErr          error
	blockedCalls        []backend.BlockedOpts
	deferredResult      []backend.IssueData
	deferredErr         error
	deferredCalls       []backend.DeferredOpts
	createResult        *backend.IssueData
	createErr           error
	createParams        []backend.CreateParams
	updateErr           error
	updateCalls         []updateCall
	closeResult         *backend.CloseResult
	closeErr            error
	closeCalls          []closeCall
	deleteErr           error
	deleteCalls         []backend.DeleteParams
	addCommentResult    *backend.CommentData
	addCommentErr       error
	addCommentParams    []backend.CommentAddParams
	addDepErr           error
	addDepParams        []backend.DepAddParams
	removeDepErr        error
	removeDepParams     []backend.DepRemoveParams
	listEventsResult    []backend.EventData
	listEventsErr       error
	listEventsCalls     []listEventsCall
	claimErr            error
	claimCalls          []claimCall
	postClaimUpdateErr  error
	overrideUpdateClaim bool
}

type updateCall struct {
	id     string
	params backend.UpdateParams
}

type closeCall struct {
	id     string
	params backend.CloseParams
}

type listEventsCall struct {
	id    string
	limit int
}

type claimCall struct {
	id      string
	lockTTL time.Duration
}

type testWorkspaceValidator struct {
	targetID string
	err      error
}

func (v testWorkspaceValidator) ValidateTarget(_ string) (string, error) {
	return v.targetID, v.err
}

func (v testWorkspaceValidator) CurrentWorkspace() string {
	return "source-ws"
}

func (f *fakeIssueBackend) Get(_ context.Context, id string) (*backend.IssueDetailData, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getCalls = append(f.getCalls, id)
	return f.getResult, f.getErr
}

func (f *fakeIssueBackend) List(_ context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls = append(f.listCalls, opts)
	return f.listResult, f.listErr
}
func (f *fakeIssueBackend) Ready(_ context.Context, opts backend.ReadyOpts) ([]backend.IssueData, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.readyCalls = append(f.readyCalls, opts)
	return f.readyResult, f.readyErr
}
func (f *fakeIssueBackend) Blocked(_ context.Context, opts backend.BlockedOpts) ([]backend.IssueData, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blockedCalls = append(f.blockedCalls, opts)
	return f.blockedResult, f.blockedErr
}
func (f *fakeIssueBackend) Deferred(_ context.Context, opts backend.DeferredOpts) ([]backend.IssueData, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deferredCalls = append(f.deferredCalls, opts)
	return f.deferredResult, f.deferredErr
}
func (f *fakeIssueBackend) Stats(_ context.Context) (*backend.StatsData, error) { return nil, nil }
func (f *fakeIssueBackend) Count(_ context.Context, _ backend.CountOpts) (int, error) {
	return 0, nil
}
func (f *fakeIssueBackend) GetChildren(_ context.Context, _ string) ([]backend.IssueData, error) {
	return nil, nil
}
func (f *fakeIssueBackend) SearchIssues(_ context.Context, _ string, _ int) ([]backend.IssueData, error) {
	return nil, nil
}

func (f *fakeIssueBackend) Create(_ context.Context, params backend.CreateParams) (*backend.IssueData, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createParams = append(f.createParams, params)
	return f.createResult, f.createErr
}

func (f *fakeIssueBackend) Update(_ context.Context, id string, params backend.UpdateParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateCalls = append(f.updateCalls, updateCall{id: id, params: params})
	// Differentiate the post-claim status update (Claim=false but Status set
	// to in_progress) from regular Patch calls so ClaimIssue tests can
	// inject a separate error if they want.
	if !params.Claim && params.Status != nil && *params.Status == "in_progress" && f.overrideUpdateClaim {
		return f.postClaimUpdateErr
	}
	return f.updateErr
}

func (f *fakeIssueBackend) ClaimIssue(_ context.Context, id string, lockTTL time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claimCalls = append(f.claimCalls, claimCall{id: id, lockTTL: lockTTL})
	return f.claimErr
}

func (f *fakeIssueBackend) ReleaseIssueLock(_ context.Context, _, _ string) error { return nil }

func (f *fakeIssueBackend) DeferIssue(_ context.Context, _ string, _ time.Time) error { return nil }
func (f *fakeIssueBackend) UndeferIssue(_ context.Context, _ string) error            { return nil }

func (f *fakeIssueBackend) Close(_ context.Context, id string, params backend.CloseParams) (*backend.CloseResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeCalls = append(f.closeCalls, closeCall{id: id, params: params})
	return f.closeResult, f.closeErr
}

func (f *fakeIssueBackend) Reopen(_ context.Context, _ string, _ backend.ReopenParams) error {
	return nil
}

func (f *fakeIssueBackend) Delete(_ context.Context, params backend.DeleteParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls = append(f.deleteCalls, params)
	return f.deleteErr
}

func (f *fakeIssueBackend) AddDependency(_ context.Context, params backend.DepAddParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addDepParams = append(f.addDepParams, params)
	return f.addDepErr
}

func (f *fakeIssueBackend) RemoveDependency(_ context.Context, params backend.DepRemoveParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removeDepParams = append(f.removeDepParams, params)
	return f.removeDepErr
}

func (f *fakeIssueBackend) AddLabel(_ context.Context, _, _ string) error    { return nil }
func (f *fakeIssueBackend) RemoveLabel(_ context.Context, _, _ string) error { return nil }
func (f *fakeIssueBackend) ListComments(_ context.Context, _ string) ([]backend.CommentData, error) {
	return nil, nil
}

func (f *fakeIssueBackend) AddComment(_ context.Context, params backend.CommentAddParams) (*backend.CommentData, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addCommentParams = append(f.addCommentParams, params)
	return f.addCommentResult, f.addCommentErr
}

func (f *fakeIssueBackend) ListEvents(_ context.Context, id string, limit int) ([]backend.EventData, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listEventsCalls = append(f.listEventsCalls, listEventsCall{id: id, limit: limit})
	return f.listEventsResult, f.listEventsErr
}

func (f *fakeIssueBackend) Batch(_ context.Context, _ []backend.BatchOp) ([]backend.BatchResult, error) {
	return nil, nil
}
func (f *fakeIssueBackend) GetMutations(_ context.Context, _ int64) ([]backend.MutationData, error) {
	return nil, nil
}
func (f *fakeIssueBackend) WaitForMutations(_ context.Context, _, _ int64) ([]backend.MutationData, error) {
	return nil, nil
}
func (f *fakeIssueBackend) BackendName() string { return "fake" }

// newServiceWithFake constructs an issueServiceImpl wired to a fake backend
// (no daemon pool). Pool-using paths (ListIssues, MoveIssue) are unaffected.
func newServiceWithFake(fb *fakeIssueBackend) IssueService {
	return NewIssueServiceWithBackend(nil, nil, nil, func(_ context.Context) backend.IssueBackend { return fb })
}

// --- ListEvents ---

func TestListEvents_Backend_Success(t *testing.T) {
	now := time.Now().UTC()
	fb := &fakeIssueBackend{
		listEventsResult: []backend.EventData{
			{
				ID: "1787177211116-0", IssueID: "test-1", Kind: "issue.update", Actor: "alice",
				Target: "test-1", Payload: `{"status":"in_progress"}`, Category: "field_change",
				Summary: "Updated status", Changes: []backend.FieldChange{{Field: "status", Before: "open", After: "in_progress"}},
				Metadata: map[string]string{"source": "test"}, CreatedAt: now,
			},
			{ID: "2", IssueID: "test-1", Kind: "issue.status_changed", Actor: "bob", CreatedAt: now},
		},
	}
	svc := newServiceWithFake(fb)

	events, err := svc.ListEvents(context.Background(), EventListParams{IssueID: "test-1", Limit: 50})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].ID != "1787177211116-0" {
		t.Errorf("event[0].ID = %q, want stream ID", events[0].ID)
	}
	if events[0].EventType != types.EventType("issue.update") {
		t.Errorf("event[0].EventType = %q, want issue.update", events[0].EventType)
	}
	if events[0].Target != "test-1" || events[0].Payload != `{"status":"in_progress"}` || events[0].Category != "field_change" || events[0].Summary != "Updated status" {
		t.Errorf("event[0] widened fields = %+v", events[0])
	}
	if len(events[0].Changes) != 1 || events[0].Changes[0] != (types.FieldChange{Field: "status", Before: "open", After: "in_progress"}) {
		t.Errorf("event[0].Changes = %+v", events[0].Changes)
	}
	if events[0].Metadata["source"] != "test" {
		t.Errorf("event[0].Metadata = %+v", events[0].Metadata)
	}
	if len(fb.listEventsCalls) != 1 {
		t.Fatalf("expected 1 backend call, got %d", len(fb.listEventsCalls))
	}
	if fb.listEventsCalls[0].id != "test-1" || fb.listEventsCalls[0].limit != 50 {
		t.Errorf("unexpected call args: %+v", fb.listEventsCalls[0])
	}
}

func TestListEvents_Backend_NotFound_MapsTo404(t *testing.T) {
	fb := &fakeIssueBackend{
		listEventsErr: backend.ErrNotFound("ListEvents", "issue not found"),
	}
	svc := newServiceWithFake(fb)

	_, err := svc.ListEvents(context.Background(), EventListParams{IssueID: "missing", Limit: 10})
	var sErr *ServiceError
	if !errors.As(err, &sErr) {
		t.Fatalf("expected *ServiceError, got %T (%v)", err, err)
	}
	if sErr.Kind != KindNotFound {
		t.Errorf("Kind = %q, want %q", sErr.Kind, KindNotFound)
	}
}

func TestListEvents_Backend_Unavailable_NilBackend(t *testing.T) {
	svc := NewIssueServiceWithBackend(nil, nil, nil, func(_ context.Context) backend.IssueBackend { return nil })
	_, err := svc.ListEvents(context.Background(), EventListParams{IssueID: "x"})
	var sErr *ServiceError
	if !errors.As(err, &sErr) {
		t.Fatalf("expected *ServiceError, got %T", err)
	}
	if sErr.Kind != KindUnavailable {
		t.Errorf("Kind = %q, want unavailable", sErr.Kind)
	}
}

func TestListEventHistory_LegacyBackendLeavesTotalUnknown(t *testing.T) {
	now := time.Now().UTC()
	fb := &fakeIssueBackend{
		listEventsResult: []backend.EventData{{
			ID:        "1",
			IssueID:   "test-1",
			Kind:      "issue.created",
			CreatedAt: now,
		}},
	}
	svc := newServiceWithFake(fb)

	result, err := svc.ListEventHistory(context.Background(), EventListParams{IssueID: "test-1", Limit: 100})
	if err != nil {
		t.Fatalf("ListEventHistory: %v", err)
	}
	if len(result.Events) != 1 {
		t.Fatalf("event count = %d, want 1", len(result.Events))
	}
	if result.TotalEvents != 0 {
		t.Errorf("TotalEvents = %d, want 0 when the legacy backend has no total", result.TotalEvents)
	}
}

// --- AddDependency / RemoveDependency ---

func TestAddDependency_Backend_Success(t *testing.T) {
	fb := &fakeIssueBackend{}
	svc := newServiceWithFake(fb)

	err := svc.AddDependency(context.Background(), AddDependencyParams{
		IssueID: "i-1", DependsOnID: "i-2", DepType: "blocks",
	})
	if err != nil {
		t.Fatalf("AddDependency: %v", err)
	}
	if len(fb.addDepParams) != 1 {
		t.Fatalf("expected 1 backend call, got %d", len(fb.addDepParams))
	}
	got := fb.addDepParams[0]
	if got.FromID != "i-1" || got.ToID != "i-2" || got.DepType != "blocks" {
		t.Errorf("unexpected backend args: %+v", got)
	}
}

func TestAddDependency_Backend_DefaultDepType(t *testing.T) {
	fb := &fakeIssueBackend{}
	svc := newServiceWithFake(fb)
	if err := svc.AddDependency(context.Background(), AddDependencyParams{
		IssueID: "i-1", DependsOnID: "i-2",
	}); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}
	if fb.addDepParams[0].DepType != "blocks" {
		t.Errorf("DepType = %q, want %q (default)", fb.addDepParams[0].DepType, "blocks")
	}
}

func TestAddDependency_Backend_SelfDependency_ValidationError(t *testing.T) {
	fb := &fakeIssueBackend{}
	svc := newServiceWithFake(fb)
	err := svc.AddDependency(context.Background(), AddDependencyParams{
		IssueID: "i-1", DependsOnID: "i-1",
	})
	var sErr *ServiceError
	if !errors.As(err, &sErr) || sErr.Kind != KindValidation {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	if len(fb.addDepParams) != 0 {
		t.Errorf("backend should not be called on validation failure")
	}
}

func TestAddDependency_Backend_Conflict_MapsTo409(t *testing.T) {
	fb := &fakeIssueBackend{
		addDepErr: backend.ErrConflict("AddDependency", "dependency already exists"),
	}
	svc := newServiceWithFake(fb)
	err := svc.AddDependency(context.Background(), AddDependencyParams{
		IssueID: "i-1", DependsOnID: "i-2",
	})
	var sErr *ServiceError
	if !errors.As(err, &sErr) || sErr.Kind != KindConflict {
		t.Fatalf("expected ConflictError, got %v", err)
	}
}

func TestRemoveDependency_Backend_Success(t *testing.T) {
	fb := &fakeIssueBackend{}
	svc := newServiceWithFake(fb)
	err := svc.RemoveDependency(context.Background(), RemoveDependencyParams{
		IssueID: "i-1", DepID: "i-2",
	})
	if err != nil {
		t.Fatalf("RemoveDependency: %v", err)
	}
	if len(fb.removeDepParams) != 1 {
		t.Fatalf("expected 1 call, got %d", len(fb.removeDepParams))
	}
	if fb.removeDepParams[0].FromID != "i-1" || fb.removeDepParams[0].ToID != "i-2" {
		t.Errorf("unexpected args: %+v", fb.removeDepParams[0])
	}
}

func TestRemoveDependency_Backend_NotFound_MapsTo404(t *testing.T) {
	fb := &fakeIssueBackend{
		removeDepErr: backend.ErrNotFound("RemoveDependency", "dependency not found"),
	}
	svc := newServiceWithFake(fb)
	err := svc.RemoveDependency(context.Background(), RemoveDependencyParams{
		IssueID: "i-1", DepID: "i-99",
	})
	var sErr *ServiceError
	if !errors.As(err, &sErr) || sErr.Kind != KindNotFound {
		t.Fatalf("expected NotFoundError, got %v", err)
	}
}

// --- MoveIssue ---

func TestMoveIssue_Backend_Success_UsesBackendWithoutDaemonPool(t *testing.T) {
	now := time.Now().UTC()
	source := &fakeIssueBackend{
		getResult: &backend.IssueDetailData{
			IssueData: backend.IssueData{
				ID: "SRC-1", Title: "Move me", Status: "open", Priority: 2, IssueType: "task",
				Assignee: "[H] Tyson", Labels: []string{"ui"}, CreatedAt: now, UpdatedAt: now,
			},
			Description: "details",
		},
		addCommentResult: &backend.CommentData{ID: 1, IssueID: "SRC-1", Author: "web-ui", Text: "moved", CreatedAt: now},
		closeResult:      &backend.CloseResult{Closed: &backend.IssueData{ID: "SRC-1", Status: "closed"}},
	}
	target := &fakeIssueBackend{
		createResult: &backend.IssueData{ID: "DST-9", Title: "Move me", Status: "open", CreatedAt: now, UpdatedAt: now},
	}

	type workspaceKey struct{}
	withWorkspace := func(ctx context.Context, wsID string) context.Context {
		return context.WithValue(ctx, workspaceKey{}, wsID)
	}
	provider := func(ctx context.Context) backend.IssueBackend {
		if wsID, _ := ctx.Value(workspaceKey{}).(string); wsID == "target-ws" {
			return target
		}
		return source
	}
	svc := NewIssueServiceWithBackend(nil, nil, withWorkspace, provider)

	result, err := svc.MoveIssue(context.Background(), MoveIssueParams{
		IssueID:         "SRC-1",
		TargetWorkspace: "Target",
		Validator:       testWorkspaceValidator{targetID: "target-ws"},
	})
	if err != nil {
		t.Fatalf("MoveIssue: %v", err)
	}
	if result.SourceID != "SRC-1" || result.TargetID != "DST-9" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "[H] Tyson") {
		t.Fatalf("expected active agent warning, got %+v", result.Warnings)
	}
	if len(target.createParams) != 1 {
		t.Fatalf("expected 1 target create call, got %d", len(target.createParams))
	}
	create := target.createParams[0]
	if create.Title != "Move me" || create.IssueType != "task" || create.CreatedBy != "web-ui" {
		t.Errorf("unexpected create params: %+v", create)
	}
	if !strings.Contains(create.Description, "(Moved from SRC-1)") {
		t.Errorf("description missing move marker: %q", create.Description)
	}
	if len(create.Labels) != 1 || create.Labels[0] != "ui" {
		t.Errorf("labels = %+v, want [ui]", create.Labels)
	}
	if len(source.addCommentParams) != 1 || !strings.Contains(source.addCommentParams[0].Text, "DST-9") {
		t.Errorf("unexpected source comment params: %+v", source.addCommentParams)
	}
	if len(source.closeCalls) != 1 || !source.closeCalls[0].params.Force {
		t.Errorf("unexpected source close calls: %+v", source.closeCalls)
	}
}

func TestMoveIssue_Backend_ClosedSource_Validation(t *testing.T) {
	now := time.Now().UTC()
	fb := &fakeIssueBackend{
		getResult: &backend.IssueDetailData{
			IssueData: backend.IssueData{
				ID: "SRC-1", Title: "Closed", Status: "closed", Priority: 1, IssueType: "task", CreatedAt: now, UpdatedAt: now,
			},
		},
	}
	svc := newServiceWithFake(fb)

	_, err := svc.MoveIssue(context.Background(), MoveIssueParams{
		IssueID:         "SRC-1",
		TargetWorkspace: "Target",
		Validator:       testWorkspaceValidator{targetID: "target-ws"},
	})
	var sErr *ServiceError
	if !errors.As(err, &sErr) || sErr.Kind != KindValidation {
		t.Fatalf("expected ValidationError, got %v", err)
	}
}

// --- AddComment ---

func TestAddComment_Backend_Success(t *testing.T) {
	now := time.Now().UTC()
	fb := &fakeIssueBackend{
		addCommentResult: &backend.CommentData{
			ID: 99, IssueID: "i-1", Author: "web-ui", Text: "hello", CreatedAt: now,
		},
	}
	svc := newServiceWithFake(fb)

	c, err := svc.AddComment(context.Background(), AddCommentParams{
		IssueID: "i-1", Text: "hello",
	})
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	if c.ID != 99 || c.IssueID != "i-1" || c.Author != "web-ui" || c.Text != "hello" {
		t.Errorf("unexpected comment: %+v", c)
	}
	if fb.addCommentParams[0].Author != "web-ui" {
		t.Errorf("Author = %q, want web-ui (default)", fb.addCommentParams[0].Author)
	}
}

func TestAddComment_Backend_EmptyText_Validation(t *testing.T) {
	fb := &fakeIssueBackend{}
	svc := newServiceWithFake(fb)
	_, err := svc.AddComment(context.Background(), AddCommentParams{IssueID: "i-1", Text: "   "})
	var sErr *ServiceError
	if !errors.As(err, &sErr) || sErr.Kind != KindValidation {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	if len(fb.addCommentParams) != 0 {
		t.Errorf("backend should not be called on validation failure")
	}
}

// --- CloseIssue ---

func TestCloseIssue_Backend_Success_WrapsCloseResult(t *testing.T) {
	now := time.Now().UTC()
	closed := &backend.IssueData{
		ID: "i-1", Title: "Done", Status: "closed", Priority: 2, CreatedAt: now, UpdatedAt: now,
	}
	fb := &fakeIssueBackend{
		closeResult: &backend.CloseResult{
			Closed:    closed,
			Unblocked: []backend.IssueData{{ID: "i-2", Title: "Next", Status: "open", CreatedAt: now, UpdatedAt: now}},
		},
	}
	svc := newServiceWithFake(fb)

	raw, err := svc.CloseIssue(context.Background(), CloseIssueParams{IssueID: "i-1", Reason: "fixed"})
	if err != nil {
		t.Fatalf("CloseIssue: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal close response: %v", err)
	}
	closedMap, ok := got["closed"].(map[string]any)
	if !ok {
		t.Fatalf("expected closed object, got %T (%v)", got["closed"], got["closed"])
	}
	if closedMap["id"] != "i-1" || closedMap["status"] != "closed" {
		t.Errorf("unexpected closed shape: %+v", closedMap)
	}
	unblocked, ok := got["unblocked"].([]any)
	if !ok {
		t.Fatalf("expected unblocked array, got %T", got["unblocked"])
	}
	if len(unblocked) != 1 {
		t.Errorf("expected 1 unblocked, got %d", len(unblocked))
	}
	if fb.closeCalls[0].id != "i-1" || fb.closeCalls[0].params.Reason != "fixed" {
		t.Errorf("unexpected backend args: %+v", fb.closeCalls[0])
	}
}

func TestCloseIssue_Backend_NotFound_MapsTo404(t *testing.T) {
	fb := &fakeIssueBackend{
		closeErr: backend.ErrNotFound("Close", "issue not found"),
	}
	svc := newServiceWithFake(fb)
	_, err := svc.CloseIssue(context.Background(), CloseIssueParams{IssueID: "missing"})
	var sErr *ServiceError
	if !errors.As(err, &sErr) || sErr.Kind != KindNotFound {
		t.Fatalf("expected NotFoundError, got %v", err)
	}
}

// --- ClaimIssue ---

func TestClaimIssue_Backend_HappyPath_RoutesToBackendAndReturnsIssue(t *testing.T) {
	now := time.Now().UTC()
	fb := &fakeIssueBackend{
		getResult: &backend.IssueDetailData{
			IssueData: backend.IssueData{
				ID: "i-1", Title: "T", Status: "in_progress", Priority: 1, CreatedAt: now, UpdatedAt: now,
			},
		},
	}
	svc := newServiceWithFake(fb)

	raw, err := svc.ClaimIssue(context.Background(), ClaimIssueParams{IssueID: "i-1"})
	if err != nil {
		t.Fatalf("ClaimIssue: %v", err)
	}
	if len(fb.claimCalls) != 1 || fb.claimCalls[0].id != "i-1" {
		t.Errorf("expected 1 claim call for i-1, got %+v", fb.claimCalls)
	}
	// Post-claim status update should be recorded as well.
	hasInProgressUpdate := false
	for _, u := range fb.updateCalls {
		if u.params.Status != nil && *u.params.Status == "in_progress" {
			hasInProgressUpdate = true
			break
		}
	}
	if !hasInProgressUpdate {
		t.Error("expected post-claim status update to in_progress")
	}
	// Final Get should produce a sane wire body.
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal claim response: %v", err)
	}
	if got["id"] != "i-1" || got["status"] != "in_progress" {
		t.Errorf("unexpected claim response shape: %+v", got)
	}
}

func TestClaimIssue_Backend_AlreadyClaimed_MapsTo409(t *testing.T) {
	now := time.Now().UTC()
	fb := &fakeIssueBackend{
		getResult: &backend.IssueDetailData{
			IssueData: backend.IssueData{
				ID: "i-1", Title: "T", Status: "open", Priority: 1, CreatedAt: now, UpdatedAt: now,
			},
		},
		claimErr: backend.ErrConflict("ClaimIssue", "issue already claimed by other-agent"),
	}
	svc := newServiceWithFake(fb)
	_, err := svc.ClaimIssue(context.Background(), ClaimIssueParams{IssueID: "i-1"})
	var sErr *ServiceError
	if !errors.As(err, &sErr) || sErr.Kind != KindConflict {
		t.Fatalf("expected ConflictError, got %v", err)
	}
}

func TestClaimIssue_Backend_BlockedIssue_MapsTo409AndDoesNotClaim(t *testing.T) {
	now := time.Now().UTC()
	fb := &fakeIssueBackend{
		getResult: &backend.IssueDetailData{
			IssueData: backend.IssueData{
				ID: "i-1", Title: "T", Status: "open", Priority: 1, CreatedAt: now, UpdatedAt: now,
			},
			Dependencies: []backend.DependencyData{
				{
					IssueID:     "i-1",
					DependsOnID: "blocker-1",
					Type:        "blocks",
					Status:      "open",
					CreatedAt:   now,
				},
			},
		},
	}
	svc := newServiceWithFake(fb)
	_, err := svc.ClaimIssue(context.Background(), ClaimIssueParams{IssueID: "i-1"})
	var sErr *ServiceError
	if !errors.As(err, &sErr) || sErr.Kind != KindConflict {
		t.Fatalf("expected ConflictError, got %v", err)
	}
	if !strings.Contains(sErr.Message, "blocked by open dependency blocker-1") {
		t.Fatalf("unexpected conflict message: %q", sErr.Message)
	}
	if len(fb.claimCalls) != 0 {
		t.Fatalf("blocked issue should not be claimed, got calls %+v", fb.claimCalls)
	}
}

func TestClaimIssue_Backend_AllReadyWorkBlockers_MapTo409AndDoNotClaim(t *testing.T) {
	blockingTypes := []string{
		"blocks",
		"parent-child",
		"conditional-blocks",
		"waits-for",
	}

	for _, depType := range blockingTypes {
		t.Run(depType, func(t *testing.T) {
			now := time.Now().UTC()
			fb := &fakeIssueBackend{
				getResult: &backend.IssueDetailData{
					IssueData: backend.IssueData{
						ID: "i-1", Title: "T", Status: "open", Priority: 1, CreatedAt: now, UpdatedAt: now,
					},
					Dependencies: []backend.DependencyData{
						{
							IssueID:     "i-1",
							DependsOnID: "blocker-1",
							Type:        depType,
							Status:      "open",
							CreatedAt:   now,
						},
					},
				},
			}
			svc := newServiceWithFake(fb)
			_, err := svc.ClaimIssue(context.Background(), ClaimIssueParams{IssueID: "i-1"})
			var sErr *ServiceError
			if !errors.As(err, &sErr) || sErr.Kind != KindConflict {
				t.Fatalf("expected ConflictError, got %v", err)
			}
			if len(fb.claimCalls) != 0 {
				t.Fatalf("blocked issue should not be claimed, got calls %+v", fb.claimCalls)
			}
		})
	}
}

func TestClaimIssue_Backend_NonBlockingDependencyTypes_AllowClaim(t *testing.T) {
	nonBlockingTypes := []string{
		"related",
		"discovered-from",
		"replies-to",
		"relates-to",
		"duplicates",
		"supersedes",
		"authored-by",
		"assigned-to",
		"approved-by",
		"attests",
		"tracks",
		"until",
		"caused-by",
		"validates",
		"delegated-from",
	}

	for _, depType := range nonBlockingTypes {
		t.Run(depType, func(t *testing.T) {
			now := time.Now().UTC()
			fb := &fakeIssueBackend{
				getResult: &backend.IssueDetailData{
					IssueData: backend.IssueData{
						ID: "i-1", Title: "T", Status: "in_progress", Priority: 1, CreatedAt: now, UpdatedAt: now,
					},
					Dependencies: []backend.DependencyData{
						{
							IssueID:     "i-1",
							DependsOnID: "linked-1",
							Type:        depType,
							Status:      "open",
							CreatedAt:   now,
						},
					},
				},
			}
			svc := newServiceWithFake(fb)
			if _, err := svc.ClaimIssue(context.Background(), ClaimIssueParams{IssueID: "i-1"}); err != nil {
				t.Fatalf("ClaimIssue: %v", err)
			}
			if len(fb.claimCalls) != 1 {
				t.Fatalf("expected claim call, got %+v", fb.claimCalls)
			}
		})
	}
}

func TestClaimIssue_Backend_ClosedBlockingDependency_AllowsClaim(t *testing.T) {
	now := time.Now().UTC()
	fb := &fakeIssueBackend{
		getResult: &backend.IssueDetailData{
			IssueData: backend.IssueData{
				ID: "i-1", Title: "T", Status: "in_progress", Priority: 1, CreatedAt: now, UpdatedAt: now,
			},
			Dependencies: []backend.DependencyData{
				{
					IssueID:     "i-1",
					DependsOnID: "blocker-1",
					Type:        "blocks",
					Status:      "closed",
					CreatedAt:   now,
				},
			},
		},
	}
	svc := newServiceWithFake(fb)
	if _, err := svc.ClaimIssue(context.Background(), ClaimIssueParams{IssueID: "i-1"}); err != nil {
		t.Fatalf("ClaimIssue: %v", err)
	}
	if len(fb.claimCalls) != 1 {
		t.Fatalf("expected claim call, got %+v", fb.claimCalls)
	}
}

func TestClaimIssue_Backend_EmptyID_Validation(t *testing.T) {
	fb := &fakeIssueBackend{}
	svc := newServiceWithFake(fb)
	_, err := svc.ClaimIssue(context.Background(), ClaimIssueParams{IssueID: "  "})
	var sErr *ServiceError
	if !errors.As(err, &sErr) || sErr.Kind != KindValidation {
		t.Fatalf("expected ValidationError, got %v", err)
	}
}

// --- DeleteIssue ---

func TestDeleteIssue_Backend_Success_ReturnsEnvelope(t *testing.T) {
	fb := &fakeIssueBackend{}
	svc := newServiceWithFake(fb)
	raw, err := svc.DeleteIssue(context.Background(), "i-1")
	if err != nil {
		t.Fatalf("DeleteIssue: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["deleted_count"].(float64) != 1 {
		t.Errorf("deleted_count = %v, want 1", got["deleted_count"])
	}
	ids, ok := got["deleted_ids"].([]any)
	if !ok || len(ids) != 1 || ids[0] != "i-1" {
		t.Errorf("deleted_ids = %v, want [i-1]", got["deleted_ids"])
	}
	if len(fb.deleteCalls) != 1 || fb.deleteCalls[0].IDs[0] != "i-1" || !fb.deleteCalls[0].Force {
		t.Errorf("unexpected backend call: %+v", fb.deleteCalls[0])
	}
}

// --- CreateIssue ---

func TestCreateIssue_Backend_Success_ReturnsIssueShape(t *testing.T) {
	now := time.Now().UTC()
	fb := &fakeIssueBackend{
		createResult: &backend.IssueData{
			ID: "loom-x1", Title: "New", Status: "open", Priority: 2,
			IssueType: "task", CreatedAt: now, UpdatedAt: now,
		},
		// Post-create Get fetches the full detail; FE expects rich fields.
		getResult: &backend.IssueDetailData{
			IssueData: backend.IssueData{
				ID: "loom-x1", Title: "New", Status: "open", Priority: 2,
				IssueType: "task", SourceRepo: "repo-a", CreatedAt: now, UpdatedAt: now,
				Notes: "note",
			},
			Description: "body",
		},
	}
	svc := newServiceWithFake(fb)

	raw, err := svc.CreateIssue(context.Background(), CreateIssueParams{
		Title:      "New",
		IssueType:  "task",
		Priority:   2,
		Status:     "deferred",
		SourceRepo: "repo-a",
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["id"] != "loom-x1" || got["title"] != "New" || got["status"] != "open" {
		t.Errorf("unexpected issue shape: %+v", got)
	}
	// Detail-only fields must be present on the create response so the FE
	// roundtrip matches the previous *rpc.Client behavior.
	if got["description"] != "body" {
		t.Errorf("description = %v, want body", got["description"])
	}
	if got["notes"] != "note" {
		t.Errorf("notes = %v, want note", got["notes"])
	}
	if got["source_repo"] != "repo-a" || got["repo"] != "repo-a" {
		t.Errorf("repo aliases = source_repo:%v repo:%v, want repo-a", got["source_repo"], got["repo"])
	}
	if len(fb.createParams) != 1 {
		t.Fatalf("expected 1 backend call, got %d", len(fb.createParams))
	}
	if fb.createParams[0].Title != "New" || fb.createParams[0].IssueType != "task" || fb.createParams[0].Status != "deferred" || fb.createParams[0].SourceRepo != "repo-a" {
		t.Errorf("unexpected backend params: %+v", fb.createParams[0])
	}
}

// TestCreateIssue_Backend_GetFails_FallsBackToSlimProjection verifies the
// create-then-Get fallback path: when the follow-up Get errors out, we
// still return success with the slim IssueData payload rather than failing
// the create.
func TestCreateIssue_Backend_GetFails_FallsBackToSlimProjection(t *testing.T) {
	now := time.Now().UTC()
	fb := &fakeIssueBackend{
		createResult: &backend.IssueData{
			ID: "loom-x2", Title: "Slim", Status: "open", Priority: 2,
			IssueType: "task", CreatedAt: now, UpdatedAt: now,
		},
		getErr: backend.ErrUnavailable("Get", "transient", nil),
	}
	svc := newServiceWithFake(fb)

	raw, err := svc.CreateIssue(context.Background(), CreateIssueParams{
		Title:     "Slim",
		IssueType: "task",
		Priority:  2,
	})
	if err != nil {
		t.Fatalf("CreateIssue (get-fail fallback): %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["id"] != "loom-x2" || got["title"] != "Slim" {
		t.Errorf("unexpected fallback shape: %+v", got)
	}
}

func TestCreateIssue_Backend_ValidationFails_DoesNotCallBackend(t *testing.T) {
	fb := &fakeIssueBackend{}
	svc := newServiceWithFake(fb)
	_, err := svc.CreateIssue(context.Background(), CreateIssueParams{
		// Missing required Title triggers validation error.
		IssueType: "task",
		Priority:  2,
	})
	var sErr *ServiceError
	if !errors.As(err, &sErr) || sErr.Kind != KindValidation {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	if len(fb.createParams) != 0 {
		t.Errorf("backend should not be called when validation fails")
	}
}

func TestCreateIssue_Backend_InvalidStatusFails(t *testing.T) {
	fb := &fakeIssueBackend{}
	svc := newServiceWithFake(fb)
	_, err := svc.CreateIssue(context.Background(), CreateIssueParams{
		Title:     "Invalid",
		IssueType: "task",
		Priority:  2,
		Status:    "in_progress",
	})
	var sErr *ServiceError
	if !errors.As(err, &sErr) || sErr.Kind != KindValidation {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	if len(fb.createParams) != 0 {
		t.Errorf("backend should not be called when validation fails")
	}
}

// --- PatchIssue ---

func TestPatchIssue_Backend_Success_PassesParams(t *testing.T) {
	fb := &fakeIssueBackend{}
	svc := newServiceWithFake(fb)
	title := "Renamed"
	status := "in_progress"
	err := svc.PatchIssue(context.Background(), PatchIssueParams{
		IssueID: "i-1", Title: &title, Status: &status,
	})
	if err != nil {
		t.Fatalf("PatchIssue: %v", err)
	}
	if len(fb.updateCalls) != 1 {
		t.Fatalf("expected 1 update call, got %d", len(fb.updateCalls))
	}
	got := fb.updateCalls[0]
	if got.id != "i-1" || got.params.Title == nil || *got.params.Title != "Renamed" {
		t.Errorf("unexpected update args: %+v", got)
	}
	if got.params.Status == nil || *got.params.Status != "in_progress" {
		t.Errorf("Status not propagated: %+v", got.params.Status)
	}
}

func TestPatchIssue_Backend_TemplateError_MapsToConflict(t *testing.T) {
	fb := &fakeIssueBackend{
		updateErr: backend.ErrInternal("Update", "cannot update template issue", nil),
	}
	svc := newServiceWithFake(fb)
	err := svc.PatchIssue(context.Background(), PatchIssueParams{IssueID: "i-1"})
	var sErr *ServiceError
	if !errors.As(err, &sErr) {
		t.Fatalf("expected *ServiceError, got %T", err)
	}
	if sErr.Kind != KindConflict {
		t.Errorf("Kind = %q, want conflict", sErr.Kind)
	}
}

// --- GetIssue ---

func TestGetIssue_Backend_Success_ReturnsDetailWire(t *testing.T) {
	now := time.Now().UTC()
	fb := &fakeIssueBackend{
		getResult: &backend.IssueDetailData{
			IssueData: backend.IssueData{
				ID: "i-1", Title: "T", Status: "open", Priority: 1, IssueType: "bug",
				Labels: []string{"alpha"}, CreatedAt: now, UpdatedAt: now,
			},
			Description: "body",
			Comments: []backend.CommentData{
				{ID: 1, IssueID: "i-1", Author: "alice", Text: "first", CreatedAt: now},
			},
			Dependencies: []backend.DependencyData{
				{IssueID: "i-1", DependsOnID: "i-0", Type: "blocks", Title: "Blocker", Status: "open", Priority: 0, CreatedAt: now},
			},
		},
	}
	svc := newServiceWithFake(fb)
	raw, err := svc.GetIssue(context.Background(), "i-1")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["id"] != "i-1" || got["title"] != "T" {
		t.Errorf("unexpected issue shape: %+v", got)
	}
	if got["description"] != "body" {
		t.Errorf("description not surfaced")
	}
	deps, ok := got["dependencies"].([]any)
	if !ok || len(deps) != 1 {
		t.Fatalf("dependencies = %v, want array of 1", got["dependencies"])
	}
	dep0 := deps[0].(map[string]any)
	if dep0["dependency_type"] != "blocks" {
		t.Errorf("dep dependency_type = %v, want blocks", dep0["dependency_type"])
	}
	if dep0["id"] != "i-0" {
		t.Errorf("dep id = %v, want i-0", dep0["id"])
	}
	comments, ok := got["comments"].([]any)
	if !ok || len(comments) != 1 {
		t.Fatalf("comments = %v, want array of 1", got["comments"])
	}
	c0 := comments[0].(map[string]any)
	if c0["author"] != "alice" || c0["text"] != "first" {
		t.Errorf("unexpected comment shape: %+v", c0)
	}
	labels, ok := got["labels"].([]any)
	if !ok || len(labels) != 1 || labels[0] != "alpha" {
		t.Errorf("labels = %v, want [alpha]", got["labels"])
	}
}

func TestGetIssue_Backend_NotFound_MapsTo404(t *testing.T) {
	fb := &fakeIssueBackend{
		getErr: backend.ErrNotFound("Get", "issue not found"),
	}
	svc := newServiceWithFake(fb)
	_, err := svc.GetIssue(context.Background(), "missing")
	var sErr *ServiceError
	if !errors.As(err, &sErr) || sErr.Kind != KindNotFound {
		t.Fatalf("expected NotFoundError, got %v", err)
	}
}

func TestGetIssue_Backend_NilDetail_TreatedAsNotFound(t *testing.T) {
	fb := &fakeIssueBackend{} // returns nil result, nil err
	svc := newServiceWithFake(fb)
	_, err := svc.GetIssue(context.Background(), "missing")
	var sErr *ServiceError
	if !errors.As(err, &sErr) || sErr.Kind != KindNotFound {
		t.Fatalf("expected NotFoundError, got %v", err)
	}
}

// --- translateBackendError fall-throughs ---

func TestTranslateBackendError_NonBackendError(t *testing.T) {
	err := translateBackendError(errors.New("boom"))
	if err == nil || err.Kind != KindInternal {
		t.Fatalf("expected internal, got %+v", err)
	}
	if !strings.Contains(err.Message, "boom") {
		t.Errorf("expected 'boom' in message, got %q", err.Message)
	}
}

func TestTranslateBackendError_DeadlineExceeded(t *testing.T) {
	err := translateBackendError(context.DeadlineExceeded)
	if err == nil || err.Kind != KindTimeout {
		t.Fatalf("expected timeout, got %+v", err)
	}
}

func TestTranslateBackendError_NilReturnsNil(t *testing.T) {
	if err := translateBackendError(nil); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

// --- NewIssueService without a backend returns ErrUnavailable for backend-only paths ---

func TestNewIssueService_NoBackend_ListEvents_Unavailable(t *testing.T) {
	svc := NewIssueService(nil, nil, nil)
	_, err := svc.ListEvents(context.Background(), EventListParams{IssueID: "x"})
	var sErr *ServiceError
	if !errors.As(err, &sErr) || sErr.Kind != KindUnavailable {
		t.Fatalf("expected ErrUnavailable from no-backend constructor, got %v", err)
	}
}

func TestCloseIssue_Backend_AlreadyClosed_IsQuietNoOp(t *testing.T) {
	// Old fleet-db surfaces a doubled close as a conflict; serve must treat
	// "already closed" as success (the desired state is true), not an
	// ERROR-level failure. New fleet-db returns 200 and never hits this.
	fb := &fakeIssueBackend{
		closeErr: backend.ErrConflict("Close", "issue is already closed"),
	}
	svc := newServiceWithFake(fb)

	raw, err := svc.CloseIssue(context.Background(), CloseIssueParams{IssueID: "i-1"})
	if err != nil {
		t.Fatalf("already-closed close must be a no-op success, got %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal close response: %v", err)
	}
	closedMap, ok := got["closed"].(map[string]any)
	if !ok || closedMap["id"] != "i-1" {
		t.Errorf("expected synthetic closed result for i-1, got %v", got["closed"])
	}
}

func TestCloseIssue_Backend_BlockerConflict_StillFails(t *testing.T) {
	fb := &fakeIssueBackend{
		closeErr: backend.ErrConflict("Close", "issue has open blockers"),
	}
	svc := newServiceWithFake(fb)
	_, err := svc.CloseIssue(context.Background(), CloseIssueParams{IssueID: "i-1"})
	var sErr *ServiceError
	if !errors.As(err, &sErr) || sErr.Kind != KindConflict {
		t.Fatalf("blocker conflicts must keep failing as conflicts, got %v", err)
	}
}

func TestCreateIssue_Backend_ForwardsIdempotency(t *testing.T) {
	now := time.Now().UTC()
	fb := &fakeIssueBackend{
		createResult: &backend.IssueData{ID: "i-9", Title: "t", Status: "open", CreatedAt: now, UpdatedAt: now},
	}
	svc := newServiceWithFake(fb)

	_, err := svc.CreateIssue(context.Background(), CreateIssueParams{
		Title:          "t",
		IssueType:      "task",
		IdempotencyKey: "key-xyz",
		Force:          true,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if len(fb.createParams) != 1 {
		t.Fatalf("expected one backend create, got %d", len(fb.createParams))
	}
	if fb.createParams[0].IdempotencyKey != "key-xyz" || !fb.createParams[0].Force {
		t.Errorf("idempotency not forwarded to backend.CreateParams: %+v", fb.createParams[0])
	}
}
