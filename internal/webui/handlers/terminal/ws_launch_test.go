package terminal

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
	webuterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

type wsAgentStoreOverride struct {
	store.Store
	agents store.AgentStore
}

func (s wsAgentStoreOverride) Agents() store.AgentStore { return s.agents }

type signalingAgentGetStore struct {
	store.AgentStore
	once sync.Once
	got  chan struct{}
}

func (s *signalingAgentGetStore) Get(ctx context.Context, workspaceKey, name string) (*domain.Agent, error) {
	agent, err := s.AgentStore.Get(ctx, workspaceKey, name)
	s.once.Do(func() { close(s.got) })
	return agent, err
}

type signalingAttachPTYSource struct {
	webuterminal.PTYSource
	once         sync.Once
	attachCalled chan struct{}
}

func (s *signalingAttachPTYSource) AttachSession(
	key webuterminal.SessionKey,
	cols, rows uint16,
	launch *tabmeta.LaunchSpec,
) (webuterminal.Attachment, bool, error) {
	s.once.Do(func() { close(s.attachCalled) })
	return s.PTYSource.AttachSession(key, cols, rows, launch)
}

func newTabMetaStoreForWSTest(t *testing.T) *tabmeta.Store {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return tabmeta.NewStore(rdb, nil)
}

func TestLaunchSpecRejectsUUIDSessionWithoutMetadata(t *testing.T) {
	ctx := context.Background()
	p := &terminalWSParams{tabMetaStore: newTabMetaStoreForWSTest(t)}

	launch, err := launchSpecForTerminalSession(ctx, p, "E2E", "term_550e8400-e29b-41d4-a716-446655440000")
	if launch != nil {
		t.Fatalf("launch = %#v, want nil", launch)
	}
	if !errors.Is(err, errTerminalLaunchMetaMissing) {
		t.Fatalf("err = %v, want errTerminalLaunchMetaMissing", err)
	}
}

func TestLaunchSpecRejectsUUIDSessionWithoutTabStore(t *testing.T) {
	ctx := context.Background()
	p := &terminalWSParams{}

	launch, err := launchSpecForTerminalSession(ctx, p, "E2E", "term_550e8400-e29b-41d4-a716-446655440000")
	if launch != nil {
		t.Fatalf("launch = %#v, want nil", launch)
	}
	if !errors.Is(err, errTerminalLaunchMetaMissing) {
		t.Fatalf("err = %v, want errTerminalLaunchMetaMissing", err)
	}
}

func TestLaunchSpecKeepsLegacyNamedLeadTabs(t *testing.T) {
	ctx := context.Background()
	p := &terminalWSParams{tabMetaStore: newTabMetaStoreForWSTest(t)}

	launch, err := launchSpecForTerminalSession(ctx, p, "E2E", "lead-codex-1")
	if err != nil {
		t.Fatalf("launchSpecForTerminalSession: %v", err)
	}
	if launch == nil || len(launch.Argv) == 0 {
		t.Fatalf("launch = %#v, want legacy lead argv", launch)
	}
}

