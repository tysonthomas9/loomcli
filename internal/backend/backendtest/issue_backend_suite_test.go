package backendtest

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func TestRunIssueBackendConformance(t *testing.T) {
	RunIssueBackendConformance(t, IssueBackendSuiteConfig{
		NewBackend: func(t testing.TB) backend.IssueBackend {
			t.Helper()
			return newMemoryIssueBackend()
		},
		SupportsExplicitCreateID: true,
	})
}

func TestRunIssueBackendConformanceRequiresBackend(t *testing.T) {
	if safeName("prefix/with spaces and symbols !@#") == "" {
		t.Fatal("safeName returned empty string")
	}
	if containsIssueID([]backend.IssueData{{ID: "A"}}, "B") {
		t.Fatal("containsIssueID matched missing ID")
	}
	if ids := issueIDs([]backend.IssueData{{ID: "A"}, {ID: "B"}}); len(ids) != 2 || ids[0] != "A" || ids[1] != "B" {
		t.Fatalf("issueIDs = %#v", ids)
	}
}

type memoryIssueBackend struct {
	mu     sync.Mutex
	nextID int
	issues map[string]backend.IssueData
}

func newMemoryIssueBackend() *memoryIssueBackend {
	return &memoryIssueBackend{issues: make(map[string]backend.IssueData)}
}

func (m *memoryIssueBackend) Get(context.Context, string) (*backend.IssueDetailData, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, issue := range m.issues {
		detail := backend.IssueDetailData{IssueData: issue}
		return &detail, nil
	}
	return nil, fmt.Errorf("not found")
}

func (m *memoryIssueBackend) List(_ context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []backend.IssueData
	for _, issue := range m.issues {
		if opts.Status != "" && issue.Status != opts.Status {
			continue
		}
		out = append(out, issue)
	}
	return out, nil
}

func (m *memoryIssueBackend) Ready(context.Context, backend.ReadyOpts) ([]backend.IssueData, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []backend.IssueData
	for _, issue := range m.issues {
		if issue.Status == "open" {
			out = append(out, issue)
		}
	}
	return out, nil
}

func (m *memoryIssueBackend) Blocked(context.Context, backend.BlockedOpts) ([]backend.IssueData, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []backend.IssueData
	for _, issue := range m.issues {
		if issue.Status == "blocked" {
			out = append(out, issue)
		}
	}
	return out, nil
}

func (m *memoryIssueBackend) Stats(context.Context) (*backend.StatsData, error) {
	return &backend.StatsData{}, nil
}

func (m *memoryIssueBackend) Count(context.Context, backend.CountOpts) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.issues), nil
}

func (m *memoryIssueBackend) GetChildren(context.Context, string) ([]backend.IssueData, error) {
	return nil, nil
}

func (m *memoryIssueBackend) SearchIssues(context.Context, string, int) ([]backend.IssueData, error) {
	return nil, nil
}

func (m *memoryIssueBackend) Create(_ context.Context, params backend.CreateParams) (*backend.IssueData, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := params.ID
	if id == "" {
		m.nextID++
		id = fmt.Sprintf("ISSUE-%d", m.nextID)
	}
	status := params.Status
	if status == "" {
		status = "open"
	}
	issue := backend.IssueData{
		ID:        id,
		Title:     params.Title,
		Status:    status,
		IssueType: params.IssueType,
		Priority:  params.Priority,
		Labels:    params.Labels,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	m.issues[id] = issue
	copy := issue
	return &copy, nil
}

func (m *memoryIssueBackend) Update(_ context.Context, id string, params backend.UpdateParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	issue, ok := m.issues[id]
	if !ok {
		return fmt.Errorf("not found")
	}
	if params.Title != nil {
		issue.Title = *params.Title
	}
	if params.Status != nil {
		issue.Status = *params.Status
	}
	issue.UpdatedAt = time.Now()
	m.issues[id] = issue
	return nil
}

func (m *memoryIssueBackend) ClaimIssue(context.Context, string, time.Duration) error { return nil }
func (m *memoryIssueBackend) DeferIssue(context.Context, string, time.Time) error     { return nil }
func (m *memoryIssueBackend) UndeferIssue(context.Context, string) error              { return nil }
func (m *memoryIssueBackend) ReleaseIssueLock(context.Context, string, string) error  { return nil }

func (m *memoryIssueBackend) Close(_ context.Context, id string, _ backend.CloseParams) (*backend.CloseResult, error) {
	status := "closed"
	if err := m.Update(context.Background(), id, backend.UpdateParams{Status: &status}); err != nil {
		return nil, err
	}
	detail, err := m.Get(context.Background(), id)
	if err != nil {
		return nil, err
	}
	return &backend.CloseResult{Closed: &detail.IssueData}, nil
}

func (m *memoryIssueBackend) Reopen(_ context.Context, id string, _ backend.ReopenParams) error {
	status := "open"
	return m.Update(context.Background(), id, backend.UpdateParams{Status: &status})
}

func (m *memoryIssueBackend) Delete(context.Context, backend.DeleteParams) error        { return nil }
func (m *memoryIssueBackend) AddDependency(context.Context, backend.DepAddParams) error { return nil }
func (m *memoryIssueBackend) RemoveDependency(context.Context, backend.DepRemoveParams) error {
	return nil
}
func (m *memoryIssueBackend) AddLabel(context.Context, string, string) error    { return nil }
func (m *memoryIssueBackend) RemoveLabel(context.Context, string, string) error { return nil }
func (m *memoryIssueBackend) ListComments(context.Context, string) ([]backend.CommentData, error) {
	return nil, nil
}
func (m *memoryIssueBackend) AddComment(context.Context, backend.CommentAddParams) (*backend.CommentData, error) {
	return &backend.CommentData{}, nil
}
func (m *memoryIssueBackend) ListEvents(context.Context, string, int) ([]backend.EventData, error) {
	return nil, nil
}
func (m *memoryIssueBackend) Batch(context.Context, []backend.BatchOp) ([]backend.BatchResult, error) {
	return nil, nil
}
func (m *memoryIssueBackend) GetMutations(context.Context, int64) ([]backend.MutationData, error) {
	return nil, nil
}
func (m *memoryIssueBackend) WaitForMutations(context.Context, int64, int64) ([]backend.MutationData, error) {
	return nil, nil
}
func (m *memoryIssueBackend) BackendName() string { return "memory" }
