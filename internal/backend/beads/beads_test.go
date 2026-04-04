package beads

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
)

// ---------------------------------------------------------------------------
// mockClient implements beadsClient for testing
// ---------------------------------------------------------------------------

type mockClient struct {
	ReadyFn            func(*rpc.ReadyArgs) (*rpc.Response, error)
	ListFn             func(*rpc.ListArgs) (*rpc.Response, error)
	ShowFn             func(*rpc.ShowArgs) (*rpc.Response, error)
	BlockedFn          func(*rpc.BlockedArgs) (*rpc.Response, error)
	StatsFn            func() (*rpc.Response, error)
	CountFn            func(*rpc.CountArgs) (*rpc.Response, error)
	CreateFn           func(*rpc.CreateArgs) (*rpc.Response, error)
	UpdateFn           func(*rpc.UpdateArgs) (*rpc.Response, error)
	CloseIssueFn       func(*rpc.CloseArgs) (*rpc.Response, error)
	DeleteFn           func(*rpc.DeleteArgs) (*rpc.Response, error)
	AddDependencyFn    func(*rpc.DepAddArgs) (*rpc.Response, error)
	RemoveDependencyFn func(*rpc.DepRemoveArgs) (*rpc.Response, error)
	AddLabelFn         func(*rpc.LabelAddArgs) (*rpc.Response, error)
	RemoveLabelFn      func(*rpc.LabelRemoveArgs) (*rpc.Response, error)
	ListCommentsFn     func(*rpc.CommentListArgs) (*rpc.Response, error)
	AddCommentFn       func(*rpc.CommentAddArgs) (*rpc.Response, error)
	ListEventsFn       func(*rpc.EventListArgs) (*rpc.Response, error)
	BatchFn            func(*rpc.BatchArgs) (*rpc.Response, error)
	GetMutationsFn     func(*rpc.GetMutationsArgs) (*rpc.Response, error)
	WaitForMutationsFn func(*rpc.WaitForMutationsArgs) (*rpc.Response, error)
}

func (m *mockClient) Ready(args *rpc.ReadyArgs) (*rpc.Response, error) {
	if m.ReadyFn != nil {
		return m.ReadyFn(args)
	}
	return nil, errors.New("Ready not mocked")
}

func (m *mockClient) List(args *rpc.ListArgs) (*rpc.Response, error) {
	if m.ListFn != nil {
		return m.ListFn(args)
	}
	return nil, errors.New("List not mocked")
}

func (m *mockClient) Show(args *rpc.ShowArgs) (*rpc.Response, error) {
	if m.ShowFn != nil {
		return m.ShowFn(args)
	}
	return nil, errors.New("Show not mocked")
}

func (m *mockClient) Blocked(args *rpc.BlockedArgs) (*rpc.Response, error) {
	if m.BlockedFn != nil {
		return m.BlockedFn(args)
	}
	return nil, errors.New("Blocked not mocked")
}

func (m *mockClient) Stats() (*rpc.Response, error) {
	if m.StatsFn != nil {
		return m.StatsFn()
	}
	return nil, errors.New("Stats not mocked")
}

func (m *mockClient) Count(args *rpc.CountArgs) (*rpc.Response, error) {
	if m.CountFn != nil {
		return m.CountFn(args)
	}
	return nil, errors.New("Count not mocked")
}

func (m *mockClient) Create(args *rpc.CreateArgs) (*rpc.Response, error) {
	if m.CreateFn != nil {
		return m.CreateFn(args)
	}
	return nil, errors.New("Create not mocked")
}

func (m *mockClient) Update(args *rpc.UpdateArgs) (*rpc.Response, error) {
	if m.UpdateFn != nil {
		return m.UpdateFn(args)
	}
	return nil, errors.New("Update not mocked")
}

func (m *mockClient) CloseIssue(args *rpc.CloseArgs) (*rpc.Response, error) {
	if m.CloseIssueFn != nil {
		return m.CloseIssueFn(args)
	}
	return nil, errors.New("CloseIssue not mocked")
}

func (m *mockClient) Delete(args *rpc.DeleteArgs) (*rpc.Response, error) {
	if m.DeleteFn != nil {
		return m.DeleteFn(args)
	}
	return nil, errors.New("Delete not mocked")
}

func (m *mockClient) AddDependency(args *rpc.DepAddArgs) (*rpc.Response, error) {
	if m.AddDependencyFn != nil {
		return m.AddDependencyFn(args)
	}
	return nil, errors.New("AddDependency not mocked")
}

func (m *mockClient) RemoveDependency(args *rpc.DepRemoveArgs) (*rpc.Response, error) {
	if m.RemoveDependencyFn != nil {
		return m.RemoveDependencyFn(args)
	}
	return nil, errors.New("RemoveDependency not mocked")
}

func (m *mockClient) AddLabel(args *rpc.LabelAddArgs) (*rpc.Response, error) {
	if m.AddLabelFn != nil {
		return m.AddLabelFn(args)
	}
	return nil, errors.New("AddLabel not mocked")
}

