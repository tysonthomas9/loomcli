package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/lockfile"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func captureDaemonStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = old
		_ = r.Close()
	}()

	fn()
	_ = w.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("copy stdout: %v", err)
	}
	return buf.String()
}

func withDaemonTempCwd(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return withDaemonCwd(t, dir)
}

func withShortDaemonTempCwd(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "loomd-")
	if err != nil {
		t.Fatalf("mktemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return withDaemonCwd(t, dir)
}

func withDaemonCwd(t *testing.T, dir string) string {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
	t.Setenv("LOOM_CONFIG_DIR", filepath.Join(dir, "config"))
	t.Setenv("LOOM_WORKSPACE", "")
	return dir
}

func TestDaemonPathPIDLockAndCleanupHelpers(t *testing.T) {
	projectDir := t.TempDir()
	cfg := &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{
		PIDFile:   ".loom/run/daemon.pid",
		LogDir:    ".loom/logs",
		EventsDir: ".loom/events",
	}}

	paths := resolveDaemonPaths(projectDir, cfg)
	if paths.pidFile != filepath.Join(projectDir, ".loom/run/daemon.pid") {
		t.Fatalf("pidFile = %q", paths.pidFile)
	}
	if paths.lockFile != filepath.Join(projectDir, ".loom/run/daemon.lock") {
		t.Fatalf("lockFile = %q", paths.lockFile)
	}
	if paths.eventsDir != filepath.Join(projectDir, ".loom/events") {
		t.Fatalf("eventsDir = %q", paths.eventsDir)
	}

	prepareDaemonDirs(paths.pidFile, paths.logDir)
	initPIDFile(paths.pidFile)
	pidData, err := os.ReadFile(paths.pidFile)
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}
	if !strings.Contains(string(pidData), "\n") {
		t.Fatalf("pid file should include trailing newline, got %q", string(pidData))
	}

	lockFile := acquireDaemonLock(paths.lockFile)
	lockData, err := os.ReadFile(paths.lockFile)
	if err != nil {
		t.Fatalf("read lock file: %v", err)
	}
	var info lockfile.LockInfo
	if err := json.Unmarshal(lockData, &info); err != nil {
		t.Fatalf("lock file JSON: %v", err)
	}
	if info.PID != os.Getpid() {
		t.Fatalf("lock PID = %d, want %d", info.PID, os.Getpid())
	}

	if err := os.WriteFile(paths.stateFile, []byte("{}"), 0600); err != nil {
		t.Fatalf("write state file: %v", err)
	}
	cleanupOnStartFailure(paths.pidFile, paths.stateFile, lockFile, paths.lockFile)
	for _, path := range []string{paths.pidFile, paths.stateFile, paths.lockFile} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s still exists or unexpected stat error: %v", path, err)
		}
	}
}

func TestDaemonCommandNoDaemonPaths(t *testing.T) {
	withDaemonTempCwd(t)

	statusOut := captureDaemonStdout(t, func() { runDaemonStatus(&cobra.Command{}, nil) })
	if !strings.Contains(statusOut, "Daemon: not running") {
		t.Fatalf("status output = %q", statusOut)
	}

	stopOut := captureDaemonStdout(t, func() { runDaemonStop(&cobra.Command{}, nil) })
	if !strings.Contains(stopOut, "Daemon is not running.") {
		t.Fatalf("stop output = %q", stopOut)
	}

	socketPath, err := resolveControlSocketFromCwd()
	if err != nil {
		t.Fatalf("resolve control socket: %v", err)
	}
	if !strings.HasSuffix(socketPath, filepath.Join(".loom", "daemon.sock")) {
		t.Fatalf("socket path = %q", socketPath)
	}
}

