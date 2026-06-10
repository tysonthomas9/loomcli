package platformdb

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/workflows/platform"
)

func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := New(Config{BaseURL: srv.URL, Actor: "test-actor"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestDriverRunCreate_SendsAdmissionFields(t *testing.T) {
	t.Parallel()
	var got map[string]any
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v1/ws1/driver-runs" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if a := r.Header.Get("X-Actor"); a != "test-actor" {
			t.Errorf("X-Actor = %q", a)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(platform.DriverRun{RunID: "run-1", Status: platform.DriverRunQueued})
	}))

	run, err := c.DriverRuns().Create(context.Background(), "ws1", platform.DriverRunCreate{
		RunID: "run-1", DriverID: "epic-runner", DriverVersionID: "ver-1",
		EpicID: "EPIC-1", IdempotencyKey: "k1",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if run.RunID != "run-1" {
		t.Fatalf("run: %+v", run)
	}
	for _, k := range []string{"run_id", "driver_id", "driver_version_id", "epic_id", "idempotency_key"} {
		if _, ok := got[k]; !ok {
			t.Errorf("request body missing %s: %v", k, got)
		}
	}
}

func TestDriverRunFinish_FlattensOwnerAndResult(t *testing.T) {
	t.Parallel()
	var got map[string]any
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/ws1/driver-runs/run-1/finish" {
			t.Errorf("path: %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_ = json.NewEncoder(w).Encode(platform.DriverRun{RunID: "run-1", Status: platform.DriverRunCompleted})
	}))

	_, err := c.DriverRuns().Finish(context.Background(), "ws1", "run-1", "node-a", "lease-a", 7, platform.DriverRunFinish{
		Status: platform.DriverRunCompleted, Summary: "done", Output: map[string]string{"k": "v"},
	})
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if got["node_id"] != "node-a" || got["fencing_token"] != float64(7) || got["status"] != "completed" || got["summary"] != "done" {
		t.Fatalf("finish body not flattened: %v", got)
	}
}

func TestErrorClassification(t *testing.T) {
	t.Parallel()
	cases := []struct {
		status int
		body   string
		want   error
	}{
		{http.StatusNotFound, `{"error":{"code":"not_found","message":"nope"}}`, domain.ErrNotFound},
		{http.StatusConflict, `{"error":{"code":"already_exists","message":"dupe"}}`, domain.ErrAlreadyExists},
		{http.StatusConflict, `{"error":{"code":"invalid_transition","message":"bad"}}`, domain.ErrConflict},
		{http.StatusConflict, `{"error":{"code":"already_claimed","message":"taken"}}`, domain.ErrConflict},
		{http.StatusBadRequest, `{"error":{"code":"validation_failed","message":"bad"}}`, domain.ErrInvalid},
	}
	for _, tc := range cases {
		status, body := tc.status, tc.body
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
		_, err := c.TaskRuns().Create(context.Background(), "ws1", platform.TaskRunCreate{TaskRunID: "tr-1", TaskID: "T1"})
		if !errors.Is(err, tc.want) {
			t.Errorf("status %d body %s: got %v, want %v", tc.status, tc.body, err, tc.want)
		}
	}
}

func TestEventsPoll_QueryAndDecode(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("since") != "12-0" || q.Get("timeout") != "5000" || q.Get("limit") != "50" {
			t.Errorf("query: %v", q)
		}
		_ = json.NewEncoder(w).Encode(platform.MutationPage{
			Events: []platform.MutationEvent{{ID: "13-0", Action: "issue.close", EntityType: "issue", EntityID: "T1"}},
			Cursor: "13-0",
		})
	}))
	page, err := c.Events().Poll(context.Background(), "ws1", platform.MutationPoll{Since: "12-0", Timeout: 5 * time.Second, Limit: 50})
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(page.Events) != 1 || page.Cursor != "13-0" || page.Events[0].EntityType != "issue" {
		t.Fatalf("page: %+v", page)
	}
}

func TestLedgerCreateAndComplete(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/ws1/action-ledger":
			_ = json.NewEncoder(w).Encode(platform.LedgerEntry{ActionID: "a1", Status: platform.LedgerPending})
		case "/api/v1/ws1/action-ledger/a1/complete":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["status"] != "applied" {
				t.Errorf("complete body: %v", body)
			}
			_ = json.NewEncoder(w).Encode(platform.LedgerEntry{ActionID: "a1", Status: platform.LedgerApplied})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	e, err := c.ActionLedger().Create(context.Background(), "ws1", platform.LedgerCreate{IdempotencyKey: "k", ActionType: "update_status", TargetRef: "E1"})
	if err != nil || e.ActionID != "a1" {
		t.Fatalf("create: %v %+v", err, e)
	}
	e, err = c.ActionLedger().Complete(context.Background(), "ws1", "a1", platform.LedgerApplied)
	if err != nil || e.Status != platform.LedgerApplied {
		t.Fatalf("complete: %v %+v", err, e)
	}
}
