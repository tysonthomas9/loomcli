package daemonregistry

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const testWorkspace = "WS-TEST"

// localNodeID builds the form the supervisor uses so
// hostnameMatchesLocal accepts it.
func localNodeID(t *testing.T, pid int) string {
	t.Helper()
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown-host"
	}
	return fmt.Sprintf("loom-supervisor-%s-%d", host, pid)
}

func TestDetect_NilStore(t *testing.T) {
	info := Detect(context.Background(), nil, testWorkspace)
	if info.Running {
		t.Fatalf("expected Running=false for nil store, got %+v", info)
	}
}

func TestDetect_EmptyWorkspace(t *testing.T) {
	st := memstore.New()
	info := Detect(context.Background(), st, testWorkspace)
	if info.Running {
		t.Fatalf("expected Running=false for empty registry, got %+v", info)
	}
}

func TestDetect_LiveLocalNode(t *testing.T) {
	st := memstore.New()
	livePID := os.Getpid() // our own PID — guaranteed alive
	_, err := st.Nodes().Create(context.Background(), store.NodeCreate{
		WorkspaceKey:    testWorkspace,
		NodeID:          localNodeID(t, livePID),
		RuntimeProvider: domain.RuntimeProviderLocal,
		Labels: []string{
			LabelPID + strconv.Itoa(livePID),
			LabelCwd + "/tmp/x",
			LabelSocket + "/tmp/x/agent.sock",
		},
		DrainState: domain.NodeDrainActive,
		TTL:        2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	info := Detect(context.Background(), st, testWorkspace)
	if !info.Running {
		t.Fatal("expected Running=true for live local Node")
	}
	if info.PID != livePID {
		t.Errorf("PID = %d, want %d", info.PID, livePID)
	}
	if info.Cwd != "/tmp/x" {
		t.Errorf("Cwd = %q, want /tmp/x", info.Cwd)
	}
	if info.Socket != "/tmp/x/agent.sock" {
		t.Errorf("Socket = %q, want /tmp/x/agent.sock", info.Socket)
	}
}

func TestDetect_DeadLocalPID(t *testing.T) {
	st := memstore.New()
	// PID 2147483647 (MaxInt32) is essentially never allocated; if it
	// is, the test will tolerantly pass — we are checking that the
	// liveness rule rejects dead PIDs from local-host nodes.
	deadPID := 2147483647
	_, err := st.Nodes().Create(context.Background(), store.NodeCreate{
		WorkspaceKey:    testWorkspace,
		NodeID:          localNodeID(t, deadPID),
		RuntimeProvider: domain.RuntimeProviderLocal,
		Labels:          []string{LabelPID + strconv.Itoa(deadPID)},
		TTL:             2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	info := Detect(context.Background(), st, testWorkspace)
	if info.Running {
		t.Fatalf("expected Running=false for dead PID label, got %+v", info)
	}
}

func TestDetect_NoPIDLabel(t *testing.T) {
	// A local Node with a fresh heartbeat but no PID label still
	// counts as running — we trust the heartbeat alone.
	st := memstore.New()
	_, err := st.Nodes().Create(context.Background(), store.NodeCreate{
		WorkspaceKey:    testWorkspace,
		NodeID:          localNodeID(t, 99999),
		RuntimeProvider: domain.RuntimeProviderLocal,
		Labels:          []string{LabelCwd + "/x"},
		TTL:             2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	info := Detect(context.Background(), st, testWorkspace)
	if !info.Running {
		t.Fatal("expected Running=true with fresh heartbeat (no PID label)")
	}
	if info.PID != 0 {
		t.Errorf("PID = %d, want 0", info.PID)
	}
	if info.Cwd != "/x" {
		t.Errorf("Cwd = %q, want /x", info.Cwd)
	}
}

func TestDetect_ExpiredNode(t *testing.T) {
	st := memstore.New()
	// Negative TTL → ExpiresAt is in the past; memstore stamps
	// ExpiresAt = now + ttl.
	pastNodeID := localNodeID(t, os.Getpid())
	_, err := st.Nodes().Create(context.Background(), store.NodeCreate{
		WorkspaceKey:    testWorkspace,
		NodeID:          pastNodeID,
		RuntimeProvider: domain.RuntimeProviderLocal,
		Labels:          []string{LabelPID + strconv.Itoa(os.Getpid())},
		TTL:             1 * time.Nanosecond,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	time.Sleep(2 * time.Millisecond) // ensure expiry
	info := Detect(context.Background(), st, testWorkspace)
	if info.Running {
		t.Fatalf("expected Running=false for expired Node, got %+v", info)
	}
}

func TestDetect_IgnoresNonLocalProvider(t *testing.T) {
	st := memstore.New()
	_, err := st.Nodes().Create(context.Background(), store.NodeCreate{
		WorkspaceKey:    testWorkspace,
		NodeID:          "k8s-foo-1",
		RuntimeProvider: domain.RuntimeProviderKubernetes,
		Labels:          []string{LabelPID + strconv.Itoa(os.Getpid())},
		TTL:             2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	info := Detect(context.Background(), st, testWorkspace)
	if info.Running {
		t.Fatalf("expected Running=false for non-local provider, got %+v", info)
	}
}

func TestDetect_RemoteHostLocalNode(t *testing.T) {
	// A local-provider Node whose hostname does NOT match this host
	// (e.g. another machine in a multi-host deployment) is still
	// trusted — we never probe a PID on a different machine.
	st := memstore.New()
	_, err := st.Nodes().Create(context.Background(), store.NodeCreate{
		WorkspaceKey:    testWorkspace,
		NodeID:          "loom-supervisor-other-host-12345",
		RuntimeProvider: domain.RuntimeProviderLocal,
		Labels: []string{
			LabelPID + "12345", // would be dead locally
			LabelCwd + "/elsewhere",
		},
		TTL: 2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	info := Detect(context.Background(), st, testWorkspace)
	if !info.Running {
		t.Fatal("expected Running=true for remote-host Node (PID probe must be skipped)")
	}
	if info.Cwd != "/elsewhere" {
		t.Errorf("Cwd = %q, want /elsewhere", info.Cwd)
	}
}

func TestDetect_PrefersMostRecentHeartbeat(t *testing.T) {
	st := memstore.New()
	livePID := os.Getpid()
	ctx := context.Background()

	olderID := localNodeID(t, livePID) + "-a"
	if _, err := st.Nodes().Create(ctx, store.NodeCreate{
		WorkspaceKey:    testWorkspace,
		NodeID:          olderID,
		RuntimeProvider: domain.RuntimeProviderLocal,
		Labels:          []string{LabelCwd + "/old"},
		TTL:             2 * time.Minute,
	}); err != nil {
		t.Fatalf("create older: %v", err)
	}
	newerID := localNodeID(t, livePID) + "-b"
	if _, err := st.Nodes().Create(ctx, store.NodeCreate{
		WorkspaceKey:    testWorkspace,
		NodeID:          newerID,
		RuntimeProvider: domain.RuntimeProviderLocal,
		Labels: []string{
			LabelPID + strconv.Itoa(livePID),
			LabelCwd + "/new",
		},
		TTL: 2 * time.Minute,
	}); err != nil {
		t.Fatalf("create newer: %v", err)
	}
	// Bump newer's heartbeat deterministically — Create stamps both
	// rows at "now", which on fast machines can collide and make
	// last-write-wins ordering flaky.
	if _, err := st.Nodes().Heartbeat(ctx, testWorkspace, newerID, 2*time.Minute); err != nil {
		t.Fatalf("heartbeat newer: %v", err)
	}
	info := Detect(ctx, st, testWorkspace)
	if !info.Running {
		t.Fatal("expected Running=true")
	}
	if info.Cwd != "/new" {
		t.Errorf("Cwd = %q, want /new (newest heartbeat should win)", info.Cwd)
	}
}

func TestParseLabels_IgnoresGarbage(t *testing.T) {
	pid, cwd, socket := parseLabels([]string{
		"unrelated.label=foo",
		"loom.daemon.pid=not-an-int",
		"loom.daemon.pid=-7",
		"loom.daemon.cwd=/abc",
		"loom.daemon.socket=/abc/sock",
	})
	if pid != 0 {
		t.Errorf("pid = %d, want 0 (negative + garbage rejected)", pid)
	}
	if cwd != "/abc" {
		t.Errorf("cwd = %q, want /abc", cwd)
	}
	if socket != "/abc/sock" {
		t.Errorf("socket = %q, want /abc/sock", socket)
	}
}

func TestHostnameMatchesLocal_Cases(t *testing.T) {
	cases := []struct {
		nodeID    string
		localHost string
		want      bool
	}{
		{"loom-supervisor-host1-12", "host1", true},
		{"loom-supervisor-host-with-dashes-12", "host-with-dashes", true},
		{"loom-supervisor-other-12", "host1", false},
		{"unrelated-id", "host1", false},
		{"", "host1", false},
		{"loom-supervisor-host1-12", "", false},
	}
	for _, tc := range cases {
		got := hostnameMatchesLocal(tc.nodeID, tc.localHost)
		if got != tc.want {
			t.Errorf("hostnameMatchesLocal(%q, %q) = %v, want %v", tc.nodeID, tc.localHost, got, tc.want)
		}
	}
}
