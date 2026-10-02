package git

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/publish"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

func TestMergeUpToHTTPPassesExactConfirmedHeads(t *testing.T) {
	oldPreview, oldRequest := mergePreview, mergeRequest
	t.Cleanup(func() { mergePreview, mergeRequest = oldPreview, oldRequest })
	mergePreview = func(_ context.Context, workspace, lead, stack, target string) (publish.MergeStackView, error) {
		if workspace != "W" || lead != "L" || stack != "feature" || target != "C" {
			t.Fatalf("preview args %s %s %s %s", workspace, lead, stack, target)
		}
		return publish.MergeStackView{StackID: stack, Target: target, Layers: []publish.MergeLayerView{{Change: "A", Head: "a"}, {Change: "B", Head: "b"}, {Change: "C", Head: "c"}, {Change: "D", Head: "d"}}}, nil
	}
	called := false
	mergeRequest = func(_ context.Context, workspace, lead, stack, target string, heads []string, actor publish.MergeActor) (publish.MergeStackView, error) {
		called = true
		if workspace != "W" || lead != "L" || stack != "feature" || target != "C" || strings.Join(heads, ",") != "a,b,c,d" ||
			actor != (publish.MergeActor{Kind: "human", ID: "local-user"}) {
			t.Fatalf("request args %s %s %s %s %v", workspace, lead, stack, target, heads)
		}
		return publish.MergeStackView{Phase: "ready"}, nil
	}
	request := httptest.NewRequest(http.MethodGet, "/?stack_id=feature&target=C", nil)
	request.SetPathValue("name", "L")
	request = request.WithContext(middleware.WithWorkspace(request.Context(), "W"))
	response := httptest.NewRecorder()
	handleMergeUpTo(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"change":"D"`) {
		t.Fatalf("preview %d %s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"stack_id":"feature","target":"C","heads":["a","b","c","d"],"actor":{"kind":"human","id":"local-user"}}`))
	request.SetPathValue("name", "L")
	request = request.WithContext(middleware.WithWorkspace(request.Context(), "W"))
	response = httptest.NewRecorder()
	handleMergeUpTo(response, request)
	if response.Code != http.StatusOK || !called {
		t.Fatalf("request %d %s called=%v", response.Code, response.Body.String(), called)
	}
}

func serveMergeRequest(method, path, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/git/merge-requests", handleMergeRequests)
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/git/merge-requests", handleMergeRequests)
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/git/merge-requests/{id}/confirm", handleConfirmMergeRequest)
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request = request.WithContext(middleware.WithWorkspace(request.Context(), "W"))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	return response
}

func TestMergeRequestHTTPCreatesListsAndConfirms(t *testing.T) {
	oldRequest, oldList, oldConfirm := requestMerge, listMergeRequests, confirmMergeRequest
	t.Cleanup(func() { requestMerge, listMergeRequests, confirmMergeRequest = oldRequest, oldList, oldConfirm })
	var calls []string
	requestMerge = func(_ context.Context, workspace, lead, stack, target string, actor publish.MergeActor) (publish.MergeRequestView, error) {
		calls = append(calls, "request "+workspace+" "+lead+" "+stack+" "+target+" "+actor.Kind+":"+actor.ID)
		return publish.MergeRequestView{ID: "R1", Status: "pending"}, nil
	}
	listMergeRequests = func(_ context.Context, workspace, lead string) ([]publish.MergeRequestView, error) {
		calls = append(calls, "list "+workspace+" "+lead)
		return []publish.MergeRequestView{{ID: "R1", Status: "pending"}}, nil
	}
	confirmMergeRequest = func(_ context.Context, workspace, lead, id string, actor publish.MergeActor) (publish.MergeStackView, error) {
		calls = append(calls, "confirm "+workspace+" "+lead+" "+id+" "+actor.Kind+":"+actor.ID)
		if actor.Kind != "human" {
			return publish.MergeStackView{}, loomgit.NewError(loomgit.MergeNotAuthorized, "a merge request can only be confirmed by a human", nil)
		}
		return publish.MergeStackView{Phase: "ready"}, nil
	}
	base := "/api/workspaces/W/agents/L/git/merge-requests"
	if response := serveMergeRequest(http.MethodPost, base, `{"stack_id":"feature","target":"C","actor":{"kind":"lead","id":"L"}}`); response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"id":"R1"`) {
		t.Fatalf("create %d %s", response.Code, response.Body.String())
	}
	if response := serveMergeRequest(http.MethodGet, base, ""); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"pending"`) {
		t.Fatalf("list %d %s", response.Code, response.Body.String())
	}
	if response := serveMergeRequest(http.MethodPost, base+"/R1/confirm", `{"actor":{"kind":"lead","id":"L"}}`); response.Code != http.StatusConflict {
		t.Fatalf("agent confirm %d %s", response.Code, response.Body.String())
	}
	if response := serveMergeRequest(http.MethodPost, base+"/R1/confirm", `{"actor":{"kind":"human","id":"local-user"}}`); response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"phase":"ready"`) {
		t.Fatalf("human confirm %d %s", response.Code, response.Body.String())
	}
	want := "request W L feature C lead:L|list W L|confirm W L R1 lead:L|confirm W L R1 human:local-user"
	if strings.Join(calls, "|") != want {
		t.Fatalf("calls=%v", calls)
	}
}
