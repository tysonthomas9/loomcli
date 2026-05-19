package automode

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/events"
)

func TestHTTPEventEmitterBranches(t *testing.T) {
	var gotAuth string
	emitter := &HTTPEventEmitter{
		ControlPlaneURL: "http://control",
		WorkerID:        "worker-1",
		Token:           "secret",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			gotAuth = req.Header.Get("Authorization")
			if req.Method != http.MethodPost || req.URL.Path != "/api/internal/workers/worker-1/events" {
				t.Fatalf("request = %s %s", req.Method, req.URL.Path)
			}
			if ct := req.Header.Get("Content-Type"); ct != "application/json" {
				t.Fatalf("Content-Type = %q", ct)
			}
			return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader(""))}, nil
		})},
	}

	if err := emitter.Emit(events.Event{Type: events.TaskStarted, Agent: "worker-1"}); err != nil {
		t.Fatalf("Emit accepted response: %v", err)
	}
	if gotAuth != "Bearer secret" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if emitter.client() == nil {
		t.Fatal("client() returned nil")
	}
	if err := emitter.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	emitter.HTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	if err := emitter.Emit(events.Event{Type: events.TaskFailed}); err == nil || !strings.Contains(err.Error(), "control plane returned 500") {
		t.Fatalf("Emit 500 error = %v", err)
	}
}

func TestLocalEventEmitterWritesAndCloses(t *testing.T) {
	emitter := NewLocalEventEmitter(t.TempDir())
	if err := emitter.Emit(events.Event{Type: events.AgentStarted, Agent: "worker"}); err != nil {
		t.Fatalf("Emit local event: %v", err)
	}
	if err := emitter.Close(); err != nil {
		t.Fatalf("Close local emitter: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
