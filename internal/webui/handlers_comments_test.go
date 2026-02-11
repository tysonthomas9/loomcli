package webui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
)

// Tests here supplement the comment handler tests in handlers_test.go,
// covering additional code paths not exercised there.

// TestHandleAddComment_RPCResponseFailure_Generic verifies 500 when
// resp.Success=false with a generic (non-"not found") error message.
func TestHandleAddComment_RPCResponseFailure_Generic(t *testing.T) {
	client := &mockCommentClient{
		addCommentFunc: func(args *rpc.CommentAddArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: false, Error: "something broke"}, nil
		},
	}
	pool := &mockCommentPool{
		getFunc: func(ctx context.Context) (commentAdder, error) { return client, nil },
		putFunc: func(c commentAdder) {},
	}
	handler := handleAddCommentWithPool(pool)

	req := httptest.NewRequest(http.MethodPost, "/api/issues/test-123/comments", strings.NewReader(`{"text":"hello"}`))
	req.SetPathValue("id", "test-123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp CommentResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error != "something broke" {
		t.Errorf("expected 'something broke', got: %s", resp.Error)
	}
}

// TestHandleAddComment_RPCResponseUnmarshalError verifies 500 when
// resp.Success=true but resp.Data contains invalid JSON.
func TestHandleAddComment_RPCResponseUnmarshalError(t *testing.T) {
	client := &mockCommentClient{
		addCommentFunc: func(args *rpc.CommentAddArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true, Data: json.RawMessage(`not json`)}, nil
		},
	}
	pool := &mockCommentPool{
		getFunc: func(ctx context.Context) (commentAdder, error) { return client, nil },
		putFunc: func(c commentAdder) {},
	}
	handler := handleAddCommentWithPool(pool)

	req := httptest.NewRequest(http.MethodPost, "/api/issues/test-123/comments", strings.NewReader(`{"text":"hello"}`))
	req.SetPathValue("id", "test-123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp CommentResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !strings.Contains(resp.Error, "failed to parse comment") {
		t.Errorf("expected error containing 'failed to parse comment', got: %s", resp.Error)
	}
}

// TestHandleAddComment_TextIsTrimmed verifies that leading/trailing whitespace
// is stripped from comment text before it reaches the RPC call.
func TestHandleAddComment_TextIsTrimmed(t *testing.T) {
	var capturedArgs *rpc.CommentAddArgs
	commentJSON, _ := json.Marshal(types.Comment{
		ID:      1,
		IssueID: "test-123",
		Author:  "web-ui",
		Text:    "hello world",
	})

	client := &mockCommentClient{
		addCommentFunc: func(args *rpc.CommentAddArgs) (*rpc.Response, error) {
			capturedArgs = args
			return &rpc.Response{Success: true, Data: commentJSON}, nil
		},
	}
	pool := &mockCommentPool{
		getFunc: func(ctx context.Context) (commentAdder, error) { return client, nil },
		putFunc: func(c commentAdder) {},
	}
	handler := handleAddCommentWithPool(pool)

	req := httptest.NewRequest(http.MethodPost, "/api/issues/test-123/comments", strings.NewReader(`{"text":"  hello world  "}`))
	req.SetPathValue("id", "test-123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	if capturedArgs == nil {
		t.Fatal("expected RPC args to be captured")
	}
	if capturedArgs.Text != "hello world" {
		t.Errorf("expected trimmed text 'hello world', got '%s'", capturedArgs.Text)
	}
}

// TestHandleAddComment_RPCError_NotFound verifies that an RPC error (err != nil)
// containing "not found" returns 404 instead of 500.
func TestHandleAddComment_RPCError_NotFound(t *testing.T) {
	client := &mockCommentClient{
		addCommentFunc: func(args *rpc.CommentAddArgs) (*rpc.Response, error) {
			return nil, errors.New("issue not found")
		},
	}
	pool := &mockCommentPool{
		getFunc: func(ctx context.Context) (commentAdder, error) { return client, nil },
		putFunc: func(c commentAdder) {},
	}
	handler := handleAddCommentWithPool(pool)

	req := httptest.NewRequest(http.MethodPost, "/api/issues/test-123/comments", strings.NewReader(`{"text":"hello"}`))
	req.SetPathValue("id", "test-123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp CommentResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error != "internal server error" {
		t.Errorf("expected 'internal server error', got: %s", resp.Error)
	}
}
