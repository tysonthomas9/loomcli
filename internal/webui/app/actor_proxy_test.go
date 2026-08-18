package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backend/fleet"
)

func TestWorkspaceMutationScopesFleetDBActorFromRequest(t *testing.T) {
	cases := []struct {
		name         string
		inboundActor string
		extAuth      bool
		wantActor    string
	}{
		{name: "caller actor overrides serve actor", inboundActor: "worker-caller", wantActor: "worker-caller"},
		{name: "absent caller actor keeps serve actor", wantActor: "serve-process"},
		{name: "external auth ignores spoofed caller actor", inboundActor: "spoofed-caller", extAuth: true, wantActor: "serve-process"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotActor string
			fleetDB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotActor = r.Header.Get("X-Actor")
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]bool{"success": true})
			}))
			defer fleetDB.Close()

			be, err := fleet.New(fleet.Config{
				BaseURL:     fleetDB.URL,
				WorkspaceID: "WS",
				Actor:       "serve-process",
			})
			if err != nil {
				t.Fatalf("fleet.New: %v", err)
			}
			app := Server{wsExistsFn: func(id string) bool { return id == "WS" }}
			if tc.extAuth {
				app.extAuthMiddleware = func(next http.Handler) http.Handler { return next }
			}
			handler := app.workspaceMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				title := "updated"
				if err := be.Update(r.Context(), "ISSUE-1", backend.UpdateParams{Title: &title}); err != nil {
					http.Error(w, err.Error(), http.StatusBadGateway)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			}))

			req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/WS/issues/ISSUE-1", nil)
			req.SetPathValue("ws", "WS")
			if tc.inboundActor != "" {
				req.Header.Set("X-Actor", tc.inboundActor)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
			}
			if gotActor != tc.wantActor {
				t.Fatalf("outbound X-Actor = %q, want %q", gotActor, tc.wantActor)
			}
		})
	}
}
