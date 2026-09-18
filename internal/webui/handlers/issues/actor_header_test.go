package issues

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// X-Actor is forwarded verbatim to fleet-db, which stores it on the lock and
// on every event it emits. An unvalidated header therefore writes caller-
// controlled bytes into the audit trail and into the lock-owner comparison
// that arbitration depends on: a value with an embedded newline can split a
// log line, and an unbounded one is a cheap way to bloat every event row for
// an issue. Reject at the edge, where the value first enters the system.
func TestActorFromRequest_Validation(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		set     bool
		want    string
		wantErr bool
	}{
		{name: "absent header is the legacy path", set: false, want: ""},
		{name: "blank header is the legacy path", set: true, header: "   ", want: ""},
		{name: "trims surrounding space", set: true, header: "  worker-3  ", want: "worker-3"},
		{name: "ordinary identity", set: true, header: "worker-3", want: "worker-3"},
		{
			name:    "over-long value rejected",
			set:     true,
			header:  strings.Repeat("a", maxActorHeaderLen+1),
			wantErr: true,
		},
		{
			name:   "exactly at the cap is accepted",
			set:    true,
			header: strings.Repeat("a", maxActorHeaderLen),
			want:   strings.Repeat("a", maxActorHeaderLen),
		},
		{
			name:    "embedded newline rejected",
			set:     true,
			header:  "worker-3\nX-Injected: yes",
			wantErr: true,
		},
		{
			name:    "embedded NUL rejected",
			set:     true,
			header:  "worker-3\x00admin",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws/issues/T-1/claim", nil)
			if tt.set {
				// Set the header on the map directly: http.Header.Set would
				// not accept the control characters we are testing against.
				req.Header["X-Actor"] = []string{tt.header}
			}

			got, err := actorFromRequest(req)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("actorFromRequest() = %q, nil; want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("actorFromRequest() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("actorFromRequest() = %q, want %q", got, tt.want)
			}
		})
	}
}

// A rejected header must fail the request rather than fall through to the
// legacy "claim as serve itself" path — silently downgrading to serve's own
// actor is exactly the collapse this whole path exists to prevent.
func TestHandleClaimIssue_RejectsInvalidActorHeader(t *testing.T) {
	called := false
	svc := &mockIssueService{
		claimIssueFunc: func(context.Context, service.ClaimIssueParams) (json.RawMessage, error) {
			called = true
			return json.RawMessage(`{"id":"T-1"}`), nil
		},
	}
	h := handleClaimIssue(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws/issues/T-1/claim", nil)
	req.SetPathValue("id", "T-1")
	req.Header["X-Actor"] = []string{strings.Repeat("a", maxActorHeaderLen+1)}
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if called {
		t.Error("service was called with a rejected actor; the claim must not silently fall back to serve's own actor")
	}
}
