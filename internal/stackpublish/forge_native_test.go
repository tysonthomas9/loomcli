package stackpublish

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestGitHubForgeNativeStackCapabilityCreateAndRetry(t *testing.T) {
	var stored []int
	creates := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-GitHub-Api-Version") != "2026-03-10" {
			t.Errorf("API version = %q", request.Header.Get("X-GitHub-Api-Version"))
		}
		switch {
		case request.Method == http.MethodGet && request.URL.Query().Get("per_page") == "1":
			_, _ = writer.Write([]byte(`[]`))
		case request.Method == http.MethodGet && request.URL.Query().Get("pull_request") != "":
			if stored == nil {
				_, _ = writer.Write([]byte(`[]`))
				return
			}
			_, _ = writer.Write([]byte(`[ {"number":7,"pull_requests":[{"number":11},{"number":12}]} ]`))
		case request.Method == http.MethodPost && request.URL.Path == "/repos/owner/repo/stacks":
			creates++
			var body struct {
				PullRequests []int `json:"pull_requests"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			stored = body.PullRequests
			writer.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.URL)
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	forge := NewGitHubForge("fixture", server.Client(), server.URL)
	enabled, err := forge.NativeStacksEnabled(context.Background(), "owner", "repo")
	if err != nil || !enabled {
		t.Fatalf("native capability = %t, %v", enabled, err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := forge.EnsureNativeStack(context.Background(), "owner", "repo", []int{11, 12}); err != nil {
			t.Fatal(err)
		}
	}
	if creates != 1 || !reflect.DeepEqual(stored, []int{11, 12}) {
		t.Fatalf("creates = %d, stack = %v", creates, stored)
	}
}

func TestGitHubForgeNativeStacksDisabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	forge := NewGitHubForge("fixture", server.Client(), server.URL)
	enabled, err := forge.NativeStacksEnabled(context.Background(), "owner", "repo")
	if err != nil || enabled {
		t.Fatalf("disabled capability = %t, %v", enabled, err)
	}
}

func TestGitHubForgeNativeAsyncMerge(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPut || request.URL.Path != "/repos/owner/repo/pulls/11/merge-async" {
			t.Errorf("request = %s %s", request.Method, request.URL)
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		if request.Header.Get("X-GitHub-Api-Version") != "2026-03-10" {
			t.Errorf("API version = %q", request.Header.Get("X-GitHub-Api-Version"))
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["merge_action"] != "default" || body["merge_method"] != nil || body["sha"] != "abc" || body["bypass_rules"] != false {
			t.Errorf("body = %+v", body)
		}
		writer.WriteHeader(http.StatusAccepted)
		_, _ = writer.Write([]byte(`{"status":"pending","details":{"uuid":"test-uuid","expected_head_sha":"abc","merge_action":"default"}}`))
	}))
	defer server.Close()
	result, err := NewGitHubForge("fixture", server.Client(), server.URL).MergeNativePull(context.Background(), "owner", "repo", 11, "abc")
	if err != nil || result.Status != "pending" || result.Details.UUID != "test-uuid" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestGitHubForgeNativeMergeQueueRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = writer.Write([]byte(`{"message":"Merge queue is required for this branch"}`))
	}))
	defer server.Close()
	_, err := NewGitHubForge("fixture", server.Client(), server.URL).MergeNativePull(context.Background(), "owner", "repo", 11, "abc")
	if !errors.Is(err, ErrMergeQueueRequired) {
		t.Fatalf("queue rejection = %v", err)
	}
}

func TestGitHubForgeNativeAsyncResultAndExistingRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-GitHub-Api-Version") != "2026-03-10" {
			t.Errorf("API version = %q", request.Header.Get("X-GitHub-Api-Version"))
		}
		if request.Method == http.MethodPut {
			writer.WriteHeader(http.StatusConflict)
			_, _ = writer.Write([]byte(`{"status":"pending","details":{"uuid":"existing","expected_head_sha":"abc","merge_action":"default"}}`))
			return
		}
		if request.Method != http.MethodGet || request.URL.Path != "/repos/owner/repo/pulls/11/merge-async/existing" {
			t.Errorf("request = %s %s", request.Method, request.URL)
		}
		_, _ = writer.Write([]byte(`{"status":"enqueued","details":{"message":"Added to queue"}}`))
	}))
	defer server.Close()
	forge := NewGitHubForge("fixture", server.Client(), server.URL)
	started, err := forge.MergeNativePull(context.Background(), "owner", "repo", 11, "abc")
	if err != nil || started.Details.UUID != "existing" {
		t.Fatalf("existing=%+v err=%v", started, err)
	}
	recovered, err := forge.RecoverNativePull(context.Background(), "owner", "repo", 11, "abc")
	if err != nil || recovered.Details.UUID != "existing" {
		t.Fatalf("recovered=%+v err=%v", recovered, err)
	}
	result, err := forge.NativeMergeStatus(context.Background(), "owner", "repo", 11, started.Details.UUID)
	if err != nil || result.Status != "enqueued" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestGitHubForgeNativeRecoveryRejectsNewRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusAccepted)
		_, _ = writer.Write([]byte(`{"status":"pending","details":{"uuid":"new","expected_head_sha":"abc","merge_action":"default"}}`))
	}))
	defer server.Close()
	_, err := NewGitHubForge("fixture", server.Client(), server.URL).RecoverNativePull(context.Background(), "owner", "repo", 11, "abc")
	if err == nil {
		t.Fatal("accepted a new request during recovery")
	}
}

func TestGitHubForgeNativeMergeRejectsAmbiguousResponse(t *testing.T) {
	for _, body := range []string{
		`{"status":"pending","details":{}}`,
		`{"status":"unknown","details":{"uuid":"test"}}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.WriteHeader(http.StatusAccepted)
			_, _ = writer.Write([]byte(body))
		}))
		_, err := NewGitHubForge("fixture", server.Client(), server.URL).MergeNativePull(context.Background(), "owner", "repo", 11, "abc")
		server.Close()
		if err == nil {
			t.Fatalf("accepted ambiguous response %s", body)
		}
	}
}
