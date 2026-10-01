package git

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	mergeRequest = func(_ context.Context, workspace, lead, stack, target string, heads []string) (publish.MergeStackView, error) {
		called = true
		if workspace != "W" || lead != "L" || stack != "feature" || target != "C" || strings.Join(heads, ",") != "a,b,c,d" {
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
	request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"stack_id":"feature","target":"C","heads":["a","b","c","d"]}`))
	request.SetPathValue("name", "L")
	request = request.WithContext(middleware.WithWorkspace(request.Context(), "W"))
	response = httptest.NewRecorder()
	handleMergeUpTo(response, request)
	if response.Code != http.StatusOK || !called {
		t.Fatalf("request %d %s called=%v", response.Code, response.Body.String(), called)
	}
}
