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

func TestCreateAcceptanceCriteriaSupportedUsesSinglePost(t *testing.T) {
	posts := 0
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/issues") {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		posts++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode create body: %v", err)
		}
		if body["acceptance_criteria"] != "AC-1" {
			t.Errorf("acceptance_criteria = %v, want AC-1", body["acceptance_criteria"])
		}
		respondOK(w, types.Issue{ID: "issue-1", Title: "With AC"})
	})
	defer ts.Close()

	issue, err := fb.Create(context.Background(), acceptanceCriteriaCreateParams())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if issue == nil || issue.ID != "issue-1" {
		t.Fatalf("issue = %#v, want issue-1", issue)
	}
	if posts != 1 {
		t.Fatalf("POST count = %d, want 1", posts)
	}
}

// An older fleet-db (one without PR #244) 400s the whole create body. The
// retry re-creates without the field, under a re-derived idempotency key, and
// then tries to apply it by PATCH.
func TestCreateAcceptanceCriteriaUnsupportedRetriesThenPatches(t *testing.T) {
	var postBodies []map[string]any
	var postKeys []string
	var patchBody map[string]any
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/issues"):
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode create body: %v", err)
			}
			postBodies = append(postBodies, body)
			postKeys = append(postKeys, r.Header.Get("X-Idempotency-Key"))
			if len(postBodies) == 1 {
				respondErr(w, http.StatusInternalServerError, unsupportedCreateAcceptanceCriteriaMessage)
				return
			}
			respondOK(w, types.Issue{ID: "issue-2", Title: "With AC"})
		case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/issues/issue-2"):
			if err := json.NewDecoder(r.Body).Decode(&patchBody); err != nil {
				t.Errorf("decode patch body: %v", err)
			}
			respondOK(w, map[string]any{})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	defer ts.Close()

	params := acceptanceCriteriaCreateParams()
	params.IdempotencyKey = "original-key"
	issue, err := fb.Create(context.Background(), params)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if issue == nil || issue.ID != "issue-2" {
		t.Fatalf("issue = %#v, want issue-2", issue)
	}
	if len(postBodies) != 2 {
		t.Fatalf("POST bodies = %d, want 2", len(postBodies))
	}
	if postBodies[0]["acceptance_criteria"] != params.AcceptanceCriteria {
		t.Errorf("first POST acceptance_criteria = %v, want %q",
			postBodies[0]["acceptance_criteria"], params.AcceptanceCriteria)
	}
	if _, present := postBodies[1]["acceptance_criteria"]; present {
		t.Errorf("retry POST unexpectedly included acceptance_criteria: %v", postBodies[1])
	}
	if postKeys[0] == postKeys[1] || postKeys[1] == "" {
		t.Errorf("POST idempotency keys = %q, %q; want distinct non-empty retry key", postKeys[0], postKeys[1])
	}
	if patchBody["acceptance_criteria"] != params.AcceptanceCriteria {
		t.Errorf("PATCH acceptance_criteria = %v, want %q",
			patchBody["acceptance_criteria"], params.AcceptanceCriteria)
	}
}

// A server that rejects the field on create rejects it on PATCH too — the
// expected path against a fleet-db without PR #244. The issue exists, so it is
// returned alongside a descriptive error rather than being lost.
func TestCreateAcceptanceCriteriaPatchFailureReturnsCreatedIssue(t *testing.T) {
	posts := 0
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/issues"):
			posts++
			if posts == 1 {
				respondErr(w, http.StatusInternalServerError, unsupportedCreateAcceptanceCriteriaMessage)
				return
			}
			respondOK(w, types.Issue{ID: "issue-3", Title: "With AC"})
		case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/issues/issue-3"):
			respondErr(w, http.StatusBadRequest, unsupportedCreateAcceptanceCriteriaMessage)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	defer ts.Close()

	issue, err := fb.Create(context.Background(), acceptanceCriteriaCreateParams())
	if err == nil {
		t.Fatal("Create error = nil, want PATCH failure")
	}
	if issue == nil || issue.ID != "issue-3" {
		t.Fatalf("issue = %#v, want partially-created issue-3", issue)
	}
	if !strings.Contains(err.Error(), "issue issue-3 was created") ||
		!strings.Contains(err.Error(), "does not accept acceptance_criteria") {
		t.Fatalf("error = %q, want partial-create context naming acceptance_criteria", err)
	}
}

func TestCreateWithoutAcceptanceCriteriaDoesNotRetry(t *testing.T) {
	posts := 0
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		posts++
		respondErr(w, http.StatusInternalServerError, unsupportedCreateAcceptanceCriteriaMessage)
	})
	defer ts.Close()

	params := acceptanceCriteriaCreateParams()
	params.AcceptanceCriteria = ""
	issue, err := fb.Create(context.Background(), params)
	if err == nil {
		t.Fatal("Create error = nil, want validation failure")
	}
	if issue != nil {
		t.Fatalf("issue = %#v, want nil", issue)
	}
	if !backend.IsKind(err, backend.KindValidation) {
		t.Fatalf("error = %v, want KindValidation", err)
	}
	if posts != 1 {
		t.Fatalf("POST count = %d, want 1", posts)
	}
}

func acceptanceCriteriaCreateParams() backend.CreateParams {
	return backend.CreateParams{
		Title:              "With AC",
		IssueType:          "task",
		AcceptanceCriteria: "AC-1",
	}
}
