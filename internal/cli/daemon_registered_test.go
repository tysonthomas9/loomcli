package cli

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

type fakeNodeLister struct {
	nodes []*domain.Node
	err   error
}

func (f *fakeNodeLister) List(context.Context, string) ([]*domain.Node, error) {
	return f.nodes, f.err
}

func TestDetectRegisteredDaemonRuntime_NilStoreReturnsZero(t *testing.T) {
	got := DetectRegisteredDaemonRuntime(context.Background(), nil, "WS")
	if got.Running {
		t.Fatalf("DetectRegisteredDaemonRuntime nil store = %+v, want zero value", got)
	}
}

func TestDetectRegisteredDaemonRuntime_EmptyWorkspaceKeyReturnsZero(t *testing.T) {
	got := detectRegisteredDaemonRuntime(context.Background(), &fakeNodeLister{}, "", time.Now(), func(int) bool { return true }, "host")
	if got.Running {
		t.Fatalf("empty workspace key = %+v, want zero value", got)
	}
}

func TestDetectRegisteredDaemonRuntime_FreshLocalNodeWithAlivePIDIsRunning(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	nodes := &fakeNodeLister{nodes: []*domain.Node{
		{
			NodeID:          "loom-supervisor-myhost-1234",
			WorkspaceKey:    "WS",
			RuntimeProvider: domain.RuntimeProviderLocal,
			Labels: []string{
				"loom.daemon.pid=1234",
				"loom.daemon.cwd=/tmp/repro",
				"loom.daemon.socket=/tmp/repro/.loom/agent-ipc.sock",
			},
			LastHeartbeat: now.Add(-10 * time.Second),
			ExpiresAt:     now.Add(2 * time.Minute),
		},
	}}
	got := detectRegisteredDaemonRuntime(context.Background(), nodes, "WS", now, func(pid int) bool { return pid == 1234 }, "myhost")
	if !got.Running {
		t.Fatalf("Running = false, want true; info=%+v", got)
	}
	if got.PID != 1234 || got.Cwd != "/tmp/repro" || got.Socket != "/tmp/repro/.loom/agent-ipc.sock" {
		t.Fatalf("info=%+v, want PID=1234 cwd=/tmp/repro socket=/tmp/repro/.loom/agent-ipc.sock", got)
	}
}

func TestDetectRegisteredDaemonRuntime_DeadLocalPIDFiltersOut(t *testing.T) {
	now := time.Now()
	nodes := &fakeNodeLister{nodes: []*domain.Node{
		{
			NodeID:          "loom-supervisor-myhost-99999",
			RuntimeProvider: domain.RuntimeProviderLocal,
			Labels:          []string{"loom.daemon.pid=99999"},
			LastHeartbeat:   now,
			ExpiresAt:       now.Add(time.Minute),
		},
	}}
	got := detectRegisteredDaemonRuntime(context.Background(), nodes, "WS", now, func(int) bool { return false }, "myhost")
	if got.Running {
		t.Fatalf("dead local PID should not count as running: %+v", got)
	}
}

func TestDetectRegisteredDaemonRuntime_NoPIDLabelStillRunning(t *testing.T) {
	now := time.Now()
	nodes := &fakeNodeLister{nodes: []*domain.Node{
		{
			NodeID:          "loom-supervisor-otherhost-100",
			RuntimeProvider: domain.RuntimeProviderLocal,
			LastHeartbeat:   now,
			ExpiresAt:       now.Add(time.Minute),
		},
	}}
	got := detectRegisteredDaemonRuntime(context.Background(), nodes, "WS", now, func(int) bool {
		t.Fatal("pidAlive should not be called when no PID label is present")
		return false
	}, "myhost")
	if !got.Running || got.PID != 0 {
		t.Fatalf("got=%+v, want Running=true PID=0", got)
	}
}

func TestDetectRegisteredDaemonRuntime_ExpiredNodeFiltersOut(t *testing.T) {
	now := time.Now()
	nodes := &fakeNodeLister{nodes: []*domain.Node{
		{
			NodeID:          "loom-supervisor-myhost-1234",
			RuntimeProvider: domain.RuntimeProviderLocal,
			Labels:          []string{"loom.daemon.pid=1234"},
			LastHeartbeat:   now.Add(-5 * time.Minute),
			ExpiresAt:       now.Add(-1 * time.Minute),
		},
	}}
	got := detectRegisteredDaemonRuntime(context.Background(), nodes, "WS", now, func(int) bool { return true }, "myhost")
	if got.Running {
		t.Fatalf("expired node should not count: %+v", got)
	}
}