func (m *mockClient) RemoveLabel(args *rpc.LabelRemoveArgs) (*rpc.Response, error) {
	if m.RemoveLabelFn != nil {
		return m.RemoveLabelFn(args)
	}
	return nil, errors.New("RemoveLabel not mocked")
}

func (m *mockClient) ListComments(args *rpc.CommentListArgs) (*rpc.Response, error) {
	if m.ListCommentsFn != nil {
		return m.ListCommentsFn(args)
	}
	return nil, errors.New("ListComments not mocked")
}

func (m *mockClient) AddComment(args *rpc.CommentAddArgs) (*rpc.Response, error) {
	if m.AddCommentFn != nil {
		return m.AddCommentFn(args)
	}
	return nil, errors.New("AddComment not mocked")
}

func (m *mockClient) ListEvents(args *rpc.EventListArgs) (*rpc.Response, error) {
	if m.ListEventsFn != nil {
		return m.ListEventsFn(args)
	}
	return nil, errors.New("ListEvents not mocked")
}

func (m *mockClient) Batch(args *rpc.BatchArgs) (*rpc.Response, error) {
	if m.BatchFn != nil {
		return m.BatchFn(args)
	}
	return nil, errors.New("Batch not mocked")
}

func (m *mockClient) GetMutations(args *rpc.GetMutationsArgs) (*rpc.Response, error) {
	if m.GetMutationsFn != nil {
		return m.GetMutationsFn(args)
	}
	return nil, errors.New("GetMutations not mocked")
}

func (m *mockClient) WaitForMutations(args *rpc.WaitForMutationsArgs) (*rpc.Response, error) {
	if m.WaitForMutationsFn != nil {
		return m.WaitForMutationsFn(args)
	}
	return nil, errors.New("WaitForMutations not mocked")
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func successResponse(t *testing.T, v any) *rpc.Response {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal mock data: %v", err)
	}
	return &rpc.Response{Success: true, Data: data}
}

func errorResponse(msg string) *rpc.Response {
	return &rpc.Response{Success: false, Error: msg}
}

func strPtr(s string) *string { return &s }
func intP(v int) *int         { return &v }

// ---------------------------------------------------------------------------
// New() tests
// ---------------------------------------------------------------------------

func TestNew_PanicsOnNil(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("New(nil) should panic")
		}
		msg, ok := r.(string)
		if !ok || msg != "beads.New: client must not be nil" {
			t.Errorf("panic message = %q, want %q", r, "beads.New: client must not be nil")
		}
	}()
	New(nil)
}

func TestNew_ValidClient(t *testing.T) {
	b := New(&mockClient{})
	if b == nil {
		t.Fatal("New returned nil for valid client")
	}
}

// ---------------------------------------------------------------------------
// BackendName
// ---------------------------------------------------------------------------

func TestBackendName(t *testing.T) {
	b := New(&mockClient{})
	if got := b.BackendName(); got != "beads" {
		t.Errorf("BackendName() = %q, want %q", got, "beads")
	}
}

// ---------------------------------------------------------------------------
// Get (happy path)
// ---------------------------------------------------------------------------

func TestGet_HappyPath(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	parent := "bd-parent"
	details := types.IssueDetails{
		Issue: types.Issue{
			ID:        "bd-42",
			Title:     "Test issue",
			Status:    types.StatusOpen,
			Priority:  2,
			IssueType: types.TypeTask,
			CreatedAt: now,
			UpdatedAt: now,
			CreatedBy: "alice",
		},
		Labels:       []string{"backend"},
		Parent:       &parent,
		Dependencies: nil,
		Dependents:   nil,
		Comments:     nil,
	}

	mc := &mockClient{
		ShowFn: func(args *rpc.ShowArgs) (*rpc.Response, error) {
			if args.ID != "bd-42" {
				t.Errorf("ShowArgs.ID = %q, want %q", args.ID, "bd-42")
			}
			return successResponse(t, details), nil
		},
	}

	b := New(mc)
	got, err := b.Get(context.Background(), "bd-42")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.ID != "bd-42" {
		t.Errorf("ID = %q, want %q", got.ID, "bd-42")
	}
	if got.CreatedBy != "alice" {
		t.Errorf("CreatedBy = %q, want %q", got.CreatedBy, "alice")
	}
	if got.IssueData.Parent != "bd-parent" {
		t.Errorf("Parent = %q, want %q", got.IssueData.Parent, "bd-parent")
	}
	if len(got.IssueData.Labels) != 1 || got.IssueData.Labels[0] != "backend" {
		t.Errorf("Labels = %v, want [backend]", got.IssueData.Labels)
	}
}

// ---------------------------------------------------------------------------
// List (happy path)
// ---------------------------------------------------------------------------