func TestDaemonStatusDisplaysRunningStateFile(t *testing.T) {
	projectDir := withDaemonTempCwd(t)
	statePath := cfgpkg.ResolveDaemonStatePath(projectDir)
	if err := os.MkdirAll(filepath.Dir(statePath), 0755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	state := DaemonState{
		PID:       os.Getpid(),
		StartedAt: time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC),
		Agents: []DaemonAgentStatus{{
			Worktree:       "falcon",
			Role:           "task",
			PID:            os.Getpid(),
			Status:         "running",
			TaskID:         "loom-123",
			RestartCount:   2,
			LastStart:      time.Date(2026, 5, 19, 12, 1, 0, 0, time.UTC),
			CurrentBackend: "codex",
		}},
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if err := os.WriteFile(statePath, data, 0600); err != nil {
		t.Fatalf("write state: %v", err)
	}

	out := captureDaemonStdout(t, func() { runDaemonStatus(&cobra.Command{}, nil) })
	for _, want := range []string{"Daemon: running", "Agents: 1", "falcon", "loom-123"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output missing %q:\n%s", want, out)
		}
	}
}

func TestDaemonStartStopAndAgentStoreLookup(t *testing.T) {
	projectDir := withDaemonTempCwd(t)
	ctx := context.Background()
	st := memstore.New()
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{WorkspaceKey: "WS", Name: "nova", RoleName: "task"}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	cfg := &cfgpkg.DaemonConfig{
		Daemon: cfgpkg.DaemonSettings{
			PIDFile:   filepath.Join(projectDir, ".loom", "daemon.pid"),
			LogDir:    filepath.Join(projectDir, ".loom", "logs"),
			EventsDir: filepath.Join(projectDir, ".loom", "events"),
		},
	}
	d, err := NewDaemon(cfg, projectDir, nil, nil, st)
	if err != nil {
		t.Fatalf("NewDaemon: %v", err)
	}
	d.sup.WorkspaceID = "WS"
	if !d.agentExistsInStore("nova") {
		t.Fatalf("agentExistsInStore(nova) = false, want true")
	}
	if d.agentExistsInStore("missing") {
		t.Fatalf("agentExistsInStore(missing) = true, want false")
	}
	if !d.agentExistsInConfig("nova") {
		t.Fatalf("agentExistsInConfig should consult store")
	}
	if got := d.Agents(); len(got) != 0 {
		t.Fatalf("Agents() = %#v, want none before configured agents", got)
	}

	if err := d.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if d.configHash == "" {
		t.Fatalf("Start did not set config hash")
	}
	d.Stop()
	d.Stop()
}

