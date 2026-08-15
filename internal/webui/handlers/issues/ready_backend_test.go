package issues

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

// stubReadyBackend implements backend.IssueBackend with only Ready
// populated. Every other method returns a sentinel error so accidental use
// surfaces as a test failure rather than silent empty payloads.
type stubReadyBackend struct {
	ready     []backend.IssueData
	err       error
	readyOpts []backend.ReadyOpts
}

func (s *stubReadyBackend) BackendName() string { return "stub-ready" }
func (s *stubReadyBackend) Ready(_ context.Context, opts backend.ReadyOpts) ([]backend.IssueData, error) {
	s.readyOpts = append(s.readyOpts, opts)
	if s.err != nil {
		return nil, s.err
	}
	return s.ready, nil
}
func (s *stubReadyBackend) Blocked(_ context.Context, _ backend.BlockedOpts) ([]backend.IssueData, error) {
	return nil, fmt.Errorf("Blocked not implemented in stubReadyBackend")
}
func (s *stubReadyBackend) List(_ context.Context, _ backend.ListOpts) ([]backend.IssueData, error) {
	return nil, fmt.Errorf("List not implemented in stubReadyBackend")
}
func (s *stubReadyBackend) Get(_ context.Context, _ string) (*backend.IssueDetailData, error) {
	return nil, fmt.Errorf("Get not implemented in stubReadyBackend")
}
func (s *stubReadyBackend) Stats(_ context.Context) (*backend.StatsData, error) {
	return nil, fmt.Errorf("Stats not implemented in stubReadyBackend")
}
func (s *stubReadyBackend) Count(_ context.Context, _ backend.CountOpts) (int, error) {
	return 0, fmt.Errorf("Count not implemented in stubReadyBackend")
}
func (s *stubReadyBackend) GetChildren(_ context.Context, _ string) ([]backend.IssueData, error) {
	return nil, fmt.Errorf("GetChildren not implemented in stubReadyBackend")
}
func (s *stubReadyBackend) SearchIssues(_ context.Context, _ string, _ int) ([]backend.IssueData, error) {
	return nil, fmt.Errorf("SearchIssues not implemented in stubReadyBackend")
}
func (s *stubReadyBackend) Create(_ context.Context, _ backend.CreateParams) (*backend.IssueData, error) {
	return nil, fmt.Errorf("Create not implemented in stubReadyBackend")
}
func (s *stubReadyBackend) Update(_ context.Context, _ string, _ backend.UpdateParams) error {
	return fmt.Errorf("Update not implemented in stubReadyBackend")
}
func (s *stubReadyBackend) ClaimIssue(_ context.Context, _ string, _ time.Duration) error {
	return fmt.Errorf("ClaimIssue not implemented in stubReadyBackend")
}
func (s *stubReadyBackend) ReleaseIssueLock(_ context.Context, _, _ string) error {
	return fmt.Errorf("ReleaseIssueLock not implemented in stubReadyBackend")
}
func (s *stubReadyBackend) DeferIssue(_ context.Context, _ string, _ time.Time) error {
	return fmt.Errorf("DeferIssue not implemented in stubReadyBackend")
}
func (s *stubReadyBackend) UndeferIssue(_ context.Context, _ string) error {
	return fmt.Errorf("UndeferIssue not implemented in stubReadyBackend")
}
func (s *stubReadyBackend) Close(_ context.Context, _ string, _ backend.CloseParams) (*backend.CloseResult, error) {
	return nil, fmt.Errorf("Close not implemented in stubReadyBackend")
}
func (s *stubReadyBackend) Reopen(_ context.Context, _ string, _ backend.ReopenParams) error {
	return fmt.Errorf("Reopen not implemented in stubReadyBackend")
}
func (s *stubReadyBackend) Delete(_ context.Context, _ backend.DeleteParams) error {
	return fmt.Errorf("Delete not implemented in stubReadyBackend")
}
func (s *stubReadyBackend) AddDependency(_ context.Context, _ backend.DepAddParams) error {
	return fmt.Errorf("AddDependency not implemented in stubReadyBackend")
}
func (s *stubReadyBackend) RemoveDependency(_ context.Context, _ backend.DepRemoveParams) error {
	return fmt.Errorf("RemoveDependency not implemented in stubReadyBackend")
}
func (s *stubReadyBackend) AddLabel(_ context.Context, _, _ string) error {
	return fmt.Errorf("AddLabel not implemented in stubReadyBackend")
}
func (s *stubReadyBackend) RemoveLabel(_ context.Context, _, _ string) error {
	return fmt.Errorf("RemoveLabel not implemented in stubReadyBackend")
}
func (s *stubReadyBackend) ListComments(_ context.Context, _ string) ([]backend.CommentData, error) {
	return nil, fmt.Errorf("ListComments not implemented in stubReadyBackend")
}
func (s *stubReadyBackend) AddComment(_ context.Context, _ backend.CommentAddParams) (*backend.CommentData, error) {
	return nil, fmt.Errorf("AddComment not implemented in stubReadyBackend")
}
func (s *stubReadyBackend) ListEvents(_ context.Context, _ string, _ int) ([]backend.EventData, error) {
	return nil, fmt.Errorf("ListEvents not implemented in stubReadyBackend")
}
func (s *stubReadyBackend) Batch(_ context.Context, _ []backend.BatchOp) ([]backend.BatchResult, error) {
	return nil, fmt.Errorf("Batch not implemented in stubReadyBackend")
}
func (s *stubReadyBackend) GetMutations(_ context.Context, _ int64) ([]backend.MutationData, error) {
	return nil, fmt.Errorf("GetMutations not implemented in stubReadyBackend")
}
func (s *stubReadyBackend) WaitForMutations(_ context.Context, _ int64, _ int64) ([]backend.MutationData, error) {
	return nil, fmt.Errorf("WaitForMutations not implemented in stubReadyBackend")
}

