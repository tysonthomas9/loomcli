package terminal

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

// fakePTYSource stubs the PTYSource interface so service-level liveness
// behavior can be tested without spawning real shells. Only the methods the
// service uses (HasSession, Kill) carry real logic; the rest panic if
// touched unexpectedly by the test subject.
type fakePTYSource struct {
	alive   map[SessionKey]bool
	killed  []SessionKey
	killErr error
}

func newFakePTYSource() *fakePTYSource {
	return &fakePTYSource{alive: map[SessionKey]bool{}}
}

func (f *fakePTYSource) AttachSession(_ SessionKey, _, _ uint16, _ []string) (Attachment, bool, error) {
	panic("AttachSession not used in service-level tests")
}
func (f *fakePTYSource) Detach(_ SessionKey, _ string) {}
func (f *fakePTYSource) Kill(key SessionKey) error {
	f.killed = append(f.killed, key)
	delete(f.alive, key)
	return f.killErr
}
func (f *fakePTYSource) HasSession(key SessionKey) bool { return f.alive[key] }
func (f *fakePTYSource) AttachmentCount(key SessionKey) int {
	if f.alive[key] {
		return 1
	}
	return 0
}
func (f *fakePTYSource) SessionCount() int { return len(f.alive) }
func (f *fakePTYSource) MaxSessions() int  { return 100 }

// newLivenessTestSvc wires a terminalServiceImpl over miniredis + a fake PTY
// source so each test can arrange metadata and liveness independently.
func newLivenessTestSvc(t *testing.T) (*terminalServiceImpl, *fakePTYSource, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	store := tabmeta.NewStore(rdb, nil)
	fake := newFakePTYSource()
	svc := &terminalServiceImpl{
		tabStore:    store,
		redisClient: rdb,
		ptyMgr:      fake,
	}
	return svc, fake, mr
}

