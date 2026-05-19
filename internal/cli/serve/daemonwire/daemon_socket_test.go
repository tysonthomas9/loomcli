package daemonwire

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	daemonpkg "github.com/tysonthomas9/loomcli/internal/cli/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/agentcontrol"
)

func TestSendControlRequestSuccessAndErrors(t *testing.T) {
	socketPath := shortDaemonwireSocketPath(t, "daemon.sock")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	defer ln.Close()

	reqCh := make(chan map[string]any, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		line, _ := bufio.NewReader(conn).ReadBytes('\n')
		var req map[string]any
		_ = json.Unmarshal(line, &req)
		reqCh <- req
		_ = json.NewEncoder(conn).Encode(agentcontrol.AgentControlResult{Success: true, Data: json.RawMessage(`{"ok":true}`)})
	}()

	got, err := sendControlRequest(socketPath, "agent_stop", "worker", true, time.Second)
	if err != nil {
		t.Fatalf("sendControlRequest: %v", err)
	}
	if !got.Success || string(got.Data) != `{"ok":true}` {
		t.Fatalf("result = %+v", got)
	}
	req := <-reqCh
	if req["operation"] != "agent_stop" || req["agent_name"] != "worker" || req["force"] != true {
		t.Fatalf("request = %#v", req)
	}

	if _, err := sendControlRequest(filepath.Join(t.TempDir(), "missing.sock"), "agent_list", "", false, time.Millisecond); err == nil {
		t.Fatal("missing socket error = nil")
	}
}

func TestSendControlRequestEmptyAndInvalidResponses(t *testing.T) {
	t.Run("empty response", func(t *testing.T) {
		socketPath := shortDaemonwireSocketPath(t, "empty.sock")
		ln, err := net.Listen("unix", socketPath)
		if err != nil {
			t.Fatalf("listen unix: %v", err)
		}
		defer ln.Close()
		go func() {
			conn, err := ln.Accept()
			if err == nil {
				_ = conn.Close()
			}
		}()
		if _, err := sendControlRequest(socketPath, "agent_list", "", false, time.Second); err == nil {
			t.Fatal("empty response error = nil")
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		socketPath := shortDaemonwireSocketPath(t, "invalid.sock")
		ln, err := net.Listen("unix", socketPath)
		if err != nil {
			t.Fatalf("listen unix: %v", err)
		}
		defer ln.Close()
		go func() {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			_, _ = bufio.NewReader(conn).ReadBytes('\n')
			_, _ = conn.Write([]byte("{not json}\n"))
		}()
		if _, err := sendControlRequest(socketPath, "agent_list", "", false, time.Second); err == nil {
			t.Fatal("invalid response error = nil")
		}
	})
}

func shortDaemonwireSocketPath(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "loom-dw-")
	if err != nil {
		t.Fatalf("mkdir temp socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, name)
}

func TestDaemonSupervisorAndConfigFns(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("LOOM_WORKSPACE", "")
	if err := os.MkdirAll(filepath.Join(dir, ".loom"), 0o755); err != nil {
		t.Fatalf("mkdir .loom: %v", err)
	}
	started := time.Now().Add(-2 * time.Second).UTC()
	state := daemonpkg.DaemonState{
		PID:       1234,
		StartedAt: started,
		Agents: []daemonpkg.DaemonAgentStatus{{
			Worktree:       "worker",
			Role:           "task",
			Repo:           "api",
			PID:            5678,
			Status:         "running",
			TaskID:         "TASK-1",
			EpicID:         "EPIC-1",
			CurrentBackend: "codex",
			RestartCount:   2,
			RemoteBranch:   "feature/worker",
		}},
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if err := os.WriteFile(config.ResolveDaemonStatePath(dir), data, 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}

	supervisorFn := BuildDaemonSupervisorFn()
	if supervisorFn == nil {
		t.Fatal("BuildDaemonSupervisorFn returned nil")
	}
	supervisor, err := supervisorFn()
	if err != nil {
		t.Fatalf("supervisorFn: %v", err)
	}
	if supervisor.PID != 1234 || len(supervisor.Agents) != 1 || supervisor.Agents[0].CurrentBackend != "codex" {
		t.Fatalf("supervisor data = %+v", supervisor)
	}
	if supervisor.UptimeSeconds <= 0 {
		t.Fatalf("uptime = %f, want >0", supervisor.UptimeSeconds)
	}

	configFn := BuildDaemonConfigFn()
	if configFn == nil {
		t.Fatal("BuildDaemonConfigFn returned nil")
	}
	raw, err := configFn()
	if err != nil {
		t.Fatalf("configFn: %v", err)
	}
	var cfg config.DaemonConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	if cfg.Daemon.PIDFile == "" || cfg.Roles == nil {
		t.Fatalf("config = %+v", cfg)
	}
}
