package leadbackend

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/leadoccupant"
)

func clearOccupantEnv(t *testing.T) {
	t.Helper()
	t.Setenv(leadoccupant.EnvOccupantToken, "")
	t.Setenv(leadoccupant.EnvLeadAPIURL, "")
	t.Setenv(leadoccupant.EnvWorkspace, "")
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
}

func TestNew_EnvironmentStates(t *testing.T) {
	t.Run("absent falls through", func(t *testing.T) {
		clearOccupantEnv(t)
		got, err := New()
		if err != nil || got != nil {
			t.Fatalf("New() = (%T, %v), want (nil, nil)", got, err)
		}
	})

	for _, tc := range []struct {
		name      string
		baseURL   string
		workspace string
	}{
		{"token only", "", ""},
		{"missing workspace", "http://example.test", ""},
		{"missing URL", "", "ws"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearOccupantEnv(t)
			t.Setenv(leadoccupant.EnvOccupantToken, "token")
			t.Setenv(leadoccupant.EnvLeadAPIURL, tc.baseURL)
			t.Setenv(leadoccupant.EnvWorkspace, tc.workspace)
			got, err := New()
			if got != nil {
				t.Fatalf("backend = %T, want nil", got)
			}
			const want = "occupant environment incomplete: LOOM_LEAD_OCCUPANT_TOKEN is set but LOOM_LEAD_API_URL/LOOM_WORKSPACE is missing"
			if err == nil || err.Error() != want {
				t.Fatalf("error = %v, want %q", err, want)
			}
		})
	}
}

func TestNew_CompleteUsesLeadDataAndExactStaleTokenMessageForHTML401(t *testing.T) {
	var gotPath, gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`<html><body>unauthorized</body></html>`))
	}))
	defer ts.Close()
	clearOccupantEnv(t)
	t.Setenv(leadoccupant.EnvOccupantToken, "occupant-token")
	t.Setenv(leadoccupant.EnvLeadAPIURL, ts.URL)
	t.Setenv(leadoccupant.EnvWorkspace, "ws")

	got, err := New()
	if err != nil {
		t.Fatal(err)
	}
	_, err = got.List(context.Background(), backend.ListOpts{})
	var be *backend.BackendError
	if !errors.As(err, &be) {
		t.Fatalf("error = %v, want BackendError", err)
	}
	if be.Message != unauthorizedMessage {
		t.Errorf("message = %q, want %q", be.Message, unauthorizedMessage)
	}
	if gotPath != "/api/workspaces/ws/lead/data/issues" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer occupant-token" {
		t.Errorf("Authorization = %q", gotAuth)
	}
}