func TestList_HappyPath(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	issues := []*types.IssueWithCounts{
		{
			Issue: &types.Issue{
				ID:        "bd-1",
				Title:     "First",
				Status:    types.StatusOpen,
				CreatedAt: now,
				UpdatedAt: now,
			},
			DependencyCount: 2,
			DependentCount:  1,
		},
		{
			Issue: &types.Issue{
				ID:        "bd-2",
				Title:     "Second",
				Status:    types.StatusInProgress,
				CreatedAt: now,
				UpdatedAt: now,
			},
			DependencyCount: 0,
			DependentCount:  0,
		},
	}

	mc := &mockClient{
		ListFn: func(args *rpc.ListArgs) (*rpc.Response, error) {
			if args.Status != "open" {
				t.Errorf("ListArgs.Status = %q, want %q", args.Status, "open")
			}
			return successResponse(t, issues), nil
		},
	}

	b := New(mc)
	got, err := b.List(context.Background(), backend.ListOpts{Status: "open"})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List() returned %d items, want 2", len(got))
	}
	if got[0].ID != "bd-1" {
		t.Errorf("got[0].ID = %q, want %q", got[0].ID, "bd-1")
	}
	if got[0].DependencyCount != 2 {
		t.Errorf("got[0].DependencyCount = %d, want 2", got[0].DependencyCount)
	}
}

// ---------------------------------------------------------------------------
// Ready (happy path)
// ---------------------------------------------------------------------------

func TestReady_HappyPath(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	issues := []*types.Issue{
		{ID: "bd-r1", Title: "Ready 1", Status: types.StatusOpen, CreatedAt: now, UpdatedAt: now},
	}

	mc := &mockClient{
		ReadyFn: func(args *rpc.ReadyArgs) (*rpc.Response, error) {
			if args.Assignee != "alice" {
				t.Errorf("ReadyArgs.Assignee = %q, want %q", args.Assignee, "alice")
			}
			return successResponse(t, issues), nil
		},
	}

	b := New(mc)
	got, err := b.Ready(context.Background(), backend.ReadyOpts{Assignee: "alice"})
	if err != nil {
		t.Fatalf("Ready() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Ready() returned %d items, want 1", len(got))
	}
	if got[0].ID != "bd-r1" {
		t.Errorf("got[0].ID = %q, want %q", got[0].ID, "bd-r1")
	}
}

// ---------------------------------------------------------------------------
// Blocked (happy path)
// ---------------------------------------------------------------------------

func TestBlocked_HappyPath(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	issues := []*types.Issue{
		{ID: "bd-b1", Title: "Blocked 1", Status: types.StatusBlocked, CreatedAt: now, UpdatedAt: now},
	}

	mc := &mockClient{
		BlockedFn: func(args *rpc.BlockedArgs) (*rpc.Response, error) {
			if args.ParentID != "bd-epic" {
				t.Errorf("BlockedArgs.ParentID = %q, want %q", args.ParentID, "bd-epic")
			}
			return successResponse(t, issues), nil
		},
	}

	b := New(mc)
	got, err := b.Blocked(context.Background(), backend.BlockedOpts{ParentID: "bd-epic"})
	if err != nil {
		t.Fatalf("Blocked() error = %v", err)
	}
	if len(got) != 1 || got[0].ID != "bd-b1" {
		t.Errorf("Blocked() = %v, want [{ID: bd-b1}]", got)
	}
}

// ---------------------------------------------------------------------------
// Stats (happy path)
// ---------------------------------------------------------------------------

func TestStats_HappyPath(t *testing.T) {
	stats := types.Statistics{
		TotalIssues:  100,
		OpenIssues:   40,
		ClosedIssues: 50,
		ReadyIssues:  10,
	}

	mc := &mockClient{
		StatsFn: func() (*rpc.Response, error) {
			return successResponse(t, stats), nil
		},
	}

	b := New(mc)
	got, err := b.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats() error = %v", err)
	}
	if got.TotalIssues != 100 {
		t.Errorf("TotalIssues = %d, want 100", got.TotalIssues)
	}
	if got.OpenIssues != 40 {
		t.Errorf("OpenIssues = %d, want 40", got.OpenIssues)
	}
}

// ---------------------------------------------------------------------------
// Count (happy path)
// ---------------------------------------------------------------------------

func TestCount_HappyPath(t *testing.T) {
	mc := &mockClient{
		CountFn: func(args *rpc.CountArgs) (*rpc.Response, error) {
			if args.Status != "open" {
				t.Errorf("CountArgs.Status = %q, want %q", args.Status, "open")
			}
			return successResponse(t, struct {
				Count int `json:"count"`
			}{Count: 42}), nil
		},
	}

	b := New(mc)
	got, err := b.Count(context.Background(), backend.CountOpts{Status: "open"})
	if err != nil {
		t.Fatalf("Count() error = %v", err)
	}
	if got != 42 {
		t.Errorf("Count() = %d, want 42", got)
	}
}

// ---------------------------------------------------------------------------
// Create (happy path, verify args mapping)
// ---------------------------------------------------------------------------

