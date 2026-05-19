package daemon

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestDaemonAgentControlCommandHooks(t *testing.T) {
	restore := replaceDaemonControlHooks(t)
	defer restore()

	resolveControlSocketFromCwdFn = func() (string, error) { return "/tmp/daemon.sock", nil }

	var forced []string
	forceStopAgentFn = func(socketPath, agentName string) {
		forced = append(forced, socketPath+":"+agentName)
	}
	runDaemonAgentStop("nova", true, time.Minute)
	if len(forced) != 1 || forced[0] != "/tmp/daemon.sock:nova" {
		t.Fatalf("forced stops = %+v", forced)
	}

	sendDaemonControlRequestFullFn = func(socketPath string, req DaemonControlRequest) (*DaemonControlResponse, error) {
		if socketPath != "/tmp/daemon.sock" || req.Operation != ctrlOpAgentYield || req.AgentName != "nova" {
			t.Fatalf("yield request = %+v socket=%q", req, socketPath)
		}
		return &DaemonControlResponse{Success: true}, nil
	}
	isAgentRunningViaSocketFn = func(socketPath, agentName string) bool {
		return false
	}
	runDaemonAgentStop("nova", false, time.Minute)

	var sent []string
	sendDaemonControlRequestFn = func(socketPath, operation, agentName string) (*DaemonControlResponse, error) {
		sent = append(sent, socketPath+":"+operation+":"+agentName)
		return &DaemonControlResponse{Success: true}, nil
	}
	runDaemonAgentStart(&cobra.Command{}, []string{"spark"})
	runDaemonAgentRestart(&cobra.Command{}, []string{"spark"})
	if len(sent) != 2 || sent[0] != "/tmp/daemon.sock:"+ctrlOpAgentStart+":spark" || sent[1] != "/tmp/daemon.sock:"+ctrlOpAgentRestart+":spark" {
		t.Fatalf("sent requests = %+v", sent)
	}
}

func TestDaemonControlYieldPollAndForceHelpers(t *testing.T) {
	restore := replaceDaemonControlHooks(t)
	defer restore()

	sendDaemonControlRequestFullFn = func(string, DaemonControlRequest) (*DaemonControlResponse, error) {
		return &DaemonControlResponse{Success: false, Error: "agent not running"}, nil
	}
	if requestYieldOrFallback("/sock", "nova") {
		t.Fatal("not-running yield should not continue polling")
	}

	forceCalls := 0
	forceStopAgentFn = func(string, string) { forceCalls++ }
	sendDaemonControlRequestFullFn = func(string, DaemonControlRequest) (*DaemonControlResponse, error) {
		return &DaemonControlResponse{Success: false, Error: "temporary tmux failure"}, nil
	}
	if requestYieldOrFallback("/sock", "nova") {
		t.Fatal("fallback yield should stop polling")
	}
	if forceCalls != 1 {
		t.Fatalf("force calls = %d, want 1", forceCalls)
	}

	isAgentRunningViaSocketFn = func(string, string) bool { return false }
	pollAndForceStop("/sock", "nova", time.Minute)
	if forceCalls != 1 {
		t.Fatalf("graceful poll should not force stop; force calls=%d", forceCalls)
	}

	now := time.Unix(100, 0)
	daemonControlNowFn = func() time.Time { return now }
	daemonControlSleepFn = func(d time.Duration) { now = now.Add(d) }
	isAgentRunningViaSocketFn = func(string, string) bool { return true }
	pollAndForceStop("/sock", "nova", 3*time.Second)
	if forceCalls != 2 {
		t.Fatalf("timeout poll force calls = %d, want 2", forceCalls)
	}
}

func TestDaemonControlSocketStatusHelpers(t *testing.T) {
	restore := replaceDaemonControlHooks(t)
	defer restore()

	data, err := json.Marshal([]AgentListEntry{
		{Name: "nova", Status: "running"},
		{Name: "spark", Status: "stopped"},
	})
	if err != nil {
		t.Fatalf("marshal list: %v", err)
	}
	sendDaemonControlRequestFn = func(socketPath, operation, agentName string) (*DaemonControlResponse, error) {
		if socketPath != "/sock" || operation != ctrlOpAgentList || agentName != "" {
			t.Fatalf("agent list request socket=%q op=%q agent=%q", socketPath, operation, agentName)
		}
		return &DaemonControlResponse{Success: true, Data: data}, nil
	}
	if !isAgentRunningViaSocket("/sock", "nova") {
		t.Fatal("nova should be running")
	}
	if isAgentRunningViaSocket("/sock", "spark") {
		t.Fatal("spark should not be running")
	}

	sendDaemonControlRequestFn = func(string, string, string) (*DaemonControlResponse, error) {
		return nil, errors.New("socket unavailable")
	}
	if isAgentRunningViaSocket("/sock", "nova") {
		t.Fatal("socket error should be treated as not running")
	}

	sendDaemonControlRequestFullFn = func(string, DaemonControlRequest) (*DaemonControlResponse, error) {
		return &DaemonControlResponse{Success: true}, nil
	}
	forceStopAgent("/sock", "nova")
	sendDaemonControlRequestFullFn = func(string, DaemonControlRequest) (*DaemonControlResponse, error) {
		return &DaemonControlResponse{Success: false, Error: "already stopped"}, nil
	}
	forceStopAgent("/sock", "nova")
}

func TestRunDaemonStopCommandBranches(t *testing.T) {
	restore := replaceDaemonControlHooks(t)
	defer restore()

	oldForce, oldTimeout := daemonStopForce, daemonStopTimeout
	t.Cleanup(func() {
		daemonStopForce, daemonStopTimeout = oldForce, oldTimeout
	})

	var stoppedAgent string
	runDaemonAgentStopFn = func(agentName string, force bool, timeout time.Duration) {
		stoppedAgent = agentName
		if force || timeout != 60*time.Second {
			t.Fatalf("unexpected stop args force=%t timeout=%v", force, timeout)
		}
	}
	runDaemonStop(&cobra.Command{}, []string{"nova"})
	if stoppedAgent != "nova" {
		t.Fatalf("stopped agent = %q", stoppedAgent)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	tmp := t.TempDir()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, ".loom"), 0o755); err != nil {
		t.Fatalf("mkdir .loom: %v", err)
	}
	runDaemonStop(&cobra.Command{}, nil)
}

func replaceDaemonControlHooks(t *testing.T) func() {
	t.Helper()
	oldResolve := resolveControlSocketFromCwdFn
	oldSend := sendDaemonControlRequestFn
	oldSendFull := sendDaemonControlRequestFullFn
	oldRunStop := runDaemonAgentStopFn
	oldForceStop := forceStopAgentFn
	oldIsRunning := isAgentRunningViaSocketFn
	oldStopForce := stopDaemonForceFn
	oldStopGraceful := stopDaemonGracefulFn
	oldSleep := daemonControlSleepFn
	oldNow := daemonControlNowFn
	return func() {
		resolveControlSocketFromCwdFn = oldResolve
		sendDaemonControlRequestFn = oldSend
		sendDaemonControlRequestFullFn = oldSendFull
		runDaemonAgentStopFn = oldRunStop
		forceStopAgentFn = oldForceStop
		isAgentRunningViaSocketFn = oldIsRunning
		stopDaemonForceFn = oldStopForce
		stopDaemonGracefulFn = oldStopGraceful
		daemonControlSleepFn = oldSleep
		daemonControlNowFn = oldNow
	}
}