func TestDetectRegisteredDaemonRuntime_NonLocalProviderIgnored(t *testing.T) {
	now := time.Now()
	nodes := &fakeNodeLister{nodes: []*domain.Node{
		{
			NodeID:          "k8s-node-1",
			RuntimeProvider: domain.RuntimeProviderKubernetes,
			Labels:          []string{"loom.daemon.pid=1234"},
			LastHeartbeat:   now,
			ExpiresAt:       now.Add(time.Minute),
		},
	}}
	got := detectRegisteredDaemonRuntime(context.Background(), nodes, "WS", now, func(int) bool { return true }, "myhost")
	if got.Running {
		t.Fatalf("non-local provider must not count: %+v", got)
	}
}

func TestDetectRegisteredDaemonRuntime_RemoteHostSkipsPIDProbe(t *testing.T) {
	now := time.Now()
	nodes := &fakeNodeLister{nodes: []*domain.Node{
		{
			NodeID:          "loom-supervisor-otherhost-1234",
			RuntimeProvider: domain.RuntimeProviderLocal,
			Labels: []string{
				"loom.daemon.pid=1234",
				"loom.daemon.cwd=/remote/path",
			},
			LastHeartbeat: now,
			ExpiresAt:     now.Add(time.Minute),
		},
	}}
	got := detectRegisteredDaemonRuntime(context.Background(), nodes, "WS", now, func(int) bool {
		t.Fatal("pidAlive must not run for remote-host nodes")
		return false
	}, "myhost")
	if !got.Running || got.PID != 1234 || got.Cwd != "/remote/path" {
		t.Fatalf("got=%+v, want Running=true PID=1234 cwd=/remote/path", got)
	}
}

func TestDetectRegisteredDaemonRuntime_ListErrorReturnsZero(t *testing.T) {
	nodes := &fakeNodeLister{err: errors.New("boom")}
	got := detectRegisteredDaemonRuntime(context.Background(), nodes, "WS", time.Now(), func(int) bool { return true }, "myhost")
	if got.Running {
		t.Fatalf("list error must yield zero value, got %+v", got)
	}
}

func TestDetectRegisteredDaemonRuntime_PicksMostRecentHeartbeat(t *testing.T) {
	now := time.Now()
	nodes := &fakeNodeLister{nodes: []*domain.Node{
		{
			NodeID:          "loom-supervisor-myhost-100",
			RuntimeProvider: domain.RuntimeProviderLocal,
			Labels:          []string{"loom.daemon.pid=100"},
			LastHeartbeat:   now.Add(-time.Minute),
			ExpiresAt:       now.Add(time.Minute),
		},
		{
			NodeID:          "loom-supervisor-myhost-200",
			RuntimeProvider: domain.RuntimeProviderLocal,
			Labels:          []string{"loom.daemon.pid=200"},
			LastHeartbeat:   now,
			ExpiresAt:       now.Add(time.Minute),
		},
	}}
	got := detectRegisteredDaemonRuntime(context.Background(), nodes, "WS", now, func(int) bool { return true }, "myhost")
	if got.PID != 200 {
		t.Fatalf("expected most-recent heartbeat PID 200, got %+v", got)
	}
}

func TestParseDaemonLabels_RejectsInvalidPIDs(t *testing.T) {
	cases := []struct {
		name   string
		labels []string
		want   int
	}{
		{"negative", []string{"loom.daemon.pid=-1"}, 0},
		{"zero", []string{"loom.daemon.pid=0"}, 0},
		{"non numeric", []string{"loom.daemon.pid=abc"}, 0},
		{"valid", []string{"loom.daemon.pid=42"}, 42},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseDaemonLabels(c.labels).pid; got != c.want {
				t.Fatalf("pid = %d, want %d", got, c.want)
			}
		})
	}
}

func TestNodeHostMatches(t *testing.T) {
	cases := []struct {
		nodeID, host string
		want         bool
	}{
		{"loom-supervisor-myhost-1234", "myhost", true},
		{"loom-supervisor-otherhost-1234", "myhost", false},
		{"loom-supervisor-multi-part-host-99", "multi-part-host", true},
		{"loom-supervisor-myhost-1234", "", true},
		{"some-other-id", "myhost", false},
		{"loom-supervisor-justhost", "justhost", false},
	}
	for _, c := range cases {
		if got := nodeHostMatches(c.nodeID, c.host); got != c.want {
			t.Errorf("nodeHostMatches(%q, %q) = %t, want %t", c.nodeID, c.host, got, c.want)
		}
	}
}

func TestLocalHostnameMatchesOSHostname(t *testing.T) {
	want, err := os.Hostname()
	if err != nil {
		t.Skip("os.Hostname unavailable")
	}
	if got := localHostname(); got != want {
		t.Fatalf("localHostname()=%q, want %q", got, want)
	}
}
