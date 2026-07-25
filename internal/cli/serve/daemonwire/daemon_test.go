package daemonwire

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/agentcontrol"
)

func TestSendControlRequestWaitsForDaemonLifecycleResult(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { _ = clientConn.Close() })
	t.Cleanup(func() { _ = serverConn.Close() })

	requestReceived := make(chan struct{})
	allowCompletion := make(chan struct{})
	serverDone := make(chan error, 1)
	go func() {
		defer func() { _ = serverConn.Close() }()

		scanner := bufio.NewScanner(serverConn)
		if !scanner.Scan() {
			serverDone <- scanner.Err()
			return
		}
		var req struct {
			Operation string `json:"operation"`
			AgentName string `json:"agent_name"`
			Force     bool   `json:"force"`
		}
		if unmarshalErr := json.Unmarshal(scanner.Bytes(), &req); unmarshalErr != nil {
			serverDone <- unmarshalErr
			return
		}
		if req.Operation != "agent_stop" || req.AgentName != "nova" || req.Force {
			serverDone <- fmt.Errorf("request = %+v, want graceful agent_stop for nova", req)
			return
		}
		close(requestReceived)

		<-allowCompletion
		_, writeErr := serverConn.Write([]byte(`{"success":true}` + "\n"))
		serverDone <- writeErr
	}()

	type controlCallResult struct {
		result *agentcontrol.AgentControlResult
		err    error
	}
	callDone := make(chan controlCallResult, 1)
	go func() {
		result, callErr := sendControlRequestOnConn(
			clientConn,
			"agent_stop",
			"nova",
			false,
			5*time.Second,
		)
		callDone <- controlCallResult{result: result, err: callErr}
	}()

	select {
	case <-requestReceived:
	case serverErr := <-serverDone:
		t.Fatalf("server before request: %v", serverErr)
	case <-time.After(time.Second):
		t.Fatal("daemon did not receive lifecycle request")
	}

	select {
	case got := <-callDone:
		t.Fatalf("request returned before daemon lifecycle completed: %+v", got)
	case <-time.After(50 * time.Millisecond):
	}

	close(allowCompletion)
	select {
	case serverErr := <-serverDone:
		if serverErr != nil {
			t.Fatalf("server response: %v", serverErr)
		}
	case <-time.After(time.Second):
		t.Fatal("daemon did not finish lifecycle response")
	}
	select {
	case got := <-callDone:
		if got.err != nil {
			t.Fatalf("send control request: %v", got.err)
		}
		if got.result == nil || !got.result.Success {
			t.Fatalf("control result = %+v, want daemon-confirmed success", got.result)
		}
	case <-time.After(time.Second):
		t.Fatal("request did not return after daemon lifecycle response")
	}
}

func TestSendControlRequestReturnsDaemonRejection(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { _ = clientConn.Close() })
	t.Cleanup(func() { _ = serverConn.Close() })

	go func() {
		defer func() { _ = serverConn.Close() }()
		scanner := bufio.NewScanner(serverConn)
		if scanner.Scan() {
			_, _ = serverConn.Write([]byte(`{"error":"agent \"missing\" not found"}` + "\n"))
		}
	}()

	result, err := sendControlRequestOnConn(
		clientConn,
		"agent_restart",
		"missing",
		false,
		time.Second,
	)
	if err != nil {
		t.Fatalf("send control request: %v", err)
	}
	if result == nil || result.Success || !strings.Contains(result.Error, "not found") {
		t.Fatalf("control result = %+v, want observable daemon rejection", result)
	}
}

func TestAgentControlReadDeadlineCoversGracefulEscalation(t *testing.T) {
	dc := &config.DaemonConfig{}
	if got, want := agentControlReadDeadline(dc, "agent_stop", false), 380*time.Second; got != want {
		t.Fatalf("default graceful stop deadline = %v, want %v", got, want)
	}
	if got, want := agentControlReadDeadline(dc, "agent_stop", true), 320*time.Second; got != want {
		t.Fatalf("default force stop deadline = %v, want %v", got, want)
	}

	yieldTimeout := 12
	sigtermTimeout := 34
	dc.Daemon.RestartPolicy.YieldTimeout = &yieldTimeout
	dc.Daemon.RestartPolicy.SigtermTimeout = &sigtermTimeout
	if got, want := agentControlReadDeadline(dc, "agent_restart", false), 66*time.Second; got != want {
		t.Fatalf("configured restart deadline = %v, want %v", got, want)
	}
}

func TestBuildStoreBackedDaemonConfigFnUsesFleetDBStore(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	t.Setenv(bootstrap.EnvWorkspace, "WS1")

	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS1", Name: "Workspace One"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	maxPriority := 2
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "WS1",
		Name:         "task",
		TaskFilter:   "ready",
		Backend:      "codex",
		MaxPriority:  &maxPriority,
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	maxAgents := 7
	profile, err := st.Daemon().Get(ctx, "WS1")
	if err != nil {
		t.Fatalf("get profile: %v", err)
	}
	profile.PIDFile = ".loom/custom.pid"
	profile.MaxAgents = &maxAgents
	if _, err := st.Daemon().Upsert(ctx, profile); err != nil {
		t.Fatalf("upsert profile: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey:     "WS1",
		Name:             "nova",
		RoleName:         "task",
		Backend:          "codex",
		FallbackBackends: []string{"claude"},
		Repos:            []string{"api"},
		RepoGroups:       []string{"backend"},
		CrossRepo:        true,
		Parent:           "epic-1",
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	fn := BuildStoreBackedDaemonConfigFn(st)
	if fn == nil {
		t.Fatal("BuildStoreBackedDaemonConfigFn returned nil")
	}
	raw, err := fn()
	if err != nil {
		t.Fatalf("daemon config fn: %v", err)
	}
	var got config.DaemonConfig
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}

	if got.Backend != "fleetdb" || got.Daemon.IssueBackend != "fleetdb" {
		t.Fatalf("backend = %q daemon.issue_backend = %q, want fleetdb", got.Backend, got.Daemon.IssueBackend)
	}
	if got.Daemon.PIDFile != ".loom/custom.pid" {
		t.Fatalf("pid_file = %q", got.Daemon.PIDFile)
	}
	if got.Daemon.MaxAgents == nil || *got.Daemon.MaxAgents != 7 {
		t.Fatalf("max_agents = %v, want 7", got.Daemon.MaxAgents)
	}
	if role, ok := got.Roles["task"]; !ok || role.TaskFilter != "ready" || role.Backend != "codex" {
		t.Fatalf("role task = %+v, ok=%v", role, ok)
	}
	if len(got.Agents) != 1 {
		t.Fatalf("agents len = %d, want 1", len(got.Agents))
	}
	agent := got.Agents[0]
	if agent.Worktree != "nova" || agent.Role != "task" || !agent.CrossRepo || agent.Parent != "epic-1" {
		t.Fatalf("agent = %+v", agent)
	}
	if len(agent.Repos) != 1 || agent.Repos[0] != "api" {
		t.Fatalf("agent repos = %v, want [api]", agent.Repos)
	}
	if len(agent.RepoGroups) != 1 || agent.RepoGroups[0] != "backend" {
		t.Fatalf("agent repo_groups = %v, want [backend]", agent.RepoGroups)
	}
}
