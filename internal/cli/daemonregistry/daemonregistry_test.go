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
	return fmt.Sprintf("loom-supervisor-%s-%d", testHostname(t), pid)
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

// taskWorkerNodeID mirrors driver.TaskWorker's node ID form so the
// regression below registers exactly what `loom serve` publishes.
func taskWorkerNodeID(t *testing.T, pid int) string {
	t.Helper()
	return fmt.Sprintf("loom-task-worker-%s-%d", testHostname(t), pid)
}

func testHostname(t *testing.T) string {
	t.Helper()
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown-host"
	}
	return host
}

// TestDetect_IgnoresTaskWorkerNode is the DOGFOOD-3 regression: the
// `loom serve` task worker registers a fresh local Node with only
// loom-driver-executor / loom-task-worker labels and no daemon PID.
// It is not a supervisor and must never confirm daemon liveness.
func TestDetect_IgnoresTaskWorkerNode(t *testing.T) {
	st := memstore.New()
	_, err := st.Nodes().Create(context.Background(), store.NodeCreate{
		WorkspaceKey:    testWorkspace,
		NodeID:          taskWorkerNodeID(t, os.Getpid()),
		RuntimeProvider: domain.RuntimeProviderLocal,
		Labels:          []string{"loom-driver-executor", "loom-task-worker"},
		Capabilities:    []string{"loom-driver-executor"},
		DrainState:      domain.NodeDrainActive,
		TTL:             5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	info := Detect(context.Background(), st, testWorkspace)
	if info.Running {
		t.Fatalf("expected Running=false for a `loom serve` task-worker Node, got %+v", info)
	}
}

// TestDetect_NoOrMalformedPIDLabel proves that daemon identity is
// mandatory: cwd/socket metadata alone, and PID labels that don't parse
// to a positive integer, never qualify a Node as a supervisor.
func TestDetect_NoOrMalformedPIDLabel(t *testing.T) {
	cases := []struct {
		name   string
		labels []string
	}{
		{"no pid label", []string{LabelCwd + "/x", LabelSocket + "/x/agent.sock"}},
		{"empty pid", []string{LabelPID, LabelCwd + "/x"}},
		{"non-numeric pid", []string{LabelPID + "not-an-int", LabelCwd + "/x"}},
		{"zero pid", []string{LabelPID + "0", LabelCwd + "/x"}},
		{"negative pid", []string{LabelPID + "-7", LabelCwd + "/x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := memstore.New()
			_, err := st.Nodes().Create(context.Background(), store.NodeCreate{
				WorkspaceKey:    testWorkspace,
				NodeID:          localNodeID(t, 99999),
				RuntimeProvider: domain.RuntimeProviderLocal,
				Labels:          tc.labels,
				TTL:             2 * time.Minute,
			})
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			info := Detect(context.Background(), st, testWorkspace)
			if info.Running {
				t.Fatalf("expected Running=false without valid daemon PID identity, got %+v", info)
			}
		})
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

// TestDetect_PrefersMostRecentHeartbeat covers recency selection on the
// remote-host path: the "-a"/"-b" suffixes push the PID out of the
// NodeID's trailing position, so hostnameMatchesLocal reports false and
// no local process probe runs. Same-host recency (where the probe does
// run) is covered by TestDetect_NewerDeadSupervisorDoesNotSuppressOlderLive.
func TestDetect_PrefersMostRecentHeartbeat(t *testing.T) {
	st := memstore.New()
	livePID := os.Getpid()
	ctx := context.Background()

	olderID := localNodeID(t, livePID) + "-a"
	if _, err := st.Nodes().Create(ctx, store.NodeCreate{
		WorkspaceKey:    testWorkspace,
		NodeID:          olderID,
		RuntimeProvider: domain.RuntimeProviderLocal,
		Labels: []string{
			LabelPID + strconv.Itoa(livePID),
			LabelCwd + "/old",
		},
		TTL: 2 * time.Minute,
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

// TestDetect_NewerDeadSupervisorDoesNotSuppressOlderLive covers the
// same-host ghost-row case that candidate-before-recency ordering exists
// to protect: a crashed supervisor's Node stays valid for its whole TTL
// and keeps a newer heartbeat than the supervisor that replaced it. The
// dead row must be filtered out before the newest-heartbeat comparison,
// so Detect still reports the older live supervisor rather than going
// dark or returning the ghost's metadata.
func TestDetect_NewerDeadSupervisorDoesNotSuppressOlderLive(t *testing.T) {
	st := memstore.New()
	ctx := context.Background()
	livePID := os.Getpid()
	const deadPID = 99999999

	liveID := localNodeID(t, livePID)
	if _, err := st.Nodes().Create(ctx, store.NodeCreate{
		WorkspaceKey:    testWorkspace,
		NodeID:          liveID,
		RuntimeProvider: domain.RuntimeProviderLocal,
		Labels: []string{
			LabelPID + strconv.Itoa(livePID),
			LabelCwd + "/supervisor/live",
		},
		TTL: 2 * time.Minute,
	}); err != nil {
		t.Fatalf("create live supervisor: %v", err)
	}
	ghostID := localNodeID(t, deadPID)
	if _, err := st.Nodes().Create(ctx, store.NodeCreate{
		WorkspaceKey:    testWorkspace,
		NodeID:          ghostID,
		RuntimeProvider: domain.RuntimeProviderLocal,
		Labels: []string{
			LabelPID + strconv.Itoa(deadPID),
			LabelCwd + "/supervisor/ghost",
		},
		TTL: 2 * time.Minute,
	}); err != nil {
		t.Fatalf("create ghost supervisor: %v", err)
	}
	// Make the ghost unambiguously the newest row.
	if _, err := st.Nodes().Heartbeat(ctx, testWorkspace, ghostID, 2*time.Minute); err != nil {
		t.Fatalf("heartbeat ghost: %v", err)
	}

	info := Detect(ctx, st, testWorkspace)
	if !info.Running {
		t.Fatal("expected Running=true from the older live supervisor Node")
	}
	if info.PID != livePID {
		t.Errorf("PID = %d, want %d (live supervisor row)", info.PID, livePID)
	}
	if info.Cwd != "/supervisor/live" {
		t.Errorf("Cwd = %q, want /supervisor/live (newer dead row must not win)", info.Cwd)
	}
}

// TestDetect_MixedRegistryPrefersSupervisorOverTaskWorker covers the
// real-world registry shape from DOGFOOD-3: a `loom serve` task-worker
// Node heartbeating continuously alongside a valid supervisor row. The
// worker must neither confirm liveness on its own nor — being the
// newer row — displace the supervisor's metadata.
func TestDetect_MixedRegistryPrefersSupervisorOverTaskWorker(t *testing.T) {
	st := memstore.New()
	ctx := context.Background()
	livePID := os.Getpid()

	supervisorID := localNodeID(t, livePID)
	if _, err := st.Nodes().Create(ctx, store.NodeCreate{
		WorkspaceKey:    testWorkspace,
		NodeID:          supervisorID,
		RuntimeProvider: domain.RuntimeProviderLocal,
		Labels: []string{
			LabelPID + strconv.Itoa(livePID),
			LabelCwd + "/supervisor/cwd",
			LabelSocket + "/supervisor/agent-ipc.sock",
		},
		DrainState: domain.NodeDrainActive,
		TTL:        2 * time.Minute,
	}); err != nil {
		t.Fatalf("create supervisor: %v", err)
	}
	workerID := taskWorkerNodeID(t, livePID)
	if _, err := st.Nodes().Create(ctx, store.NodeCreate{
		WorkspaceKey:    testWorkspace,
		NodeID:          workerID,
		RuntimeProvider: domain.RuntimeProviderLocal,
		Labels:          []string{"loom-driver-executor", "loom-task-worker"},
		DrainState:      domain.NodeDrainActive,
		TTL:             5 * time.Minute,
	}); err != nil {
		t.Fatalf("create task worker: %v", err)
	}
	// Make the task worker unambiguously the newest row.
	if _, err := st.Nodes().Heartbeat(ctx, testWorkspace, workerID, 5*time.Minute); err != nil {
		t.Fatalf("heartbeat task worker: %v", err)
	}

	info := Detect(ctx, st, testWorkspace)
	if !info.Running {
		t.Fatal("expected Running=true from the valid supervisor Node")
	}
	if info.PID != livePID {
		t.Errorf("PID = %d, want %d (supervisor row)", info.PID, livePID)
	}
	if info.Cwd != "/supervisor/cwd" {
		t.Errorf("Cwd = %q, want /supervisor/cwd (newer task worker must not win)", info.Cwd)
	}
	if info.Socket != "/supervisor/agent-ipc.sock" {
		t.Errorf("Socket = %q, want /supervisor/agent-ipc.sock", info.Socket)
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

// TestParseLabels_GarbageDoesNotClearValidPID pins the skip-vs-reset
// behavior: an invalid PID label is passed over rather than zeroing an
// already-parsed one. daemonRuntimeLabels only ever emits a single PID
// label, so this only matters if some other producer appears.
func TestParseLabels_GarbageDoesNotClearValidPID(t *testing.T) {
	pid, _, _ := parseLabels([]string{
		LabelPID + "5",
		LabelPID + "not-an-int",
	})
	if pid != 5 {
		t.Errorf("pid = %d, want 5 (garbage must not clear a valid PID)", pid)
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
