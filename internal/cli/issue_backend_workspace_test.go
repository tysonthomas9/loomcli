package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

func TestWorkspaceAwareIssueBackendForURL_UsesConcreteURLWhenEnvUnset(t *testing.T) {
	t.Setenv(bootstrap.EnvFleetDBURL, "")
	t.Setenv(bootstrap.EnvFleetDBActor, "")

	fn := WorkspaceAwareIssueBackendForURL("http://127.0.0.1:12345", "tester")
	be := fn(middleware.WithWorkspace(context.Background(), "CLEAN"))
	if be == nil {
		t.Fatal("backend was nil")
	}
	if got := be.BackendName(); got != "fleet" {
		t.Fatalf("BackendName() = %q, want fleet", got)
	}
}

func TestFactory_BrowserPathSendsConfiguredProcessActor(t *testing.T) {
	var actors []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actors = append(actors, r.Header.Get("X-Actor"))
		writeIssueBackendSuccess(t, w)
	}))
	t.Cleanup(server.Close)

	factory := WorkspaceAwareIssueBackendForURL(server.URL, "serve-actor")
	workspaceCtx := middleware.WithWorkspace(context.Background(), "WS")
	withoutActor := factory(workspaceCtx)
	withWebUIActor := factory(middleware.WithActor(workspaceCtx, middleware.WebUIActor()))
	if withoutActor != withWebUIActor {
		t.Fatal("browser contexts did not resolve to the same cached backend")
	}

	if err := withoutActor.ClaimIssue(context.Background(), "issue-1", 0); err != nil {
		t.Fatalf("ClaimIssue without actor: %v", err)
	}
	if err := withWebUIActor.ClaimIssue(context.Background(), "issue-2", 0); err != nil {
		t.Fatalf("ClaimIssue with WebUIActor: %v", err)
	}
	if len(actors) != 2 {
		t.Fatalf("recorded %d requests, want 2", len(actors))
	}
	for i, got := range actors {
		if got != "serve-actor" {
			t.Errorf("request %d X-Actor = %q, want serve-actor", i, got)
		}
	}
}

func TestFactory_OccupantActorsSendDistinctXActor(t *testing.T) {
	var actors []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actors = append(actors, r.Header.Get("X-Actor"))
		writeIssueBackendSuccess(t, w)
	}))
	t.Cleanup(server.Close)

	factory := WorkspaceAwareIssueBackendForURL(server.URL, "serve-actor")
	workspaceCtx := middleware.WithWorkspace(context.Background(), "WS")
	actorA := mustOccupantActor(t, "lead-occupant:a")
	actorB := mustOccupantActor(t, "lead-occupant:b")
	backendA := factory(middleware.WithActor(workspaceCtx, actorA))
	backendB := factory(middleware.WithActor(workspaceCtx, actorB))
	if backendA == backendB {
		t.Fatal("distinct occupant actors resolved to the same backend")
	}

	if err := backendA.ClaimIssue(context.Background(), "issue-a", 0); err != nil {
		t.Fatalf("ClaimIssue as occupant a: %v", err)
	}
	if err := backendB.ClaimIssue(context.Background(), "issue-b", 0); err != nil {
		t.Fatalf("ClaimIssue as occupant b: %v", err)
	}
	want := []string{"lead-occupant:a", "lead-occupant:b"}
	if len(actors) != len(want) {
		t.Fatalf("recorded %d requests, want %d", len(actors), len(want))
	}
	for i := range want {
		if actors[i] != want[i] {
			t.Errorf("request %d X-Actor = %q, want %q", i, actors[i], want[i])
		}
	}
}

func TestFactory_SameOccupantIsCached(t *testing.T) {
	var actors []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actors = append(actors, r.Header.Get("X-Actor"))
		writeIssueBackendSuccess(t, w)
	}))
	t.Cleanup(server.Close)

	factory := WorkspaceAwareIssueBackendForURL(server.URL, "serve-actor")
	actor := mustOccupantActor(t, "lead-occupant:p1")
	ctx := middleware.WithActor(middleware.WithWorkspace(context.Background(), "WS"), actor)

	first, second := factory(ctx), factory(ctx)
	if first != second {
		t.Fatal("same workspace and occupant did not resolve to the same cached backend")
	}
	if err := first.ClaimIssue(context.Background(), "issue-1", 0); err != nil {
		t.Fatalf("first ClaimIssue: %v", err)
	}
	if err := second.ClaimIssue(context.Background(), "issue-2", 0); err != nil {
		t.Fatalf("second ClaimIssue: %v", err)
	}
	if len(actors) != 2 {
		t.Fatalf("recorded %d requests, want 2", len(actors))
	}
	for i, got := range actors {
		if got != "lead-occupant:p1" {
			t.Errorf("request %d X-Actor = %q, want lead-occupant:p1", i, got)
		}
	}
}