func TestAgentTerminalAttachRequiresStartAfterStop(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "E2E",
		Name:         "reviewer",
		RoleName:     "lead",
		DesiredState: domain.AgentDesiredStopped,
	}); err != nil {
		t.Fatalf("create interactive agent: %v", err)
	}
	stopped := domain.AgentStateStopped
	if _, err := st.Agents().Update(ctx, "E2E", "reviewer", store.AgentUpdate{
		State: &stopped,
	}); err != nil {
		t.Fatalf("stop interactive agent: %v", err)
	}

	tabStore := newTabMetaStoreForWSTest(t)
	now := time.Now().UTC()
	if err := tabStore.Set(ctx, &tabmeta.TabMetadata{
		Workspace:   "E2E",
		SessionName: "term_reviewer",
		Label:       "reviewer",
		Kind:        "agent",
		AgentID:     "reviewer",
		Role:        "lead",
		Writable:    true,
		Launch:      &tabmeta.LaunchSpec{Argv: []string{"-c", "cat"}},
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("persist terminal metadata: %v", err)
	}

	manager := webuterminal.NewPTYManager("/bin/sh", 1, t.TempDir())
	t.Cleanup(func() { _ = manager.Shutdown() })
	p := &terminalWSParams{
		manager:      manager,
		store:        st,
		tabMetaStore: tabStore,
	}
	key := webuterminal.SessionKey{Workspace: "E2E", Name: "term_reviewer"}

	attachment, reattached, err := attachTerminalSession(ctx, p, key, 80, 24)
	if attachment != nil || reattached {
		t.Fatalf("stopped attach = (%#v, %v), want no attachment", attachment, reattached)
	}
	if !errors.Is(err, errAgentTerminalStopped) {
		t.Fatalf("stopped attach error = %v, want errAgentTerminalStopped", err)
	}
	if manager.HasSession(key) || manager.SessionCountFor("E2E") != 0 {
		t.Fatal("stopped WebSocket attach spawned a PTY")
	}

	active := domain.AgentStateActive
	running := domain.AgentDesiredRunning
	if _, err := st.Agents().Update(ctx, "E2E", "reviewer", store.AgentUpdate{
		State:        &active,
		DesiredState: &running,
	}); err != nil {
		t.Fatalf("start interactive agent: %v", err)
	}

	attachment, reattached, err = attachTerminalSession(ctx, p, key, 80, 24)
	if err != nil {
		t.Fatalf("attach after Start: %v", err)
	}
	if attachment == nil || reattached {
		t.Fatalf("attach after Start = (%#v, %v), want fresh attachment", attachment, reattached)
	}
	if !manager.HasSession(key) || manager.SessionCountFor("E2E") != 1 {
		t.Fatal("Start did not restore PTY attach")
	}
	manager.Detach(key, attachment.ConnID())
	if err := manager.Kill(key); err != nil {
		t.Fatalf("kill test PTY: %v", err)
	}
}

func TestAgentTerminalAttachRejectsDaemonSupervisedWorkerStoredLaunch(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "E2E",
		Name:         "advanced-worker",
		RoleName:     "task",
		Mode:         domain.AgentModeService,
		DesiredState: domain.AgentDesiredRunning,
	}); err != nil {
		t.Fatalf("create worker agent: %v", err)
	}
	active := domain.AgentStateActive
	if _, err := st.Agents().Update(ctx, "E2E", "advanced-worker", store.AgentUpdate{State: &active}); err != nil {
		t.Fatalf("activate worker agent: %v", err)
	}

	tabStore := newTabMetaStoreForWSTest(t)
	now := time.Now().UTC()
	if err := tabStore.Set(ctx, &tabmeta.TabMetadata{
		Workspace:   "E2E",
		SessionName: "term_advanced_worker",
		Label:       "advanced-worker",
		Kind:        "agent",
		AgentID:     "advanced-worker",
		Role:        "task",
		Writable:    true,
		Launch:      &tabmeta.LaunchSpec{Argv: []string{"-c", "cat"}},
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("persist worker terminal metadata: %v", err)
	}

	manager := webuterminal.NewPTYManager("/bin/sh", 1, t.TempDir())
	t.Cleanup(func() { _ = manager.Shutdown() })
	p := &terminalWSParams{
		manager:      manager,
		store:        st,
		tabMetaStore: tabStore,
	}
	key := webuterminal.SessionKey{Workspace: "E2E", Name: "term_advanced_worker"}

	attachment, reattached, err := attachTerminalSession(ctx, p, key, 80, 24)
	if attachment != nil || reattached {
		t.Fatalf("worker attach = (%#v, %v), want no attachment", attachment, reattached)
	}
	if !errors.Is(err, errDaemonSupervisedWorkerTerminal) {
		t.Fatalf("worker attach error = %v, want errDaemonSupervisedWorkerTerminal", err)
	}
	if manager.HasSession(key) || manager.SessionCountFor("E2E") != 0 {
		t.Fatal("stored worker launch spawned a PTY")
	}
}

