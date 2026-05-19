package daemonwire

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/kv"
	"github.com/tysonthomas9/loomcli/internal/webui"
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

func TestBuildAgentQueueFnUsesHookedDependencies(t *testing.T) {
	oldGetwd := daemonwireGetwdFn
	oldLoad := daemonwireLoadDaemonConfigFn
	oldFetch := daemonwireFetchReadyIssuesFn
	t.Cleanup(func() {
		daemonwireGetwdFn = oldGetwd
		daemonwireLoadDaemonConfigFn = oldLoad
		daemonwireFetchReadyIssuesFn = oldFetch
	})

	cfg := &config.DaemonConfig{
		Agents: []config.AgentEntry{{Worktree: "spark", Role: "task", Parent: "EPIC-1", Repo: "api"}},
		Roles: map[string]config.RoleConfig{
			"task": {TaskFilter: "has_design", Skills: []string{"go"}},
		},
	}
	daemonwireGetwdFn = func() (string, error) { return "/repo", nil }
	daemonwireLoadDaemonConfigFn = func(projectDir string) (*config.DaemonConfig, error) {
		if projectDir != "/repo" {
			t.Fatalf("projectDir = %q", projectDir)
		}
		return cfg, nil
	}
	daemonwireFetchReadyIssuesFn = func(parentID, repoLabel string) ([]backend.IssueData, error) {
		if parentID != "EPIC-1" || repoLabel != "api" {
			t.Fatalf("fetch args parent=%q repo=%q", parentID, repoLabel)
		}
		return []backend.IssueData{
			{ID: "TASK-1", Title: "first", Status: "open", IssueType: "task", Priority: 2, Labels: []string{"go"}, Design: "ready"},
			{ID: "TASK-2", Title: "filtered", Status: "open", IssueType: "task", Priority: 1},
		}, nil
	}

	queueFn := BuildAgentQueueFn()
	if queueFn == nil {
		t.Fatal("BuildAgentQueueFn returned nil")
	}
	entries, err := queueFn("spark")
	if err != nil {
		t.Fatalf("queueFn: %v", err)
	}
	if len(entries) != 1 || entries[0].IssueID != "TASK-1" || entries[0].Score == 0 {
		t.Fatalf("entries = %+v", entries)
	}
	if _, err := queueFn("missing"); err != webui.ErrAgentNotFound {
		t.Fatalf("missing agent err = %v", err)
	}

	daemonwireGetwdFn = func() (string, error) { return "", os.ErrNotExist }
	if got := BuildAgentQueueFn(); got != nil {
		t.Fatal("BuildAgentQueueFn getwd failure returned non-nil callback")
	}
}
