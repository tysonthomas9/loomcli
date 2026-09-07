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

// A fleet-db that predates BOTH newer create fields rejects them one at a time,
// because DisallowUnknownFields reports only the first unknown key it meets.
// The retry loop must therefore strip cumulatively: the second retry keeps
// external_ref out while also dropping acceptance_criteria. The per-field
// fallbacks this replaced each retried from the caller's ORIGINAL params, so
// attempt three re-sent external_ref and the create could never converge.
func TestCreateStripsSeveralUnsupportedFieldsThenPatchesAllBack(t *testing.T) {
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
			if _, present := body["external_ref"]; present {
				respondErr(w, http.StatusBadRequest, unsupportedCreateExternalRefMessage)
				return
			}
			if _, present := body["acceptance_criteria"]; present {
				respondErr(w, http.StatusBadRequest, unsupportedCreateAcceptanceCriteriaMessage)
				return
			}
			respondOK(w, types.Issue{ID: "issue-9", Title: "Both fields"})
		case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/issues/issue-9"):
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

	params := bothCompatFieldsCreateParams()
	params.IdempotencyKey = "original-key"
	issue, err := fb.Create(context.Background(), params)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if issue == nil || issue.ID != "issue-9" {
		t.Fatalf("issue = %#v, want issue-9", issue)
	}
	if len(postBodies) != 3 {
		t.Fatalf("POST bodies = %d, want 3 (original + two strips)", len(postBodies))
	}
	if _, present := postBodies[1]["external_ref"]; present {
		t.Errorf("second POST still carried external_ref: %v", postBodies[1])
	}
	if postBodies[1]["acceptance_criteria"] != params.AcceptanceCriteria {
		t.Errorf("second POST acceptance_criteria = %v, want %q",
			postBodies[1]["acceptance_criteria"], params.AcceptanceCriteria)
	}
	if _, present := postBodies[2]["external_ref"]; present {
		t.Errorf("third POST re-sent the already-stripped external_ref: %v", postBodies[2])
	}
	if _, present := postBodies[2]["acceptance_criteria"]; present {
		t.Errorf("third POST still carried acceptance_criteria: %v", postBodies[2])
	}
	// Every attempt must carry a key matching the bytes it actually sent.
	if postKeys[0] == postKeys[1] || postKeys[1] == postKeys[2] || postKeys[2] == "" {
		t.Errorf("POST idempotency keys = %q, %q, %q; want three distinct non-empty keys",
			postKeys[0], postKeys[1], postKeys[2])
	}
	// Both stripped values come back in ONE follow-up PATCH.
	if patchBody["external_ref"] != params.ExternalRef {
		t.Errorf("PATCH external_ref = %v, want %q", patchBody["external_ref"], params.ExternalRef)
	}
	if patchBody["acceptance_criteria"] != params.AcceptanceCriteria {
		t.Errorf("PATCH acceptance_criteria = %v, want %q",
			patchBody["acceptance_criteria"], params.AcceptanceCriteria)
	}
	if issue.ExternalRef != params.ExternalRef {
		t.Errorf("issue.ExternalRef = %q, want %q", issue.ExternalRef, params.ExternalRef)
	}
}

// When several fields were stripped, the failed PATCH names all of them — no
// single field's wording could describe what actually went unset.
func TestCreateMultiFieldPatchFailureNamesEveryStrippedField(t *testing.T) {
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/issues"):
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode create body: %v", err)
			}
			if _, present := body["external_ref"]; present {
				respondErr(w, http.StatusBadRequest, unsupportedCreateExternalRefMessage)
				return
			}
			if _, present := body["acceptance_criteria"]; present {
				respondErr(w, http.StatusBadRequest, unsupportedCreateAcceptanceCriteriaMessage)
				return
			}
			respondOK(w, types.Issue{ID: "issue-10", Title: "Both fields"})
		case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/issues/issue-10"):
			respondErr(w, http.StatusBadRequest, unsupportedCreateAcceptanceCriteriaMessage)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	defer ts.Close()

	issue, err := fb.Create(context.Background(), bothCompatFieldsCreateParams())
	if err == nil {
		t.Fatal("Create error = nil, want PATCH failure")
	}
	if issue == nil || issue.ID != "issue-10" {
		t.Fatalf("issue = %#v, want partially-created issue-10", issue)
	}
	if !backend.IsKind(err, backend.KindValidation) {
		t.Fatalf("error = %v, want KindValidation", err)
	}
	for _, want := range []string{"issue issue-10 was created", "external_ref", "acceptance_criteria"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}
}

// A rejection naming a compat field the caller never set cannot be fixed by
// stripping it, so it is passed straight through instead of being retried.
func TestCreateUnsupportedFieldNotSetDoesNotRetry(t *testing.T) {
	posts := 0
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		posts++
		respondErr(w, http.StatusBadRequest, unsupportedCreateExternalRefMessage)
	})
	defer ts.Close()

	params := bothCompatFieldsCreateParams()
	params.ExternalRef = ""
	params.AcceptanceCriteria = ""
	issue, err := fb.Create(context.Background(), params)
	if err == nil {
		t.Fatal("Create error = nil, want validation failure")
	}
	if issue != nil {
		t.Fatalf("issue = %#v, want nil", issue)
	}
	if posts != 1 {
		t.Fatalf("POST count = %d, want 1", posts)
	}
}

// The compat table is only consulted for a VALIDATION rejection. A non-validation
// error carrying the same message — the shape a proxy or a generic 5xx could
// produce — must not start a retry: the create may well have landed, and
// re-posting it is not safe. classifyErrorString promotes any `unknown field`
// body to KindValidation whatever the status code, so this guard is asserted on
// the matcher directly rather than through a fake server.
func TestRejectedCreateCompatFieldRequiresValidationKind(t *testing.T) {
	params := bothCompatFieldsCreateParams()

	validation := backend.NewBackendError(
		backend.KindValidation, "Create", unsupportedCreateExternalRefMessage, nil)
	field, ok := rejectedCreateCompatField(validation, params)
	if !ok || field.name != "external_ref" {
		t.Fatalf("validation rejection: field = %q, ok = %v; want external_ref, true", field.name, ok)
	}

	for _, kind := range []backend.ErrorKind{
		backend.KindInternal, backend.KindUnavailable, backend.KindConflict,
	} {
		err := backend.NewBackendError(kind, "Create", unsupportedCreateExternalRefMessage, nil)
		if _, ok := rejectedCreateCompatField(err, params); ok {
			t.Errorf("kind %v matched the compat table; want no retry", kind)
		}
	}
}

func bothCompatFieldsCreateParams() backend.CreateParams {
	return backend.CreateParams{
		Title:              "Both fields",
		IssueType:          "task",
		ExternalRef:        "https://github.com/acme/api/pull/7",
		AcceptanceCriteria: "AC-1",
	}
}
