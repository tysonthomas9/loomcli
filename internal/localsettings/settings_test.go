package localsettings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRedisFromURL_ParsesRedisCLIUpstashCommand(t *testing.T) {
	cfg, err := RedisFromURL("redis-cli --tls -u redis://default:secret@example.upstash.io:6379")
	if err != nil {
		t.Fatalf("RedisFromURL returned error: %v", err)
	}
	if !cfg.Enabled {
		t.Fatal("expected parsed config to be enabled")
	}
	if cfg.Addr != "example.upstash.io:6379" {
		t.Fatalf("Addr = %q, want example.upstash.io:6379", cfg.Addr)
	}
	if cfg.Password != "secret" {
		t.Fatalf("Password = %q, want secret", cfg.Password)
	}
	if cfg.DB != 0 {
		t.Fatalf("DB = %d, want 0", cfg.DB)
	}
	if !cfg.TLS {
		t.Fatal("expected TLS from redis-cli --tls")
	}
}

func TestRedisFromURL_ParsesRedissAndDB(t *testing.T) {
	cfg, err := RedisFromURL("rediss://default:secret@example.upstash.io:6380/3")
	if err != nil {
		t.Fatalf("RedisFromURL returned error: %v", err)
	}
	if cfg.Addr != "example.upstash.io:6380" {
		t.Fatalf("Addr = %q, want example.upstash.io:6380", cfg.Addr)
	}
	if cfg.DB != 3 {
		t.Fatalf("DB = %d, want 3", cfg.DB)
	}
	if !cfg.TLS {
		t.Fatal("expected TLS from rediss scheme")
	}
}

func TestSaveUsesPrivatePermissionsAndSanitizesPassword(t *testing.T) {
	dir := t.TempDir()
	settings := Default()
	settings.FleetDBRedis = RedisConfig{
		Enabled:  true,
		Addr:     "example.upstash.io:6379",
		Password: "secret",
		TLS:      true,
	}
	if err := Save(dir, settings); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	info, err := os.Stat(Path(dir))
	if err != nil {
		t.Fatalf("stat settings: %v", err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("settings mode = %#o, want 0600", got)
	}
	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	safe := Sanitize(loaded)
	if !safe.FleetDBRedis.PasswordSet {
		t.Fatal("expected sanitized settings to report password_set")
	}
}

func TestSaveCreatesPrivateSettingsDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "loom-data")
	settings := Default()
	settings.FleetDBRedis = RedisConfig{
		Enabled:  true,
		Addr:     "example.upstash.io:6379",
		Password: "secret",
		TLS:      true,
	}

	if err := Save(dir, settings); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat settings dir: %v", err)
	}
	if got := info.Mode().Perm(); got != 0700 {
		t.Fatalf("settings dir mode = %#o, want 0700", got)
	}
}

func TestCodexRuntimeCredentialRoundTripsAndSanitizes(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 8, 7, 12, 30, 0, 0, time.UTC)
	authJSON := `{"tokens":{"access":"secret"},"last_refresh":"2026-08-07T12:30:00Z"}`

	credential, err := SealRuntimeCredential(dir, RuntimeCredentialProviderCodex, authJSON, now)
	if err != nil {
		t.Fatalf("SealRuntimeCredential(codex): %v", err)
	}
	settings := Default()
	settings.RuntimeCredentials.Codex = credential

	got, err := UnsealRuntimeCredential(dir, settings, RuntimeCredentialProviderCodex)
	if err != nil {
		t.Fatalf("UnsealRuntimeCredential(codex): %v", err)
	}
	if got != authJSON {
		t.Fatalf("unsealed codex credential = %q, want auth JSON", got)
	}

	safe := Sanitize(settings)
	if !safe.RuntimeCredentials.Codex.Configured {
		t.Fatal("expected sanitized codex credential to report configured")
	}
	if safe.RuntimeCredentials.Codex.UpdatedAt != now.Format(time.RFC3339) {
		t.Fatalf("codex UpdatedAt = %q, want %q", safe.RuntimeCredentials.Codex.UpdatedAt, now.Format(time.RFC3339))
	}
	if strings.Contains(safe.RuntimeCredentials.Codex.UpdatedAt, "secret") {
		t.Fatalf("sanitized credential leaked auth JSON: %+v", safe.RuntimeCredentials.Codex)
	}
}

func TestUnconfiguredCodexRuntimeCredentialErrors(t *testing.T) {
	_, err := UnsealRuntimeCredential(t.TempDir(), Default(), RuntimeCredentialProviderCodex)
	if err == nil || !strings.Contains(err.Error(), "codex runtime credential is not configured") {
		t.Fatalf("UnsealRuntimeCredential(codex unconfigured) = %v, want unconfigured error", err)
	}
}