func putTestTab(t *testing.T, svc *terminalServiceImpl, wsID, name string) {
	t.Helper()
	meta := &tabmeta.TabMetadata{
		SessionName: name,
		Workspace:   wsID,
		Label:       name,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	// Write via the store directly to bypass the service-level liveness
	// guard — tests want to set up arbitrary states.
	if err := svc.tabStore.Set(context.Background(), meta); err != nil {
		t.Fatalf("seed PutTab: %v", err)
	}
}

func TestListTabs_AnnotatesPTYAlive(t *testing.T) {
	svc, fake, _ := newLivenessTestSvc(t)
	ctx := context.Background()
	const ws = "w"

	putTestTab(t, svc, ws, "alive-tab")
	putTestTab(t, svc, ws, "dead-tab")
	fake.alive[SessionKey{Workspace: ws, Name: "alive-tab"}] = true

	tabs, err := svc.ListTabs(ctx, ws)
	if err != nil {
		t.Fatalf("ListTabs: %v", err)
	}
	got := map[string]bool{}
	for _, tb := range tabs {
		got[tb.SessionName] = tb.PTYAlive
	}
	if !got["alive-tab"] {
		t.Errorf("alive-tab: expected pty_alive=true, got false")
	}
	if got["dead-tab"] {
		t.Errorf("dead-tab: expected pty_alive=false, got true")
	}
}

func TestGetTab_AnnotatesPTYAlive(t *testing.T) {
	svc, fake, _ := newLivenessTestSvc(t)
	ctx := context.Background()
	const ws = "w"

	putTestTab(t, svc, ws, "sess")
	fake.alive[SessionKey{Workspace: ws, Name: "sess"}] = true

	meta, err := svc.GetTab(ctx, ws, "sess")
	if err != nil {
		t.Fatalf("GetTab: %v", err)
	}
	if !meta.PTYAlive {
		t.Errorf("expected pty_alive=true on live PTY, got false")
	}
}

func TestPutTab_RejectsOverwriteWhenPTYIsLive(t *testing.T) {
	svc, fake, _ := newLivenessTestSvc(t)
	ctx := context.Background()
	const ws = "w"

	// Live session: PutTab should refuse to replace.
	fake.alive[SessionKey{Workspace: ws, Name: "sess"}] = true
	meta := &tabmeta.TabMetadata{SessionName: "sess", Workspace: ws, Label: "replacement"}
	err := svc.PutTab(ctx, ws, meta)
	var svcErr *service.ServiceError
	if !errors.As(err, &svcErr) || svcErr.Kind != service.KindConflict {
		t.Fatalf("expected ServiceError.Kind=Conflict, got %v", err)
	}
}

func TestPutTab_AllowsReclaimWhenPTYIsDead(t *testing.T) {
	svc, _, _ := newLivenessTestSvc(t)
	ctx := context.Background()
	const ws = "w"

	// Stale metadata from a prior process, no live PTY — replace OK.
	putTestTab(t, svc, ws, "sess")
	meta := &tabmeta.TabMetadata{SessionName: "sess", Workspace: ws, Label: "reclaimed"}
	if err := svc.PutTab(ctx, ws, meta); err != nil {
		t.Fatalf("PutTab reclaim: %v", err)
	}
	got, err := svc.GetTab(ctx, ws, "sess")
	if err != nil {
		t.Fatalf("GetTab after reclaim: %v", err)
	}
	if got.Label != "reclaimed" {
		t.Errorf("label after reclaim: got %q, want %q", got.Label, "reclaimed")
	}
}

func TestDeleteTab_KillsPTY(t *testing.T) {
	svc, fake, _ := newLivenessTestSvc(t)
	ctx := context.Background()
	const ws = "w"

	putTestTab(t, svc, ws, "sess")
	fake.alive[SessionKey{Workspace: ws, Name: "sess"}] = true

	if err := svc.DeleteTab(ctx, ws, "sess"); err != nil {
		t.Fatalf("DeleteTab: %v", err)
	}
	wantKey := SessionKey{Workspace: ws, Name: "sess"}
	if len(fake.killed) != 1 || fake.killed[0] != wantKey {
		t.Errorf("expected Kill(%v), got %v", wantKey, fake.killed)
	}
}

func TestDeleteTab_IgnoresKillErrorAndStillRemovesMetadata(t *testing.T) {
	svc, fake, _ := newLivenessTestSvc(t)
	ctx := context.Background()
	const ws = "w"

	putTestTab(t, svc, ws, "sess")
	fake.alive[SessionKey{Workspace: ws, Name: "sess"}] = true
	fake.killErr = errors.New("PTY kill failed deep in the OS")

	if err := svc.DeleteTab(ctx, ws, "sess"); err != nil {
		t.Fatalf("DeleteTab should swallow Kill error, got %v", err)
	}
	if meta, _ := svc.tabStore.Get(ctx, ws, "sess"); meta != nil {
		t.Errorf("metadata should have been removed despite Kill failure")
	}
}

func TestGetTerminalState_ClearsStaleActiveTab(t *testing.T) {
	svc, _, _ := newLivenessTestSvc(t)
	ctx := context.Background()
	const ws = "w"

	// Persist active_tab pointing at a session with neither PTY nor
	// metadata — fully stale; service should clear it so the UI doesn't
	// start by attaching to a ghost.
	if err := svc.redisClient.HSet(ctx, terminalUIStateKeyImpl, "active_tab", "ghost").Err(); err != nil {
		t.Fatalf("seed active_tab: %v", err)
	}

	got, err := svc.GetTerminalState(ctx, ws)
	if err != nil {
		t.Fatalf("GetTerminalState: %v", err)
	}
	if got != "" {
		t.Errorf("fully stale active_tab should be cleared, got %q", got)
	}
}

func TestGetTerminalState_KeepsActiveTabWithDeadPTYButLiveMetadata(t *testing.T) {
	svc, _, _ := newLivenessTestSvc(t)
	ctx := context.Background()
	const ws = "w"

	// Metadata exists but PTY is dead — the UI wants to render this tab
	// in its "session ended" state, so keep the active_tab reference.
	putTestTab(t, svc, ws, "just-metadata")
	if err := svc.redisClient.HSet(ctx, terminalUIStateKeyImpl, "active_tab", "just-metadata").Err(); err != nil {
		t.Fatalf("seed active_tab: %v", err)
	}

	got, err := svc.GetTerminalState(ctx, ws)
	if err != nil {
		t.Fatalf("GetTerminalState: %v", err)
	}
	if got != "just-metadata" {
		t.Errorf("expected active_tab kept (so UI can show ended-state), got %q", got)
	}
}
