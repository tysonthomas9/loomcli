package git

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/publish"
)

func serveMergeApproval(method, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	for _, verb := range []string{"GET", "POST", "DELETE"} {
		mux.HandleFunc(verb+" /api/workspaces/{ws}/changes/{change}/merge-approval", handleMergeApproval)
	}
	request := httptest.NewRequest(method, "/api/workspaces/W/changes/C/merge-approval", strings.NewReader(body))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	return response
}

func TestMergeApprovalHTTPRecordsTheHeadTheHumanSaw(t *testing.T) {
	oldApprove, oldCancel, oldState := approveMerge, cancelMergeApproval, mergeApprovalState
	t.Cleanup(func() { approveMerge, cancelMergeApproval, mergeApprovalState = oldApprove, oldCancel, oldState })
	approveMerge = func(_ context.Context, workspace, lead, change, head string, actor publish.MergeActor) (publish.MergeApprovalView, error) {
		if workspace != "W" || lead != "L" || change != "C" || head != "h1" || actor != (publish.MergeActor{Kind: "human", ID: "local-user"}) {
			t.Fatalf("approve args %s %s %s %s %+v", workspace, lead, change, head, actor)
		}
		return publish.MergeApprovalView{Change: change, Status: "waiting", Reason: "merges after #1", MergeAfter: []int{1}}, nil
	}
	cancelled := false
	cancelMergeApproval = func(_ context.Context, workspace, change string, actor publish.MergeActor) (publish.MergeApprovalView, error) {
		cancelled = workspace == "W" && change == "C" && actor.Kind == "human"
		return publish.MergeApprovalView{Change: change, Status: "cancelled"}, nil
	}
	mergeApprovalState = func(_ context.Context, workspace, change string) (publish.MergeApprovalView, error) {
		return publish.MergeApprovalView{Change: change, Status: "waiting"}, nil
	}
	response := serveMergeApproval(http.MethodPost, `{"lead":"L","head_sha":"h1","actor":{"kind":"human","id":"local-user"}}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"merge_after":[1]`) {
		t.Fatalf("approve %d %s", response.Code, response.Body.String())
	}
	if response = serveMergeApproval(http.MethodPost, `{"lead":"L","actor":{"kind":"human","id":"x"}}`); response.Code != http.StatusBadRequest {
		t.Fatalf("approve without head %d", response.Code)
	}
	if response = serveMergeApproval(http.MethodDelete, `{"actor":{"kind":"human","id":"local-user"}}`); response.Code != http.StatusOK || !cancelled {
		t.Fatalf("cancel %d %s", response.Code, response.Body.String())
	}
	if response = serveMergeApproval(http.MethodGet, ""); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"waiting"`) {
		t.Fatalf("state %d %s", response.Code, response.Body.String())
	}
}

func TestMergeApprovalHTTPRefusesALeadWithConflict(t *testing.T) {
	oldApprove := approveMerge
	t.Cleanup(func() { approveMerge = oldApprove })
	approveMerge = func(context.Context, string, string, string, string, publish.MergeActor) (publish.MergeApprovalView, error) {
		return publish.MergeApprovalView{}, loomgit.NewError(loomgit.MergeNotAuthorized, "only a human can approve a merge", nil)
	}
	response := serveMergeApproval(http.MethodPost, `{"lead":"L","head_sha":"h1","actor":{"kind":"lead","id":"L"}}`)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "only a human") {
		t.Fatalf("lead approve %d %s", response.Code, response.Body.String())
	}
}

// S1 (D38): while Lead may merge is off, the lead's Approve and merge gets the
// same refusal as `loom merge`; with it on, the lead still cannot approve a merge (D15).
func TestMergeApprovalHTTPLeadRefusalFollowsLeadMayMerge(t *testing.T) {
	useGitSettingsStore(t)
	lead := `{"lead":"L","head_sha":"h1","actor":{"kind":"lead","id":"L"}}`
	response := serveMergeApproval(http.MethodPost, lead)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "Lead may merge is off") {
		t.Fatalf("lead approve with Lead may merge off = %d %s", response.Code, response.Body.String())
	}
	if code, got := serveGitSettings(t, http.MethodPut, `{"actor":{"kind":"human"},"lead_may_merge":"when_green"}`); code != http.StatusOK || !strings.Contains(fmt.Sprint(got["settings"]), "lead_may_merge:when_green") {
		t.Fatalf("turn Lead may merge on = %d %v", code, got)
	}
	response = serveMergeApproval(http.MethodPost, lead)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "only a human can approve a merge") {
		t.Fatalf("lead approve with Lead may merge on = %d %s", response.Code, response.Body.String())
	}
}
