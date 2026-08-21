package data

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/leadoccupant"
)

// fakeAuthConfigServer returns an httptest.Server whose /api/config endpoint
// advertises auth mode "open" — enough to let httpclient.New() construct a
// client without triggering OIDC device flow.
func fakeAuthConfigServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"mode": "open"})
	})
	return httptest.NewServer(mux)
}

// withDataClientState resets cli/data package singletons and flag vars
// around the test body so tests can set serverURL/LOOM_SERVER_URL and not
// leak state across tests.
func withDataClientState(t *testing.T, fn func()) {
	t.Helper()
	t.Setenv(leadoccupant.EnvOccupantToken, "")
	t.Setenv(leadoccupant.EnvLeadAPIURL, "")
	t.Setenv(leadoccupant.EnvPlacementID, "")
	prevServer := serverURL
	prevOutput := outputFormat
	prevWorkspace, hadWorkspace := os.LookupEnv("LOOM_WORKSPACE")
	resetClient()
	t.Cleanup(func() {
		serverURL = prevServer
		outputFormat = prevOutput
		if hadWorkspace {
			_ = os.Setenv("LOOM_WORKSPACE", prevWorkspace)
		} else {
			_ = os.Unsetenv("LOOM_WORKSPACE")
		}
		resetClient()
	})
	fn()
}

func setCompleteOccupantEnv(t *testing.T, baseURL string) {
	t.Helper()
	t.Setenv(leadoccupant.EnvOccupantToken, "occupant-token")
	t.Setenv(leadoccupant.EnvLeadAPIURL, baseURL)
	t.Setenv(leadoccupant.EnvWorkspace, "occupant-ws")
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
}

func TestGetHTTPClient_NoServerURL(t *testing.T) {
	withDataClientState(t, func() {
		t.Setenv("LOOM_SERVER_URL", "")
		serverURL = ""
		_, _, err := getHTTPClient()
		if err == nil {
			t.Fatal("expected error when neither --server nor LOOM_SERVER_URL is set")
		}
		if !strings.Contains(err.Error(), "require --server") {
			t.Errorf("error = %q, want one containing 'require --server'", err.Error())
		}
	})
}

func TestGetHTTPClient_FromFlag(t *testing.T) {
	srv := fakeAuthConfigServer(t)
	defer srv.Close()

	withDataClientState(t, func() {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		serverURL = srv.URL
		cli, url, err := getHTTPClient()
		if err != nil {
			t.Fatalf("getHTTPClient: %v", err)
		}
		if cli == nil {
			t.Fatal("expected non-nil http.Client")
		}
		if url != srv.URL {
			t.Errorf("resolved URL = %q, want %q", url, srv.URL)
		}
	})
}

func TestGetHTTPClient_FromEnv(t *testing.T) {
	srv := fakeAuthConfigServer(t)
	defer srv.Close()

	withDataClientState(t, func() {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		t.Setenv("LOOM_SERVER_URL", srv.URL)
		serverURL = ""
		cli, url, err := getHTTPClient()
		if err != nil {
			t.Fatalf("getHTTPClient: %v", err)
		}
		if cli == nil {
			t.Fatal("expected non-nil http.Client")
		}
		if url != srv.URL {
			t.Errorf("resolved URL = %q, want %q", url, srv.URL)
		}
	})
}

func TestResetClient(t *testing.T) {
	srv1 := fakeAuthConfigServer(t)
	defer srv1.Close()
	srv2 := fakeAuthConfigServer(t)
	defer srv2.Close()

	withDataClientState(t, func() {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

		serverURL = srv1.URL
		_, url1, err := getHTTPClient()
		if err != nil {
			t.Fatalf("first getHTTPClient: %v", err)
		}
		if url1 != srv1.URL {
			t.Errorf("first call URL = %q, want %q", url1, srv1.URL)
		}

		resetClient()
		serverURL = srv2.URL
		_, url2, err := getHTTPClient()
		if err != nil {
			t.Fatalf("second getHTTPClient: %v", err)
		}
		if url2 != srv2.URL {
			t.Errorf("after reset URL = %q, want %q", url2, srv2.URL)
		}
	})
}

func TestGetIssueBackend_NoServerNoProvider(t *testing.T) {
	withDataClientState(t, func() {
		t.Setenv("LOOM_SERVER_URL", "")
		serverURL = ""
		SetLocalIssueBackendProvider(nil)

		_, err := getIssueBackend(context.Background())
		if err == nil {
			t.Fatal("expected missing backend provider error")
		}
		if !strings.Contains(err.Error(), "local backend provider") {
			t.Fatalf("error = %q, want local backend provider hint", err.Error())
		}
	})
}

