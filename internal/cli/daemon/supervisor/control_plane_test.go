package supervisor

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemonregistry"
	"github.com/tysonthomas9/loomcli/internal/cli/sessionfinalize"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestSupervisorRegistersControlPlaneNodeOnStart(t *testing.T) {
	st := memstore.New()
	s := newControlPlaneTestSupervisor(st)
	s.ProjectDir = "/tmp/project-dir"
	s.IpcSocketPath = "/tmp/project-dir/.loom/agent.sock"

	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	node, err := st.Nodes().Get(t.Context(), "WS", "node-1")
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if node.RuntimeProvider != domain.RuntimeProviderLocal {
		t.Fatalf("RuntimeProvider = %q, want local", node.RuntimeProvider)
	}
	if node.DrainState != domain.NodeDrainActive {
		t.Fatalf("DrainState = %q, want active", node.DrainState)
	}
	if node.Capacity != 0 {
		t.Fatalf("Capacity = %d, want 0", node.Capacity)
	}

	// LOOM-3: the supervisor must publish PID / Cwd / Socket labels so
	// diagnose can detect a daemon that was launched from any cwd.
	wantPID := daemonregistry.LabelPID + strconv.Itoa(os.Getpid())
	wantCwd := daemonregistry.LabelCwd + "/tmp/project-dir"
	wantSocket := daemonregistry.LabelSocket + "/tmp/project-dir/.loom/agent.sock"
	if !containsString(node.Labels, wantPID) {
		t.Errorf("labels = %v, want to contain %q", node.Labels, wantPID)
	}
	if !containsString(node.Labels, wantCwd) {
		t.Errorf("labels = %v, want to contain %q", node.Labels, wantCwd)
	}
	if !containsString(node.Labels, wantSocket) {
		t.Errorf("labels = %v, want to contain %q", node.Labels, wantSocket)
	}
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func TestSupervisorDaemonRuntimeLabelsOmitMissingFields(t *testing.T) {
	st := memstore.New()
	s := newControlPlaneTestSupervisor(st)
	// ProjectDir / IpcSocketPath intentionally blank.

	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	node, err := st.Nodes().Get(t.Context(), "WS", "node-1")
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	// PID must still be present (it's the strongest liveness signal).
	if !containsString(node.Labels, daemonregistry.LabelPID+strconv.Itoa(os.Getpid())) {
		t.Errorf("labels = %v, want to contain pid label", node.Labels)
	}
	// Empty Cwd / Socket must not produce dangling "key=" labels.
	for _, label := range node.Labels {
		if strings.HasPrefix(label, daemonregistry.LabelCwd) || strings.HasPrefix(label, daemonregistry.LabelSocket) {
			t.Errorf("unexpected empty label: %q", label)
		}
	}
}

// TestSupervisorRefreshNodeLabelsRepublishesSocket simulates the
// real daemon ordering: Start runs before IpcSocketPath is set, then
// RefreshNodeLabels is called from startDaemonSockets to publish the
// loom.daemon.socket label.
func TestSupervisorRefreshNodeLabelsRepublishesSocket(t *testing.T) {
	st := memstore.New()
	s := newControlPlaneTestSupervisor(st)
	s.ProjectDir = "/tmp/project-dir"
	// IpcSocketPath intentionally unset at Start time.

	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	node, err := st.Nodes().Get(t.Context(), "WS", "node-1")
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	for _, label := range node.Labels {
		if strings.HasPrefix(label, daemonregistry.LabelSocket) {
			t.Fatalf("did not expect socket label before refresh, got %v", node.Labels)
		}
	}

	// Mimic startDaemonSockets: socket becomes known, then we re-publish.
	s.IpcSocketPath = "/tmp/project-dir/.loom/agent.sock"
	s.RefreshNodeLabels()

	node, err = st.Nodes().Get(t.Context(), "WS", "node-1")
	if err != nil {
		t.Fatalf("get node after refresh: %v", err)
	}
	want := daemonregistry.LabelSocket + "/tmp/project-dir/.loom/agent.sock"
	if !containsString(node.Labels, want) {
		t.Errorf("labels = %v, want to contain %q after refresh", node.Labels, want)
	}
	// PID label must still be present after refresh (we re-publish the full set).
	if !containsString(node.Labels, daemonregistry.LabelPID+strconv.Itoa(os.Getpid())) {
		t.Errorf("labels = %v, lost PID label after refresh", node.Labels)
	}
}

func TestSupervisorHeartbeatsControlPlaneNodeUntilStop(t *testing.T) {
	st := memstore.New()
	s := newControlPlaneTestSupervisor(st)
	s.NodeInterval = 10 * time.Millisecond

	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	node, err := st.Nodes().Get(t.Context(), "WS", "node-1")
	if err != nil {
		t.Fatalf("get initial node: %v", err)
	}
	initialHeartbeat := node.LastHeartbeat

	deadline := time.Now().Add(2 * time.Second)
	for {
		node, err = st.Nodes().Get(t.Context(), "WS", "node-1")
		if err != nil {
			t.Fatalf("get heartbeat node: %v", err)
		}
		if node.LastHeartbeat.After(initialHeartbeat) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("node heartbeat did not advance after %s", time.Since(initialHeartbeat))
		}
		time.Sleep(5 * time.Millisecond)
	}

	s.Stop()
	stoppedHeartbeat := node.LastHeartbeat
	time.Sleep(30 * time.Millisecond)
	node, err = st.Nodes().Get(t.Context(), "WS", "node-1")
	if err != nil {
		t.Fatalf("get stopped node: %v", err)
	}
	if !node.LastHeartbeat.Equal(stoppedHeartbeat) {
		t.Fatalf("heartbeat advanced after Stop: %s -> %s", stoppedHeartbeat, node.LastHeartbeat)
	}
}

