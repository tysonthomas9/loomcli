package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func TestBlockRepositoryRequired_UsesAtomicCommandAndReturnsCanonicalIssue(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/test-ws/issues/task-11/repository-requirement/block" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		respondOK(w, map[string]any{
			"issue": fleetIssueWithCountsWire{fleetIssueWire: fleetIssueWire{
				ID: "task-11", Title: "Needs repo", Status: "blocked", CreatedAt: now, UpdatedAt: now,
			}},
			"changed":        true,
			"replayed":       false,
			"dispatch_ready": false,
			"blocked":        true,
			"outcome":        "applied",
		})
	})
	defer ts.Close()

	result, err := fb.BlockRepositoryRequired(context.Background(), "task-11")
	if err != nil {
		t.Fatalf("BlockRepositoryRequired: %v", err)
	}
	if result == nil || result.Issue == nil || result.Issue.ID != "task-11" || result.Issue.Status != "blocked" || !result.Changed || result.Replayed || !result.Blocked || result.Outcome != "applied" {
		t.Fatalf("result = %+v", result)
	}
}

func TestBlockRepositoryRequired_ParsesCommitTimeDispatchReady(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/test-ws/issues/task-12/repository-requirement/block" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		respondOK(w, map[string]any{
			"issue": fleetIssueWithCountsWire{fleetIssueWire: fleetIssueWire{
				ID: "task-12", Title: "Repo assigned concurrently", Status: "open", Repo: "fleet-source",
				CreatedAt: now, UpdatedAt: now,
			}},
			"changed": false, "replayed": false, "dispatch_ready": true,
			"outcome": "not_required",
		})
	})
	defer ts.Close()

	result, err := fb.BlockRepositoryRequired(context.Background(), "task-12")
	if err != nil {
		t.Fatalf("BlockRepositoryRequired: %v", err)
	}
	if result == nil || !result.DispatchReady || result.Changed || result.Replayed ||
		result.Issue == nil || result.Issue.Status != "open" || result.Issue.SourceRepo != "fleet-source" {
		t.Fatalf("result = %+v", result)
	}
}

func TestSetIssueRepository_UsesAtomicCommandAndReturnsCanonicalIssue(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v1/test-ws/issues/task-11/repository" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			Repo string `json:"repo"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.Repo != "hello-world" {
			t.Fatalf("repo = %q", body.Repo)
		}
		respondOK(w, fleetIssueWithCountsWire{fleetIssueWire: fleetIssueWire{
			ID: "task-11", Title: "Recovered", Status: "open", Repo: "hello-world", CreatedAt: now, UpdatedAt: now,
		}})
	})
	defer ts.Close()

	issue, err := fb.SetIssueRepository(context.Background(), "task-11", " hello-world ")
	if err != nil {
		t.Fatalf("SetIssueRepository: %v", err)
	}
	if issue == nil || issue.ID != "task-11" || issue.Status != "open" || issue.SourceRepo != "hello-world" {
		t.Fatalf("issue = %+v", issue)
	}
}

func TestSetIssueRepositoryRejectsNonCanonicalWrappedIssue(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondOK(w, map[string]any{
			"issue": fleetIssueWithCountsWire{fleetIssueWire: fleetIssueWire{ID: "task-11", Status: "open"}},
		})
	})
	defer ts.Close()

	issue, err := fb.SetIssueRepository(context.Background(), "task-11", "hello-world")
	if err == nil {
		t.Fatal("SetIssueRepository error = nil, want canonical-shape rejection")
	}
	if issue != nil {
		t.Fatalf("issue = %+v, want nil", issue)
	}
	if !backend.IsKind(err, backend.KindInternal) {
		t.Fatalf("error = %v, want KindInternal", err)
	}
}

func TestRepositoryRequirementCommands_ValidateInput(t *testing.T) {
	fb, err := New(Config{BaseURL: "http://unused", WorkspaceID: "test-ws"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := fb.BlockRepositoryRequired(context.Background(), ""); !backend.IsKind(err, backend.KindValidation) {
		t.Fatalf("block error = %v", err)
	}
	for _, args := range [][2]string{{"", "hello-world"}, {"task-11", ""}} {
		if _, err := fb.SetIssueRepository(context.Background(), args[0], args[1]); !backend.IsKind(err, backend.KindValidation) {
			t.Fatalf("set args = %q, error = %v", args, err)
		}
	}
}
