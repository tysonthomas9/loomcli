package cireport_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cireport"
)

func TestObserverPostsNeutralBillingCheckForExactWorkflowHead(t *testing.T) {
	const headSHA = "0123456789abcdef0123456789abcdef01234567"
	var posted map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("authorization header = %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/repos/acme/fleet/actions/runs/42":
			writeJSON(t, w, map[string]any{"status": "completed", "conclusion": "failure", "head_sha": headSHA, "check_suite_id": 77})
		case "/repos/acme/fleet/actions/runs/42/jobs":
			if r.URL.Query().Get("filter") != "latest" {
				t.Errorf("job attempt filter = %q", r.URL.Query().Get("filter"))
			}
			writeJSON(t, w, map[string]any{"total_count": 0, "jobs": []any{}})
		case "/repos/acme/fleet/check-suites/77/check-runs":
			writeJSON(t, w, map[string]any{"total_count": 1, "check_runs": []any{
				map[string]any{"name": "GitHub Actions", "output": map[string]any{"title": "Account payments failed", "summary": "Billing must be resolved before jobs start."}},
			}})
		case "/repos/acme/fleet/check-runs":
			if r.Method != http.MethodPost {
				t.Errorf("check-run method = %s", r.Method)
			}
			if err := json.NewDecoder(r.Body).Decode(&posted); err != nil {
				t.Fatal(err)
			}
			w.WriteHeader(http.StatusCreated)
			writeJSON(t, w, map[string]any{"id": 99})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result, err := cireport.ObserveAndReport(context.Background(), cireport.ObserverConfig{
		APIURL: server.URL, Token: "test-token", Repository: "acme/fleet", RunID: 42, HeadSHA: headSHA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Category != cireport.CategoryBilling || result.Conclusion != cireport.ConclusionNeutral {
		t.Fatalf("result = %#v", result)
	}
	if posted["head_sha"] != headSHA || posted["conclusion"] != "neutral" || posted["status"] != "completed" {
		t.Fatalf("posted check = %#v", posted)
	}
	if posted["conclusion"] == "success" {
		t.Fatal("external observer reported product success")
	}
}

func TestObserverRefusesMismatchedWorkflowHead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{
			"status": "completed", "conclusion": "success",
			"head_sha": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "check_suite_id": 77,
		})
	}))
	defer server.Close()

	_, err := cireport.ObserveAndReport(context.Background(), cireport.ObserverConfig{
		APIURL: server.URL, Token: "test-token", Repository: "acme/fleet", RunID: 42,
		HeadSHA: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	})
	if err == nil {
		t.Fatal("mismatched workflow head accepted")
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatal(err)
	}
}
