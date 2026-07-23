package localsettings

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestRuntimeCredentialKeyConcurrentFirstPublicationConverges(t *testing.T) {
	dir := t.TempDir()
	const callers = 32
	start := make(chan struct{})
	results := make(chan []byte, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			key, err := runtimeCredentialKey(dir)
			if err != nil {
				errs <- err
				return
			}
			results <- key
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("runtimeCredentialKey: %v", err)
	}

	var winner []byte
	for key := range results {
		if winner == nil {
			winner = key
			continue
		}
		if !bytes.Equal(key, winner) {
			t.Fatal("concurrent callers returned different runtime credential keys")
		}
	}
	persisted, err := readRuntimeCredentialKey(filepath.Join(dir, runtimeCredentialKeyFileName))
	if err != nil {
		t.Fatalf("read persisted runtime credential key: %v", err)
	}
	if !bytes.Equal(persisted, winner) {
		t.Fatal("persisted runtime credential key differs from returned winner")
	}
}

func TestSaveConcurrentPublishersUseIndependentTempFiles(t *testing.T) {
	dir := t.TempDir()
	settings := Default()
	const callers = 32
	start := make(chan struct{})
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- Save(dir, settings)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Save: %v", err)
		}
	}
	if _, err := Load(dir); err != nil {
		t.Fatalf("load concurrently published settings: %v", err)
	}
}

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
