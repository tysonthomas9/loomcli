package localsettings

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	runtimesettings "github.com/tysonthomas9/loomcli/internal/localsettings"
)

func TestHandlePatch_SavesRedisURLWithoutReturningPassword(t *testing.T) {
	dir := t.TempDir()
	req := httptest.NewRequest(http.MethodPatch, "/api/local/settings", strings.NewReader(`{
		"fleetdb_redis": {
			"enabled": true,
			"redis_url": "redis-cli --tls -u redis://default:secret@example.upstash.io:6379",
			"db": 1,
			"tls": true
		}
	}`))
	rec := httptest.NewRecorder()

	HandlePatch(dir).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success || resp.Data == nil {
		t.Fatalf("expected success response, got %+v", resp)
	}
	if !resp.Data.FleetDBRedis.PasswordSet {
		t.Fatal("expected password_set in sanitized response")
	}
	if strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("response leaked password: %s", rec.Body.String())
	}

	data, err := os.ReadFile(runtimesettings.Path(dir))
	if err != nil {
		t.Fatalf("read settings file: %v", err)
	}
	if !strings.Contains(string(data), "secret") {
		t.Fatal("expected password to be persisted in private settings file")
	}
}

func TestHandlePatch_RejectsInvalidRedisAddress(t *testing.T) {
	dir := t.TempDir()
	req := httptest.NewRequest(http.MethodPatch, "/api/local/settings", strings.NewReader(`{
		"fleetdb_redis": {
			"enabled": true,
			"addr": "missing-port"
		}
	}`))
	rec := httptest.NewRecorder()

	HandlePatch(dir).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandlePatch_SavesRuntimeSettingsWithoutReturningCredentials(t *testing.T) {
	dir := t.TempDir()
	req := httptest.NewRequest(http.MethodPatch, "/api/local/settings", strings.NewReader(`{
		"agent_runtime": {"default": "daytona"},
		"runtime_credentials": {
			"daytona": {"api_key": "dtn-secret"},
			"github": {"token": "gh-secret"},
			"codex": {"auth_json": "{\"tokens\":{\"access\":\"codex-secret\"}}"}
		}
	}`))
	rec := httptest.NewRecorder()

	HandlePatch(dir).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Data == nil || resp.Data.AgentRuntime.Default != "daytona" {
		t.Fatalf("agent runtime response = %+v", resp.Data)
	}
	if !resp.Data.RuntimeCredentials.Daytona.Configured ||
		!resp.Data.RuntimeCredentials.GitHub.Configured ||
		!resp.Data.RuntimeCredentials.Codex.Configured {
		t.Fatalf("runtime credentials response = %+v", resp.Data.RuntimeCredentials)
	}
	if strings.Contains(rec.Body.String(), "dtn-secret") ||
		strings.Contains(rec.Body.String(), "gh-secret") ||
		strings.Contains(rec.Body.String(), "codex-secret") {
		t.Fatalf("response leaked credentials: %s", rec.Body.String())
	}
	data, err := os.ReadFile(runtimesettings.Path(dir))
	if err != nil {
		t.Fatalf("read settings file: %v", err)
	}
	if strings.Contains(string(data), "dtn-secret") ||
		strings.Contains(string(data), "gh-secret") ||
		strings.Contains(string(data), "codex-secret") {
		t.Fatalf("settings file leaked credentials: %s", data)
	}

	settings, err := runtimesettings.Load(dir)
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	got, err := runtimesettings.UnsealRuntimeCredential(dir, settings, runtimesettings.RuntimeCredentialProviderDaytona)
	if err != nil || got != "dtn-secret" {
		t.Fatalf("unseal Daytona credential = %q, %v", got, err)
	}
	got, err = runtimesettings.UnsealRuntimeCredential(dir, settings, runtimesettings.RuntimeCredentialProviderCodex)
	if err != nil || got != `{"tokens":{"access":"codex-secret"}}` {
		t.Fatalf("unseal Codex credential = %q, %v", got, err)
	}
}

func TestHandlePatch_NotifiesOnlyActualGitHubCredentialChanges(t *testing.T) {
	dir := t.TempDir()
	notifications := 0
	handler := HandlePatch(dir, PatchOptions{
		OnGitHubRuntimeCredentialChanged: func() { notifications++ },
	})
	patch := func(body string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPatch, "/api/local/settings", strings.NewReader(body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
	}

	patch(`{"runtime_credentials":{"daytona":{"api_key":"dtn-secret"}}}`)
	patch(`{"runtime_credentials":{"codex":{"auth_json":"{\"tokens\":{\"access\":\"codex-secret\"}}"}}}`)
	patch(`{"runtime_credentials":{"github":{}}}`)
	patch(`{"runtime_credentials":{"github":{"clear":true}}}`)
	if notifications != 0 {
		t.Fatalf("notifications before GitHub change = %d, want 0", notifications)
	}

	patch(`{"runtime_credentials":{"github":{"token":"gh-secret"}}}`)
	if notifications != 1 {
		t.Fatalf("notifications after GitHub set = %d, want 1", notifications)
	}
	patch(`{"runtime_credentials":{"github":{"clear":true}}}`)
	if notifications != 2 {
		t.Fatalf("notifications after GitHub clear = %d, want 2", notifications)
	}
	patch(`{"runtime_credentials":{"github":{"clear":true}}}`)
	if notifications != 2 {
		t.Fatalf("notifications after no-op GitHub clear = %d, want 2", notifications)
	}
}