func TestRunDaemonMainLoopStartsAndStopsEmptyDaemon(t *testing.T) {
	projectDir := withShortDaemonTempCwd(t)
	st := memstore.New()
	t.Cleanup(func() { _ = st.Close() })

	cfg := &cfgpkg.DaemonConfig{
		Daemon: cfgpkg.DaemonSettings{
			PIDFile:   filepath.Join(projectDir, ".loom", "daemon.pid"),
			LogDir:    filepath.Join(projectDir, ".loom", "logs"),
			EventsDir: filepath.Join(projectDir, ".loom", "events"),
		},
	}
	paths := resolveDaemonPaths(projectDir, cfg)
	prepareDaemonDirs(paths.pidFile, paths.logDir)
	lockFile := acquireDaemonLock(paths.lockFile)
	t.Cleanup(func() {
		_ = lockFile.Close()
		_ = os.Remove(paths.lockFile)
	})

	d, err := NewDaemon(cfg, projectDir, nil, nil, st)
	if err != nil {
		t.Fatalf("NewDaemon: %v", err)
	}
	shutdown := make(chan struct{})
	done := make(chan struct{})
	go func() {
		runDaemonMainLoop(cfg, projectDir, paths, shutdown, d, lockFile)
		close(done)
	}()

	waitForFile(t, paths.stateFile)
	close(shutdown)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("daemon main loop did not stop")
	}
	if _, err := ReadStateFile(paths.stateFile); err != nil {
		t.Fatalf("ReadStateFile after shutdown: %v", err)
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func TestRunDaemonConfigPrintsDefaultConfig(t *testing.T) {
	withDaemonTempCwd(t)

	out := captureDaemonStdout(t, func() { runDaemonConfig(&cobra.Command{}, nil) })
	if !strings.Contains(out, "pid_file: .loom/daemon.pid") {
		t.Fatalf("config output missing default pid file: %s", out)
	}
	if !strings.Contains(out, "max_agents: 20") {
		t.Fatalf("config output missing default max agents: %s", out)
	}
}

func TestRunDaemonDryRunAndStopAgentWrapper(t *testing.T) {
	projectDir := withShortDaemonTempCwd(t)
	oldDryRun, oldStopForce, oldStopTimeout := daemonDryRun, daemonStopForce, daemonStopTimeout
	oldIsolate := isolateProcessGroupFn
	oldGetwd := daemonGetwdFn
	oldLoadConfig := loadDaemonConfigFn
	t.Cleanup(func() {
		daemonDryRun, daemonStopForce, daemonStopTimeout = oldDryRun, oldStopForce, oldStopTimeout
		isolateProcessGroupFn = oldIsolate
		daemonGetwdFn = oldGetwd
		loadDaemonConfigFn = oldLoadConfig
	})

	daemonDryRun = true
	isolateProcessGroupFn = func() {}
	daemonGetwdFn = func() (string, error) { return projectDir, nil }
	loadDaemonConfigFn = func(string) (*DaemonConfig, error) {
		return &DaemonConfig{Agents: []AgentEntry{{Worktree: "falcon", Role: "task"}}}, nil
	}
	out := captureDaemonStdout(t, func() { runDaemon(&cobra.Command{}, nil) })
	if !strings.Contains(out, "DRY RUN") {
		t.Fatalf("dry-run output = %q", out)
	}

	socketPath := filepath.Join(projectDir, ".loom", "daemon.sock")
	stopServer := startFakeDaemonControlServer(t, socketPath, func(req DaemonControlRequest) DaemonControlResponse {
		if req.Operation == ctrlOpAgentList {
			data, _ := json.Marshal([]AgentListEntry{{Name: "falcon", Status: "stopped"}})
			return DaemonControlResponse{Success: true, Data: data}
		}
		return DaemonControlResponse{Success: true}
	})
	defer stopServer()

	cmd := &cobra.Command{}
	cmd.Flags().IntVarP(&daemonStopTimeout, "timeout", "t", 0, "")
	if err := cmd.Flags().Set("timeout", "1"); err != nil {
		t.Fatalf("set timeout: %v", err)
	}
	daemonStopForce = true
	out = captureDaemonStdout(t, func() { runDaemonStop(cmd, []string{"falcon"}) })
	if !strings.Contains(out, `Agent "falcon" stopped.`) {
		t.Fatalf("agent stop wrapper output = %q", out)
	}
}

func TestSetupSignalHandlerClosesOnSignal(t *testing.T) {
	shutdown := setupSignalHandler()
	if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatalf("send SIGHUP: %v", err)
	}
	select {
	case <-shutdown:
	case <-time.After(2 * time.Second):
		t.Fatal("signal handler did not close shutdown channel")
	}
}

func TestDaemonAgentControlCommandsSuccessPaths(t *testing.T) {
	withShortDaemonTempCwd(t)
	socketPath, err := resolveControlSocketFromCwd()
	if err != nil {
		t.Fatalf("resolve socket: %v", err)
	}

	var mu sync.Mutex
	var requests []DaemonControlRequest
	stopServer := startFakeDaemonControlServer(t, socketPath, func(req DaemonControlRequest) DaemonControlResponse {
		mu.Lock()
		requests = append(requests, req)
		mu.Unlock()
		switch req.Operation {
		case ctrlOpAgentList:
			data, _ := json.Marshal([]AgentListEntry{{Name: req.AgentName, Status: "stopped"}})
			return DaemonControlResponse{Success: true, Data: data}
		default:
			return DaemonControlResponse{Success: true}
		}
	})
	defer stopServer()

	out := captureDaemonStdout(t, func() { runDaemonAgentStart(&cobra.Command{}, []string{"falcon"}) })
	if !strings.Contains(out, `Agent "falcon" started.`) {
		t.Fatalf("start output = %q", out)
	}
	out = captureDaemonStdout(t, func() { runDaemonAgentRestart(&cobra.Command{}, []string{"falcon"}) })
	if !strings.Contains(out, `Agent "falcon" restarted.`) {
		t.Fatalf("restart output = %q", out)
	}
	out = captureDaemonStdout(t, func() { runDaemonAgentStop("falcon", true, time.Minute) })
	if !strings.Contains(out, `Agent "falcon" stopped.`) {
		t.Fatalf("force stop output = %q", out)
	}
	out = captureDaemonStdout(t, func() { runDaemonAgentStop("falcon", false, time.Minute) })
	if !strings.Contains(out, "stopped gracefully") {
		t.Fatalf("graceful stop output = %q", out)
	}

	mu.Lock()
	defer mu.Unlock()
	seen := map[string]bool{}
	for _, req := range requests {
		seen[req.Operation] = true
	}
	for _, op := range []string{ctrlOpAgentStart, ctrlOpAgentRestart, ctrlOpAgentStop, ctrlOpAgentYield, ctrlOpAgentList} {
		if !seen[op] {
			t.Fatalf("operation %s was not sent; requests=%+v", op, requests)
		}
	}
}

