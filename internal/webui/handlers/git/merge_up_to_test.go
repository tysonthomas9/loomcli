package git

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/publish"
)

func serveMergeQueue(method, path, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	(&Module{}).Register(mux)
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	return response
}

func TestMergeUpToQueuesForTheReportedActor(t *testing.T) {
	oldQueue, oldHuman := queueMergeUpTo, localHumanID
	t.Cleanup(func() { queueMergeUpTo, localHumanID = oldQueue, oldHuman })
	localHumanID = func() string { return "Tyson" }
	var calls []string
	queueMergeUpTo = func(_ context.Context, workspace, change string, actor publish.MergeActor) (publish.MergeStackView, error) {
		calls = append(calls, workspace+" "+change+" "+actor.Kind+":"+actor.ID)
		if actor.Kind == "lead" {
			return publish.MergeStackView{}, loomgit.NewError(loomgit.MergeNotAuthorized, publish.LeadMayMergeOff, nil)
		}
		return publish.MergeStackView{StackID: "feature", Target: change, Phase: "ready"}, nil
	}
	path := "/api/workspaces/W/changes/C/merge-up-to"
	if response := serveMergeQueue(http.MethodPost, path, `{"actor":{"kind":"human"}}`); response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"phase":"ready"`) {
		t.Fatalf("human %d %s", response.Code, response.Body.String())
	}
	if response := serveMergeQueue(http.MethodPost, path, `{"actor":{"kind":"lead","id":"L"}}`); response.Code != http.StatusConflict ||
		!strings.Contains(response.Body.String(), "Lead may merge is off") {
		t.Fatalf("lead %d %s", response.Code, response.Body.String())
	}
	if response := serveMergeQueue(http.MethodPost, path, `not json`); response.Code != http.StatusBadRequest {
		t.Fatalf("bad body %d", response.Code)
	}
	if strings.Join(calls, "|") != "W C human:Tyson|W C lead:L" {
		t.Fatalf("calls=%v", calls)
	}
}

func TestMergeUpToShowsTheChangesMerge(t *testing.T) {
	old := mergeUpToView
	t.Cleanup(func() { mergeUpToView = old })
	mergeUpToView = func(_ context.Context, workspace, change string) (publish.MergeStackView, error) {
		return publish.MergeStackView{StackID: "feature-" + workspace, Target: change, Phase: "done"}, nil
	}
	response := serveMergeQueue(http.MethodGet, "/api/workspaces/W/changes/C/merge-up-to", "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"stack_id":"feature-W"`) ||
		!strings.Contains(response.Body.String(), `"phase":"done"`) {
		t.Fatalf("view %d %s", response.Code, response.Body.String())
	}
}

func TestMergeQueueListsTheWorkspaceQueue(t *testing.T) {
	old := mergeQueue
	t.Cleanup(func() { mergeQueue = old })
	mergeQueue = func(_ context.Context, workspace string) ([]publish.QueuedMerge, error) {
		return []publish.QueuedMerge{{StackID: "feature-" + workspace, Target: "C", Phase: "ready", QueuedBy: "lead"}}, nil
	}
	response := serveMergeQueue(http.MethodGet, "/api/workspaces/W/git/merge-queue", "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"stack_id":"feature-W"`) ||
		!strings.Contains(response.Body.String(), `"queued_by":"lead"`) {
		t.Fatalf("queue %d %s", response.Code, response.Body.String())
	}
}

func TestOldMergeRequestRoutesAreGone(t *testing.T) {
	for _, path := range []string{"/api/workspaces/W/agents/L/git/merge-requests", "/api/workspaces/W/agents/L/git/merge-requests/R1/confirm",
		"/api/workspaces/W/agents/L/git/merge-up-to"} {
		if response := serveMergeQueue(http.MethodPost, path, `{}`); response.Code != http.StatusNotFound && response.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s still served: %d", path, response.Code)
		}
	}
}

func TestStacksListsThePublishedStacks(t *testing.T) {
	old := stackCards
	t.Cleanup(func() { stackCards = old })
	stackCards = func(_ context.Context, workspace string) ([]publish.StackCard, error) {
		return []publish.StackCard{{StackID: "feature-" + workspace, Repo: "owner/repo", Backend: "native",
			Layers: []publish.StackCardLayer{{Change: "A", PRNumber: 7, State: "ready"}}}}, nil
	}
	response := serveMergeQueue(http.MethodGet, "/api/workspaces/W/git/stacks", "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"stack_id":"feature-W"`) ||
		!strings.Contains(response.Body.String(), `"state":"ready"`) {
		t.Fatalf("stacks %d %s", response.Code, response.Body.String())
	}
}
