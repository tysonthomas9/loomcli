package subscription

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// withFastAgentStatePoll temporarily shrinks agentStatePollInterval for the
// duration of a test so watcher-tick scenarios complete quickly. It restores
// the previous value on cleanup.
func withFastAgentStatePoll(t *testing.T, d time.Duration) {
	t.Helper()
	prev := agentStatePollInterval
	agentStatePollInterval = d
	t.Cleanup(func() { agentStatePollInterval = prev })
}

// collectAgentStateBroadcast waits up to timeout for an agent_state_change
// mutation on client.Send(). Returns the payload (or nil on timeout).
// Other mutation types are ignored so unrelated broadcasts don't fool the
// test; in practice only the agent watcher runs here.
func collectAgentStateBroadcast(client *realtime.Client, wsID string, timeout time.Duration) *realtime.MutationPayload {
	deadline := time.After(timeout)
	for {
		select {
		case m := <-client.Send():
			if m != nil && m.Type == "agent_state_change" && m.WorkspaceID == wsID {
				return m
			}
		case <-deadline:
			return nil
		}
	}
}

// assertNoAgentStateBroadcast verifies that no agent_state_change event is
// received for wsID within the given window.
func assertNoAgentStateBroadcast(t *testing.T, client *realtime.Client, wsID string, window time.Duration) {
	t.Helper()
	if got := collectAgentStateBroadcast(client, wsID, window); got != nil {
		t.Fatalf("unexpected agent_state_change broadcast: %+v", got)
	}
}

// newWatchedSubscriber builds a DaemonSubscriber wired to a live hub with a
// registered client that filters on wsID. Returns the subscriber and the
// client used to observe broadcasts. Caller is responsible for calling
// subscriber.Stop() (or using t.Cleanup).
func newWatchedSubscriber(t *testing.T, wsID string) (*DaemonSubscriber, *realtime.Client, *realtime.Hub) {
	t.Helper()
	hub := realtime.NewHub()
	go hub.Run()
	t.Cleanup(func() { hub.Stop() })

	client := realtime.NewClient(1, 64, 0, nil, wsID)
	hub.RegisterClient(client)
	// Give the hub goroutine a moment to register the client so broadcasts
	// are delivered rather than silently dropped.
	time.Sleep(25 * time.Millisecond)

	sub := NewDaemonSubscriber(nil, hub)
	sub.workspaceID = wsID
	return sub, client, hub
}

// touchFile sets the mtime of path to now-ish, guaranteeing a strictly-later
// mtime than any previous observation. Filesystems like APFS have
// nanosecond resolution, but HFS+ and some Linux FS are 1-second; bumping
// the timestamp forward by at least 1s avoids flakes on coarse filesystems.
func touchFile(t *testing.T, path string, offset time.Duration) time.Time {
	t.Helper()
	newMtime := time.Now().Add(offset)
	if err := os.Chtimes(path, newMtime, newMtime); err != nil {
		t.Fatalf("os.Chtimes(%s): %v", path, err)
	}
	return newMtime
}