func TestDaemonAgentControlFallbackBranches(t *testing.T) {
	projectDir := withShortDaemonTempCwd(t)
	socketPath := filepath.Join(projectDir, ".loom", "daemon.sock")
	phase := "not-running"
	stopServer := startFakeDaemonControlServer(t, socketPath, func(req DaemonControlRequest) DaemonControlResponse {
		switch phase {
		case "not-running":
			return DaemonControlResponse{Success: false, Error: "agent is not running"}
		case "force-not-found":
			return DaemonControlResponse{Success: false, Error: "agent not found"}
		default:
			if req.Operation == ctrlOpAgentList {
				data, _ := json.Marshal([]AgentListEntry{{Name: "falcon", Status: "running"}})
				return DaemonControlResponse{Success: true, Data: data}
			}
			return DaemonControlResponse{Success: true}
		}
	})
	defer stopServer()

	out := captureDaemonStdout(t, func() {
		if requestYieldOrFallback(socketPath, "falcon") {
			t.Fatal("not-running yield should not continue to polling")
		}
	})
	if !strings.Contains(out, "not running") {
		t.Fatalf("not-running output = %q", out)
	}

	phase = "force-not-found"
	out = captureDaemonStdout(t, func() { forceStopAgent(socketPath, "falcon") })
	if !strings.Contains(out, `Agent "falcon" stopped.`) {
		t.Fatalf("force not-found output = %q", out)
	}

	phase = "running"
	if !isAgentRunningViaSocket(socketPath, "falcon") {
		t.Fatal("isAgentRunningViaSocket should report running agent")
	}
	if isAgentRunningViaSocket(filepath.Join(projectDir, ".loom", "missing.sock"), "falcon") {
		t.Fatal("missing socket should be treated as not running")
	}
}

func TestDaemonControlTimeoutAndProcessStopHelpers(t *testing.T) {
	projectDir := withShortDaemonTempCwd(t)
	socketPath := filepath.Join(projectDir, ".loom", "daemon.sock")
	var mu sync.Mutex
	var requests []DaemonControlRequest
	stopServer := startFakeDaemonControlServer(t, socketPath, func(req DaemonControlRequest) DaemonControlResponse {
		mu.Lock()
		requests = append(requests, req)
		mu.Unlock()
		return DaemonControlResponse{Success: true}
	})
	defer stopServer()

	out := captureDaemonStdout(t, func() { pollAndForceStop(socketPath, "falcon", -time.Nanosecond) })
	if !strings.Contains(out, "Yield timeout") || !strings.Contains(out, `Agent "falcon" stopped.`) {
		t.Fatalf("pollAndForceStop output = %q", out)
	}

	phase := "yield-error"
	stopServer2 := startFakeDaemonControlServer(t, filepath.Join(projectDir, ".loom", "daemon2.sock"), func(req DaemonControlRequest) DaemonControlResponse {
		if phase == "yield-error" && req.Operation == ctrlOpAgentYield {
			return DaemonControlResponse{Success: false, Error: "temporary control failure"}
		}
		return DaemonControlResponse{Success: true}
	})
	defer stopServer2()
	if requestYieldOrFallback(filepath.Join(projectDir, ".loom", "daemon2.sock"), "nova") {
		t.Fatal("yield fallback should force-stop and stop polling")
	}

	for _, tc := range []struct {
		name string
		stop func(int)
		want string
	}{
		{name: "force", stop: stopDaemonForce, want: "Force-stopping daemon"},
		{name: "graceful", stop: stopDaemonGraceful, want: "Stopping daemon"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pid := startDetachedSleepProcess(t)
			out := captureDaemonStdout(t, func() { tc.stop(pid) })
			if !strings.Contains(out, tc.want) {
				t.Fatalf("%s output = %q", tc.name, out)
			}
		})
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) == 0 || requests[len(requests)-1].Operation != ctrlOpAgentStop {
		t.Fatalf("pollAndForceStop requests = %+v", requests)
	}
}

