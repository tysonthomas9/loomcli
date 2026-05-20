package daemonwire

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/kv"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/agentcontrol"
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

func TestResolveFleetJWTKeyEmptyAndConfigConversions(t *testing.T) {
	t.Setenv("LOOM_FLEET_JWT_KEY", "")
	key, redisCfg := ResolveFleetJWTKey(context.Background(), "", "")
	if key != nil || redisCfg != nil {
		t.Fatalf("empty fleet key = %v redis=%+v, want nils", key, redisCfg)
	}

	mr := miniredis.RunT(t)
	redisAddr := mr.Addr()
	key, redisCfg = ResolveFleetJWTKey(context.Background(), redisAddr, "")
	if len(key) != 32 || redisCfg == nil || redisCfg.Address != redisAddr {
		t.Fatalf("redis fleet key len=%d redis=%+v", len(key), redisCfg)
	}

	mr.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	key, redisCfg = ResolveFleetJWTKey(ctx, redisAddr, "")
	if key != nil || redisCfg == nil || redisCfg.Address != redisAddr {
		t.Fatalf("failed redis fleet key = %v redis=%+v", key, redisCfg)
	}

	if got := BuildStoreBackedDaemonConfigFn(nil); got != nil {
		t.Fatal("BuildStoreBackedDaemonConfigFn(nil) returned callback")
	}
	settings := daemonSettingsFromProfile(nil)
	if settings.IssueBackend != "fleetdb" {
		t.Fatalf("nil daemon profile settings = %+v", settings)
	}
	if got := roleConfigFromDomain(nil); got.Description != "" {
		t.Fatalf("nil role conversion = %+v", got)
	}
	if got := agentEntryFromDomain(nil); got.Worktree != "" {
		t.Fatalf("nil agent conversion = %+v", got)
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

func TestStaleDetectorEnabledHandlerReportsStatus(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := InitStaleDetectorHandler(ctx, "127.0.0.1:1", "pw")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/stale", nil))
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q", ct)
	}
	var status kv.StaleDetectorStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if !status.Enabled {
		t.Fatalf("enabled stale detector status = %+v", status)
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

func TestScoreAndSortQueueTieBreakersAndListWorkspaces(t *testing.T) {
	entries := scoreAndSortQueue([]backend.IssueData{
		{ID: "TASK-C", Title: "third", Status: "open", IssueType: "task", Priority: 3, Design: "ready", Labels: []string{"go"}},
		{ID: "TASK-A", Title: "first", Status: "open", IssueType: "task", Priority: 1, Design: "ready", Labels: []string{"go"}},
		{ID: "TASK-B", Title: "second", Status: "open", IssueType: "task", Priority: 1, Design: "ready", Labels: []string{"go"}},
		{ID: "TASK-Z", Title: "filtered", Status: "open", IssueType: "task"},
	}, cli.MergeRoleConstraints(config.RoleConfig{TaskFilter: "has_design", Skills: []string{"go"}}, config.AgentEntry{}))
	if len(entries) != 3 {
		t.Fatalf("entries = %+v, want three scored tasks", entries)
	}
	if entries[0].IssueID != "TASK-A" || entries[1].IssueID != "TASK-B" || entries[2].IssueID != "TASK-C" {
		t.Fatalf("sorted entries = %+v, want score then priority then issue id order", entries)
	}

	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	t.Setenv(bootstrap.EnvWorkspace, "WS")
	t.Setenv(bootstrap.EnvFleetDBURL, "")
	t.Setenv(bootstrap.EnvFleetDBActor, "daemonwire-test")
	config.InvalidateConfigCache()
	t.Cleanup(config.InvalidateConfigCache)
	ctx := context.Background()
	handle, err := bootstrap.OpenStore(ctx, configDir, nil)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	if _, err := handle.Store.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		_ = handle.Close()
		t.Fatalf("create workspace: %v", err)
	}
	if err := handle.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	if err := bootstrap.SaveStateCache(&bootstrap.StateCache{
		LastWorkspace: "WS",
		Workspaces: map[string]bootstrap.WorkspaceLocalState{
			"WS": {Path: "/tmp/workspace"},
		},
	}); err != nil {
		t.Fatalf("SaveStateCache: %v", err)
	}
	got, err := ListWorkspaces()
	if err != nil {
		t.Fatalf("ListWorkspaces: %v", err)
	}
	if got["WS"] != "/tmp/workspace" {
		t.Fatalf("ListWorkspaces = %+v, want WS path", got)
	}
}

func TestSendControlRequestResponseBranches(t *testing.T) {
	socketDir, err := os.MkdirTemp("/tmp", "loom-dw-")
	if err != nil {
		t.Fatalf("mktemp socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socketPath := filepath.Join(socketDir, "daemon.sock")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	defer ln.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if !bufio.NewScanner(conn).Scan() {
			return
		}
		_ = json.NewEncoder(conn).Encode(agentcontrol.AgentControlResult{Success: true, Data: json.RawMessage(`{"message":"ok"}`)})
	}()

	got, err := sendControlRequest(socketPath, "agent_stop", "nova", true, time.Second)
	if err != nil {
		t.Fatalf("sendControlRequest success: %v", err)
	}
	if got == nil || !got.Success || string(got.Data) != `{"message":"ok"}` {
		t.Fatalf("control result = %+v", got)
	}
	<-done

	badJSONSocket := filepath.Join(socketDir, "bad.sock")
	badLn, err := net.Listen("unix", badJSONSocket)
	if err != nil {
		t.Fatalf("listen bad unix: %v", err)
	}
	defer badLn.Close()
	go func() {
		conn, err := badLn.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = bufio.NewReader(conn).ReadString('\n')
		_, _ = conn.Write([]byte("{bad json}\n"))
	}()
	if _, err := sendControlRequest(badJSONSocket, "agent_list", "", false, time.Second); err == nil {
		t.Fatal("sendControlRequest bad JSON err = nil")
	}

	if _, err := sendControlRequest(filepath.Join(t.TempDir(), "missing.sock"), "agent_list", "", false, time.Millisecond); err == nil {
		t.Fatal("missing control socket err = nil")
	}
}

func TestBuildDaemonSupervisorFnReadsState(t *testing.T) {
	projectDir := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldwd) })

	statePath := config.ResolveDaemonStatePath(projectDir)
	if err := os.MkdirAll(filepath.Dir(statePath), 0755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	state := daemon.DaemonState{
		PID:       123,
		StartedAt: time.Now().Add(-time.Minute),
		Agents: []daemon.DaemonAgentStatus{{
			Worktree:     "nova",
			Role:         "planner",
			Status:       "running",
			RestartCount: 2,
		}},
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if err := os.WriteFile(statePath, data, 0600); err != nil {
		t.Fatalf("write state: %v", err)
	}

	fn := BuildDaemonSupervisorFn()
	if fn == nil {
		t.Fatal("BuildDaemonSupervisorFn returned nil")
	}
	got, err := fn()
	if err != nil {
		t.Fatalf("supervisor fn: %v", err)
	}
	if got.PID != 123 || len(got.Agents) != 1 || got.Agents[0].Worktree != "nova" {
		t.Fatalf("supervisor data = %+v", got)
	}
}

func TestBuildStoreBackedDaemonConfigFnProjectsStoreData(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	t.Cleanup(func() { _ = st.Close() })
	t.Setenv(bootstrap.EnvWorkspace, "WS")

	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	maxAgents := 3
	maxRetries := 4
	readOnly := true
	budget := 2.5
	traces := true
	metrics := false
	if _, err := st.Daemon().Upsert(ctx, &domain.DaemonProfile{
		WorkspaceKey:   "WS",
		PIDFile:        "/tmp/daemon.pid",
		LogDir:         "/tmp/logs",
		EventsDir:      "/tmp/events",
		IssueBackend:   "fleetdb",
		MaxAgents:      &maxAgents,
		StartupTimeout: config.IntPtr(12),
		RestartPolicy:  domain.RestartPolicy{MaxRetries: &maxRetries},
		OTel:           &domain.OTelSettings{Enabled: true, Endpoint: "http://otel", Protocol: "grpc", ServiceName: "loom", SampleRate: 0.5, FlushIntervalMs: 250, Traces: &traces, Metrics: &metrics},
	}); err != nil {
		t.Fatalf("upsert daemon: %v", err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey:   "WS",
		Name:           "task",
		Description:    "Task role",
		PromptFile:     "task.md",
		Model:          "gpt",
		TaskFilter:     "has_design",
		Backend:        "codex",
		PathPatterns:   []string{"*.go"},
		Skills:         []string{"go"},
		MaxPriority:    config.IntPtr(2),
		MaxConcurrency: config.IntPtr(1),
		ReadOnly:       readOnly,
		AllowedTools:   []string{"git"},
		DeniedTools:    []string{"rm"},
		MaxBudgetUSD:   &budget,
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey:     "WS",
		Name:             "nova",
		RoleName:         "task",
		Auto:             true,
		Backend:          "codex",
		FallbackBackends: []string{"claude"},
		Repos:            []string{"api"},
		RepoGroups:       []string{"backend"},
		CrossRepo:        true,
		Parent:           "EPIC-1",
		DesiredState:     domain.AgentDesiredRunning,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	fn := BuildStoreBackedDaemonConfigFn(st)
	if fn == nil {
		t.Fatal("BuildStoreBackedDaemonConfigFn returned nil")
	}
	raw, err := fn()
	if err != nil {
		t.Fatalf("store-backed config fn: %v", err)
	}
	var got config.DaemonConfig
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode daemon config: %v", err)
	}
	if got.Backend != "fleetdb" || got.Daemon.PIDFile != "/tmp/daemon.pid" || got.Daemon.RestartPolicy.MaxRetries == nil || *got.Daemon.RestartPolicy.MaxRetries != 4 {
		t.Fatalf("daemon projection = %+v", got.Daemon)
	}
	if got.Daemon.OTel == nil || !got.Daemon.OTel.Enabled || got.Daemon.OTel.Traces == nil || got.Daemon.OTel.Metrics == nil {
		t.Fatalf("otel projection = %+v", got.Daemon.OTel)
	}
	if role := got.Roles["task"]; role.Backend != "codex" || !role.ReadOnly || role.MaxBudgetUSD == nil || *role.MaxBudgetUSD != budget {
		t.Fatalf("role projection = %+v", role)
	}
	if len(got.Agents) != 1 || got.Agents[0].Worktree != "nova" || !got.Agents[0].CrossRepo || got.Agents[0].DesiredState != domain.AgentDesiredRunning {
		t.Fatalf("agent projection = %+v", got.Agents)
	}
}