// TestAgentStateWatch_BroadcastsOnMtimeChange verifies the watcher emits an
// agent_state_change mutation when the observed file's mtime changes.
func TestAgentStateWatch_BroadcastsOnMtimeChange(t *testing.T) {
	withFastAgentStatePoll(t, 25*time.Millisecond)

	dir := t.TempDir()
	path := filepath.Join(dir, "daemon-agents.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}

	sub, client, _ := newWatchedSubscriber(t, "ws-mtime")
	sub.SetAgentStatePath(path)
	sub.Start()
	t.Cleanup(sub.Stop)

	// Let the watcher observe the initial mtime. The first tick will record
	// it as "new" (prev is zero) and will broadcast once. Drain that.
	if got := collectAgentStateBroadcast(client, "ws-mtime", 500*time.Millisecond); got == nil {
		t.Fatal("expected initial broadcast after watcher observes file for the first time")
	}

	// Bump the mtime forward — this should trigger a second broadcast.
	touchFile(t, path, 2*time.Second)

	got := collectAgentStateBroadcast(client, "ws-mtime", 500*time.Millisecond)
	if got == nil {
		t.Fatal("expected agent_state_change broadcast after mtime bump")
	}
	if got.WorkspaceID != "ws-mtime" {
		t.Errorf("WorkspaceID = %q, want %q", got.WorkspaceID, "ws-mtime")
	}
	if got.Timestamp == "" {
		t.Error("expected non-empty Timestamp")
	}
}

// TestAgentStateWatch_NoBroadcastWhenUnchanged verifies the watcher does not
// broadcast when the file's mtime is stable between ticks.
func TestAgentStateWatch_NoBroadcastWhenUnchanged(t *testing.T) {
	withFastAgentStatePoll(t, 25*time.Millisecond)

	dir := t.TempDir()
	path := filepath.Join(dir, "daemon-agents.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}

	sub, client, _ := newWatchedSubscriber(t, "ws-stable")
	sub.SetAgentStatePath(path)
	sub.Start()
	t.Cleanup(sub.Stop)

	// The watcher will fire one broadcast on the first observed mtime.
	if got := collectAgentStateBroadcast(client, "ws-stable", 500*time.Millisecond); got == nil {
		t.Fatal("expected initial broadcast on first observation")
	}

	// Without any further mtime changes, subsequent ticks must not broadcast.
	assertNoAgentStateBroadcast(t, client, "ws-stable", 250*time.Millisecond)
}

// TestAgentStateWatch_FileMissing verifies the watcher survives os.ErrNotExist
// without broadcasting and without errors logged. We can't easily intercept
// slog output here, but the behavior we care about is that the loop does not
// broadcast and continues running (so a later SetAgentStatePath still works).
func TestAgentStateWatch_FileMissing(t *testing.T) {
	withFastAgentStatePoll(t, 25*time.Millisecond)

	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist.json")

	sub, client, _ := newWatchedSubscriber(t, "ws-missing")
	sub.SetAgentStatePath(missing)
	sub.Start()
	t.Cleanup(sub.Stop)

	// With the file absent, several ticks should fire without any broadcast.
	assertNoAgentStateBroadcast(t, client, "ws-missing", 250*time.Millisecond)

	// Loop must still be alive: create the file now, expect a broadcast.
	if err := os.WriteFile(missing, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("create file: %v", err)
	}
	if got := collectAgentStateBroadcast(client, "ws-missing", 500*time.Millisecond); got == nil {
		t.Fatal("expected broadcast after previously-missing file is created")
	}
}

// TestAgentStateWatch_DeleteThenRecreate verifies the watcher re-broadcasts
// when the file is deleted and then recreated (daemon-restart scenario).
func TestAgentStateWatch_DeleteThenRecreate(t *testing.T) {
	withFastAgentStatePoll(t, 25*time.Millisecond)

	dir := t.TempDir()
	path := filepath.Join(dir, "daemon-agents.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}

	sub, client, _ := newWatchedSubscriber(t, "ws-recreate")
	sub.SetAgentStatePath(path)
	sub.Start()
	t.Cleanup(sub.Stop)

	// Drain initial broadcast.
	if got := collectAgentStateBroadcast(client, "ws-recreate", 500*time.Millisecond); got == nil {
		t.Fatal("expected initial broadcast")
	}

	// Delete the file; the watcher should NOT broadcast while it's missing.
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove file: %v", err)
	}
	assertNoAgentStateBroadcast(t, client, "ws-recreate", 200*time.Millisecond)

	// Recreate the file with a fresh mtime; the watcher should broadcast again.
	if err := os.WriteFile(path, []byte(`{"v":2}`), 0o644); err != nil {
		t.Fatalf("recreate file: %v", err)
	}
	// Nudge the mtime forward explicitly so the recreated mtime is strictly
	// greater than the pre-delete mtime on coarse-resolution filesystems.
	touchFile(t, path, 3*time.Second)

	if got := collectAgentStateBroadcast(client, "ws-recreate", 500*time.Millisecond); got == nil {
		t.Fatal("expected broadcast after file recreation")
	}
}

// TestAgentStateWatch_StopTerminatesWatcher verifies that Stop() causes the
// watcher goroutine to exit promptly, even while it's sitting on a ticker.
func TestAgentStateWatch_StopTerminatesWatcher(t *testing.T) {
	withFastAgentStatePoll(t, 50*time.Millisecond)

	dir := t.TempDir()
	path := filepath.Join(dir, "daemon-agents.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}

	sub, _, _ := newWatchedSubscriber(t, "ws-stop")
	sub.SetAgentStatePath(path)
	sub.Start()

	// Stop should return promptly. The WaitGroup in Stop waits for all three
	// goroutines, including agentStateWatchLoop; if the watcher is wedged this
	// call blocks.
	stopped := make(chan struct{})
	go func() {
		sub.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
		// good
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not terminate the watcher promptly")
	}
}

// TestAgentStateWatch_SetPathBeforeStart verifies that configuring the path
// before Start() works: the first tick picks up the path and emits a broadcast.
func TestAgentStateWatch_SetPathBeforeStart(t *testing.T) {
	withFastAgentStatePoll(t, 25*time.Millisecond)

	dir := t.TempDir()
	path := filepath.Join(dir, "daemon-agents.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}

	sub, client, _ := newWatchedSubscriber(t, "ws-before")
	sub.SetAgentStatePath(path) // set BEFORE Start
	sub.Start()
	t.Cleanup(sub.Stop)

	if got := collectAgentStateBroadcast(client, "ws-before", 500*time.Millisecond); got == nil {
		t.Fatal("expected broadcast after Start() with path set beforehand")
	}
}

// TestAgentStateWatch_SetPathAfterStart verifies that the watcher picks up a
// path that is set AFTER Start() within one tick interval.
func TestAgentStateWatch_SetPathAfterStart(t *testing.T) {
	withFastAgentStatePoll(t, 25*time.Millisecond)

	dir := t.TempDir()
	path := filepath.Join(dir, "daemon-agents.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}

	sub, client, _ := newWatchedSubscriber(t, "ws-after")
	sub.Start() // start first — watcher is idle with empty path
	t.Cleanup(sub.Stop)

	// While the path is empty, there should be no broadcast.
	assertNoAgentStateBroadcast(t, client, "ws-after", 150*time.Millisecond)

	// Now configure the path. The watcher should pick it up on its next tick
	// and fire a broadcast.
	sub.SetAgentStatePath(path)
	if got := collectAgentStateBroadcast(client, "ws-after", 1*time.Second); got == nil {
		t.Fatal("expected broadcast after SetAgentStatePath called post-Start")
	}
}

// TestAgentStateWatch_EmptyPathNoop verifies that an empty path produces no
// broadcasts and no observable errors (the loop is idle but alive).
func TestAgentStateWatch_EmptyPathNoop(t *testing.T) {
	withFastAgentStatePoll(t, 25*time.Millisecond)

	sub, client, _ := newWatchedSubscriber(t, "ws-empty")
	// Intentionally do NOT call SetAgentStatePath; leave it as "".
	sub.Start()
	t.Cleanup(sub.Stop)

	// Several ticks should elapse with no broadcast.
	assertNoAgentStateBroadcast(t, client, "ws-empty", 200*time.Millisecond)
}