func TestAgentTerminalAttachCannotSpawnDuringStopSnapshotGap(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "E2E",
		Name:         "reviewer-race",
		RoleName:     "lead",
		DesiredState: domain.AgentDesiredRunning,
	}); err != nil {
		t.Fatalf("create interactive agent: %v", err)
	}
	active := domain.AgentStateActive
	if _, err := st.Agents().Update(ctx, "E2E", "reviewer-race", store.AgentUpdate{State: &active}); err != nil {
		t.Fatalf("activate interactive agent: %v", err)
	}

	tabStore := newTabMetaStoreForWSTest(t)
	now := time.Now().UTC()
	if err := tabStore.Set(ctx, &tabmeta.TabMetadata{
		Workspace:   "E2E",
		SessionName: "term_reviewer_race",
		Label:       "reviewer-race",
		Kind:        "agent",
		AgentID:     "reviewer-race",
		Role:        "lead",
		Writable:    true,
		Launch:      &tabmeta.LaunchSpec{Argv: []string{"-c", "cat"}},
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("persist terminal metadata: %v", err)
	}

	baseManager := webuterminal.NewPTYManager("/bin/sh", 1, t.TempDir())
	t.Cleanup(func() { _ = baseManager.Shutdown() })
	attachCalled := make(chan struct{})
	manager := &signalingAttachPTYSource{
		PTYSource:    baseManager,
		attachCalled: attachCalled,
	}
	firstAgentRead := make(chan struct{})
	agents := &signalingAgentGetStore{
		AgentStore: st.Agents(),
		got:        firstAgentRead,
	}
	p := &terminalWSParams{
		manager:      manager,
		store:        wsAgentStoreOverride{Store: st, agents: agents},
		tabMetaStore: tabStore,
	}
	key := webuterminal.SessionKey{Workspace: "E2E", Name: "term_reviewer_race"}

	// Model Stop after it has snapshotted owned terminal keys but before it
	// persists stopped. The real lifecycle path holds this same boundary.
	unlockStop := webuterminal.LockAgentLifecycle("E2E", "reviewer-race")
	stopUnlocked := false
	defer func() {
		if !stopUnlocked {
			unlockStop()
		}
	}()

	type attachResult struct {
		attachment webuterminal.Attachment
		reattached bool
		err        error
	}
	resultCh := make(chan attachResult, 1)
	go func() {
		attachment, reattached, err := attachTerminalSession(ctx, p, key, 80, 24)
		resultCh <- attachResult{attachment: attachment, reattached: reattached, err: err}
	}()

	select {
	case <-firstAgentRead:
	case <-time.After(2 * time.Second):
		t.Fatal("attach did not reach its initial running-state authorization")
	}
	select {
	case <-attachCalled:
		t.Fatal("AttachSession ran inside Stop's snapshot-to-state-update gap")
	case <-time.After(200 * time.Millisecond):
	}

	stopped := domain.AgentStateStopped
	desiredStopped := domain.AgentDesiredStopped
	if _, err := st.Agents().Update(ctx, "E2E", "reviewer-race", store.AgentUpdate{
		State:        &stopped,
		DesiredState: &desiredStopped,
	}); err != nil {
		t.Fatalf("persist stopped lifecycle state: %v", err)
	}
	unlockStop()
	stopUnlocked = true

	var result attachResult
	select {
	case result = <-resultCh:
	case <-time.After(2 * time.Second):
		t.Fatal("attach did not finish after Stop released the lifecycle boundary")
	}
	if result.attachment != nil || result.reattached {
		t.Fatalf("racing attach = (%#v, %v), want no attachment", result.attachment, result.reattached)
	}
	if !errors.Is(result.err, errAgentTerminalStopped) {
		t.Fatalf("racing attach error = %v, want errAgentTerminalStopped", result.err)
	}
	select {
	case <-attachCalled:
		t.Fatal("stopped racing attach reached PTY creation")
	default:
	}
	if baseManager.HasSession(key) || baseManager.SessionCountFor("E2E") != 0 {
		t.Fatal("stopped agent retained or spawned a PTY")
	}
}

func TestEnsureWorkspacePTYRegisteredUsesLocalState(t *testing.T) {
	stateDir := t.TempDir()
	wsDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", stateDir)
	if err := bootstrap.MutateStateCache(func(sc *bootstrap.StateCache) error {
		sc.Workspaces["E2E"] = bootstrap.WorkspaceLocalState{Path: wsDir}
		return nil
	}); err != nil {
		t.Fatalf("MutateStateCache: %v", err)
	}

	mm := webuterminal.NewMultiPTYManager("cat", 0)
	t.Cleanup(func() { _ = mm.Close() })
	p := &terminalWSParams{manager: mm}

	ensureWorkspacePTYRegistered(context.Background(), p, "E2E")

	_, _, err := mm.AttachSession(webuterminal.SessionKey{Workspace: "E2E", Name: "s"}, 80, 24, &webuterminal.LaunchSpec{Argv: []string{"-c", "cat"}})
	if err != nil {
		t.Fatalf("AttachSession after self-heal: %v", err)
	}
}
