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
