package fleetdb

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func awaitWireRow(key, status string) map[string]any {
	return map[string]any{
		"workspace_key": "WS",
		"instance_key":  key,
		"run_id":        "run-1",
		"pattern":       "pr.approved:repo-1",
		"actor_allow":   []string{"alice"},
		"deadline":      time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC),
		"registered_at": time.Date(2026, 6, 12, 11, 0, 0, 0, time.UTC),
		"status":        status,
	}
}

func awaitTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

func TestAwaitRegisterAndCheckWire(t *testing.T) {
	deadline := time.Now().Add(time.Hour).UTC()
	client := awaitTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/WS/awaits/register-and-check" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var req struct {
			InstanceKey string    `json:"instance_key"`
			RunID       string    `json:"run_id"`
			Pattern     string    `json:"pattern"`
			ActorAllow  []string  `json:"actor_allow"`
			Deadline    time.Time `json:"deadline"`
		}
		decodeJSONBody(t, r, &req)
		if req.InstanceKey != "run-1#await-1" || req.RunID != "run-1" ||
			req.Pattern != "pr.approved:repo-1" || !req.Deadline.Equal(deadline) ||
			len(req.ActorAllow) != 1 || req.ActorAllow[0] != "alice" {
			t.Errorf("register body = %+v", req)
		}
		row := awaitWireRow("run-1#await-1", "satisfied")
		row["satisfied_by_event_id"] = "event-7"
		row["satisfied_payload"] = json.RawMessage(`{"ok":true}`)
		writeJSON(t, w, map[string]any{"await": row, "satisfied": true})
	})
	res, err := client.Awaits().RegisterAwaitAndCheck(t.Context(), "WS", store.AwaitRegistration{
		InstanceKey: "run-1#await-1",
		RunID:       "run-1",
		Pattern:     "pr.approved:repo-1",
		ActorAllow:  []string{"alice"},
		Deadline:    deadline,
	})
	if err != nil {
		t.Fatalf("RegisterAwaitAndCheck: %v", err)
	}
	if !res.Satisfied || res.Instance.Status != domain.AwaitSatisfied ||
		res.Instance.SatisfiedByEventID != "event-7" || string(res.Instance.SatisfiedPayload) != `{"ok":true}` {
		t.Fatalf("register result = %+v / %+v", res.Satisfied, res.Instance)
	}
}

// Client-side registration validation fails fast with the domain sentinels —
// no request reaches the server.
func TestAwaitRegisterValidatesClientSide(t *testing.T) {
	client := awaitTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s for invalid registration", r.Method, r.URL.Path)
	})
	cases := []struct {
		name    string
		in      store.AwaitRegistration
		wantErr error
	}{
		{"missing deadline", store.AwaitRegistration{InstanceKey: "run-1#await-1", RunID: "run-1", Pattern: "a:b"}, domain.ErrAwaitTimeoutRequired},
		{"unscoped pattern", store.AwaitRegistration{InstanceKey: "run-1#await-1", RunID: "run-1", Pattern: "a", Deadline: time.Now().Add(time.Hour)}, domain.ErrAwaitPatternUnscoped},
		{"malformed key", store.AwaitRegistration{InstanceKey: "run-1#await-0", RunID: "run-1", Pattern: "a:b", Deadline: time.Now().Add(time.Hour)}, domain.ErrAwaitInstanceKeyMalformed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.Awaits().RegisterAwaitAndCheck(t.Context(), "WS", tc.in)
			if !errors.Is(err, tc.wantErr) || !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("err = %v, want %v wrapping ErrInvalid", err, tc.wantErr)
			}
		})
	}
}

func TestAwaitResolveWire(t *testing.T) {
	client := awaitTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		// Instance keys contain '#': the path must be percent-escaped.
		if r.URL.EscapedPath() != "/api/v1/WS/awaits/run-1%23await-1/resolve" {
			t.Errorf("escaped path = %s", r.URL.EscapedPath())
		}
		var req struct {
			EventID string          `json:"event_id"`
			Status  string          `json:"status"`
			Actor   string          `json:"actor"`
			Payload json.RawMessage `json:"payload"`
		}
		decodeJSONBody(t, r, &req)
		if req.EventID != "event-9" || req.Status != "satisfied" || req.Actor != "alice" ||
			string(req.Payload) != `{"decision":"approved"}` {
			t.Errorf("resolve body = %+v", req)
		}
		row := awaitWireRow("run-1#await-1", "satisfied")
		row["satisfied_by_event_id"] = "event-9"
		writeJSON(t, w, map[string]any{"await": row, "resume": true})
	})
	res, err := client.Awaits().ResolveAwait(t.Context(), "WS", "run-1#await-1", "event-9",
		json.RawMessage(`{"decision":"approved"}`), "alice")
	if err != nil || !res.Resume || res.Instance.SatisfiedByEventID != "event-9" {
		t.Fatalf("resolve = %+v err=%v", res, err)
	}
}

