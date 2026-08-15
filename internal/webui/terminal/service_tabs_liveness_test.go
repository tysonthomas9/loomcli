package terminal

import (
	"context"
	"errors"
	"reflect"
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
	closed  map[SessionKey]bool
	killed  []SessionKey
	killErr error
}

func newFakePTYSource() *fakePTYSource {
	return &fakePTYSource{alive: map[SessionKey]bool{}, closed: map[SessionKey]bool{}}
}

func (f *fakePTYSource) AttachSession(_ SessionKey, _, _ uint16, _ *LaunchSpec) (Attachment, bool, error) {
	panic("AttachSession not used in service-level tests")
}
func (f *fakePTYSource) Detach(_ SessionKey, _ string) {}
func (f *fakePTYSource) Kill(key SessionKey) error {
	f.killed = append(f.killed, key)
	delete(f.alive, key)
	f.closed[key] = true
	return f.killErr
}
func (f *fakePTYSource) HasSession(key SessionKey) bool { return f.alive[key] }
func (f *fakePTYSource) SessionClosed(key SessionKey) bool {
	return f.closed[key]
}
func (f *fakePTYSource) AttachmentCount(key SessionKey) int {
	if f.alive[key] {
		return 1
	}
	return 0
}
func (f *fakePTYSource) SessionCount() int            { return len(f.alive) }
func (f *fakePTYSource) SessionCountFor(_ string) int { return len(f.alive) }
func (f *fakePTYSource) MaxSessions() int             { return 100 }

// SessionNamesFor makes the fake a PTYSessionLister: it reports the live
// sessions of wsID, the same way a real PTYManager enumerates its map.
func (f *fakePTYSource) SessionNamesFor(wsID string) []string {
	names := make([]string, 0, len(f.alive))
	for key, live := range f.alive {
		if live && key.Workspace == wsID {
			names = append(names, key.Name)
		}
	}
	return names
}

// bareFakePTYSource is a PTYSource that deliberately does NOT implement
// PTYSessionLister — it embeds the interface, so only PTYSource's own methods
// are promoted. Stands in for backends (a remote agentd client) that cannot
// enumerate their sessions.
type bareFakePTYSource struct{ PTYSource }

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
	putTestTabAt(t, svc, wsID, name, time.Now().UTC())
}

func putTestTabAt(t *testing.T, svc *terminalServiceImpl, wsID, name string, at time.Time) {
	t.Helper()
	meta := &tabmeta.TabMetadata{
		SessionName: name,
		Workspace:   wsID,
		Label:       name,
		CreatedAt:   at,
		UpdatedAt:   at,
	}
	// Write via the store directly to bypass the service-level liveness
	// guard — tests want to set up arbitrary states.
	if err := svc.tabStore.Set(context.Background(), meta); err != nil {
		t.Fatalf("seed PutTab: %v", err)
	}
}

func TestListTabs_AnnotatesAttachable(t *testing.T) {
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
		got[tb.SessionName] = tb.Attachable
	}
	if !got["alive-tab"] {
		t.Errorf("alive-tab: expected attachable=true, got false")
	}
	if got["dead-tab"] {
		t.Errorf("dead-tab: expected attachable=false, got true")
	}
}

func TestListTabs_TreatsCurrentProcessMetadataAsAttachable(t *testing.T) {
	svc, _, _ := newLivenessTestSvc(t)
	ctx := context.Background()
	const ws = "w"
	startedAt := time.Date(2026, 5, 5, 1, 0, 0, 0, time.UTC)
	svc.startedAt = startedAt

	putTestTabAt(t, svc, ws, "fresh-tab", startedAt.Add(time.Second))
	putTestTabAt(t, svc, ws, "stale-tab", startedAt.Add(-time.Second))

	tabs, err := svc.ListTabs(ctx, ws)
	if err != nil {
		t.Fatalf("ListTabs: %v", err)
	}
	got := map[string]bool{}
	for _, tb := range tabs {
		got[tb.SessionName] = tb.Attachable
	}
	if !got["fresh-tab"] {
		t.Errorf("fresh-tab: expected attachable=true so the UI can spawn it, got false")
	}
	if got["stale-tab"] {
		t.Errorf("stale-tab: expected attachable=false after server restart, got true")
	}
}

func TestListTabs_DoesNotTreatAgentMetadataWithoutLaunchAsAttachable(t *testing.T) {
	svc, _, _ := newLivenessTestSvc(t)
	ctx := context.Background()
	const ws = "w"
	startedAt := time.Date(2026, 5, 5, 1, 0, 0, 0, time.UTC)
	svc.startedAt = startedAt

	if err := svc.tabStore.Set(ctx, &tabmeta.TabMetadata{
		SessionName: "term_stopped_agent",
		Workspace:   ws,
		Label:       "agent-worker-done",
		Kind:        "agent",
		AgentID:     "worker-done",
		CreatedAt:   startedAt.Add(time.Second),
		UpdatedAt:   startedAt.Add(time.Second),
	}); err != nil {
		t.Fatalf("seed stopped agent tab: %v", err)
	}

	tabs, err := svc.ListTabs(ctx, ws)
	if err != nil {
		t.Fatalf("ListTabs: %v", err)
	}
	for _, tb := range tabs {
		if tb.SessionName == "term_stopped_agent" && tb.Attachable {
			t.Fatal("term_stopped_agent: expected attachable=false without launch spec")
		}
	}
}

