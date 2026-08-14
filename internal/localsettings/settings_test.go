package localsettings

import (
	"os"
	"path/filepath"
	"sync"
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

func TestUpdateSerializesConcurrentReadModifyWrite(t *testing.T) {
	dir := t.TempDir()
	firstInCallback := make(chan struct{})
	releaseFirst := make(chan struct{})
	errCh := make(chan error, 2)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := Update(dir, func(settings *Settings) error {
			settings.UIPreferences.WorkspaceOrder = []string{"BETA", "ALPHA"}
			close(firstInCallback)
			<-releaseFirst
			return nil
		})
		errCh <- err
	}()

	<-firstInCallback
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := Update(dir, func(settings *Settings) error {
			settings.AgentRuntime.Default = AgentRuntimeDaytona
			return nil
		})
		errCh <- err
	}()

	time.Sleep(20 * time.Millisecond)
	close(releaseFirst)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("Update returned error: %v", err)
		}
	}
	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if got, want := loaded.UIPreferences.WorkspaceOrder, []string{"BETA", "ALPHA"}; !equalStrings(got, want) {
		t.Fatalf("WorkspaceOrder = %#v, want %#v", got, want)
	}
	if loaded.AgentRuntime.Default != AgentRuntimeDaytona {
		t.Fatalf("AgentRuntime.Default = %q, want %q", loaded.AgentRuntime.Default, AgentRuntimeDaytona)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
