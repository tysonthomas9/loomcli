package stackpublish

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGitHubForgeLoomMergeUsesQueueAwareActionAndSHA(t *testing.T) {
	merges := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPut || request.URL.Path != "/repos/owner/repo/pulls/7/merge-async" {
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["sha"] != "confirmed-head" || body["merge_method"] != "squash" || body["merge_action"] != "default" || body["bypass_rules"] != false {
			t.Errorf("unsafe merge request: %+v", body)
		}
		merges++
		writer.WriteHeader(http.StatusAccepted)
		_, _ = writer.Write([]byte(`{"status":"pending","details":{"uuid":"request-1","expected_head_sha":"confirmed-head"}}`))
	}))
	defer server.Close()
	forge := NewGitHubForge("fixture", server.Client(), server.URL)
	result, err := forge.MergeLoomPull(context.Background(), "owner", "repo", 7, "confirmed-head")
	if err != nil || merges != 1 || result.Details.UUID != "request-1" {
		t.Fatalf("merge calls = %d, result = %+v, err = %v", merges, result, err)
	}
}

func TestGitHubForgeLoomMergeStatusPollsRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/repos/owner/repo/pulls/7/merge-async/request-1" {
			t.Errorf("unexpected poll: %s %s", request.Method, request.URL.Path)
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = writer.Write([]byte(`{"status":"pending","details":{"uuid":"request-1","expected_head_sha":"confirmed-head"}}`))
	}))
	defer server.Close()
	forge := NewGitHubForge("fixture", server.Client(), server.URL)
	result, err := forge.LoomMergeStatus(context.Background(), "owner", "repo", 7, "request-1")
	if err != nil || result.Status != "pending" || result.Details.ExpectedHeadSHA != "confirmed-head" {
		t.Fatalf("poll = %+v, %v", result, err)
	}
}

func TestGitHubForgeLoomMergeAdoptsMatchingConflictOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusConflict)
		_, _ = writer.Write([]byte(`{"status":"pending","details":{"uuid":"request-1",` +
			`"expected_head_sha":"confirmed-head","merge_action":"default","bypass_rules":false}}`))
	}))
	defer server.Close()
	forge := NewGitHubForge("fixture", server.Client(), server.URL)
	result, err := forge.MergeLoomPull(context.Background(), "owner", "repo", 7, "confirmed-head")
	if err != nil || result.Details.UUID != "request-1" {
		t.Fatalf("matching conflict = %+v, %v", result, err)
	}
	if _, err := forge.MergeLoomPull(context.Background(), "owner", "repo", 7, "other-head"); err == nil {
		t.Fatal("adopted request for another head")
	}
}

func TestGitHubForgeLoomMergeRejectsUnshapedConflict(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests++
		writer.WriteHeader(http.StatusConflict)
		_, _ = writer.Write([]byte(`{"message":"merge already in progress"}`))
	}))
	defer server.Close()
	forge := NewGitHubForge("fixture", server.Client(), server.URL)
	_, err := forge.MergeLoomPull(context.Background(), "owner", "repo", 7, "confirmed-head")
	var rejected *LoomMergeRejectedError
	if !errors.As(err, &rejected) || requests != 1 {
		t.Fatalf("unshaped conflict = %v, requests = %d", err, requests)
	}
}

func TestGitHubForgeLoomMergeRejectsDefinitiveHTTPFailure(t *testing.T) {
	for _, status := range []int{http.StatusMethodNotAllowed, http.StatusUnprocessableEntity} {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(status)
			_, _ = writer.Write([]byte(`{"message":"not mergeable"}`))
		}))
		forge := NewGitHubForge("fixture", server.Client(), server.URL)
		_, err := forge.MergeLoomPull(context.Background(), "owner", "repo", 7, "confirmed-head")
		var rejected *LoomMergeRejectedError
		if !errors.As(err, &rejected) {
			t.Errorf("status %d should reject definitively: %v", status, err)
		}
		server.Close()
	}
}

func TestGitHubForgeLoomMergeTreatsServerErrorAsUnknown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	forge := NewGitHubForge("fixture", server.Client(), server.URL)
	_, err := forge.MergeLoomPull(context.Background(), "owner", "repo", 7, "confirmed-head")
	var rejected *LoomMergeRejectedError
	if err == nil || errors.As(err, &rejected) {
		t.Fatalf("5xx outcome should be unknown, got %v", err)
	}
}

func TestGitHubForgeNamesFailedLoomChecks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/repos/owner/repo/commits/head/check-runs":
			_, _ = writer.Write([]byte(`{"check_runs":[{"name":"build","conclusion":"failure"},{"name":"lint","conclusion":"success"}]}`))
		case "/repos/owner/repo/commits/head/status":
			_, _ = writer.Write([]byte(`{"statuses":[{"context":"test","state":"failure"}]}`))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	forge := NewGitHubForge("fixture", server.Client(), server.URL)
	names, err := forge.FailedLoomChecks(context.Background(), "owner", "repo", "head")
	if err != nil || len(names) != 2 || names[0] != "build" || names[1] != "test" {
		t.Fatalf("failed checks = %v, err = %v", names, err)
	}
}
