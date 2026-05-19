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

func TestHandleGetAndPatchValidationBranches(t *testing.T) {
	dir := t.TempDir()
	if err := runtimesettings.Save(dir, runtimesettings.Settings{
		FleetDBRedis: runtimesettings.RedisConfig{
			Enabled:  true,
			Addr:     "localhost:6379",
			Password: "secret",
		},
	}); err != nil {
		t.Fatalf("save settings: %v", err)
	}

	rec := httptest.NewRecorder()
	HandleGet(dir).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/local/settings", nil))
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("HandleGet status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got response
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if !got.Success || got.Data == nil || !got.Data.FleetDBRedis.PasswordSet {
		t.Fatalf("get response = %+v", got)
	}

	rec = httptest.NewRecorder()
	HandleGet("").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/local/settings", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("empty data dir get status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	HandlePatch(dir).ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/api/local/settings", strings.NewReader("{bad")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid patch status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	HandlePatch("").ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/api/local/settings", strings.NewReader(`{}`)))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("empty data dir patch status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	HandlePatch(dir).ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/api/local/settings", strings.NewReader(`{
		"fleetdb_redis": {"clear_password": true, "addr": "localhost:6379"}
	}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("clear password patch status=%d body=%s", rec.Code, rec.Body.String())
	}
	loaded, err := runtimesettings.Load(dir)
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if loaded.FleetDBRedis.Password != "" {
		t.Fatalf("password was not cleared: %+v", loaded.FleetDBRedis)
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
