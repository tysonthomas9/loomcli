package api

import (
	"context"
	"net/http"
	"testing"
)

func TestListEventsRequiresCanonicalHistory(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		wantError  bool
	}{
		{"absent", `{"success":true}`, true},
		{"null", `{"success":true,"data":null}`, true},
		{"object", `{"success":true,"data":{}}`, true},
		{"string", `{"success":true,"data":"[]"}`, true},
		{"null row", `{"success":true,"data":[null]}`, true},
		{"empty row", `{"success":true,"data":[{}]}`, true},
		{"wrong issue", `{"success":true,"data":[{"id":"opaque","issue_id":"other","event_type":"issue.create","actor":"","created_at":"2026-09-05T12:00:00Z"}]}`, true},
		{"bad timestamp", `{"success":true,"data":[{"id":"opaque","issue_id":"loom-1","event_type":"issue.create","actor":"","created_at":"invalid"}]}`, true},
		{"empty", `{"success":true,"data":[]}`, false},
		{"canonical", `{"success":true,"data":[{"id":"opaque","issue_id":"loom-1","event_type":"issue.create","actor":"","created_at":"2026-09-05T12:00:00Z"}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ab, server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/workspaces/test-ws/issues/loom-1/events" || r.URL.Query().Get("limit") != "15" {
					t.Errorf("unexpected history request: %s", r.URL.String())
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			})
			defer server.Close()
			events, err := ab.ListEvents(context.Background(), "loom-1", 15)
			if (err != nil) != tc.wantError {
				t.Fatalf("ListEvents() = %#v, %v; want error %v", events, err, tc.wantError)
			}
			if tc.wantError && events != nil {
				t.Fatalf("failed history exposed records: %#v", events)
			}
			if !tc.wantError && events == nil {
				t.Fatal("valid history must return non-nil slice")
			}
		})
	}
}
