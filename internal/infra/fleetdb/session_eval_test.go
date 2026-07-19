package fleetdb

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestSessionEvalStoreFleetDBQueryAndConflictMapping(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	since := now.Add(-time.Hour)
	until := now.Add(time.Hour)
	httpClient := newWorkspaceHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/session-evals":
			var body domain.SessionEval
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode create: %v", err)
			}
			if body.EvalID == "eval-dup" {
				w.WriteHeader(http.StatusConflict)
				writeJSON(t, w, map[string]any{"error": map[string]any{"code": "already_exists", "message": "duplicate"}})
				return
			}
			writeJSON(t, w, body)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/session-evals":
			q := r.URL.Query()
			if q.Get("session_id") != "sess-1" || q.Get("task_id") != "T-1" || q.Get("agent_id") != "agent-1" || q.Get("judge_prompt_version") != "v1" || q.Get("limit") != "2" {
				t.Fatalf("query = %s", r.URL.RawQuery)
			}
			if q.Get("since") != since.Format(time.RFC3339) || q.Get("until") != until.Format(time.RFC3339) {
				t.Fatalf("time query = %s", r.URL.RawQuery)
			}
			writeJSON(t, w, map[string]any{"session_evals": []domain.SessionEval{{WorkspaceKey: "WS", EvalID: "eval-1", SessionID: "sess-1", AgentID: "agent-1"}}})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))

	client, err := New(Config{BaseURL: "http://fleet.test", Actor: "tester", HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.SessionEvals().Create(t.Context(), &domain.SessionEval{WorkspaceKey: "WS", EvalID: "eval-dup", SessionID: "sess-1", AgentID: "agent-1"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate Create err = %v, want ErrConflict", err)
	}
	got, err := client.SessionEvals().List(t.Context(), "WS", store.SessionEvalFilter{
		SessionID:          "sess-1",
		TaskID:             "T-1",
		AgentID:            "agent-1",
		JudgePromptVersion: "v1",
		Since:              &since,
		Until:              &until,
		Limit:              2,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].EvalID != "eval-1" {
		t.Fatalf("session evals = %+v", got)
	}
}