func TestSupervisorMirrorsAgentSessionToControlPlane(t *testing.T) {
	st := memstore.New()
	s := newControlPlaneTestSupervisor(st)
	worktree := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldWd)
		cli.ResetWorkspaceRuntimeDirCache()
	})
	cli.ResetWorkspaceRuntimeDirCache()

	ap := &AgentProcess{
		Entry:           cfgpkg.AgentEntry{Worktree: "worker-1", Role: "task", Repo: "repo-a"},
		RoleConfig:      cfgpkg.RoleConfig{Backend: "claude"},
		WorktreePath:    worktree,
		AssignedTaskID:  "task-1",
		ParentSessionID: "lead-session-1",
	}

	s.createAgentSession(ap, "epic-1")
	if ap.AgentSessionID == "" {
		t.Fatal("AgentSessionID was not set")
	}
	if ap.AgentLeaseID == "" || ap.AgentLeaseToken == "" {
		t.Fatalf("lease id/token not set: %q/%q", ap.AgentLeaseID, ap.AgentLeaseToken)
	}
	session, err := st.AgentSessions().Get(t.Context(), "WS", ap.AgentSessionID)
	if err != nil {
		t.Fatalf("get created agent session: %v", err)
	}
	if session.AgentID != "worker-1" || session.NodeID != "node-1" {
		t.Fatalf("session agent/node = %q/%q, want worker-1/node-1", session.AgentID, session.NodeID)
	}
	if session.Status != domain.AgentSessionStarting {
		t.Fatalf("status = %q, want starting", session.Status)
	}
	if session.TaskID != "task-1" {
		t.Fatalf("task id = %q, want task-1", session.TaskID)
	}
	if session.ParentSessionID != "lead-session-1" {
		t.Fatalf("parent session id = %q, want lead-session-1", session.ParentSessionID)
	}
	if session.Metadata["epic_id"] != "epic-1" || session.Metadata["task_id"] != "task-1" || session.Metadata["repo"] != "repo-a" {
		t.Fatalf("metadata = %#v, want epic/task/repo", session.Metadata)
	}
	lease, err := st.AgentLeases().Get(t.Context(), "WS", ap.AgentLeaseID)
	if err != nil {
		t.Fatalf("get created lease: %v", err)
	}
	if lease.SessionID != ap.AgentSessionID || lease.Status != domain.AgentLeaseActive {
		t.Fatalf("lease session/status = %q/%q, want %q/active", lease.SessionID, lease.Status, ap.AgentSessionID)
	}

	ap.Mu.Lock()
	ap.LogFilePath = "/tmp/worker-1.log"
	ap.Mu.Unlock()
	s.markControlPlaneAgentSessionRunning(ap)
	session, err = st.AgentSessions().Get(t.Context(), "WS", ap.AgentSessionID)
	if err != nil {
		t.Fatalf("get running agent session: %v", err)
	}
	if session.Status != domain.AgentSessionRunning {
		t.Fatalf("status = %q, want running", session.Status)
	}
	if session.Metadata["backend"] != "claude" {
		t.Fatalf("backend metadata = %q, want claude", session.Metadata["backend"])
	}
	if session.Metadata["transcript_path"] == "" || session.Metadata["log_path"] != "/tmp/worker-1.log" {
		t.Fatalf("path metadata = %#v, want transcript and log path", session.Metadata)
	}

	sessionID := ap.AgentSessionID
	leaseID := ap.AgentLeaseID
	leaseToken := ap.AgentLeaseToken
	s.completeControlPlaneAgentSession(ap, agentSessionCompletionInput{
		sessionID:  sessionID,
		leaseID:    leaseID,
		leaseToken: leaseToken,
		exitCode:   7,
		errClass:   "Fatal",
		taskID:     "task-final",
		diffResult: sessionfinalize.WithWorktreeResult{
			DiffStats: sessions.DiffStats{
				FilesChanged: 1,
				LinesAdded:   2,
				LinesRemoved: 3,
			},
			FilesTouched: []string{"file.txt"},
		},
	})
	session, err = st.AgentSessions().Get(t.Context(), "WS", sessionID)
	if err != nil {
		t.Fatalf("get completed agent session: %v", err)
	}
	if session.TaskID != "task-final" {
		t.Fatalf("completed task id = %q, want task-final", session.TaskID)
	}
	if session.Status != domain.AgentSessionFailed {
		t.Fatalf("status = %q, want failed", session.Status)
	}
	if session.ExitCode == nil || *session.ExitCode != 7 {
		t.Fatalf("exit code = %v, want 7", session.ExitCode)
	}
	if session.ErrorClass != "Fatal" {
		t.Fatalf("error class = %q, want Fatal", session.ErrorClass)
	}
	if session.FinishedAt == nil {
		t.Fatal("FinishedAt was not set")
	}
	if session.Metadata["files_changed"] != "1" || session.Metadata["lines_added"] != "2" || session.Metadata["lines_removed"] != "3" || session.Metadata["files_touched"] != "file.txt" {
		t.Fatalf("diff metadata = %#v", session.Metadata)
	}
	lease, err = st.AgentLeases().Get(t.Context(), "WS", leaseID)
	if err != nil {
		t.Fatalf("get released lease: %v", err)
	}
	if lease.Status != domain.AgentLeaseReleased {
		t.Fatalf("lease status = %q, want released", lease.Status)
	}
}

