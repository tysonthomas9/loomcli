package data

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

type localBackendCall struct {
	method string
	id     string
	args   interface{}
}

type localBackendStub struct {
	calls       []localBackendCall
	readyItems  []backend.IssueData
	detail      *backend.IssueDetailData
	closeResult *backend.CloseResult
}

func (b *localBackendStub) record(method, id string, args interface{}) {
	b.calls = append(b.calls, localBackendCall{method: method, id: id, args: args})
}

func (b *localBackendStub) Get(_ context.Context, id string) (*backend.IssueDetailData, error) {
	b.record("Get", id, nil)
	return b.detail, nil
}

func (b *localBackendStub) List(_ context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
	b.record("List", "", opts)
	return b.readyItems, nil
}

func (b *localBackendStub) Ready(_ context.Context, opts backend.ReadyOpts) ([]backend.IssueData, error) {
	b.record("Ready", "", opts)
	return b.readyItems, nil
}

func (b *localBackendStub) Blocked(_ context.Context, opts backend.BlockedOpts) ([]backend.IssueData, error) {
	b.record("Blocked", "", opts)
	return b.readyItems, nil
}

func (b *localBackendStub) Stats(context.Context) (*backend.StatsData, error) { return nil, nil }
func (b *localBackendStub) Count(context.Context, backend.CountOpts) (int, error) {
	return 0, nil
}
func (b *localBackendStub) GetChildren(context.Context, string) ([]backend.IssueData, error) {
	return nil, nil
}
func (b *localBackendStub) SearchIssues(context.Context, string, int) ([]backend.IssueData, error) {
	return nil, nil
}
func (b *localBackendStub) Create(context.Context, backend.CreateParams) (*backend.IssueData, error) {
	return nil, nil
}
func (b *localBackendStub) Update(_ context.Context, id string, params backend.UpdateParams) error {
	b.record("Update", id, params)
	return nil
}

func (b *localBackendStub) ClaimIssue(_ context.Context, id string, lockTTL time.Duration) error {
	b.record("ClaimIssue", id, lockTTL)
	return nil
}

func (b *localBackendStub) DeferIssue(context.Context, string, time.Time) error { return nil }
func (b *localBackendStub) UndeferIssue(context.Context, string) error          { return nil }

func (b *localBackendStub) Close(_ context.Context, id string, params backend.CloseParams) (*backend.CloseResult, error) {
	b.record("Close", id, params)
	if b.closeResult != nil {
		return b.closeResult, nil
	}
	return &backend.CloseResult{}, nil
}

func (b *localBackendStub) Reopen(context.Context, string, backend.ReopenParams) error {
	return nil
}
func (b *localBackendStub) Delete(context.Context, backend.DeleteParams) error { return nil }
func (b *localBackendStub) AddDependency(context.Context, backend.DepAddParams) error {
	return nil
}
func (b *localBackendStub) RemoveDependency(context.Context, backend.DepRemoveParams) error {
	return nil
}
func (b *localBackendStub) AddLabel(context.Context, string, string) error    { return nil }
func (b *localBackendStub) RemoveLabel(context.Context, string, string) error { return nil }
func (b *localBackendStub) ListComments(context.Context, string) ([]backend.CommentData, error) {
	return nil, nil
}
func (b *localBackendStub) AddComment(context.Context, backend.CommentAddParams) (*backend.CommentData, error) {
	return nil, nil
}
func (b *localBackendStub) ListEvents(context.Context, string, int) ([]backend.EventData, error) {
	return nil, nil
}
func (b *localBackendStub) Batch(context.Context, []backend.BatchOp) ([]backend.BatchResult, error) {
	return nil, nil
}
func (b *localBackendStub) GetMutations(context.Context, int64) ([]backend.MutationData, error) {
	return nil, nil
}
func (b *localBackendStub) WaitForMutations(context.Context, int64, int64) ([]backend.MutationData, error) {
	return nil, nil
}
func (b *localBackendStub) BackendName() string { return "local-stub" }

func withLocalBackend(t *testing.T, stub *localBackendStub, fn func()) {
	t.Helper()
	withDataClientState(t, func() {
		t.Setenv("LOOM_SERVER_URL", "")
		serverURL = ""
		SetLocalIssueBackendProvider(func(context.Context) backend.IssueBackend {
			return stub
		})
		t.Cleanup(func() {
			SetLocalIssueBackendProvider(nil)
		})
		fn()
	})
}

func captureDataStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	original := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	runErr := fn()
	_ = w.Close()
	os.Stdout = original
	out, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Fatalf("read stdout pipe: %v", readErr)
	}
	_ = r.Close()
	return string(out), runErr
}

func TestDataReady_NoServerUsesLocalBackend(t *testing.T) {
	stub := &localBackendStub{
		readyItems: []backend.IssueData{{
			ID:        "loom-1",
			Title:     "local task",
			Status:    "open",
			IssueType: "task",
			Priority:  1,
		}},
	}
	withLocalBackend(t, stub, func() {
		readyLimit = 3
		readyAssignee = "agent-1"
		readyType = "task"
		readyParent = "epic-1"
		outputFormat = "text"

		out, err := captureDataStdout(t, func() error {
			return readyCmd.RunE(readyCmd, nil)
		})
		if err != nil {
			t.Fatalf("ready: %v", err)
		}
		if !strings.Contains(out, "loom-1") || !strings.Contains(out, "local task") {
			t.Fatalf("ready output = %q, want issue", out)
		}
		if len(stub.calls) != 1 || stub.calls[0].method != "Ready" {
			t.Fatalf("calls = %#v, want one Ready call", stub.calls)
		}
		opts := stub.calls[0].args.(backend.ReadyOpts)
		if opts.Limit != 3 || opts.Assignee != "agent-1" || opts.Type != "task" || opts.ParentID != "epic-1" {
			t.Fatalf("Ready opts = %#v", opts)
		}
	})
}

func TestDataShowClaimClose_NoServerUsesLocalBackend(t *testing.T) {
	stub := &localBackendStub{
		detail: &backend.IssueDetailData{
			IssueData: backend.IssueData{
				ID:        "loom-1",
				Title:     "local task",
				Status:    "open",
				IssueType: "task",
				Priority:  1,
			},
		},
	}
	withLocalBackend(t, stub, func() {
		outputFormat = "text"
		closeReason = "done"
		closeSession = "session-1"
		closeForce = true

		out, err := captureDataStdout(t, func() error {
			return showCmd.RunE(showCmd, []string{"loom-1"})
		})
		if err != nil {
			t.Fatalf("show: %v", err)
		}
		if !strings.Contains(out, "ID:       loom-1") || !strings.Contains(out, "local task") {
			t.Fatalf("show output = %q, want issue detail", out)
		}
		if _, err := captureDataStdout(t, func() error {
			return claimCmd.RunE(claimCmd, []string{"loom-1"})
		}); err != nil {
			t.Fatalf("claim: %v", err)
		}
		if _, err := captureDataStdout(t, func() error {
			return closeCmd.RunE(closeCmd, []string{"loom-1"})
		}); err != nil {
			t.Fatalf("close: %v", err)
		}

		status := "blocked"
		notes := "waiting"
		updateStatus = status
		updateNotes = notes
		updateCmd.Flags().Set("status", status)
		updateCmd.Flags().Set("notes", notes)
		if _, err := captureDataStdout(t, func() error {
			return updateCmd.RunE(updateCmd, []string{"loom-1"})
		}); err != nil {
			t.Fatalf("update: %v", err)
		}

		wantMethods := []string{"Get", "ClaimIssue", "Close", "Update"}
		if len(stub.calls) != len(wantMethods) {
			t.Fatalf("calls = %#v, want %v", stub.calls, wantMethods)
		}
		for i, want := range wantMethods {
			if stub.calls[i].method != want || stub.calls[i].id != "loom-1" {
				t.Fatalf("call %d = %#v, want %s loom-1", i, stub.calls[i], want)
			}
		}
		params := stub.calls[2].args.(backend.CloseParams)
		if params.Reason != "done" || params.Session != "session-1" || !params.Force {
			t.Fatalf("Close params = %#v", params)
		}
		updateParams := stub.calls[3].args.(backend.UpdateParams)
		if updateParams.Status == nil || *updateParams.Status != status {
			t.Fatalf("Update status = %#v", updateParams.Status)
		}
		if updateParams.Notes == nil || *updateParams.Notes != notes {
			t.Fatalf("Update notes = %#v", updateParams.Notes)
		}
	})
}