func TestGetIssueBackend_NilProviderResult(t *testing.T) {
	withDataClientState(t, func() {
		t.Setenv("LOOM_SERVER_URL", "")
		serverURL = ""
		SetLocalIssueBackendProvider(func(context.Context) backend.IssueBackend {
			return nil
		})
		t.Cleanup(func() { SetLocalIssueBackendProvider(nil) })

		_, err := getIssueBackend(context.Background())
		if err == nil {
			t.Fatal("expected nil provider result error")
		}
		if !strings.Contains(err.Error(), "returned nil") {
			t.Fatalf("error = %q, want returned nil hint", err.Error())
		}
	})
}

func TestGetIssueBackend_FromServerURL(t *testing.T) {
	srv := fakeAuthConfigServer(t)
	defer srv.Close()

	withDataClientState(t, func() {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		t.Setenv("LOOM_WORKSPACE", "default")
		serverURL = srv.URL

		ib, err := getIssueBackend(context.Background())
		if err != nil {
			t.Fatalf("getIssueBackend: %v", err)
		}
		if ib == nil {
			t.Fatal("expected issue backend")
		}
		if got := ib.BackendName(); got != "api" {
			t.Fatalf("BackendName = %q, want api", got)
		}
	})
}

func TestGetIssueBackend_OccupantBeatsServerURL(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		respondOK := map[string]any{"success": true, "data": []any{}}
		_ = json.NewEncoder(w).Encode(respondOK)
	}))
	defer srv.Close()

	withDataClientState(t, func() {
		setCompleteOccupantEnv(t, srv.URL)
		t.Setenv("LOOM_SERVER_URL", "http://ordinary.invalid")
		serverURL = "http://flag.invalid"
		ib, err := getIssueBackend(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ib.List(context.Background(), backend.ListOpts{}); err != nil {
			t.Fatal(err)
		}
		if gotPath != "/api/workspaces/occupant-ws/lead/data/issues" {
			t.Fatalf("path = %q", gotPath)
		}
	})
}

func TestOccupantMode_DisablesGeneralHTTPClient(t *testing.T) {
	for _, state := range []string{"partial", "complete"} {
		t.Run(state, func(t *testing.T) {
			withDataClientState(t, func() {
				t.Setenv(leadoccupant.EnvOccupantToken, "token")
				t.Setenv(leadoccupant.EnvLeadAPIURL, "")
				t.Setenv(leadoccupant.EnvWorkspace, "")
				if state == "complete" {
					t.Setenv(leadoccupant.EnvLeadAPIURL, "http://lead.invalid")
					t.Setenv(leadoccupant.EnvWorkspace, "ws")
				}
				t.Setenv("LOOM_SERVER_URL", "http://ordinary.invalid")
				_, _, err := getHTTPClient()
				if err == nil || err.Error() != occupantHTTPError {
					t.Fatalf("error = %v, want %q", err, occupantHTTPError)
				}
			})
		})
	}
}

func TestGetIssueBackend_PartialOccupantFailsClosed(t *testing.T) {
	withDataClientState(t, func() {
		t.Setenv(leadoccupant.EnvOccupantToken, "token")
		t.Setenv(leadoccupant.EnvLeadAPIURL, "")
		t.Setenv(leadoccupant.EnvWorkspace, "")
		t.Setenv("LOOM_SERVER_URL", "http://ordinary.invalid")
		_, err := getIssueBackend(context.Background())
		const want = "occupant environment incomplete: LOOM_LEAD_OCCUPANT_TOKEN is set but LOOM_LEAD_API_URL/LOOM_WORKSPACE is missing"
		if err == nil || err.Error() != want {
			t.Fatalf("error = %v, want %q", err, want)
		}
	})
}

func TestPartialOccupantNeverStartsOrdinaryAuthDiscovery(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_ = json.NewEncoder(w).Encode(map[string]string{"mode": "open"})
	}))
	defer srv.Close()
	for _, tc := range []struct {
		name      string
		leadURL   string
		workspace string
	}{
		{"token only", "", ""},
		{"token and URL", srv.URL, ""},
		{"token and workspace", "", "ws"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withDataClientState(t, func() {
				t.Setenv(leadoccupant.EnvOccupantToken, "token")
				t.Setenv(leadoccupant.EnvLeadAPIURL, tc.leadURL)
				t.Setenv(leadoccupant.EnvWorkspace, tc.workspace)
				t.Setenv("LOOM_SERVER_URL", srv.URL)
				_, _ = getIssueBackend(context.Background())
			})
		})
	}
	if hits != 0 {
		t.Fatalf("ordinary auth discovery hits = %d, want zero", hits)
	}
}