// The synthetic-timeout-event convention: an await-timeout-* event ID is
// sent with status timed_out (resume-with-timeout-event decision).
func TestAwaitResolveTimeoutStatus(t *testing.T) {
	client := awaitTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Status string `json:"status"`
		}
		decodeJSONBody(t, r, &req)
		if req.Status != "timed_out" {
			t.Errorf("timeout resolve status = %q, want timed_out", req.Status)
		}
		writeJSON(t, w, map[string]any{"await": awaitWireRow("run-1#await-1", "timed_out"), "resume": true})
	})
	res, err := client.Awaits().ResolveAwait(t.Context(), "WS", "run-1#await-1",
		domain.AwaitTimeoutEventIDPrefix+"deadline-1", nil, "system")
	if err != nil || res.Instance.Status != domain.AwaitTimedOut {
		t.Fatalf("timeout resolve = %+v err=%v", res, err)
	}
}

func TestAwaitResolvePayloadCapClientSide(t *testing.T) {
	client := awaitTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("oversized payload reached the wire")
	})
	oversize := json.RawMessage(make([]byte, domain.DefaultAwaitResumePayloadCap+1))
	_, err := client.Awaits().ResolveAwait(t.Context(), "WS", "run-1#await-1", "event-1", oversize, "alice")
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("oversize resolve err = %v, want ErrInvalid", err)
	}
}

func TestAwaitListWires(t *testing.T) {
	client := awaitTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/WS/awaits" && r.URL.Query().Get("pattern") == "pr.approved:repo-1":
			writeJSON(t, w, map[string]any{
				"awaits": []any{awaitWireRow("run-1#await-1", "pending"), awaitWireRow("run-2#await-1", "pending")},
				"count":  2,
			})
		case r.URL.Path == "/api/v1/WS/awaits/due":
			if r.URL.Query().Get("limit") != "5" || r.URL.Query().Get("before") == "" {
				t.Errorf("due query = %s", r.URL.RawQuery)
			}
			writeJSON(t, w, map[string]any{"awaits": []any{awaitWireRow("run-1#await-1", "pending")}, "count": 1})
		default:
			t.Errorf("unexpected request %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	byPattern, err := client.Awaits().ListAwaitsByPattern(t.Context(), "WS", "pr.approved:repo-1")
	if err != nil || len(byPattern) != 2 || byPattern[0].InstanceKey != "run-1#await-1" {
		t.Fatalf("list by pattern = %+v err=%v", byPattern, err)
	}
	due, err := client.Awaits().ListDueAwaitDeadlines(t.Context(), "WS", time.Now(), 5)
	if err != nil || len(due) != 1 {
		t.Fatalf("list due = %+v err=%v", due, err)
	}
}

func TestAwaitGetSatisfiedWire(t *testing.T) {
	client := awaitTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/api/v1/WS/awaits/run-1%23await-1/satisfied" {
			t.Errorf("escaped path = %s", r.URL.EscapedPath())
		}
		row := awaitWireRow("run-1#await-1", "satisfied")
		row["satisfied_payload"] = json.RawMessage(`{"replay":1}`)
		writeJSON(t, w, row)
	})
	row, err := client.Awaits().GetSatisfiedAwait(t.Context(), "WS", "run-1#await-1")
	if err != nil || string(row.SatisfiedPayload) != `{"replay":1}` {
		t.Fatalf("satisfied replay = %+v err=%v", row, err)
	}
}

