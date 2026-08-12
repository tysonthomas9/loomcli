package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

func TestCreateExternalRefUsesCanonicalSinglePost(t *testing.T) {
	posts := 0
	externalRef := "https://github.com/acme/api/pull/7"
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
		respondOK(w, testIssue{ID: "issue-1", Title: "Review PR", ExternalRef: &externalRef})
	})
	defer ts.Close()

	issue, err := fb.Create(context.Background(), externalRefCreateParams())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if issue == nil || issue.ID != "issue-1" || issue.ExternalRef != "https://github.com/acme/api/pull/7" {
		t.Fatalf("issue = %#v, want canonical external_ref", issue)
	}
	if posts != 1 {
		t.Fatalf("POST count = %d, want 1", posts)
	}
}

func TestCreateExternalRefFailureDoesNotRetry(t *testing.T) {
	posts := 0
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		posts++
		respondErr(w, http.StatusBadRequest, "external_ref is invalid")
	})
	defer ts.Close()

	issue, err := fb.Create(context.Background(), externalRefCreateParams())
	if err == nil {
		t.Fatal("Create error = nil, want validation failure")
	}
	if issue != nil {
		t.Fatalf("issue = %#v, want nil", issue)
	}
	if !workitems.IsKind(err, workitems.KindValidation) {
		t.Fatalf("error = %v, want KindValidation", err)
	}
	if posts != 1 {
		t.Fatalf("POST count = %d, want 1", posts)
	}
}

func externalRefCreateParams() workitems.CreateCommand {
	return workitems.CreateCommand{
		Title:       "Review PR",
		IssueType:   "task",
		ExternalRef: "https://github.com/acme/api/pull/7",
	}
}