func TestOccupantMode_AgentsMonitorAndAgentControlFailFast(t *testing.T) {
	withDataClientState(t, func() {
		setCompleteOccupantEnv(t, "http://lead.invalid")
		commands := []struct {
			name string
			run  func() error
		}{
			{"agents", func() error { return agentsCmd.RunE(agentsCmd, nil) }},
			{"monitor", func() error { return monitorCmd.RunE(monitorCmd, nil) }},
			{"agent control", func() error { return runAgentControl(context.Background(), "worker", "stop", false, true) }},
		}
		for _, command := range commands {
			t.Run(command.name, func(t *testing.T) {
				resetClient()
				err := command.run()
				if err == nil || err.Error() != occupantHTTPError {
					t.Fatalf("error = %v, want %q", err, occupantHTTPError)
				}
			})
		}
	})
}

func TestOccupantBackend_AllIssueCommandsUseLeadDataMount(t *testing.T) {
	type seenRequest struct {
		method string
		path   string
		query  string
		body   string
	}
	var seen []seenRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := new(bytes.Buffer)
		_, _ = body.ReadFrom(r.Body)
		seen = append(seen, seenRequest{r.Method, r.URL.Path, r.URL.RawQuery, body.String()})
		var data any = map[string]any{}
		if r.Method == http.MethodGet && (strings.HasSuffix(r.URL.Path, "/issues") || strings.HasSuffix(r.URL.Path, "/ready") || strings.HasSuffix(r.URL.Path, "/blocked")) {
			data = []any{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data})
	}))
	defer srv.Close()

	withDataClientState(t, func() {
		setCompleteOccupantEnv(t, srv.URL)
		ib, err := getIssueBackend(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		_, _ = ib.List(ctx, backend.ListOpts{})
		_, _ = ib.Get(ctx, "i1")
		_, _ = ib.Create(ctx, backend.CreateParams{Title: "new"})
		title := "updated"
		_ = ib.Update(ctx, "i1", backend.UpdateParams{Title: &title})
		_ = ib.ClaimIssue(ctx, "i1", 0)
		_, _ = ib.Close(ctx, "i1", backend.CloseParams{})
		_ = ib.AddDependency(ctx, backend.DepAddParams{FromID: "i1", ToID: "i2"})
		_ = ib.RemoveDependency(ctx, backend.DepRemoveParams{FromID: "i1", ToID: "i2"})
		_, _ = ib.AddComment(ctx, backend.CommentAddParams{IssueID: "i1", Text: "hello"})
		_, _ = ib.Ready(ctx, backend.ReadyOpts{})
		_, _ = ib.Blocked(ctx, backend.BlockedOpts{})
		_, _ = ib.Stats(ctx)
		_, _ = ib.SearchIssues(ctx, "needle", 3)
	})

	want := []seenRequest{
		{http.MethodGet, "/api/workspaces/occupant-ws/lead/data/issues", "", ""},
		{http.MethodGet, "/api/workspaces/occupant-ws/lead/data/issues/i1", "", ""},
		{http.MethodPost, "/api/workspaces/occupant-ws/lead/data/issues", "", ""},
		{http.MethodPatch, "/api/workspaces/occupant-ws/lead/data/issues/i1", "", ""},
		{http.MethodPost, "/api/workspaces/occupant-ws/lead/data/issues/i1/claim", "", ""},
		{http.MethodPost, "/api/workspaces/occupant-ws/lead/data/issues/i1/close", "", ""},
		{http.MethodPost, "/api/workspaces/occupant-ws/lead/data/issues/i1/dependencies", "", ""},
		{http.MethodDelete, "/api/workspaces/occupant-ws/lead/data/issues/i1/dependencies/i2", "", ""},
		{http.MethodPost, "/api/workspaces/occupant-ws/lead/data/issues/i1/comments", "", `{"text":"hello"}`},
		{http.MethodGet, "/api/workspaces/occupant-ws/lead/data/ready", "", ""},
		{http.MethodGet, "/api/workspaces/occupant-ws/lead/data/blocked", "", ""},
		{http.MethodGet, "/api/workspaces/occupant-ws/lead/data/stats", "", ""},
		{http.MethodGet, "/api/workspaces/occupant-ws/lead/data/issues", "q=needle&limit=3", ""},
	}
	if len(seen) != len(want) {
		t.Fatalf("requests = %d, want %d: %#v", len(seen), len(want), seen)
	}
	for i := range want {
		if seen[i].method != want[i].method || seen[i].path != want[i].path || seen[i].query != want[i].query {
			t.Errorf("request %d = %#v, want %#v", i, seen[i], want[i])
		}
		if want[i].body != "" && seen[i].body != want[i].body {
			t.Errorf("request %d body = %q, want %q", i, seen[i].body, want[i].body)
		}
		if strings.Contains(seen[i].path, "/api/workspaces/occupant-ws/issues") {
			t.Errorf("request %d escaped occupant mount: %q", i, seen[i].path)
		}
	}
}
