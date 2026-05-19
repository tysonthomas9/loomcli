package localsettings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestLoadValidateEnvHashAndRedisURLEdges(t *testing.T) {
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "data dir is required") {
		t.Fatalf("Load empty err = %v", err)
	}
	if got, err := Load(t.TempDir()); err != nil || got.Version != 1 {
		t.Fatalf("Load missing got=%+v err=%v", got, err)
	}
	badDir := t.TempDir()
	if err := os.WriteFile(Path(badDir), []byte("{bad-json"), 0600); err != nil {
		t.Fatalf("write bad settings: %v", err)
	}
	if _, err := Load(badDir); err == nil || !strings.Contains(err.Error(), "parse local settings") {
		t.Fatalf("Load bad JSON err = %v", err)
	}

	for _, cfg := range []RedisConfig{
		{Enabled: true},
		{Enabled: true, Addr: "localhost:6379", DB: -1},
		{Enabled: true, Addr: "localhost"},
		{Enabled: true, Addr: "localhost:not-a-port"},
	} {
		if err := Validate(cfg); err == nil {
			t.Fatalf("Validate(%+v) err = nil", cfg)
		}
	}
	if err := Validate(RedisConfig{Enabled: false}); err != nil {
		t.Fatalf("Validate disabled: %v", err)
	}

	disabledEnv := Env([]string{"BASE=1"}, RedisConfig{})
	if len(disabledEnv) != 1 || disabledEnv[0] != "BASE=1" {
		t.Fatalf("disabled Env = %#v", disabledEnv)
	}
	cfg := RedisConfig{Enabled: true, Addr: " redis.example:6379 ", Password: "secret", DB: 2, TLS: true}
	env := Env(nil, cfg)
	joined := strings.Join(env, "\n")
	for _, want := range []string{
		EnvFleetDBRedisAddr + "=redis.example:6379",
		EnvFleetDBRedisDB + "=2",
		EnvFleetDBRedisTLS + "=true",
		EnvFleetDBRedisPassword + "=secret",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("Env missing %q in %#v", want, env)
		}
	}
	if RuntimeHash(RedisConfig{}) != "" {
		t.Fatal("disabled RuntimeHash should be empty")
	}
	if RuntimeHash(cfg) == "" || RuntimeHash(cfg) != RuntimeHash(cfg) {
		t.Fatal("enabled RuntimeHash should be stable and non-empty")
	}

	parsed, err := RedisFromURL("redis://:pw@example.com")
	if err != nil {
		t.Fatalf("RedisFromURL default port: %v", err)
	}
	if parsed.Addr != "example.com:6379" || parsed.Password != "pw" || parsed.TLS {
		t.Fatalf("default-port parsed cfg = %+v", parsed)
	}
	parsed, err = RedisFromURL("redis-cli --uri=redis://default:pw@example.com:6379/4")
	if err != nil || parsed.DB != 4 || parsed.Password != "pw" {
		t.Fatalf("--uri parsed=%+v err=%v", parsed, err)
	}
	for _, raw := range []string{"", "http://example.com:6379", "redis://example.com/not-number", "redis://:6379"} {
		if _, err := RedisFromURL(raw); err == nil {
			t.Fatalf("RedisFromURL(%q) err = nil", raw)
		}
	}
}
