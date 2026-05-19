package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/types"
)

func TestUpdateStatusWorkflowBranches(t *testing.T) {
	tests := []struct {
		name      string
		current   types.Status
		assignee  string
		target    string
		newActor  *string
		cfgActor  string
		wantPath  string
		wantActor string
	}{
		{name: "same open clears assignee", current: types.StatusOpen, assignee: "nova", target: "open", wantPath: "/assign"},
		{name: "claim uses explicit assignee", current: types.StatusOpen, target: "in_progress", newActor: workflowStrPtr("ember"), wantPath: "/claim", wantActor: "ember"},
		{name: "claim uses configured actor", current: types.StatusOpen, target: "in_progress", cfgActor: "configured", wantPath: "/claim", wantActor: "configured"},
		{name: "closed reopens", current: types.StatusClosed, target: "open", wantPath: "/reopen"},
		{name: "deferred undeferred", current: types.StatusDeferred, target: "open", wantPath: "/undefer"},
		{name: "in progress releases actor", current: types.StatusInProgress, assignee: "nova", target: "open", wantPath: "/release", wantActor: "nova"},
		{name: "closed target closes", current: types.StatusOpen, target: "closed", wantPath: "/close"},
		{name: "blocked patches status", current: types.StatusOpen, target: "blocked", wantPath: "/issues/ISS-1"},
		{name: "review patches status", current: types.StatusOpen, target: "review", wantPath: "/issues/ISS-1"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var actionPath, actor string
			fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/deps"):
					respondOK(w, map[string]any{"dependencies": []any{}})
				case strings.HasSuffix(r.URL.Path, "/comments"):
					respondOK(w, map[string]any{"comments": []any{}})
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/issues/ISS-1"):
					respondOK(w, types.IssueDetails{Issue: types.Issue{
						ID:       "ISS-1",
						Title:    "issue",
						Status:   tc.current,
						Assignee: tc.assignee,
					}})
				default:
					actionPath = r.URL.Path
					actor = r.Header.Get("X-Actor")
					respondOK(w, map[string]any{})
				}
			})
			defer ts.Close()
			if tc.cfgActor != "" {
				fb.actor = tc.cfgActor
			}

			params := backend.UpdateParams{Status: &tc.target, Assignee: tc.newActor}
			if err := fb.Update(context.Background(), "ISS-1", params); err != nil {
				t.Fatalf("Update: %v", err)
			}
			if !strings.HasSuffix(actionPath, tc.wantPath) {
				t.Fatalf("action path = %q, want suffix %q", actionPath, tc.wantPath)
			}
			if tc.wantActor != "" && actor != tc.wantActor {
				t.Fatalf("actor header = %q, want %q", actor, tc.wantActor)
			}
		})
	}
}

func TestUpdateStatusValidationAndClaimActorBranches(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/deps"):
			respondOK(w, map[string]any{"dependencies": []any{}})
		case strings.HasSuffix(r.URL.Path, "/comments"):
			respondOK(w, map[string]any{"comments": []any{}})
		case r.Method == http.MethodGet:
			respondOK(w, types.IssueDetails{Issue: types.Issue{ID: "ISS-1", Title: "issue", Status: types.StatusOpen}})
		default:
			respondOK(w, map[string]any{})
		}
	})
	defer ts.Close()

	if err := fb.Update(context.Background(), "ISS-1", backend.UpdateParams{Claim: true}); err == nil {
		t.Fatal("Update with Claim succeeded")
	}
	if err := fb.Update(context.Background(), "", backend.UpdateParams{}); err == nil {
		t.Fatal("Update with empty id succeeded")
	}
	empty := ""
	if err := fb.Update(context.Background(), "ISS-1", backend.UpdateParams{Status: &empty}); err == nil {
		t.Fatal("Update with empty status succeeded")
	}
	unsupported := "triaged"
	if err := fb.Update(context.Background(), "ISS-1", backend.UpdateParams{Status: &unsupported}); err == nil {
		t.Fatal("Update with unsupported status succeeded")
	}
	inProgress := "in_progress"
	if err := fb.Update(context.Background(), "ISS-1", backend.UpdateParams{Status: &inProgress}); err == nil {
		t.Fatal("Update claim without actor succeeded")
	}
	if err := fb.Update(context.Background(), "ISS-1", backend.UpdateParams{}); err == nil {
		t.Fatal("Update with no supported fields succeeded")
	}

	fb.actor = "configured"
	if got := fb.claimActor(nil, nil); got != "configured" {
		t.Fatalf("claimActor configured = %q", got)
	}
}

func TestUpdateAppliesAddRemoveAndSetLabels(t *testing.T) {
	labels := []string{"old", "keep"}
	var operations []string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/deps"):
			respondOK(w, map[string]any{"dependencies": []any{}})
		case strings.HasSuffix(r.URL.Path, "/comments"):
			respondOK(w, map[string]any{"comments": []any{}})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/issues/ISS-1"):
			respondOK(w, types.IssueDetails{Issue: types.Issue{
				ID:     "ISS-1",
				Title:  "issue",
				Status: types.StatusOpen,
				Labels: append([]string(nil), labels...),
			}, Labels: append([]string(nil), labels...)})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/labels"):
			var body struct {
				Label string `json:"label"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			operations = append(operations, "add:"+body.Label)
			if !containsString(labels, body.Label) {
				labels = append(labels, body.Label)
			}
			respondOK(w, map[string]any{})
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/labels/"):
			label := ""
			if idx := strings.LastIndex(r.URL.Path, "/labels/"); idx >= 0 {
				label = r.URL.Path[idx+len("/labels/"):]
			}
			operations = append(operations, "remove:"+label)
			labels = removeLabel(labels, label)
			respondOK(w, map[string]any{})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})
	defer ts.Close()

	if err := fb.Update(context.Background(), "ISS-1", backend.UpdateParams{
		AddLabels:    []string{"new"},
		RemoveLabels: []string{"old"},
		SetLabels:    []string{"keep", "fresh"},
	}); err != nil {
		t.Fatalf("Update labels: %v", err)
	}
	wantOps := []string{"add:new", "remove:old", "remove:new", "add:fresh"}
	if strings.Join(operations, ",") != strings.Join(wantOps, ",") {
		t.Fatalf("operations = %#v, want %#v", operations, wantOps)
	}
	if strings.Join(labels, ",") != "keep,fresh" {
		t.Fatalf("labels = %#v", labels)
	}
}

func workflowStrPtr(s string) *string { return &s }

func removeLabel(labels []string, label string) []string {
	out := labels[:0]
	for _, existing := range labels {
		if existing != label {
			out = append(out, existing)
		}
	}
	return out
}