func TestDriverRunSuspendResumeWire(t *testing.T) {
	client := awaitTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/WS/driver-runs/run-1/suspend":
			var req struct {
				NodeID           string `json:"node_id"`
				LeaseID          string `json:"lease_id"`
				FencingToken     int64  `json:"fencing_token"`
				AwaitInstanceKey string `json:"await_instance_key"`
			}
			decodeJSONBody(t, r, &req)
			if req.NodeID != "node-1" || req.LeaseID != "lease-1" || req.FencingToken != 3 ||
				req.AwaitInstanceKey != "run-1#await-1" {
				t.Errorf("suspend body = %+v", req)
			}
			writeJSON(t, w, map[string]any{
				"workspace_key": "WS", "run_id": "run-1",
				"status": "suspended_awaiting_event", "suspended_at": time.Now().UTC(),
			})
		case "/api/v1/WS/driver-runs/run-1/resume":
			var req struct {
				AwaitInstanceKey string `json:"await_instance_key"`
				ResumeEventID    string `json:"resume_event_id"`
			}
			decodeJSONBody(t, r, &req)
			if req.AwaitInstanceKey != "run-1#await-1" || req.ResumeEventID != "event-9" {
				t.Errorf("resume body = %+v", req)
			}
			writeJSON(t, w, map[string]any{
				"workspace_key": "WS", "run_id": "run-1",
				"status": "queued", "resume_source_event_id": "event-9",
			})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})
	run, err := client.DriverRuns().Suspend(t.Context(), "WS", "run-1", "node-1", "lease-1", 3, "run-1#await-1")
	if err != nil || run.Status != domain.DriverRunSuspendedAwaitingEvent || run.SuspendedAt == nil {
		t.Fatalf("suspend = %+v err=%v", run, err)
	}
	resumed, err := client.DriverRuns().ResumeAwaiting(t.Context(), "WS", "run-1", "run-1#await-1", "event-9")
	if err != nil || resumed.Status != domain.DriverRunQueued || resumed.ResumeSourceEventID != "event-9" {
		t.Fatalf("resume = %+v err=%v", resumed, err)
	}
}

// Structured error classification: the await_* and driver_run_already_resumed
// codes map onto the matching domain sentinels.
func TestAwaitErrorClassification(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		code     string
		call     func(c *Client, ctx context.Context) error
		wantErr  error
		alsoWant error
	}{
		{
			name: "timeout required", status: http.StatusBadRequest, code: "await_timeout_required",
			call: func(c *Client, ctx context.Context) error {
				// Bypass client-side validation with a server-rejected (race) deadline.
				_, err := c.Awaits().RegisterAwaitAndCheck(ctx, "WS", store.AwaitRegistration{
					InstanceKey: "run-1#await-1", RunID: "run-1", Pattern: "a:b",
					Deadline: time.Now().Add(time.Hour),
				})
				return err
			},
			wantErr: domain.ErrAwaitTimeoutRequired, alsoWant: domain.ErrInvalid,
		},
		{
			name: "payload too large server-side", status: http.StatusBadRequest, code: "await_payload_too_large",
			call: func(c *Client, ctx context.Context) error {
				_, err := c.Awaits().ResolveAwait(ctx, "WS", "run-1#await-1", "event-1", nil, "alice")
				return err
			},
			wantErr: domain.ErrInvalid,
		},
		{
			name: "actor forbidden", status: http.StatusForbidden, code: "await_actor_forbidden",
			call: func(c *Client, ctx context.Context) error {
				_, err := c.Awaits().ResolveAwait(ctx, "WS", "run-1#await-1", "event-1", nil, "mallory")
				return err
			},
			wantErr: domain.ErrAwaitActorForbidden,
		},
		{
			name: "resolve not found", status: http.StatusNotFound, code: "not_found",
			call: func(c *Client, ctx context.Context) error {
				_, err := c.Awaits().ResolveAwait(ctx, "WS", "run-9#await-1", "event-1", nil, "alice")
				return err
			},
			wantErr: domain.ErrNotFound,
		},
		{
			name: "suspend already resumed (park->suspend window)", status: http.StatusConflict, code: "driver_run_already_resumed",
			call: func(c *Client, ctx context.Context) error {
				_, err := c.DriverRuns().Suspend(ctx, "WS", "run-1", "node-1", "lease-1", 3, "run-1#await-1")
				return err
			},
			wantErr: domain.ErrDriverRunAlreadyResumed,
		},
		{
			name: "suspend owner mismatch", status: http.StatusForbidden, code: "forbidden",
			call: func(c *Client, ctx context.Context) error {
				_, err := c.DriverRuns().Suspend(ctx, "WS", "run-1", "node-2", "lease-2", 4, "run-1#await-1")
				return err
			},
			wantErr: domain.ErrNotOwner,
		},
		{
			name: "resume racing loser", status: http.StatusConflict, code: "invalid_transition",
			call: func(c *Client, ctx context.Context) error {
				_, err := c.DriverRuns().ResumeAwaiting(ctx, "WS", "run-1", "run-1#await-1", "event-9")
				return err
			},
			wantErr: domain.ErrInvalidTransition,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := awaitTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]any{"code": tc.code, "message": tc.name},
				})
			})
			err := tc.call(client, t.Context())
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.alsoWant != nil && !errors.Is(err, tc.alsoWant) {
				t.Fatalf("err = %v, want it to also wrap %v", err, tc.alsoWant)
			}
		})
	}
}
