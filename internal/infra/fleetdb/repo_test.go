package fleetdb

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestRepoStoreCreateUsesSingleAtomicFleetCommand(t *testing.T) {
	requests := 0
	httpClient := newWorkspaceHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/FLEET/repos" {
			t.Fatalf("unexpected non-atomic repository request: %s %s", r.Method, r.URL.String())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"workspace_key":"FLEET","name":"loomcli","remote_url":"https://example.test/loomcli.git"}`))
	}))
	client, err := New(Config{BaseURL: "http://fleet.test", Actor: "tester", HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}

	repo, err := client.Repos().Create(t.Context(), store.RepoCreate{
		WorkspaceKey: "FLEET",
		Name:         "loomcli",
		RemoteURL:    "https://example.test/loomcli.git",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if repo.WorkspaceKey != "FLEET" || repo.Name != "loomcli" {
		t.Fatalf("Create repo = %#v, want FLEET/loomcli", repo)
	}
	if requests != 1 {
		t.Fatalf("request count = %d, want one atomic repository POST", requests)
	}
}

func TestRepoStoreDeleteStopsBeforeWorkspaceRemovalOnReferenceConflict(t *testing.T) {
	requests := 0
	httpClient := newWorkspaceHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v1/FLEET/repos/linked-worktree" {
			t.Fatalf("unexpected request after guarded delete: %s %s", r.Method, r.URL.String())
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"conflict","message":"repository is referenced by non-terminal work or active execution"}}`))
	}))
	client, err := New(Config{BaseURL: "http://fleet.test", Actor: "tester", HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}

	err = client.Repos().Delete(t.Context(), "FLEET", "linked-worktree")
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Delete err = %v, want domain.ErrConflict", err)
	}
	if requests != 1 {
		t.Fatalf("request count = %d, want one guarded repository DELETE and no workspace PATCH", requests)
	}
}

func TestRepoStoreDeleteUsesSingleAtomicFleetCommand(t *testing.T) {
	requests := 0
	httpClient := newWorkspaceHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v1/FLEET/repos/linked-worktree" {
			t.Fatalf("unexpected non-atomic repository request: %s %s", r.Method, r.URL.String())
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	client, err := New(Config{BaseURL: "http://fleet.test", Actor: "tester", HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}

	if err := client.Repos().Delete(t.Context(), "FLEET", "linked-worktree"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if requests != 1 {
		t.Fatalf("request count = %d, want one atomic repository DELETE", requests)
	}
}