// errorDaemonPool implements daemon.Pool and always errors from Get.
type errorDaemonPool struct{ err error }

func (p *errorDaemonPool) Get(_ context.Context) (*rpc.Client, error) { return nil, p.err }
func (p *errorDaemonPool) Put(_ *rpc.Client)                          {}
func (p *errorDaemonPool) PutAfterError(_ *rpc.Client)                {}
func (p *errorDaemonPool) Discard(_ *rpc.Client)                      {}
func (p *errorDaemonPool) Stats() daemon.PoolStats                    { return daemon.PoolStats{} }
func (p *errorDaemonPool) Close() error                               { return nil }

func TestHandleReady_BackendWhenNoPool(t *testing.T) {
	be := &stubReadyBackend{
		ready: []backend.IssueData{
			{ID: "RDY-1", Title: "Ready One", Status: "open", Priority: 1, IssueType: "task", Parent: "EPIC-1", SourceRepo: "repoA", Design: "approved plan body"},
			{ID: "RDY-2", Title: "Ready Two", Status: "open", Priority: 2, IssueType: "bug"},
		},
	}
	handler := HandleReadyWithBackend(nil, func(_ context.Context) backend.IssueBackend { return be })

	req := httptest.NewRequest(http.MethodGet, "/api/ready", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var resp ReadyResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success=true; error=%q", resp.Error)
	}
	if len(be.readyOpts) != 1 || !be.readyOpts[0].IncludeRecommended {
		t.Fatalf("Ready opts = %#v, want IncludeRecommended=true", be.readyOpts)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("data length = %d, want 2", len(resp.Data))
	}
	var found *ReadyIssueWithParent
	for _, it := range resp.Data {
		if it.ID == "RDY-1" {
			found = it
		}
	}
	if found == nil {
		t.Fatal("missing RDY-1")
	}
	if found.Parent == nil || *found.Parent != "EPIC-1" {
		t.Errorf("Parent = %v, want &EPIC-1", found.Parent)
	}
	if found.Repo == nil || *found.Repo != "repoA" {
		t.Errorf("Repo = %v, want &repoA", found.Repo)
	}
	// Design must survive the projection: agents reading the ready queue via
	// the API backend gate on has_design (ReadyToImplement). Dropping it here
	// starved implementation agents with perpetual NoWork.
	if found.Design != "approved plan body" {
		t.Errorf("Design = %q, want %q (must be carried for the has_design task filter)", found.Design, "approved plan body")
	}
}

func TestHandleReady_PoolErrorDoesNotUseBackend(t *testing.T) {
	dp := &errorDaemonPool{err: errors.New("pool unavailable")}
	be := &stubReadyBackend{
		ready: []backend.IssueData{
			{ID: "BE-1", Title: "Via backend", Status: "open", Priority: 1},
		},
	}
	handler := HandleReadyWithBackend(dp, func(_ context.Context) backend.IssueBackend { return be })

	req := httptest.NewRequest(http.MethodGet, "/api/ready", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleReady_PoolPathClientErrorPreserved(t *testing.T) {
	// When backendFn is non-nil but a daemon pool is configured, the pool path
	// remains authoritative and client errors are surfaced directly.
	dp := &errorDaemonPool{err: errors.New("should not be invoked")}
	be := &stubReadyBackend{
		ready: []backend.IssueData{{ID: "SHOULD-NOT-APPEAR"}},
	}
	handler := HandleReadyWithBackend(dp, func(_ context.Context) backend.IssueBackend { return be })

	req := httptest.NewRequest(http.MethodGet, "/api/ready?priority=abc", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (client-error preserved); body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleReady_NoPoolNoBackendReturns503(t *testing.T) {
	handler := HandleReadyWithBackend(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/ready", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
}
