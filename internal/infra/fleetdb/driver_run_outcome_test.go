package fleetdb

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestDriverRunOutcomeTransportClaimCompleteAndRetry(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	opaqueRunID := " run/1 " + strings.Repeat("x", 300)
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch {
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/v1/WS/driver-run-outcomes/claim":
			var request struct {
				ClaimID    string    `json:"claim_id"`
				Before     time.Time `json:"before"`
				ClaimUntil time.Time `json:"claim_until"`
				Limit      int       `json:"limit"`
			}
			decodeJSONBody(t, r, &request)
			if request.ClaimID != "publisher-1" || !request.Before.Equal(now) ||
				!request.ClaimUntil.Equal(now.Add(time.Minute)) || request.Limit != 7 {
				t.Fatalf("claim request = %+v", request)
			}
			writeJSON(t, w, map[string]any{"outcomes": []store.DriverRunOutcome{{
				WorkspaceKey: "WS", RunID: "run-1", Status: domain.DriverRunFailed,
				Summary: "failed", ErrorClass: "driver_runtime", ParentRunID: "parent-run",
				ParentEventID: "parent-event", EpicID: "WS-1", OccurredAt: now, Attempt: 2,
			}}})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/v1/WS/driver-run-outcomes/complete":
			var request struct {
				RunID       string    `json:"run_id"`
				ClaimID     string    `json:"claim_id"`
				CompletedAt time.Time `json:"completed_at"`
			}
			decodeJSONBody(t, r, &request)
			if request.RunID != opaqueRunID || request.ClaimID != "publisher-1" || !request.CompletedAt.Equal(now.Add(2*time.Second)) {
				t.Fatalf("complete request = %+v", request)
			}
			writeJSON(t, w, map[string]bool{"completed": true})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/v1/WS/driver-run-outcomes/retry":
			var request struct {
				RunID       string    `json:"run_id"`
				ClaimID     string    `json:"claim_id"`
				AvailableAt time.Time `json:"available_at"`
				Error       string    `json:"error"`
			}
			decodeJSONBody(t, r, &request)
			if request.RunID != opaqueRunID || request.ClaimID != "publisher-2" || !request.AvailableAt.Equal(now.Add(time.Minute)) || request.Error != "temporary" {
				t.Fatalf("retry request = %+v", request)
			}
			writeJSON(t, w, map[string]bool{"retried": true})
		default:
			http.Error(w, "unexpected route", http.StatusNotFound)
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.EscapedPath())
		}
	}))
	defer server.Close()

	client, err := New(Config{BaseURL: server.URL, Actor: "runtime-host"})
	if err != nil {
		t.Fatal(err)
	}
	outbox, ok := client.DriverRuns().(store.DriverRunOutcomeStore)
	if !ok {
		t.Fatal("Fleet DriverRun adapter does not expose durable outcome capability")
	}
	claimed, err := outbox.ClaimDriverRunOutcomes(t.Context(), store.DriverRunOutcomeClaim{
		WorkspaceKey: "WS", ClaimID: "publisher-1", Before: now, ClaimUntil: now.Add(time.Minute), Limit: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].RunID != "run-1" || claimed[0].Attempt != 2 ||
		claimed[0].ParentEventID != "parent-event" || claimed[0].Status != domain.DriverRunFailed {
		t.Fatalf("claimed outcomes = %+v", claimed)
	}
	if err := outbox.CompleteDriverRunOutcome(t.Context(), store.DriverRunOutcomeCompletion{
		WorkspaceKey: "WS", RunID: opaqueRunID, ClaimID: "publisher-1", CompletedAt: now.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if err := outbox.RetryDriverRunOutcome(t.Context(), store.DriverRunOutcomeRetry{
		WorkspaceKey: "WS", RunID: opaqueRunID, ClaimID: "publisher-2",
		AvailableAt: now.Add(time.Minute), Error: "temporary",
	}); err != nil {
		t.Fatal(err)
	}
	if requests != 3 {
		t.Fatalf("requests = %d, want 3", requests)
	}
}

func TestDriverRunOutcomeTransportNormalizesNilClaimList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"count": 0})
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	outbox := client.DriverRuns().(store.DriverRunOutcomeStore)
	now := time.Now().UTC()
	values, err := outbox.ClaimDriverRunOutcomes(t.Context(), store.DriverRunOutcomeClaim{
		WorkspaceKey: "WS", ClaimID: "publisher-1", Before: now, ClaimUntil: now.Add(time.Minute), Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if values == nil || len(values) != 0 {
		t.Fatalf("values = %#v, want non-nil empty", values)
	}
}
