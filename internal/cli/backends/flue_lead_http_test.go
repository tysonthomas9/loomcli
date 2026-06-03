package backends

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeSSE writes an SSE frame for a single JSON event object.
func fakeSSE(w io.Writer, eventType, jsonData string) {
	_, _ = io.WriteString(w, "event: "+eventType+"\n")
	_, _ = io.WriteString(w, "data: "+jsonData+"\n\n")
}

func TestFlueLeadPrompt_RequestShapeAndStreaming(t *testing.T) {
	var gotMethod, gotPath, gotAccept, gotContentType string
	var gotBody map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAccept = r.Header.Get("Accept")
		gotContentType = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fakeSSE(w, "text_delta", `{"type":"text_delta","text":"Hello "}`)
		fakeSSE(w, "text_delta", `{"type":"text_delta","text":"there"}`)
		fakeSSE(w, "idle", `{"type":"idle"}`)
	}))
	defer srv.Close()

	var out strings.Builder
	if err := flueLeadPrompt(context.Background(), srv.URL, "ws-abc123", "hi", &out); err != nil {
		t.Fatalf("flueLeadPrompt: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/agents/lead/ws-abc123" {
		t.Errorf("path = %s, want /agents/lead/ws-abc123", gotPath)
	}
	if gotAccept != "text/event-stream" {
		t.Errorf("Accept = %q, want text/event-stream", gotAccept)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}
	if gotBody["message"] != "hi" || gotBody["session"] != "default" {
		t.Errorf("body = %v, want {message:hi, session:default}", gotBody)
	}
	if !strings.HasPrefix(out.String(), "Hello there") {
		t.Errorf("streamed output = %q, want prefix 'Hello there'", out.String())
	}
}

func TestFlueLeadPrompt_Non200ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"type":"boom","message":"server exploded"}}`)
	}))
	defer srv.Close()

	var out strings.Builder
	err := flueLeadPrompt(context.Background(), srv.URL, "ws-x", "hi", &out)
	if err == nil {
		t.Fatal("expected error on 500, got nil")
	}
	if !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "server exploded") {
		t.Fatalf("error = %v, want it to mention 500 and the body", err)
	}
}

func TestFlueLeadPrompt_ErrorEventStreamed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fakeSSE(w, "error", `{"type":"error","error":{"type":"rate_limit","message":"slow down"}}`)
	}))
	defer srv.Close()

	var out strings.Builder
	err := flueLeadPrompt(context.Background(), srv.URL, "ws-x", "hi", &out)
	if err == nil || !strings.Contains(err.Error(), "slow down") {
		t.Fatalf("expected streamed error 'slow down', got %v", err)
	}
}

func TestFlueLeadPrompt_OperationFallbackText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// No text_delta — only the final operation result, then idle.
		fakeSSE(w, "operation", `{"type":"operation","operationKind":"prompt","result":{"text":"final only"}}`)
		fakeSSE(w, "idle", `{"type":"idle"}`)
	}))
	defer srv.Close()

	var out strings.Builder
	if err := flueLeadPrompt(context.Background(), srv.URL, "ws-x", "hi", &out); err != nil {
		t.Fatalf("flueLeadPrompt: %v", err)
	}
	if !strings.Contains(out.String(), "final only") {
		t.Fatalf("output = %q, want fallback 'final only'", out.String())
	}
}
