package localsettings

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	runtimesettings "github.com/tysonthomas9/loomcli/internal/localsettings"
)

func TestHandleGet_ReportsConfiguredButUnusableCredentialWithoutLeakingIt(t *testing.T) {
	dir := t.TempDir()
	settings := runtimesettings.Default()
	credential, err := runtimesettings.SealRuntimeCredential(
		dir,
		runtimesettings.RuntimeCredentialProviderGitHub,
		"gh-secret",
		time.Now(),
	)
	if err != nil {
		t.Fatalf("seal GitHub credential: %v", err)
	}
	settings.RuntimeCredentials.GitHub = credential
	if err := runtimesettings.Save(dir, settings); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	wrongKey := base64.StdEncoding.EncodeToString(make([]byte, 32))
	if err := os.WriteFile(filepath.Join(dir, "runtime-credentials.key"), []byte(wrongKey), 0600); err != nil {
		t.Fatalf("replace runtime credential key: %v", err)
	}

	rec := httptest.NewRecorder()
	HandleGet(dir).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/local/settings", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Data == nil {
		t.Fatalf("missing settings data: %+v", resp)
	}
	status := resp.Data.RuntimeCredentials.GitHub
	if !status.Configured || status.Usable {
		t.Fatalf("GitHub readiness = %+v, want configured but unusable", status)
	}
	if strings.Contains(rec.Body.String(), "gh-secret") ||
		strings.Contains(rec.Body.String(), credential.Sealed) ||
		strings.Contains(strings.ToLower(rec.Body.String()), "unseal") {
		t.Fatalf("readiness response leaked credential details: %s", rec.Body.String())
	}
}

func TestHandleRuntimeCredentialPreflight_RejectsUnsealableCredentialWithoutDetails(t *testing.T) {
	dir := t.TempDir()
	settings := runtimesettings.Default()
	credential, err := runtimesettings.SealRuntimeCredential(
		dir,
		runtimesettings.RuntimeCredentialProviderGitHub,
		"gh-secret",
		time.Now(),
	)
	if err != nil {
		t.Fatalf("seal GitHub credential: %v", err)
	}
	settings.RuntimeCredentials.GitHub = credential
	if err := runtimesettings.Save(dir, settings); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	wrongKey := base64.StdEncoding.EncodeToString(make([]byte, 32))
	if err := os.WriteFile(filepath.Join(dir, "runtime-credentials.key"), []byte(wrongKey), 0600); err != nil {
		t.Fatalf("replace runtime credential key: %v", err)
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/local/settings/runtime-credentials/preflight",
		strings.NewReader(`{"provider":"github"}`),
	)
	rec := httptest.NewRecorder()
	HandleRuntimeCredentialPreflight(dir).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp runtimeCredentialPreflightResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success || resp.Data == nil {
		t.Fatalf("preflight response = %+v", resp)
	}
	if !resp.Data.Configured || resp.Data.Usable {
		t.Fatalf("preflight data = %+v, want configured but unusable", resp.Data)
	}
	if strings.Contains(rec.Body.String(), "gh-secret") ||
		strings.Contains(rec.Body.String(), credential.Sealed) ||
		strings.Contains(strings.ToLower(rec.Body.String()), "unseal") {
		t.Fatalf("preflight response leaked credential details: %s", rec.Body.String())
	}
}

func TestHandleRuntimeCredentialPreflight_AcceptsUsableCredential(t *testing.T) {
	dir := t.TempDir()
	settings := runtimesettings.Default()
	credential, err := runtimesettings.SealRuntimeCredential(
		dir,
		runtimesettings.RuntimeCredentialProviderGitHub,
		"gh-secret",
		time.Now(),
	)
	if err != nil {
		t.Fatalf("seal GitHub credential: %v", err)
	}
	settings.RuntimeCredentials.GitHub = credential
	if err := runtimesettings.Save(dir, settings); err != nil {
		t.Fatalf("save settings: %v", err)
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/local/settings/runtime-credentials/preflight",
		strings.NewReader(`{"provider":"github"}`),
	)
	rec := httptest.NewRecorder()
	HandleRuntimeCredentialPreflight(dir).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp runtimeCredentialPreflightResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Data == nil || !resp.Data.Configured || !resp.Data.Usable {
		t.Fatalf("preflight data = %+v, want configured and usable", resp.Data)
	}
}

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
			"github": {"token": "gh-secret"}
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
	if !resp.Data.RuntimeCredentials.Daytona.Configured || !resp.Data.RuntimeCredentials.GitHub.Configured {
		t.Fatalf("runtime credentials response = %+v", resp.Data.RuntimeCredentials)
	}
	if strings.Contains(rec.Body.String(), "dtn-secret") || strings.Contains(rec.Body.String(), "gh-secret") {
		t.Fatalf("response leaked credentials: %s", rec.Body.String())
	}
	data, err := os.ReadFile(runtimesettings.Path(dir))
	if err != nil {
		t.Fatalf("read settings file: %v", err)
	}
	if strings.Contains(string(data), "dtn-secret") || strings.Contains(string(data), "gh-secret") {
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
}

func TestHandlePatchSerializesLoadApplySave(t *testing.T) {
	for attempt := 0; attempt < 25; attempt++ {
		dir := t.TempDir()
		patch := HandlePatch(dir)
		start := make(chan struct{})
		statuses := make(chan int, 2)
		for _, body := range []string{
			`{"agent_runtime":{"default":"daytona"}}`,
			`{"local_task_runner":{"opencode_model":"open-model"}}`,
		} {
			body := body
			go func() {
				<-start
				req := httptest.NewRequest(http.MethodPatch, "/api/local/settings", strings.NewReader(body))
				rec := httptest.NewRecorder()
				patch.ServeHTTP(rec, req)
				statuses <- rec.Code
			}()
		}
		close(start)
		for range 2 {
			if status := <-statuses; status != http.StatusOK {
				t.Fatalf("attempt %d concurrent PATCH status = %d, want 200", attempt, status)
			}
		}
		settings, err := runtimesettings.Load(dir)
		if err != nil {
			t.Fatalf("attempt %d load settings: %v", attempt, err)
		}
		if settings.AgentRuntime.Default != "daytona" ||
			settings.LocalTaskRunner.OpenCodeModel != "open-model" {
			t.Fatalf("attempt %d concurrent PATCH lost a field: %+v", attempt, settings)
		}
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
