package app

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/route"
)

// TestRegisteredRoutes_MergesAllThreeMuxes pins the contract the openapi drift
// check depends on: routes live on three muxes, and registeredRoutes must
// surface all of them, sorted and deduplicated.
func TestRegisteredRoutes_MergesAllThreeMuxes(t *testing.T) {
	var app Server
	app.mux = route.NewRecorder()
	app.mux.HandleFunc("GET /api/health", func(http.ResponseWriter, *http.Request) {})
	app.mux.Handle("/api/workspaces/{ws}/", http.NotFoundHandler())

	app.wsMuxRec = route.NewRecorder()
	app.wsMuxRec.HandleFunc("GET /api/workspaces/{ws}/issues", func(http.ResponseWriter, *http.Request) {})

	app.workerRoutes = []string{
		"POST /api/internal/workers/register",
		// Duplicated on purpose: the same route reaches the outer mux too.
		"GET /api/health",
	}

	want := []string{
		"/api/workspaces/{ws}/",
		"GET /api/health",
		"GET /api/workspaces/{ws}/issues",
		"POST /api/internal/workers/register",
	}
	if got := app.registeredRoutes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("registeredRoutes() = %v, want %v", got, want)
	}
}

// TestRegisteredRoutes_ZeroValueServer guards the nil-mux paths: a Server that
// never finished construction must not panic.
func TestRegisteredRoutes_ZeroValueServer(t *testing.T) {
	var app Server
	if got := app.registeredRoutes(); len(got) != 0 {
		t.Fatalf("registeredRoutes() on zero-value Server = %v, want empty", got)
	}
}

// TestRegisteredRoutes_CoversRealRegistration checks that a really-registered
// server reports routes from the outer mux and the workspace sub-mux, which is
// what makes a drift check meaningful.
func TestRegisteredRoutes_CoversRealRegistration(t *testing.T) {
	var app Server
	app.mux = route.NewRecorder()
	app.buildHandlers()
	app.registerRoutes()
	defer app.handlers.ClientErrLimiter.Stop()
	defer app.handlers.AuthCfgLimiter.Stop()

	got := app.registeredRoutes()
	if len(got) == 0 {
		t.Fatal("registeredRoutes() returned nothing after registerRoutes()")
	}
	found := false
	for _, p := range got {
		if p == "GET /api/health" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("registeredRoutes() = %v, missing %q", got, "GET /api/health")
	}
}