func TestFactory_DistinctWorkspacesSameActor(t *testing.T) {
	var requests []struct{ path, actor string }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, struct{ path, actor string }{path: r.URL.Path, actor: r.Header.Get("X-Actor")})
		writeIssueBackendSuccess(t, w)
	}))
	t.Cleanup(server.Close)

	factory := WorkspaceAwareIssueBackendForURL(server.URL, "serve-actor")
	actor := mustOccupantActor(t, "lead-occupant:p1")
	ctxA := middleware.WithActor(middleware.WithWorkspace(context.Background(), "WS-A"), actor)
	ctxB := middleware.WithActor(middleware.WithWorkspace(context.Background(), "WS-B"), actor)

	backendA, backendB := factory(ctxA), factory(ctxB)
	if backendA == backendB {
		t.Fatal("distinct workspaces with the same actor resolved to the same backend")
	}
	if err := backendA.ClaimIssue(context.Background(), "issue-a", 0); err != nil {
		t.Fatalf("workspace A ClaimIssue: %v", err)
	}
	if err := backendB.ClaimIssue(context.Background(), "issue-b", 0); err != nil {
		t.Fatalf("workspace B ClaimIssue: %v", err)
	}
	wantPaths := []string{
		"/api/v1/WS-A/issues/issue-a/claim",
		"/api/v1/WS-B/issues/issue-b/claim",
	}
	if len(requests) != len(wantPaths) {
		t.Fatalf("recorded %d requests, want %d", len(requests), len(wantPaths))
	}
	for i, wantPath := range wantPaths {
		if requests[i].path != wantPath || requests[i].actor != "lead-occupant:p1" {
			t.Errorf("request %d = {path:%q actor:%q}, want {path:%q actor:%q}", i, requests[i].path, requests[i].actor, wantPath, "lead-occupant:p1")
		}
	}
}

func TestFactory_OccupantWithEmptyFleetURLIsUnavailable(t *testing.T) {
	factory := WorkspaceAwareIssueBackendForURL("", "serve-actor")
	actor := mustOccupantActor(t, "lead-occupant:p1")
	ctx := middleware.WithActor(middleware.WithWorkspace(context.Background(), "WS"), actor)

	assertUnavailableFactoryBackend(t, factory(ctx))
}

func TestFactory_WebUIActorWithEmptyFleetURLKeepsDefaultBackend(t *testing.T) {
	factory := WorkspaceAwareIssueBackendForURL("", "serve-actor")
	ctx := middleware.WithActor(middleware.WithWorkspace(context.Background(), "WS"), middleware.WebUIActor())
	if got := factory(ctx).BackendName(); got != DefaultIssueBackend().BackendName() {
		t.Fatalf("BackendName() = %q, want the default backend %q", got, DefaultIssueBackend().BackendName())
	}
}

func TestFactory_InvalidActorWithEmptyFleetURLIsUnavailable(t *testing.T) {
	factory := WorkspaceAwareIssueBackendForURL("", "serve-actor")
	ctx := middleware.WithActor(middleware.WithWorkspace(context.Background(), "WS"), middleware.Actor{})

	assertUnavailableFactoryBackend(t, factory(ctx))
}

func TestFactory_OccupantWithMissingWorkspaceIsUnavailable(t *testing.T) {
	factory := WorkspaceAwareIssueBackendForURL("http://127.0.0.1:12345", "serve-actor")
	actor := mustOccupantActor(t, "lead-occupant:p1")
	ctx := middleware.WithActor(context.Background(), actor)

	assertUnavailableFactoryBackend(t, factory(ctx))
}

func TestFactory_InvalidActorIsUnavailable(t *testing.T) {
	factory := WorkspaceAwareIssueBackendForURL("http://127.0.0.1:12345", "serve-actor")
	ctx := middleware.WithActor(middleware.WithWorkspace(context.Background(), "WS"), middleware.Actor{})

	assertUnavailableFactoryBackend(t, factory(ctx))
}

func mustOccupantActor(t *testing.T, subject string) middleware.Actor {
	t.Helper()
	actor, err := middleware.OccupantActorFor(subject)
	if err != nil {
		t.Fatalf("OccupantActorFor(%q): %v", subject, err)
	}
	return actor
}

func assertUnavailableFactoryBackend(t *testing.T, got backend.IssueBackend) {
	t.Helper()
	if name := got.BackendName(); name != IssueBackendFleetDB+"-unavailable" {
		t.Fatalf("BackendName() = %q, want %q (not the default backend)", name, IssueBackendFleetDB+"-unavailable")
	}
	if _, err := got.Get(context.Background(), "issue-1"); !backend.IsKind(err, backend.KindUnavailable) {
		t.Fatalf("Get error = %v, want unavailable", err)
	}
}

func writeIssueBackendSuccess(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"data":    map[string]any{},
	}); err != nil {
		t.Errorf("encode response: %v", err)
		return
	}
}