func startDetachedSleepProcess(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sh", "-c", "sleep 60 >/dev/null 2>&1 & echo $!") //nolint:gosec //nolint:norawexec // test helper starts a controlled sleep process
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("start detached sleep: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || pid <= 0 {
		t.Fatalf("parse sleep pid from %q: %v", string(out), err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	return pid
}

func startFakeDaemonControlServer(t *testing.T, socketPath string, handler func(DaemonControlRequest) DaemonControlResponse) func() {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(socketPath), 0755); err != nil {
		t.Fatalf("mkdir socket dir: %v", err)
	}
	_ = os.Remove(socketPath)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix socket: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				scanner := bufio.NewScanner(conn)
				if !scanner.Scan() {
					return
				}
				var req DaemonControlRequest
				if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
					return
				}
				resp := handler(req)
				data, _ := json.Marshal(resp)
				data = append(data, '\n')
				_, _ = conn.Write(data)
			}(conn)
		}
	}()
	return func() {
		_ = ln.Close()
		<-done
		_ = os.Remove(socketPath)
	}
}

func TestApplyProfileFieldAllKeysAndErrors(t *testing.T) {
	p := &domain.DaemonProfile{}

	stringCases := map[string]*string{
		"pid_file":      &p.PIDFile,
		"log_dir":       &p.LogDir,
		"events_dir":    &p.EventsDir,
		"issue_backend": &p.IssueBackend,
	}
	for key, field := range stringCases {
		if err := applyProfileField(p, key, "value-"+key, false); err != nil {
			t.Fatalf("apply %s: %v", key, err)
		}
		if *field != "value-"+key {
			t.Fatalf("%s = %q", key, *field)
		}
		if err := applyProfileField(p, key, "", true); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
		if *field != "" {
			t.Fatalf("%s after unset = %q", key, *field)
		}
	}

	if err := applyProfileField(p, "max_agents", "7", false); err != nil {
		t.Fatalf("max_agents: %v", err)
	}
	if p.MaxAgents == nil || *p.MaxAgents != 7 {
		t.Fatalf("MaxAgents = %#v", p.MaxAgents)
	}
	if err := applyProfileField(p, "max_agents", "", true); err != nil {
		t.Fatalf("unset max_agents: %v", err)
	}
	if p.MaxAgents != nil {
		t.Fatalf("MaxAgents after unset = %#v", p.MaxAgents)
	}

	if err := applyProfileField(p, "startup_timeout", "12", false); err != nil {
		t.Fatalf("startup_timeout: %v", err)
	}
	if p.StartupTimeout == nil || *p.StartupTimeout != 12 {
		t.Fatalf("StartupTimeout = %#v", p.StartupTimeout)
	}
	if err := applyProfileField(p, "startup_timeout", "", true); err != nil {
		t.Fatalf("unset startup_timeout: %v", err)
	}
	if p.StartupTimeout != nil {
		t.Fatalf("StartupTimeout after unset = %#v", p.StartupTimeout)
	}

	if err := applyProfileField(p, "max_agents", "not-int", false); err == nil {
		t.Fatal("expected max_agents parse error")
	}
	if err := applyProfileField(p, "startup_timeout", "not-int", false); err == nil {
		t.Fatal("expected startup_timeout parse error")
	}
	if err := applyProfileField(p, "unknown", "x", false); err == nil {
		t.Fatal("expected unknown key error")
	}
}

