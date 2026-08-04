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

func TestCreateExternalRefSupportedUsesSinglePost(t *testing.T) {
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
		if body["external_ref"] != "https://github.com/acme/api/pull/7" {
			t.Errorf("external_ref = %v, want PR URL", body["external_ref"])
		}
		respondOK(w, types.Issue{ID: "issue-1", Title: "Review PR"})
	})
	defer ts.Close()

	issue, err := fb.Create(context.Background(), externalRefCreateParams())
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

func TestCreateExternalRefUnsupportedRetriesThenPatches(t *testing.T) {
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
				respondErr(w, http.StatusInternalServerError, unsupportedCreateExternalRefMessage)
				return
			}
			respondOK(w, types.Issue{ID: "issue-2", Title: "Review PR"})
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

	params := externalRefCreateParams()
	params.IdempotencyKey = "original-key"
	issue, err := fb.Create(context.Background(), params)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if issue == nil || issue.ID != "issue-2" || issue.ExternalRef != params.ExternalRef {
		t.Fatalf("issue = %#v, want issue-2 with external_ref", issue)
	}
	if len(postBodies) != 2 {
		t.Fatalf("POST bodies = %d, want 2", len(postBodies))
	}
	if postBodies[0]["external_ref"] != params.ExternalRef {
		t.Errorf("first POST external_ref = %v, want %q", postBodies[0]["external_ref"], params.ExternalRef)
	}
	if _, present := postBodies[1]["external_ref"]; present {
		t.Errorf("retry POST unexpectedly included external_ref: %v", postBodies[1])
	}
	if postKeys[0] == postKeys[1] || postKeys[1] == "" {
		t.Errorf("POST idempotency keys = %q, %q; want distinct non-empty retry key", postKeys[0], postKeys[1])
	}
	if patchBody["external_ref"] != params.ExternalRef {
		t.Errorf("PATCH external_ref = %v, want %q", patchBody["external_ref"], params.ExternalRef)
	}
}

func TestCreateExternalRefPatchFailureReturnsCreatedIssue(t *testing.T) {
	posts := 0
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/issues"):
			posts++
			if posts == 1 {
				respondErr(w, http.StatusInternalServerError, unsupportedCreateExternalRefMessage)
				return
			}
			respondOK(w, types.Issue{ID: "issue-3", Title: "Review PR"})
		case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/issues/issue-3"):
			respondErr(w, http.StatusBadRequest, "external_ref is invalid")
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	defer ts.Close()

	issue, err := fb.Create(context.Background(), externalRefCreateParams())
	if err == nil {
		t.Fatal("Create error = nil, want PATCH failure")
	}
	if issue == nil || issue.ID != "issue-3" {
		t.Fatalf("issue = %#v, want partially-created issue-3", issue)
	}
	if !backend.IsKind(err, backend.KindValidation) {
		t.Fatalf("error = %v, want KindValidation", err)
	}
	if !strings.Contains(err.Error(), "issue issue-3 was created") {
		t.Fatalf("error = %q, want partial-create context", err)
	}
}

func TestCreateDifferentUnknownFieldDoesNotRetry(t *testing.T) {
	posts := 0
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		posts++
		respondErr(w, http.StatusInternalServerError, `unknown field "foo"`)
	})
	defer ts.Close()

	issue, err := fb.Create(context.Background(), externalRefCreateParams())
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

func externalRefCreateParams() backend.CreateParams {
	return backend.CreateParams{
		Title:       "Review PR",
		IssueType:   "task",
		ExternalRef: "https://github.com/acme/api/pull/7",
	}
}
