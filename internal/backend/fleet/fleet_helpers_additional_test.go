package fleet

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func TestFleetConstructorAndHelperBranches(t *testing.T) {
	if fb, err := New(Config{}); err == nil || fb != nil || !strings.Contains(err.Error(), "BaseURL") {
		t.Fatalf("New without base fb=%v err=%v", fb, err)
	}
	if fb, err := New(Config{BaseURL: "http://fleet"}); err == nil || fb != nil || !strings.Contains(err.Error(), "WorkspaceID") {
		t.Fatalf("New without workspace fb=%v err=%v", fb, err)
	}
	fb, err := New(Config{BaseURL: "http://fleet/", WorkspaceID: "WS/1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	fb.SetAuthToken("token-1")
	fb.SetAPIKey("key-1")
	if fb.BackendName() != "fleet" {
		t.Fatalf("BackendName = %q", fb.BackendName())
	}

	if hasData(nil) {
		t.Fatal("hasData(nil) = true")
	}
	if hasData(&apiResponse{Data: []byte("null")}) {
		t.Fatal("hasData(null) = true")
	}
	if !hasData(&apiResponse{Data: []byte(`[]`)}) {
		t.Fatal("hasData([]) = false")
	}

	wrapped, err := unmarshalListOrWrapper[fleetIssueWithCountsWire]([]byte(`{"issues":[{"id":"I-1","title":"one"}]}`), "List")
	if err != nil {
		t.Fatalf("unmarshal wrapper: %v", err)
	}
	if len(wrapped) != 1 || wrapped[0].ID != "I-1" {
		t.Fatalf("wrapped issues = %+v", wrapped)
	}
	if _, err := unmarshalListOrWrapper[fleetIssueWithCountsWire]([]byte(`{"issues":{`), "List"); err == nil {
		t.Fatal("malformed wrapper returned nil error")
	}
	if issues, err := unmarshalIssueList(&apiResponse{}, "List"); err != nil || len(issues) != 0 {
		t.Fatalf("empty issue list = %+v err=%v", issues, err)
	}
}

func TestFleetValidationBranchesWithoutHTTP(t *testing.T) {
	fb, err := New(Config{BaseURL: "http://fleet", WorkspaceID: "WS"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := fb.GetChildren(context.Background(), ""); !backendErrorKind(err, backend.KindValidation) {
		t.Fatalf("GetChildren empty err = %v", err)
	}
	if _, err := fb.SearchIssues(context.Background(), "", 1); !backendErrorKind(err, backend.KindValidation) {
		t.Fatalf("SearchIssues empty err = %v", err)
	}
	if _, err := fb.SearchIssues(context.Background(), "query", -1); !backendErrorKind(err, backend.KindValidation) {
		t.Fatalf("SearchIssues negative limit err = %v", err)
	}
	if _, err := fb.Count(context.Background(), backend.CountOpts{GroupBy: "status"}); !backendErrorKind(err, backend.KindNotImplemented) {
		t.Fatalf("Count group_by err = %v", err)
	}
}

func TestFleetRequestBuildErrors(t *testing.T) {
	fb := &FleetBackend{client: http.DefaultClient, baseWorkspaceURL: "://bad-url"}
	if _, _, err := fb.doRequest(context.Background(), http.MethodGet, "/issues", nil); err == nil {
		t.Fatal("doRequest accepted malformed URL")
	}
	if _, _, err := fb.doRequestAsActor(context.Background(), http.MethodPost, "/issues", nil, "actor"); err == nil {
		t.Fatal("doRequestAsActor accepted malformed URL")
	}
	if _, _, err := fb.doRequestURL(context.Background(), http.MethodGet, "://bad-url", nil); err == nil {
		t.Fatal("doRequestURL accepted malformed URL")
	}
}

func backendErrorKind(err error, kind backend.ErrorKind) bool {
	var be *backend.BackendError
	return errors.As(err, &be) && be.Kind == kind
}
