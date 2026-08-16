package fleetdb

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

type workItemMoveRequesterStub struct{ err error }

func (stub *workItemMoveRequesterStub) Do(context.Context, string, string, any, any) error {
	return stub.err
}

func (stub *workItemMoveRequesterStub) DoWithHeaders(context.Context, string, string, any, any, map[string]string) error {
	return stub.err
}

func TestWorkItemMoveTransportUsesCanonicalAtomicRoute(t *testing.T) {
	revision := time.Date(2026, 8, 16, 12, 30, 0, 123000000, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/v1/SOURCE/issues/SOURCE-7/move" {
			t.Fatalf("request=%s %s", request.Method, request.URL.Path)
		}
		var input WorkItemMoveInput
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		if input.TargetWorkspace != "TARGET" || input.RequestID != "move-1" ||
			!input.ExpectedSourceRevision.Equal(revision) {
			t.Fatalf("input=%+v", input)
		}
		writeJSON(t, w, WorkItemMoveResult{
			Source: &WorkItemMoveIssue{ID: "SOURCE-7", Workspace: "SOURCE", MovedTo: &WorkItemReference{Workspace: "TARGET", IssueID: "TARGET-4"}},
			Target: &WorkItemMoveIssue{ID: "TARGET-4", Workspace: "TARGET", MovedFrom: &WorkItemReference{Workspace: "SOURCE", IssueID: "SOURCE-7"}},
		})
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, Actor: "loom-service"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.WorkItemMoves().MoveWorkItem(t.Context(), " SOURCE ", " SOURCE-7 ", WorkItemMoveInput{
		TargetWorkspace: " TARGET ", ExpectedSourceRevision: revision, RequestID: " move-1 ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Target.ID != "TARGET-4" || result.Source.MovedTo.IssueID != "TARGET-4" {
		t.Fatalf("result=%+v", result)
	}
}

func TestWorkItemMoveStableConflictCodesRemainTyped(t *testing.T) {
	for _, test := range []struct {
		code string
		want error
	}{
		{"work_item_move_revision_conflict", ErrWorkItemMoveRevisionConflict},
		{"work_item_move_idempotency_conflict", ErrWorkItemMoveIdempotencyConflict},
		{"work_item_move_ineligible", ErrWorkItemMoveIneligible},
	} {
		t.Run(test.code, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusConflict)
				writeJSON(t, w, map[string]any{"error": map[string]string{"code": test.code, "message": "rejected"}})
			}))
			defer server.Close()
			client, err := New(Config{BaseURL: server.URL, Actor: "loom-service"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.WorkItemMoves().MoveWorkItem(t.Context(), "SOURCE", "SOURCE-7", WorkItemMoveInput{
				TargetWorkspace: "TARGET", ExpectedSourceRevision: time.Now().UTC(), RequestID: "move-1",
			})
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v want=%v", err, test.want)
			}
		})
	}
}

func TestWorkItemMoveTargetAuthorizationFailureRemainsTyped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		writeJSON(t, w, map[string]any{"error": map[string]string{"code": "forbidden", "message": "denied"}})
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, Actor: "loom-service"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.WorkItemMoves().MoveWorkItem(t.Context(), "SOURCE", "SOURCE-7", WorkItemMoveInput{
		TargetWorkspace: "TARGET", ExpectedSourceRevision: time.Now().UTC(), RequestID: "move-1",
	})
	if !errors.Is(err, ErrWorkItemMoveForbidden) {
		t.Fatalf("error=%v want=%v", err, ErrWorkItemMoveForbidden)
	}
}

func TestWorkItemMoveRejectsInvalidIntentBeforeTransport(t *testing.T) {
	transport := newWorkItemMoveTransport(&workItemMoveRequesterStub{err: errors.New("should not run")})
	_, err := transport.MoveWorkItem(context.Background(), "SOURCE", "SOURCE-1", WorkItemMoveInput{})
	if !errors.Is(err, persistence.ErrInvalid) {
		t.Fatalf("error=%v", err)
	}
}