func TestCreate_HappyPath(t *testing.T) {
	now := time.Now().Truncate(time.Second)

	mc := &mockClient{
		CreateFn: func(args *rpc.CreateArgs) (*rpc.Response, error) {
			// Verify args are mapped correctly from CreateParams
			if args.Title != "New issue" {
				t.Errorf("CreateArgs.Title = %q, want %q", args.Title, "New issue")
			}
			if args.Priority != 1 {
				t.Errorf("CreateArgs.Priority = %d, want 1", args.Priority)
			}
			if args.IssueType != "task" {
				t.Errorf("CreateArgs.IssueType = %q, want %q", args.IssueType, "task")
			}
			if args.Parent != "bd-parent" {
				t.Errorf("CreateArgs.Parent = %q, want %q", args.Parent, "bd-parent")
			}
			if args.Assignee != "alice" {
				t.Errorf("CreateArgs.Assignee = %q, want %q", args.Assignee, "alice")
			}
			if args.Owner != "bob" {
				t.Errorf("CreateArgs.Owner = %q, want %q", args.Owner, "bob")
			}
			if args.Description != "Desc" {
				t.Errorf("CreateArgs.Description = %q, want %q", args.Description, "Desc")
			}
			if len(args.Labels) != 1 || args.Labels[0] != "urgent" {
				t.Errorf("CreateArgs.Labels = %v, want [urgent]", args.Labels)
			}
			if len(args.Dependencies) != 1 || args.Dependencies[0] != "bd-dep" {
				t.Errorf("CreateArgs.Dependencies = %v, want [bd-dep]", args.Dependencies)
			}

			issue := types.Issue{
				ID:        "bd-new",
				Title:     args.Title,
				Status:    types.StatusOpen,
				Priority:  args.Priority,
				IssueType: types.IssueType(args.IssueType),
				Assignee:  args.Assignee,
				CreatedAt: now,
				UpdatedAt: now,
			}
			return successResponse(t, issue), nil
		},
	}

	b := New(mc)
	got, err := b.Create(context.Background(), backend.CreateParams{
		Title:        "New issue",
		Priority:     1,
		IssueType:    "task",
		Parent:       "bd-parent",
		Assignee:     "alice",
		Owner:        "bob",
		Description:  "Desc",
		Labels:       []string{"urgent"},
		Dependencies: []string{"bd-dep"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if got.ID != "bd-new" {
		t.Errorf("ID = %q, want %q", got.ID, "bd-new")
	}
}

// ---------------------------------------------------------------------------
// Update (happy path, verify args mapping)
// ---------------------------------------------------------------------------

func TestUpdate_HappyPath(t *testing.T) {
	mc := &mockClient{
		UpdateFn: func(args *rpc.UpdateArgs) (*rpc.Response, error) {
			if args.ID != "bd-99" {
				t.Errorf("UpdateArgs.ID = %q, want %q", args.ID, "bd-99")
			}
			if args.Title == nil || *args.Title != "Updated title" {
				t.Errorf("UpdateArgs.Title = %v, want %q", args.Title, "Updated title")
			}
			if args.Priority == nil || *args.Priority != 3 {
				t.Errorf("UpdateArgs.Priority = %v, want 3", args.Priority)
			}
			if args.Status == nil || *args.Status != "in_progress" {
				t.Errorf("UpdateArgs.Status = %v, want %q", args.Status, "in_progress")
			}
			if !args.Claim {
				t.Error("UpdateArgs.Claim should be true")
			}
			return &rpc.Response{Success: true, Data: []byte(`{}`)}, nil
		},
	}

	b := New(mc)
	err := b.Update(context.Background(), "bd-99", backend.UpdateParams{
		Title:    strPtr("Updated title"),
		Priority: intP(3),
		Status:   strPtr("in_progress"),
		Claim:    true,
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
}

// ---------------------------------------------------------------------------
// Close (happy path)
// ---------------------------------------------------------------------------

func TestClose_HappyPath(t *testing.T) {
	now := time.Now().Truncate(time.Second)

	mc := &mockClient{
		CloseIssueFn: func(args *rpc.CloseArgs) (*rpc.Response, error) {
			if args.ID != "bd-50" {
				t.Errorf("CloseArgs.ID = %q, want %q", args.ID, "bd-50")
			}
			if args.Reason != "completed" {
				t.Errorf("CloseArgs.Reason = %q, want %q", args.Reason, "completed")
			}
			if !args.SuggestNext {
				t.Error("CloseArgs.SuggestNext should be true (always set)")
			}
			if !args.Force {
				t.Error("CloseArgs.Force should be true")
			}
			cr := rpc.CloseResult{
				Closed: &types.Issue{
					ID: "bd-50", Title: "Closed", Status: types.StatusClosed,
					CreatedAt: now, UpdatedAt: now,
				},
				Unblocked: []*types.Issue{
					{ID: "bd-51", Title: "Unblocked", CreatedAt: now, UpdatedAt: now},
				},
			}
			return successResponse(t, cr), nil
		},
	}

	b := New(mc)
	got, err := b.Close(context.Background(), "bd-50", backend.CloseParams{
		Reason: "completed",
		Force:  true,
	})
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if got.Closed == nil || got.Closed.ID != "bd-50" {
		t.Errorf("Closed = %v, want {ID: bd-50}", got.Closed)
	}
	if len(got.Unblocked) != 1 || got.Unblocked[0].ID != "bd-51" {
		t.Errorf("Unblocked = %v, want [{ID: bd-51}]", got.Unblocked)
	}
}

// ---------------------------------------------------------------------------
// Delete (happy path)
// ---------------------------------------------------------------------------

func TestDelete_HappyPath(t *testing.T) {
	mc := &mockClient{
		DeleteFn: func(args *rpc.DeleteArgs) (*rpc.Response, error) {
			if len(args.IDs) != 2 || args.IDs[0] != "bd-a" || args.IDs[1] != "bd-b" {
				t.Errorf("DeleteArgs.IDs = %v, want [bd-a bd-b]", args.IDs)
			}
			if args.Reason != "cleanup" {
				t.Errorf("DeleteArgs.Reason = %q, want %q", args.Reason, "cleanup")
			}
			if !args.Force {
				t.Error("DeleteArgs.Force should be true")
			}
			if !args.Cascade {
				t.Error("DeleteArgs.Cascade should be true")
			}
			return &rpc.Response{Success: true, Data: []byte(`{}`)}, nil
		},
	}

	b := New(mc)
	err := b.Delete(context.Background(), backend.DeleteParams{
		IDs:     []string{"bd-a", "bd-b"},
		Reason:  "cleanup",
		Force:   true,
		Cascade: true,
	})
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
}

// ---------------------------------------------------------------------------
// Dependency operations
// ---------------------------------------------------------------------------

func TestAddDependency_HappyPath(t *testing.T) {
	mc := &mockClient{
		AddDependencyFn: func(args *rpc.DepAddArgs) (*rpc.Response, error) {
			if args.FromID != "bd-from" || args.ToID != "bd-to" || args.DepType != "blocks" {
				t.Errorf("DepAddArgs = {%q, %q, %q}, want {bd-from, bd-to, blocks}", args.FromID, args.ToID, args.DepType)
			}
			return &rpc.Response{Success: true, Data: []byte(`{}`)}, nil
		},
	}

	b := New(mc)
	err := b.AddDependency(context.Background(), backend.DepAddParams{
		FromID: "bd-from", ToID: "bd-to", DepType: "blocks",
	})
	if err != nil {
		t.Fatalf("AddDependency() error = %v", err)
	}
}

func TestRemoveDependency_HappyPath(t *testing.T) {
	mc := &mockClient{
		RemoveDependencyFn: func(args *rpc.DepRemoveArgs) (*rpc.Response, error) {
			if args.FromID != "bd-from" || args.ToID != "bd-to" {
				t.Errorf("DepRemoveArgs = {%q, %q}, want {bd-from, bd-to}", args.FromID, args.ToID)
			}
			return &rpc.Response{Success: true, Data: []byte(`{}`)}, nil
		},
	}

	b := New(mc)
	err := b.RemoveDependency(context.Background(), backend.DepRemoveParams{
		FromID: "bd-from", ToID: "bd-to",
	})
	if err != nil {
		t.Fatalf("RemoveDependency() error = %v", err)
	}
}

// ---------------------------------------------------------------------------
// Label operations
// ---------------------------------------------------------------------------

func TestAddLabel_HappyPath(t *testing.T) {
	mc := &mockClient{
		AddLabelFn: func(args *rpc.LabelAddArgs) (*rpc.Response, error) {
			if args.ID != "bd-10" || args.Label != "urgent" {
				t.Errorf("LabelAddArgs = {%q, %q}, want {bd-10, urgent}", args.ID, args.Label)
			}
			return &rpc.Response{Success: true, Data: []byte(`{}`)}, nil
		},
	}

	b := New(mc)
	err := b.AddLabel(context.Background(), "bd-10", "urgent")
	if err != nil {
		t.Fatalf("AddLabel() error = %v", err)
	}
}

func TestRemoveLabel_HappyPath(t *testing.T) {
	mc := &mockClient{
		RemoveLabelFn: func(args *rpc.LabelRemoveArgs) (*rpc.Response, error) {
			if args.ID != "bd-10" || args.Label != "stale" {
				t.Errorf("LabelRemoveArgs = {%q, %q}, want {bd-10, stale}", args.ID, args.Label)
			}
			return &rpc.Response{Success: true, Data: []byte(`{}`)}, nil
		},
	}

	b := New(mc)
	err := b.RemoveLabel(context.Background(), "bd-10", "stale")
	if err != nil {
		t.Fatalf("RemoveLabel() error = %v", err)
	}
}

// ---------------------------------------------------------------------------
// Comment operations
// ---------------------------------------------------------------------------

func TestListComments_HappyPath(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	comments := []*types.Comment{
		{ID: 1, IssueID: "bd-5", Author: "alice", Text: "Hello", CreatedAt: now},
		{ID: 2, IssueID: "bd-5", Author: "bob", Text: "World", CreatedAt: now},
	}

	mc := &mockClient{
		ListCommentsFn: func(args *rpc.CommentListArgs) (*rpc.Response, error) {
			if args.ID != "bd-5" {
				t.Errorf("CommentListArgs.ID = %q, want %q", args.ID, "bd-5")
			}
			return successResponse(t, comments), nil
		},
	}

	b := New(mc)
	got, err := b.ListComments(context.Background(), "bd-5")
	if err != nil {
		t.Fatalf("ListComments() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListComments() returned %d, want 2", len(got))
	}
	if got[0].Author != "alice" {
		t.Errorf("got[0].Author = %q, want %q", got[0].Author, "alice")
	}
}

func TestListComments_NilEntriesSkipped(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	comments := []*types.Comment{
		nil,
		{ID: 1, IssueID: "bd-5", Author: "alice", Text: "Hello", CreatedAt: now},
		nil,
	}

	mc := &mockClient{
		ListCommentsFn: func(args *rpc.CommentListArgs) (*rpc.Response, error) {
			return successResponse(t, comments), nil
		},
	}

	b := New(mc)
	got, err := b.ListComments(context.Background(), "bd-5")
	if err != nil {
		t.Fatalf("ListComments() error = %v", err)
	}
	if len(got) != 1 {
		t.Errorf("ListComments() returned %d, want 1 (nils skipped)", len(got))
	}
}

func TestAddComment_HappyPath(t *testing.T) {
	now := time.Now().Truncate(time.Second)

	mc := &mockClient{
		AddCommentFn: func(args *rpc.CommentAddArgs) (*rpc.Response, error) {
			if args.ID != "bd-5" || args.Author != "alice" || args.Text != "New comment" {
				t.Errorf("CommentAddArgs = {%q, %q, %q}", args.ID, args.Author, args.Text)
			}
			comment := types.Comment{
				ID: 99, IssueID: "bd-5", Author: "alice", Text: "New comment", CreatedAt: now,
			}
			return successResponse(t, comment), nil
		},
	}

	b := New(mc)
	got, err := b.AddComment(context.Background(), backend.CommentAddParams{
		IssueID: "bd-5", Author: "alice", Text: "New comment",
	})
	if err != nil {
		t.Fatalf("AddComment() error = %v", err)
	}
	if got.ID != 99 {
		t.Errorf("ID = %d, want 99", got.ID)
	}
}

// ---------------------------------------------------------------------------
// Event operations
// ---------------------------------------------------------------------------

func TestListEvents_HappyPath(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	events := []*types.Event{
		{ID: 1, IssueID: "bd-7", EventType: types.EventCreated, Actor: "bob", CreatedAt: now},
	}

	mc := &mockClient{
		ListEventsFn: func(args *rpc.EventListArgs) (*rpc.Response, error) {
			if args.ID != "bd-7" || args.Limit != 10 {
				t.Errorf("EventListArgs = {%q, %d}, want {bd-7, 10}", args.ID, args.Limit)
			}
			return successResponse(t, events), nil
		},
	}

	b := New(mc)
	got, err := b.ListEvents(context.Background(), "bd-7", 10)
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListEvents() returned %d, want 1", len(got))
	}
	if got[0].ID != "1" {
		t.Errorf("got[0].ID = %q, want %q", got[0].ID, "1")
	}
	if got[0].Kind != "issue.created" {
		t.Errorf("got[0].Kind = %q, want %q", got[0].Kind, "issue.created")
	}
}

func TestListEvents_NilEntriesSkipped(t *testing.T) {
	events := []*types.Event{nil, nil}

	mc := &mockClient{
		ListEventsFn: func(args *rpc.EventListArgs) (*rpc.Response, error) {
			return successResponse(t, events), nil
		},
	}

	b := New(mc)
	got, err := b.ListEvents(context.Background(), "bd-7", 10)
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ListEvents() returned %d, want 0 (nils skipped)", len(got))
	}
}

// ---------------------------------------------------------------------------
// Batch operations
// ---------------------------------------------------------------------------

func TestBatch_HappyPath(t *testing.T) {
	mc := &mockClient{
		BatchFn: func(args *rpc.BatchArgs) (*rpc.Response, error) {
			if len(args.Operations) != 2 {
				t.Errorf("BatchArgs.Operations len = %d, want 2", len(args.Operations))
			}
			if args.Operations[0].Operation != "create" {
				t.Errorf("Operations[0].Operation = %q, want %q", args.Operations[0].Operation, "create")
			}
			br := rpc.BatchResponse{
				Results: []rpc.BatchResult{
					{Success: true, Data: []byte(`{"id":"bd-new"}`)},
					{Success: false, Error: "conflict"},
				},
			}
			return successResponse(t, br), nil
		},
	}

	b := New(mc)
	ops := []backend.BatchOp{
		{Operation: "create", Args: json.RawMessage(`{"title":"New"}`)},
		{Operation: "update", Args: json.RawMessage(`{"id":"bd-99"}`)},
	}
	got, err := b.Batch(context.Background(), ops)
	if err != nil {
		t.Fatalf("Batch() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Batch() returned %d results, want 2", len(got))
	}
	if !got[0].Success {
		t.Error("got[0].Success should be true")
	}
	if got[1].Success {
		t.Error("got[1].Success should be false")
	}
	if got[1].Error != "conflict" {
		t.Errorf("got[1].Error = %q, want %q", got[1].Error, "conflict")
	}
}

// ---------------------------------------------------------------------------
// Mutation polling
// ---------------------------------------------------------------------------

func TestGetMutations_HappyPath(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	mutations := []rpc.MutationEvent{
		{Type: rpc.MutationCreate, IssueID: "bd-1", Timestamp: now},
		{Type: rpc.MutationStatus, IssueID: "bd-2", OldStatus: "open", NewStatus: "in_progress", Timestamp: now},
	}

	mc := &mockClient{
		GetMutationsFn: func(args *rpc.GetMutationsArgs) (*rpc.Response, error) {
			if args.Since != 1000 {
				t.Errorf("GetMutationsArgs.Since = %d, want 1000", args.Since)
			}
			return successResponse(t, mutations), nil
		},
	}

	b := New(mc)
	got, err := b.GetMutations(context.Background(), 1000)
	if err != nil {
		t.Fatalf("GetMutations() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("GetMutations() returned %d, want 2", len(got))
	}
	if got[0].Type != "create" {
		t.Errorf("got[0].Type = %q, want %q", got[0].Type, "create")
	}
	if got[1].OldStatus != "open" {
		t.Errorf("got[1].OldStatus = %q, want %q", got[1].OldStatus, "open")
	}
}

func TestWaitForMutations_HappyPath(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	mutations := []rpc.MutationEvent{
		{Type: rpc.MutationUpdate, IssueID: "bd-3", Timestamp: now},
	}

	mc := &mockClient{
		WaitForMutationsFn: func(args *rpc.WaitForMutationsArgs) (*rpc.Response, error) {
			if args.Since != 2000 || args.Timeout != 5000 {
				t.Errorf("WaitForMutationsArgs = {%d, %d}, want {2000, 5000}", args.Since, args.Timeout)
			}
			return successResponse(t, mutations), nil
		},
	}

	b := New(mc)
	got, err := b.WaitForMutations(context.Background(), 2000, 5000)
	if err != nil {
		t.Fatalf("WaitForMutations() error = %v", err)
	}
	if len(got) != 1 || got[0].IssueID != "bd-3" {
		t.Errorf("WaitForMutations() = %v, want [{IssueID: bd-3}]", got)
	}
}

// ---------------------------------------------------------------------------
// Error handling tests
// ---------------------------------------------------------------------------

func TestGet_TransportError(t *testing.T) {
	mc := &mockClient{
		ShowFn: func(args *rpc.ShowArgs) (*rpc.Response, error) {
			return nil, errors.New("connection refused")
		},
	}

	b := New(mc)
	_, err := b.Get(context.Background(), "bd-1")
	if err == nil {
		t.Fatal("Get() should return error for transport failure")
	}
	if !backend.IsKind(err, backend.KindUnavailable) {
		t.Errorf("error kind = %v, want KindUnavailable", err)
	}
}

func TestGet_NotFoundResponse(t *testing.T) {
	mc := &mockClient{
		ShowFn: func(args *rpc.ShowArgs) (*rpc.Response, error) {
			return errorResponse("issue bd-1 not found"), nil
		},
	}

	b := New(mc)
	_, err := b.Get(context.Background(), "bd-1")
	if err == nil {
		t.Fatal("Get() should return error for not found")
	}
	if !backend.IsKind(err, backend.KindNotFound) {
		t.Errorf("error kind = %v, want KindNotFound", err)
	}
}

func TestGet_NilResponse(t *testing.T) {
	mc := &mockClient{
		ShowFn: func(args *rpc.ShowArgs) (*rpc.Response, error) {
			return nil, nil
		},
	}

	b := New(mc)
	_, err := b.Get(context.Background(), "bd-1")
	if err == nil {
		t.Fatal("Get() should return error for nil response")
	}
	if !backend.IsKind(err, backend.KindUnavailable) {
		t.Errorf("error kind = %v, want KindUnavailable", err)
	}
}

func TestUpdate_ConflictResponse(t *testing.T) {
	mc := &mockClient{
		UpdateFn: func(args *rpc.UpdateArgs) (*rpc.Response, error) {
			return errorResponse("already claimed by agent-1"), nil
		},
	}

	b := New(mc)
	err := b.Update(context.Background(), "bd-99", backend.UpdateParams{Claim: true})
	if err == nil {
		t.Fatal("Update() should return error for conflict")
	}
	if !backend.IsKind(err, backend.KindConflict) {
		t.Errorf("error kind = %v, want KindConflict", err)
	}
}

func TestCreate_ValidationResponse(t *testing.T) {
	mc := &mockClient{
		CreateFn: func(args *rpc.CreateArgs) (*rpc.Response, error) {
			return errorResponse("validation error: title is required"), nil
		},
	}

	b := New(mc)
	_, err := b.Create(context.Background(), backend.CreateParams{})
	if err == nil {
		t.Fatal("Create() should return error for validation failure")
	}
	if !backend.IsKind(err, backend.KindValidation) {
		t.Errorf("error kind = %v, want KindValidation", err)
	}
}

func TestDelete_TransportError(t *testing.T) {
	mc := &mockClient{
		DeleteFn: func(args *rpc.DeleteArgs) (*rpc.Response, error) {
			return nil, errors.New("broken pipe")
		},
	}

	b := New(mc)
	err := b.Delete(context.Background(), backend.DeleteParams{IDs: []string{"bd-1"}})
	if err == nil {
		t.Fatal("Delete() should return error for transport failure")
	}
	if !backend.IsKind(err, backend.KindUnavailable) {
		t.Errorf("error kind = %v, want KindUnavailable", err)
	}
}

func TestList_CanceledContext(t *testing.T) {
	mc := &mockClient{
		ListFn: func(args *rpc.ListArgs) (*rpc.Response, error) {
			return nil, context.Canceled
		},
	}

	b := New(mc)
	_, err := b.List(context.Background(), backend.ListOpts{})
	if err == nil {
		t.Fatal("List() should return error for canceled context")
	}
	if !backend.IsKind(err, backend.KindCanceled) {
		t.Errorf("error kind = %v, want KindCanceled", err)
	}
}

func TestStats_TimeoutError(t *testing.T) {
	mc := &mockClient{
		StatsFn: func() (*rpc.Response, error) {
			return nil, context.DeadlineExceeded
		},
	}

	b := New(mc)
	_, err := b.Stats(context.Background())
	if err == nil {
		t.Fatal("Stats() should return error for timeout")
	}
	if !backend.IsKind(err, backend.KindTimeout) {
		t.Errorf("error kind = %v, want KindTimeout", err)
	}
}

func TestClose_InternalErrorResponse(t *testing.T) {
	mc := &mockClient{
		CloseIssueFn: func(args *rpc.CloseArgs) (*rpc.Response, error) {
			return errorResponse("unexpected database error"), nil
		},
	}

	b := New(mc)
	_, err := b.Close(context.Background(), "bd-1", backend.CloseParams{})
	if err == nil {
		t.Fatal("Close() should return error for internal error response")
	}
	if !backend.IsKind(err, backend.KindInternal) {
		t.Errorf("error kind = %v, want KindInternal", err)
	}
}

// ---------------------------------------------------------------------------
// Unmarshal error tests
// ---------------------------------------------------------------------------

func TestGet_UnmarshalError(t *testing.T) {
	mc := &mockClient{
		ShowFn: func(args *rpc.ShowArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true, Data: []byte(`{invalid json`)}, nil
		},
	}

	b := New(mc)
	_, err := b.Get(context.Background(), "bd-1")
	if err == nil {
		t.Fatal("Get() should return error for bad JSON")
	}
	if !backend.IsKind(err, backend.KindInternal) {
		t.Errorf("error kind = %v, want KindInternal", err)
	}
}

func TestList_UnmarshalError(t *testing.T) {
	mc := &mockClient{
		ListFn: func(args *rpc.ListArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true, Data: []byte(`not json`)}, nil
		},
	}

	b := New(mc)
	_, err := b.List(context.Background(), backend.ListOpts{})
	if err == nil {
		t.Fatal("List() should return error for bad JSON")
	}
	if !backend.IsKind(err, backend.KindInternal) {
		t.Errorf("error kind = %v, want KindInternal", err)
	}
}

func TestCount_UnmarshalError(t *testing.T) {
	mc := &mockClient{
		CountFn: func(args *rpc.CountArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true, Data: []byte(`broken`)}, nil
		},
	}

	b := New(mc)
	_, err := b.Count(context.Background(), backend.CountOpts{})
	if err == nil {
		t.Fatal("Count() should return error for bad JSON")
	}
	if !backend.IsKind(err, backend.KindInternal) {
		t.Errorf("error kind = %v, want KindInternal", err)
	}
}

// ---------------------------------------------------------------------------
// List opts mapping verification
// ---------------------------------------------------------------------------

func TestList_OptsMapping(t *testing.T) {
	pri := 2
	pinned := true

	mc := &mockClient{
		ListFn: func(args *rpc.ListArgs) (*rpc.Response, error) {
			if args.Query != "search term" {
				t.Errorf("Query = %q, want %q", args.Query, "search term")
			}
			if args.Status != "open" {
				t.Errorf("Status = %q, want %q", args.Status, "open")
			}
			if args.Priority == nil || *args.Priority != 2 {
				t.Errorf("Priority = %v, want 2", args.Priority)
			}
			if args.Limit != 50 {
				t.Errorf("Limit = %d, want 50", args.Limit)
			}
			if args.Pinned == nil || !*args.Pinned {
				t.Error("Pinned should be true")
			}
			if !args.AllowStale {
				t.Error("AllowStale should be true")
			}
			if len(args.Labels) != 1 || args.Labels[0] != "critical" {
				t.Errorf("Labels = %v, want [critical]", args.Labels)
			}
			if len(args.ExcludeStatus) != 1 || args.ExcludeStatus[0] != "closed" {
				t.Errorf("ExcludeStatus = %v, want [closed]", args.ExcludeStatus)
			}
			return successResponse(t, []*types.IssueWithCounts{}), nil
		},
	}

	b := New(mc)
	_, err := b.List(context.Background(), backend.ListOpts{
		Query:         "search term",
		Status:        "open",
		Priority:      &pri,
		Limit:         50,
		Pinned:        &pinned,
		AllowStale:    true,
		Labels:        []string{"critical"},
		ExcludeStatus: []string{"closed"},
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
}

// ---------------------------------------------------------------------------
// Interface compliance
// ---------------------------------------------------------------------------

// Compile-time check that BeadsBackend implements backend.IssueBackend.
var _ backend.IssueBackend = (*BeadsBackend)(nil)
