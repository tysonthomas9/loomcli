package data

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
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

func TestGetWorkItems_NoServerNoProvider(t *testing.T) {
	withDataClientState(t, func() {
		t.Setenv("LOOM_SERVER_URL", "")
		serverURL = ""
		SetLocalWorkItemsProvider(nil)

		_, err := getWorkItems(context.Background())
		if err == nil {
			t.Fatal("expected missing backend provider error")
		}
		if !strings.Contains(err.Error(), "local Work Items provider") {
			t.Fatalf("error = %q, want local Work Items provider hint", err.Error())
		}
	})
}

func TestGetWorkItems_NilProviderResult(t *testing.T) {
	withDataClientState(t, func() {
		t.Setenv("LOOM_SERVER_URL", "")
		serverURL = ""
		SetLocalWorkItemsProvider(func(context.Context) workitems.API {
			return nil
		})
		t.Cleanup(func() { SetLocalWorkItemsProvider(nil) })

		_, err := getWorkItems(context.Background())
		if err == nil {
			t.Fatal("expected nil provider result error")
		}
		if !strings.Contains(err.Error(), "returned nil") {
			t.Fatalf("error = %q, want returned nil hint", err.Error())
		}
	})
}

func TestGetWorkItems_FromServerURL(t *testing.T) {
	srv := fakeAuthConfigServer(t)
	defer srv.Close()

	withDataClientState(t, func() {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		t.Setenv("LOOM_WORKSPACE", "default")
		serverURL = srv.URL

		ib, err := getWorkItems(context.Background())
		if err != nil {
			t.Fatalf("getWorkItems: %v", err)
		}
		if ib == nil {
			t.Fatal("expected Work Items adapter")
		}
		named, ok := ib.(interface{ BackendName() string })
		if !ok {
			t.Fatal("API Work Items adapter does not expose its diagnostic name")
		}
		if got := named.BackendName(); got != "api" {
			t.Fatalf("BackendName = %q, want api", got)
		}
	})
}
