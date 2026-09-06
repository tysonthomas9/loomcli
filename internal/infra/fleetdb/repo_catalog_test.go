package fleetdb

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestRepoLifecycleDoesNotMutateGlobalCatalog(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "POST /api/v1/WS/repos":
			writeJSON(t, w, map[string]any{"workspace_key": "WS", "name": "source-repo", "remote_url": "/workspace/source-repo"})
		case "GET /api/v1/WS/repos":
			writeJSON(t, w, map[string]any{"repos": []map[string]any{{"workspace_key": "WS", "name": "source-repo", "remote_url": "/workspace/source-repo"}}})
		case "DELETE /api/v1/WS/repos/source-repo":
			w.WriteHeader(http.StatusNoContent)
		default:
			// A bare local name is not an org/repo catalog identity. Rejecting the
			// unrelated catalog operation must never trigger compensating repo deletion.
			http.Error(w, "invalid catalog operation", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	repo, err := client.Repos().Create(t.Context(), store.RepoCreate{WorkspaceKey: "WS", Name: "source-repo", RemoteURL: "/workspace/source-repo"})
	if err != nil {
		t.Fatal(err)
	}
	if repo.Name != "source-repo" {
		t.Fatalf("unexpected repo: %#v", repo)
	}
	repos, err := client.Repos().List(t.Context(), "WS")
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 || repos[0].Name != repo.Name {
		t.Fatalf("unexpected repo list: %#v", repos)
	}
	if err := client.Repos().Delete(t.Context(), "WS", "source-repo"); err != nil {
		t.Fatal(err)
	}
	want := []string{"POST /api/v1/WS/repos", "GET /api/v1/WS/repos", "DELETE /api/v1/WS/repos/source-repo"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("HTTP operations = %v, want %v", calls, want)
	}
}
