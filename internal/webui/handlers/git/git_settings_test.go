package git

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit/publish"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

func useGitSettingsStore(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)
	if err := os.MkdirAll(filepath.Join(dir, "loomgit"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "loomgit", "store.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func serveGitSettings(t *testing.T, method, body string) (int, map[string]any) {
	t.Helper()
	mux := http.NewServeMux()
	NewModule(nil, nil).Register(mux)
	request := httptest.NewRequest(method, "/api/workspaces/W/git/settings", strings.NewReader(body))
	request = request.WithContext(middleware.WithWorkspace(request.Context(), "W"))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	var decoded map[string]any
	_ = json.Unmarshal(response.Body.Bytes(), &decoded)
	return response.Code, decoded
}

func TestGitSettingsHTTPReadsWritesAndShowsCLIChanges(t *testing.T) {
	useGitSettingsStore(t)
	code, got := serveGitSettings(t, http.MethodGet, "")
	if code != http.StatusOK || got["delivery_mode"] != "stack" || got["lead_may_approve_publish"] != true || got["lead_may_merge"] != "off" {
		t.Fatalf("defaults %d %v", code, got)
	}
	code, got = serveGitSettings(t, http.MethodPut, `{"actor":{"kind":"human"},"delivery_mode":"trunk","lead_may_approve_publish":false,"lead_may_merge":"when_green"}`)
	if code != http.StatusOK || got["warning"] == "" {
		t.Fatalf("human write %d %v", code, got)
	}
	code, got = serveGitSettings(t, http.MethodGet, "")
	if code != http.StatusOK || got["delivery_mode"] != "trunk" || got["lead_may_approve_publish"] != false || got["lead_may_merge"] != "when_green" {
		t.Fatalf("after write %d %v", code, got)
	}
	ctx := context.Background()
	if err := publish.SetDeliveryModeLocal(ctx, "W", "stack"); err != nil {
		t.Fatal(err)
	}
	if _, err := publish.SetWorkspacePolicyLocal(ctx, "W", "off", review.Actor{Kind: "human", ID: "tyson"}, nil); err != nil {
		t.Fatal(err)
	}
	code, got = serveGitSettings(t, http.MethodGet, "")
	if code != http.StatusOK || got["delivery_mode"] != "stack" || got["lead_may_merge"] != "off" {
		t.Fatalf("after CLI change %d %v", code, got)
	}
}

func TestGitSettingsHTTPLeadChangesOnlyDeliveryMode(t *testing.T) {
	useGitSettingsStore(t)
	if code, got := serveGitSettings(t, http.MethodPut, `{"actor":{"kind":"lead","id":"lead"},"delivery_mode":"trunk"}`); code != http.StatusOK {
		t.Fatalf("lead delivery mode %d %v", code, got)
	}
	for _, body := range []string{
		`{"actor":{"kind":"lead","id":"lead"},"lead_may_approve_publish":false}`,
		`{"actor":{"kind":"lead","id":"lead"},"lead_may_merge":"when_green"}`,
		`{"actor":{"kind":"lead","id":"lead"},"delivery_mode":"stack","lead_may_merge":"when_green"}`,
	} {
		if code, got := serveGitSettings(t, http.MethodPut, body); code != http.StatusForbidden {
			t.Fatalf("lead %s: %d %v", body, code, got)
		}
	}
	if code, got := serveGitSettings(t, http.MethodPut, `{"actor":{"kind":"human"},"delivery_mode":"sideways"}`); code != http.StatusBadRequest {
		t.Fatalf("invalid mode %d %v", code, got)
	}
	code, got := serveGitSettings(t, http.MethodGet, "")
	if code != http.StatusOK || got["delivery_mode"] != "trunk" || got["lead_may_approve_publish"] != true || got["lead_may_merge"] != "off" {
		t.Fatalf("refused writes changed settings %d %v", code, got)
	}
}
