package daemonwire

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/kv"
)

func TestResolveFleetJWTKeyFromEnv(t *testing.T) {
	key := strings.Repeat("ab", 32)
	t.Setenv("LOOM_FLEET_JWT_KEY", key)
	decoded, redisCfg := ResolveFleetJWTKey(context.Background(), "", "")
	if redisCfg != nil {
		t.Fatalf("redis config = %+v, want nil", redisCfg)
	}
	if got := hex.EncodeToString(decoded); got != key {
		t.Fatalf("decoded key = %q, want %q", got, key)
	}

	decoded, redisCfg = ResolveFleetJWTKey(context.Background(), "127.0.0.1:6379", "pw")
	if redisCfg == nil || redisCfg.Address != "127.0.0.1:6379" || redisCfg.Password != "pw" {
		t.Fatalf("redis config = %+v", redisCfg)
	}
	if got := hex.EncodeToString(decoded); got != key {
		t.Fatalf("decoded redis/env key = %q, want %q", got, key)
	}
}

func TestStaleDetectorDisabledHandler(t *testing.T) {
	h := InitStaleDetectorHandler(context.Background(), "", "")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/stale", nil))
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q", ct)
	}
	var status kv.StaleDetectorStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status.Enabled {
		t.Fatalf("disabled stale detector reported enabled: %+v", status)
	}
}

func TestStartLocalRedisUsesConfigDirSnapshot(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)
	ctx, cancel := context.WithCancel(context.Background())
	mgr := StartLocalRedis(ctx, true)
	if mgr == nil {
		t.Fatal("StartLocalRedis returned nil")
	}
	if mgr.Addr() == "" {
		t.Fatal("local redis manager has empty address")
	}
	cancel()
	if err := mgr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := config.GetConfigDir(); got != dir {
		t.Fatalf("config dir = %q, want %q", got, dir)
	}
	_ = filepath.Join(dir, "terminal-state", "snapshot.json")
}
