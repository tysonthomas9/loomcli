package data

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"

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
	createItem  *backend.IssueData
	closeResult *backend.CloseResult
	closeErr    error
	updateErr   error
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
func (b *localBackendStub) Create(_ context.Context, params backend.CreateParams) (*backend.IssueData, error) {
	b.record("Create", "", params)
	if b.createItem != nil {
		return b.createItem, nil
	}
	return &backend.IssueData{
		ID:         params.ID,
		Title:      params.Title,
		Status:     params.Status,
		IssueType:  params.IssueType,
		Priority:   params.Priority,
		Parent:     params.Parent,
		Labels:     append([]string(nil), params.Labels...),
		SourceRepo: params.SourceRepo,
	}, nil
}
func (b *localBackendStub) Update(_ context.Context, id string, params backend.UpdateParams) error {
	b.record("Update", id, params)
	return b.updateErr
}

func (b *localBackendStub) ClaimIssue(_ context.Context, id string, lockTTL time.Duration) error {
	b.record("ClaimIssue", id, lockTTL)
	return nil
}

func (b *localBackendStub) ReleaseIssueLock(_ context.Context, id, actor string) error {
	b.record("ReleaseIssueLock", id, actor)
	return nil
}

func (b *localBackendStub) DeferIssue(context.Context, string, time.Time) error { return nil }
func (b *localBackendStub) UndeferIssue(context.Context, string) error          { return nil }

func (b *localBackendStub) Close(_ context.Context, id string, params backend.CloseParams) (*backend.CloseResult, error) {
	b.record("Close", id, params)
	if b.closeErr != nil {
		return nil, b.closeErr
	}
	if b.closeResult != nil {
		return b.closeResult, nil
	}
	return &backend.CloseResult{}, nil
}

func (b *localBackendStub) Archive(context.Context, string, backend.ArchiveParams) error {
	return nil
}

func (b *localBackendStub) Unarchive(context.Context, string) error {
	return nil
}