func TestListTabs_DoesNotTreatClosedCurrentProcessMetadataAsAttachable(t *testing.T) {
	svc, fake, _ := newLivenessTestSvc(t)
	ctx := context.Background()
	const ws = "w"
	startedAt := time.Date(2026, 5, 5, 1, 0, 0, 0, time.UTC)
	svc.startedAt = startedAt

	putTestTabAt(t, svc, ws, "closed-tab", startedAt.Add(time.Second))
	fake.closed[SessionKey{Workspace: ws, Name: "closed-tab"}] = true

	tabs, err := svc.ListTabs(ctx, ws)
	if err != nil {
		t.Fatalf("ListTabs: %v", err)
	}
	got := map[string]bool{}
	for _, tb := range tabs {
		got[tb.SessionName] = tb.Attachable
	}
	if got["closed-tab"] {
		t.Errorf("closed-tab: expected attachable=false after PTY exit, got true")
	}
}

func TestGetTab_AnnotatesAttachable(t *testing.T) {
	svc, fake, _ := newLivenessTestSvc(t)
	ctx := context.Background()
	const ws = "w"

	putTestTab(t, svc, ws, "sess")
	fake.alive[SessionKey{Workspace: ws, Name: "sess"}] = true

	meta, err := svc.GetTab(ctx, ws, "sess")
	if err != nil {
		t.Fatalf("GetTab: %v", err)
	}
	if !meta.Attachable {
		t.Errorf("expected attachable=true on live PTY, got false")
	}
}

func TestPutTab_RejectsOverwriteWhenPTYIsLive(t *testing.T) {
	svc, fake, _ := newLivenessTestSvc(t)
	ctx := context.Background()
	const ws = "w"

	// Live session: PutTab should refuse to replace.
	putTestTab(t, svc, ws, "sess")
	fake.alive[SessionKey{Workspace: ws, Name: "sess"}] = true
	meta := &tabmeta.TabMetadata{SessionName: "sess", Workspace: ws, Label: "replacement"}
	err := svc.PutTab(ctx, ws, meta)
	var svcErr *service.ServiceError
	if !errors.As(err, &svcErr) || svcErr.Kind != service.KindConflict {
		t.Fatalf("expected ServiceError.Kind=Conflict, got %v", err)
	}
}