func TestDaemonProfileCommandsAgainstLocalStore(t *testing.T) {
	requireDaemonFleetDB(t)
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	t.Setenv(bootstrap.EnvWorkspace, "WS")
	t.Setenv(bootstrap.EnvFleetDBActor, "daemon-profile-test")
	t.Setenv(bootstrap.EnvFleetDBURL, "")

	ctx := context.Background()
	handle, err := bootstrap.OpenStore(ctx, configDir, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, err := handle.Store.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := handle.Close(); err != nil {
		t.Fatalf("close setup store: %v", err)
	}

	oldWorkspace, oldJSON := daemonProfileWorkspace, daemonProfileShowJSON
	t.Cleanup(func() {
		daemonProfileWorkspace, daemonProfileShowJSON = oldWorkspace, oldJSON
	})
	daemonProfileWorkspace = "WS"
	daemonProfileShowJSON = false

	out := captureDaemonStdout(t, func() {
		if err := runDaemonProfileShow(&cobra.Command{}, nil); err != nil {
			t.Fatalf("profile show: %v", err)
		}
	})
	if !strings.Contains(out, "Workspace:      WS") || !strings.Contains(out, "Issue backend:  fleetdb") {
		t.Fatalf("profile show output = %q", out)
	}

	out = captureDaemonStdout(t, func() {
		if err := runDaemonProfileSet(&cobra.Command{}, []string{"pid_file", ".loom/custom.pid"}); err != nil {
			t.Fatalf("profile set pid_file: %v", err)
		}
		if err := runDaemonProfileSet(&cobra.Command{}, []string{"max_agents", "3"}); err != nil {
			t.Fatalf("profile set max_agents: %v", err)
		}
	})
	if !strings.Contains(out, "Set WS.pid_file") || !strings.Contains(out, "Set WS.max_agents") {
		t.Fatalf("profile set output = %q", out)
	}

	out = captureDaemonStdout(t, func() {
		if err := runDaemonProfileShow(&cobra.Command{}, nil); err != nil {
			t.Fatalf("profile show after set: %v", err)
		}
	})
	if !strings.Contains(out, "PID file:       .loom/custom.pid") || !strings.Contains(out, "Max agents:     3") {
		t.Fatalf("profile show after set output = %q", out)
	}

	daemonProfileShowJSON = true
	out = captureDaemonStdout(t, func() {
		if err := runDaemonProfileShow(&cobra.Command{}, nil); err != nil {
			t.Fatalf("profile show json: %v", err)
		}
	})
	if !strings.Contains(out, `"workspace_key": "WS"`) || !strings.Contains(out, `"max_agents": 3`) {
		t.Fatalf("profile show json output = %q", out)
	}

	out = captureDaemonStdout(t, func() {
		if err := runDaemonProfileUnset(&cobra.Command{}, []string{"max_agents"}); err != nil {
			t.Fatalf("profile unset max_agents: %v", err)
		}
	})
	if !strings.Contains(out, "Cleared WS.max_agents") {
		t.Fatalf("profile unset output = %q", out)
	}
	verifyHandle, err := bootstrap.OpenStore(ctx, configDir, nil)
	if err != nil {
		t.Fatalf("open verify store: %v", err)
	}
	defer verifyHandle.Close()
	profile, err := verifyHandle.Store.Daemon().Get(ctx, "WS")
	if err != nil {
		t.Fatalf("get profile: %v", err)
	}
	if profile.MaxAgents != nil {
		t.Fatalf("MaxAgents after unset = %#v", profile.MaxAgents)
	}

	daemonProfileWorkspace = ""
	var resolved string
	if err := withDaemonWorkspace(func(_ context.Context, _ *bootstrap.StoreHandle, ws string) error {
		resolved = ws
		return nil
	}); err != nil {
		t.Fatalf("withDaemonWorkspace active: %v", err)
	}
	if resolved != "WS" {
		t.Fatalf("resolved workspace = %q, want WS", resolved)
	}
}

func requireDaemonFleetDB(t *testing.T) {
	t.Helper()
	if os.Getenv("FLEET_DB_BIN") != "" {
		return
	}
	if _, err := exec.LookPath("fleet-db"); err != nil {
		t.Skip("fleet-db binary not available")
	}
}

func TestDaemonLogsPureHelpers(t *testing.T) {
	projectDir := withDaemonTempCwd(t)
	cfg := loadDaemonLogsConfig(projectDir)
	if cfg.Daemon.LogDir == "" {
		t.Fatal("expected fallback/default log dir")
	}

	logDir := filepath.Join(projectDir, ".loom", "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		t.Fatalf("mkdir log dir: %v", err)
	}
	logPath := filepath.Join(logDir, "task-falcon.log")
	if err := os.WriteFile(logPath, []byte("one\ntwo\n"), 0600); err != nil {
		t.Fatalf("write log: %v", err)
	}
	state := &DaemonState{Agents: []DaemonAgentStatus{
		{Worktree: "falcon", Role: "task"},
		{Worktree: "nova", Role: "plan"},
	}}

	out := captureDaemonStdout(t, func() { listAgentLogs(projectDir, cfg, state, nil) })
	if !strings.Contains(out, "falcon") || !strings.Contains(out, "exists") {
		t.Fatalf("list output = %q", out)
	}
	if got := findAgent("nova", state, nil); got.Role != "plan" {
		t.Fatalf("findAgent role = %q", got.Role)
	}

	oldLines, oldFollow := daemonLogsLines, daemonLogsFollow
	t.Cleanup(func() {
		daemonLogsLines, daemonLogsFollow = oldLines, oldFollow
	})
	daemonLogsLines, daemonLogsFollow = 1, false
	logOut := captureDaemonStdout(t, func() { showAgentLog(logPath) })
	if strings.Contains(logOut, "one") || !strings.Contains(logOut, "two") {
		t.Fatalf("showAgentLog output = %q", logOut)
	}

	emptyPath := filepath.Join(logDir, "task-empty.log")
	if err := os.WriteFile(emptyPath, nil, 0600); err != nil {
		t.Fatalf("write empty log: %v", err)
	}
	emptyOut := captureDaemonStdout(t, func() { showAgentLog(emptyPath) })
	if !strings.Contains(emptyOut, "(empty log file)") {
		t.Fatalf("empty log output = %q", emptyOut)
	}
}

func TestRunDaemonLogsWrapperAndFollowCancel(t *testing.T) {
	projectDir := withDaemonTempCwd(t)
	logDir := filepath.Join(projectDir, ".loom", "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		t.Fatalf("mkdir logs: %v", err)
	}
	logPath := filepath.Join(logDir, "task-falcon.log")
	if err := os.WriteFile(logPath, []byte("alpha\nbeta\n"), 0600); err != nil {
		t.Fatalf("write log: %v", err)
	}
	statePath := cfgpkg.ResolveDaemonStatePath(projectDir)
	if err := os.MkdirAll(filepath.Dir(statePath), 0755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	state := DaemonState{PID: os.Getpid(), StartedAt: time.Now(), Agents: []DaemonAgentStatus{{Worktree: "falcon", Role: "task"}}}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if err := os.WriteFile(statePath, data, 0600); err != nil {
		t.Fatalf("write state: %v", err)
	}

	oldLines, oldFollow := daemonLogsLines, daemonLogsFollow
	t.Cleanup(func() {
		daemonLogsLines, daemonLogsFollow = oldLines, oldFollow
	})
	daemonLogsLines, daemonLogsFollow = 5, false

	out := captureDaemonStdout(t, func() { runDaemonLogs(&cobra.Command{}, nil) })
	if !strings.Contains(out, "Available agents") || !strings.Contains(out, "falcon") {
		t.Fatalf("logs list output = %q", out)
	}
	out = captureDaemonStdout(t, func() { runDaemonLogs(&cobra.Command{}, []string{"falcon"}) })
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Fatalf("logs show output = %q", out)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := followLogFile(ctx, logPath); err != nil {
		t.Fatalf("followLogFile canceled: %v", err)
	}
}

func TestHandleLogEventReadsWritesAndReopens(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "agent.log")
	if err := os.WriteFile(path, []byte("first"), 0600); err != nil {
		t.Fatalf("write log: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer f.Close()
	buf := make([]byte, 32)

	unchangedF, unchangedOffset := handleLogEvent(f, fsnotify.Event{Name: filepath.Join(tmp, "other.log"), Op: fsnotify.Write}, path, "agent.log", buf, 2)
	if unchangedF != f || unchangedOffset != 2 {
		t.Fatalf("unrelated event changed file/offset")
	}
	unchangedF, unchangedOffset = handleLogEvent(f, fsnotify.Event{Name: path, Op: fsnotify.Remove}, path, "agent.log", buf, 2)
	if unchangedF != f || unchangedOffset != 2 {
		t.Fatalf("non-write event changed file/offset")
	}

	writeOut := captureDaemonStdout(t, func() {
		var next *os.File
		next, unchangedOffset = handleLogEvent(f, fsnotify.Event{Name: path, Op: fsnotify.Write}, path, "agent.log", buf, 0)
		if next != f {
			t.Fatalf("write event should keep existing file")
		}
	})
	if writeOut != "first" || unchangedOffset != int64(len("first")) {
		t.Fatalf("write event output=%q offset=%d", writeOut, unchangedOffset)
	}

	if err := os.WriteFile(path, []byte("second"), 0600); err != nil {
		t.Fatalf("rewrite log: %v", err)
	}
	var reopened *os.File
	createOut := captureDaemonStdout(t, func() {
		reopened, unchangedOffset = handleLogEvent(f, fsnotify.Event{Name: path, Op: fsnotify.Create}, path, "agent.log", buf, 99)
	})
	if reopened == nil || reopened == f {
		t.Fatal("create event should reopen file")
	}
	defer reopened.Close()
	if createOut != "second" || unchangedOffset != int64(len("second")) {
		t.Fatalf("create event output=%q offset=%d", createOut, unchangedOffset)
	}
}

func TestQueueScoringAndPrintingHelpers(t *testing.T) {
	maxPriority := 3
	issues := []backend.IssueData{
		{ID: "LOOM-3", Title: "lower priority", Status: "open", Priority: 3, Labels: []string{"go"}, SourceRepo: "repo-a"},
		{ID: "LOOM-1", Title: "best task", Status: "open", Priority: 1, Labels: []string{"go"}, SourceRepo: "repo-a"},
		{ID: "LOOM-2", Title: "closed", Status: "closed", Priority: 1, Labels: []string{"go"}, SourceRepo: "repo-a"},
		{ID: "LOOM-4", Title: "repo miss", Status: "open", Priority: 1, Labels: []string{"go"}, SourceRepo: "repo-b"},
		{ID: "LOOM-5", Title: "too low", Status: "open", Priority: 4, Labels: []string{"go"}, SourceRepo: "repo-a"},
	}
	matched, rejections := scoreQueueCandidates(issues, cli.RoleConstraints{
		TaskFilter:  "any",
		Skills:      []string{"go"},
		MaxPriority: &maxPriority,
		SourceRepos: []string{"repo-a"},
	})
	if len(matched) != 3 {
		t.Fatalf("matched len = %d, want 3", len(matched))
	}
	if matched[0].Issue.ID != "LOOM-1" {
		t.Fatalf("first match = %s, want LOOM-1", matched[0].Issue.ID)
	}
	if rejections["not open"] != 1 || rejections["priority 4 exceeds max 3"] != 1 {
		t.Fatalf("rejections = %#v", rejections)
	}

	out := captureDaemonStdout(t, func() {
		printQueueResults(matched, rejections)
		printQueueHeader("falcon", &cfgpkg.AgentEntry{Worktree: "falcon", Role: "task", Parent: "EPIC-1"}, cli.RoleConstraints{
			Skills:      []string{"go", "tests"},
			MaxPriority: &maxPriority,
			SourceRepos: []string{"repo-a"},
		})
	})
	for _, want := range []string{"tasks match", "LOOM-1", "filtered", "Agent: falcon", "Epic: EPIC-1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("queue output missing %q: %s", want, out)
		}
	}
}