func (b *localBackendStub) Reopen(context.Context, string, backend.ReopenParams) error {
	return nil
}
func (b *localBackendStub) Delete(context.Context, backend.DeleteParams) error { return nil }
func (b *localBackendStub) AddDependency(_ context.Context, params backend.DepAddParams) error {
	b.record("AddDependency", params.FromID, params)
	return nil
}
func (b *localBackendStub) RemoveDependency(_ context.Context, params backend.DepRemoveParams) error {
	b.record("RemoveDependency", params.FromID, params)
	return nil
}
func (b *localBackendStub) AddLabel(context.Context, string, string) error    { return nil }
func (b *localBackendStub) RemoveLabel(context.Context, string, string) error { return nil }
func (b *localBackendStub) ListComments(context.Context, string) ([]backend.CommentData, error) {
	return nil, nil
}
func (b *localBackendStub) AddComment(_ context.Context, params backend.CommentAddParams) (*backend.CommentData, error) {
	b.record("AddComment", params.IssueID, params)
	return &backend.CommentData{IssueID: params.IssueID, Author: params.Author, Text: params.Text}, nil
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

func TestDataList_NoServerUsesLocalBackend(t *testing.T) {
	stub := &localBackendStub{
		readyItems: []backend.IssueData{{
			ID:        "loom-10",
			Title:     "filtered task",
			Status:    "review",
			IssueType: "task",
			Priority:  1,
		}},
	}
	withLocalBackend(t, stub, func() {
		outputFormat = "text"
		listStatus = "review"
		listType = "task"
		listParent = "epic-1"
		listLimit = 5
		listPriority = 1
		setTestFlagChanged(t, listCmd.Flags(), "priority", true)

		out, err := captureDataStdout(t, func() error {
			return listCmd.RunE(listCmd, nil)
		})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if !strings.Contains(out, "loom-10") || !strings.Contains(out, "filtered task") {
			t.Fatalf("list output = %q, want issue", out)
		}
		if len(stub.calls) != 1 || stub.calls[0].method != "List" {
			t.Fatalf("calls = %#v, want one List call", stub.calls)
		}
		opts := stub.calls[0].args.(backend.ListOpts)
		if opts.Status != "review" || opts.IssueType != "task" || opts.ParentID != "epic-1" || opts.Limit != 5 {
			t.Fatalf("List opts = %#v", opts)
		}
		if opts.Priority == nil || *opts.Priority != 1 {
			t.Fatalf("List priority = %#v, want 1", opts.Priority)
		}
	})
}

func TestDataBlocked_NoServerUsesLocalBackend(t *testing.T) {
	stub := &localBackendStub{
		readyItems: []backend.IssueData{{
			ID:        "loom-20",
			Title:     "blocked bug",
			Status:    "blocked",
			IssueType: "bug",
			Priority:  0,
		}},
	}
	withLocalBackend(t, stub, func() {
		outputFormat = "text"
		blockedLimit = 7
		blockedType = "bug"
		blockedParent = "epic-2"

		out, err := captureDataStdout(t, func() error {
			return blockedCmd.RunE(blockedCmd, nil)
		})
		if err != nil {
			t.Fatalf("blocked: %v", err)
		}
		if !strings.Contains(out, "loom-20") || !strings.Contains(out, "blocked bug") {
			t.Fatalf("blocked output = %q, want issue", out)
		}
		if len(stub.calls) != 1 || stub.calls[0].method != "Blocked" {
			t.Fatalf("calls = %#v, want one Blocked call", stub.calls)
		}
		opts := stub.calls[0].args.(backend.BlockedOpts)
		if opts.Limit != 7 || opts.Type != "bug" || opts.ParentID != "epic-2" {
			t.Fatalf("Blocked opts = %#v", opts)
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

func TestDataUpdate_AllChangedFields(t *testing.T) {
	stub := &localBackendStub{}
	withLocalBackend(t, stub, func() {
		outputFormat = "json"
		updateStatus = "review"
		updateAssignee = "agent-7"
		updateNotes = "ready for review"
		updateDesign = "new approach"
		updatePriority = 0
		updateTitle = "renamed title"
		updateDescription = "rewritten body"
		for _, name := range []string{"status", "assignee", "notes", "design", "priority", "title", "description"} {
			setTestFlagChanged(t, updateCmd.Flags(), name, true)
		}

		out, err := captureDataStdout(t, func() error {
			return updateCmd.RunE(updateCmd, []string{"loom-30"})
		})
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if !strings.Contains(out, `"message": "updated loom-30"`) {
			t.Fatalf("update output = %q, want JSON message", out)
		}
		if len(stub.calls) != 1 || stub.calls[0].method != "Update" || stub.calls[0].id != "loom-30" {
			t.Fatalf("calls = %#v, want one Update call", stub.calls)
		}
		params := stub.calls[0].args.(backend.UpdateParams)
		if params.Status == nil || *params.Status != "review" {
			t.Fatalf("Update status = %#v", params.Status)
		}
		if params.Assignee == nil || *params.Assignee != "agent-7" {
			t.Fatalf("Update assignee = %#v", params.Assignee)
		}
		if params.Notes == nil || *params.Notes != "ready for review" {
			t.Fatalf("Update notes = %#v", params.Notes)
		}
		if params.Design == nil || *params.Design != "new approach" {
			t.Fatalf("Update design = %#v", params.Design)
		}
		if params.Priority == nil || *params.Priority != 0 {
			t.Fatalf("Update priority = %#v", params.Priority)
		}
		if params.Title == nil || *params.Title != "renamed title" {
			t.Fatalf("Update title = %#v", params.Title)
		}
		if params.Description == nil || *params.Description != "rewritten body" {
			t.Fatalf("Update description = %#v", params.Description)
		}
	})
}

func TestDataComment_NoServerUsesLocalBackend(t *testing.T) {
	stub := &localBackendStub{}
	withLocalBackend(t, stub, func() {
		outputFormat = "text"
		commentAuthor = "planner"

		out, err := captureDataStdout(t, func() error {
			return commentCmd.RunE(commentCmd, []string{"loom-2", "ship it"})
		})
		if err != nil {
			t.Fatalf("comment: %v", err)
		}
		if !strings.Contains(out, "comment added to loom-2") {
			t.Fatalf("comment output = %q, want success message", out)
		}
		if len(stub.calls) != 1 || stub.calls[0].method != "AddComment" || stub.calls[0].id != "loom-2" {
			t.Fatalf("calls = %#v, want one AddComment call", stub.calls)
		}
		params := stub.calls[0].args.(backend.CommentAddParams)
		if params.IssueID != "loom-2" || params.Author != "planner" || params.Text != "ship it" {
			t.Fatalf("Comment params = %#v", params)
		}
	})
}

func setTestFlagChanged(t *testing.T, flags *pflag.FlagSet, name string, changed bool) {
	t.Helper()
	flag := flags.Lookup(name)
	if flag == nil {
		t.Fatalf("flag %q not found", name)
	}
	previous := flag.Changed
	flag.Changed = changed
	t.Cleanup(func() {
		flag.Changed = previous
	})
}