func TestPutTab_AllowsCreateWhenPTYIsLiveButMetadataMissing(t *testing.T) {
	svc, fake, _ := newLivenessTestSvc(t)
	ctx := context.Background()
	const ws = "w"

	// The frontend can open the WebSocket before its metadata PUT completes.
	// In that race the PTY exists, but this is still a create, not a replace.
	fake.alive[SessionKey{Workspace: ws, Name: "sess"}] = true
	meta := &tabmeta.TabMetadata{SessionName: "sess", Workspace: ws, Label: "new tab"}
	if err := svc.PutTab(ctx, ws, meta); err != nil {
		t.Fatalf("PutTab create with live PTY but missing metadata: %v", err)
	}
	got, err := svc.GetTab(ctx, ws, "sess")
	if err != nil {
		t.Fatalf("GetTab after live-PTY create: %v", err)
	}
	if got.Label != "new tab" {
		t.Errorf("label after create: got %q, want %q", got.Label, "new tab")
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
	if err := svc.redisClient.HSet(ctx, terminalUIStateKey(ws), "active_tab", "ghost").Err(); err != nil {
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
	if err := svc.redisClient.HSet(ctx, terminalUIStateKey(ws), "active_tab", "just-metadata").Err(); err != nil {
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

// TestTerminalState_WorkspaceIsolation proves that active_tab is scoped
// per-workspace so a PATCH on one workspace doesn't clobber another's
// selection. Before the key rescoping, both GETs returned "tab-b".
func TestTerminalState_WorkspaceIsolation(t *testing.T) {
	svc, fake, _ := newLivenessTestSvc(t)
	ctx := context.Background()

	putTestTab(t, svc, "ws-a", "tab-a")
	putTestTab(t, svc, "ws-b", "tab-b")
	fake.alive[SessionKey{Workspace: "ws-a", Name: "tab-a"}] = true
	fake.alive[SessionKey{Workspace: "ws-b", Name: "tab-b"}] = true

	if err := svc.PatchTerminalState(ctx, "ws-a", "tab-a"); err != nil {
		t.Fatalf("PatchTerminalState ws-a: %v", err)
	}
	if err := svc.PatchTerminalState(ctx, "ws-b", "tab-b"); err != nil {
		t.Fatalf("PatchTerminalState ws-b: %v", err)
	}

	gotA, err := svc.GetTerminalState(ctx, "ws-a")
	if err != nil {
		t.Fatalf("GetTerminalState ws-a: %v", err)
	}
	if gotA != "tab-a" {
		t.Errorf("ws-a active_tab: got %q, want %q", gotA, "tab-a")
	}
	gotB, err := svc.GetTerminalState(ctx, "ws-b")
	if err != nil {
		t.Fatalf("GetTerminalState ws-b: %v", err)
	}
	if gotB != "tab-b" {
		t.Errorf("ws-b active_tab: got %q, want %q", gotB, "tab-b")
	}
}

// TestListTabs_ResurfacesLivePTYWithoutMetadata — the failure this task
// exists for: a PTY that outlived (or never got) its metadata row was
// invisible in the tab list forever, because ListTabs passed nil live
// sessions to EnsureDefaults.
func TestListTabs_ResurfacesLivePTYWithoutMetadata(t *testing.T) {
	svc, fake, _ := newLivenessTestSvc(t)
	ctx := context.Background()
	const ws = "w"

	putTestTab(t, svc, ws, "has-metadata")
	fake.alive[SessionKey{Workspace: ws, Name: "has-metadata"}] = true
	// Live PTY with no metadata row at all.
	fake.alive[SessionKey{Workspace: ws, Name: "orphan"}] = true
	// A live PTY in another workspace must not leak into this list.
	fake.alive[SessionKey{Workspace: "other", Name: "elsewhere"}] = true

	tabs, err := svc.ListTabs(ctx, ws)
	if err != nil {
		t.Fatalf("ListTabs: %v", err)
	}
	got := map[string]bool{}
	for _, tb := range tabs {
		got[tb.SessionName] = true
	}
	if !got["orphan"] {
		t.Errorf("orphan session missing from ListTabs: %v", got)
	}
	if !got["has-metadata"] {
		t.Errorf("has-metadata session missing from ListTabs: %v", got)
	}
	if got["elsewhere"] {
		t.Errorf("session from another workspace leaked into ListTabs: %v", got)
	}

	// The resurfaced row is persisted, so a second call is stable and the
	// tab is annotated live like any other.
	again, err := svc.ListTabs(ctx, ws)
	if err != nil {
		t.Fatalf("second ListTabs: %v", err)
	}
	if len(again) != len(tabs) {
		t.Errorf("second ListTabs returned %d tabs, want %d (metadata should persist)", len(again), len(tabs))
	}
	for _, tb := range again {
		if tb.SessionName == "orphan" && !tb.PTYAlive {
			t.Errorf("resurfaced orphan tab: pty_alive=false, want true")
		}
	}
}

// TestListTabs_ResurfacedTabsHaveDeterministicOrder — session names come out
// of a Go map, so ListTabs sorts them before EnsureDefaults assigns
// sort_order. Without the sort the tab order shuffles between calls.
func TestListTabs_ResurfacedTabsHaveDeterministicOrder(t *testing.T) {
	svc, fake, _ := newLivenessTestSvc(t)
	ctx := context.Background()
	const ws = "w"

	for _, name := range []string{"delta", "alpha", "charlie", "bravo"} {
		fake.alive[SessionKey{Workspace: ws, Name: name}] = true
	}

	tabs, err := svc.ListTabs(ctx, ws)
	if err != nil {
		t.Fatalf("ListTabs: %v", err)
	}
	var order []string
	for _, tb := range tabs {
		order = append(order, tb.SessionName)
	}
	want := []string{"alpha", "bravo", "charlie", "delta"}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("tab order = %v, want %v", order, want)
	}
}

// TestListTabs_SourceWithoutSessionListerIsUnchanged — PTYSessionLister is
// optional; a source that does not implement it must keep today's behavior
// (nil live sessions, metadata-only listing) rather than failing.
func TestListTabs_SourceWithoutSessionListerIsUnchanged(t *testing.T) {
	svc, fake, _ := newLivenessTestSvc(t)
	ctx := context.Background()
	const ws = "w"

	putTestTab(t, svc, ws, "has-metadata")
	fake.alive[SessionKey{Workspace: ws, Name: "has-metadata"}] = true
	fake.alive[SessionKey{Workspace: ws, Name: "orphan"}] = true

	var bare PTYSource = &bareFakePTYSource{PTYSource: fake}
	if _, ok := bare.(PTYSessionLister); ok {
		t.Fatal("bareFakePTYSource implements PTYSessionLister; the test asserts the opposite")
	}
	svc.ptyMgr = bare

	tabs, err := svc.ListTabs(ctx, ws)
	if err != nil {
		t.Fatalf("ListTabs: %v", err)
	}
	if len(tabs) != 1 || tabs[0].SessionName != "has-metadata" {
		t.Fatalf("tabs = %+v, want only has-metadata", tabs)
	}
	if !tabs[0].PTYAlive {
		t.Errorf("has-metadata: pty_alive=false, want true")
	}
}