func TestCreateControlPlaneAgentSessionUsesFreshLeaseContext(t *testing.T) {
	base := memstore.New()
	sessionStore := &contextConsumingAgentSessionStore{AgentSessionStore: base.AgentSessions()}
	leaseStore := &rejectExpiredAgentLeaseStore{AgentLeaseStore: base.AgentLeases()}
	st := &controlPlaneStoreOverrides{
		Store:    base,
		sessions: sessionStore,
		leases:   leaseStore,
	}
	s := newControlPlaneTestSupervisor(base)
	s.ControlStore = st

	oldTimeout := controlPlaneOperationTimeout
	controlPlaneOperationTimeout = 20 * time.Millisecond
	t.Cleanup(func() { controlPlaneOperationTimeout = oldTimeout })

	ap := &AgentProcess{
		Entry: cfgpkg.AgentEntry{Worktree: "worker-1", Role: "task"},
	}
	ap.AgentSessionID = "sess-fresh-lease-context"

	s.createControlPlaneAgentSession(ap, ap.AgentSessionID, "", "implementation", 0)

	if leaseStore.sawExpiredContext {
		t.Fatal("lease create received an already-expired context")
	}
	if ap.AgentLeaseID == "" || ap.AgentLeaseToken == "" {
		t.Fatalf("lease id/token not set: %q/%q", ap.AgentLeaseID, ap.AgentLeaseToken)
	}
}

func newControlPlaneTestSupervisor(st *memstore.Store) *Supervisor {
	return &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig {
			return &cfgpkg.DaemonConfig{}
		},
		WorkspaceID:   "WS",
		ControlStore:  st,
		NodeID:        "node-1",
		NodeTTL:       time.Minute,
		NodeInterval:  time.Hour,
		Shutdown:      make(chan struct{}),
		StoppedAgents: make(map[string]struct{}),
		Agents:        make([]*AgentProcess, 0),
		Concurrency:   NewConcurrencyTracker(nil),
		EmitEvent:     func(events.Event) {},
	}
}

type controlPlaneStoreOverrides struct {
	*memstore.Store
	sessions  store.AgentSessionStore
	leases    store.AgentLeaseStore
	ownership store.AgentOwnershipLeaseStore
}

func (s *controlPlaneStoreOverrides) AgentSessions() store.AgentSessionStore {
	if s.sessions == nil {
		return s.Store.AgentSessions()
	}
	return s.sessions
}

func (s *controlPlaneStoreOverrides) AgentLeases() store.AgentLeaseStore {
	if s.leases == nil {
		return s.Store.AgentLeases()
	}
	return s.leases
}

func (s *controlPlaneStoreOverrides) AgentOwnershipLeases() store.AgentOwnershipLeaseStore {
	if s.ownership == nil {
		return s.Store.AgentOwnershipLeases()
	}
	return s.ownership
}

type contextConsumingAgentSessionStore struct {
	store.AgentSessionStore
}

func (s *contextConsumingAgentSessionStore) Create(ctx context.Context, in store.AgentSessionCreate) (*domain.AgentSession, error) {
	<-ctx.Done()
	return s.AgentSessionStore.Create(context.Background(), in)
}

type rejectExpiredAgentLeaseStore struct {
	store.AgentLeaseStore
	sawExpiredContext bool
}

func (s *rejectExpiredAgentLeaseStore) Create(ctx context.Context, in store.AgentLeaseCreate) (*domain.AgentLease, error) {
	if err := ctx.Err(); err != nil {
		s.sawExpiredContext = true
		return nil, err
	}
	return s.AgentLeaseStore.Create(ctx, in)
}
