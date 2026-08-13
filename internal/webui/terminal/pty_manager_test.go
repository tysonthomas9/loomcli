package terminal

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestNewPTYManager_EmptyCwdPanics verifies the constructor refuses an empty
// cwd instead of silently falling back to $HOME or any other default. The
// panic is load-bearing — a silent fallback is the bug class that the
// MultiPTYManager epic exists to eliminate.
func TestNewPTYManager_EmptyCwdPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on empty cwd")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("panic value = %#v, want string", r)
		}
		if !strings.Contains(msg, "cwd is required") {
			t.Errorf("panic message = %q, want contains 'cwd is required'", msg)
		}
	}()
	_ = NewPTYManager("", 0, "")
}

// TestNewPTYManager_CwdIsRespected verifies the constructor threads the cwd
// argument straight through to the manager's cwd field (the value later
// assigned to cmd.Dir at spawn time).
func TestNewPTYManager_CwdIsRespected(t *testing.T) {
	dir := t.TempDir()
	m := NewPTYManager("cat", 0, dir)
	t.Cleanup(func() { _ = m.Shutdown() })
	if m.cwd != dir {
		t.Errorf("m.cwd = %q, want %q", m.cwd, dir)
	}
}

func TestTerminalSpawnEnv_StripsStaleGeometryAndOverridesTERM(t *testing.T) {
	env := terminalSpawnEnv([]string{
		"PATH=/usr/bin",
		"COLUMNS=88",
		"LINES=33",
		"TERM=screen-256color",
		"HOME=/tmp/home",
	})

	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "COLUMNS=") {
		t.Fatalf("terminalSpawnEnv() leaked COLUMNS: %q", joined)
	}
	if strings.Contains(joined, "LINES=") {
		t.Fatalf("terminalSpawnEnv() leaked LINES: %q", joined)
	}
	if strings.Contains(joined, "TERM=screen-256color") {
		t.Fatalf("terminalSpawnEnv() kept stale TERM: %q", joined)
	}
	if !strings.Contains(joined, termEnv) {
		t.Fatalf("terminalSpawnEnv() missing %q: %q", termEnv, joined)
	}
}

func TestTerminalSessionEnv_ScopesWorkspace(t *testing.T) {
	env := terminalSessionEnv([]string{
		"PATH=/usr/bin",
		"LOOM_WORKSPACE=stale",
		termEnv,
	}, SessionKey{Workspace: "DOGFOODUI", Name: "lead-codex-1"})

	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "LOOM_WORKSPACE=stale") {
		t.Fatalf("terminalSessionEnv() leaked stale workspace: %q", joined)
	}
	if !strings.Contains(joined, "LOOM_WORKSPACE=DOGFOODUI") {
		t.Fatalf("terminalSessionEnv() missing workspace override: %q", joined)
	}
}

func TestTerminalSessionEnv_UnscopedStripsWorkspace(t *testing.T) {
	env := terminalSessionEnv([]string{
		"PATH=/usr/bin",
		"LOOM_WORKSPACE=stale",
		termEnv,
	}, SessionKey{Name: "shell"})

	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "LOOM_WORKSPACE=") {
		t.Fatalf("terminalSessionEnv() leaked workspace into unscoped session: %q", joined)
	}
}

// newTestManager returns a manager configured to spawn `/bin/bash -c "cat"`.
// cat echoes stdin to stdout so tests can deterministically drive the PTY.
func newTestManager(t *testing.T) *PTYManager {
	t.Helper()
	m := NewPTYManager("cat", 0, t.TempDir())
	m.SetGracePeriod(200 * time.Millisecond)
	m.SetIdleTimeout(200 * time.Millisecond)
	t.Cleanup(func() { _ = m.Shutdown() })
	return m
}

// readChunk drains up to 500 ms of output from an attachment, returning the
// accumulated bytes. Used to synchronize with the PTY echo.
func readChunk(t *testing.T, att Attachment, deadline time.Duration) []byte {
	t.Helper()
	out := make([]byte, 0, 256)
	timeout := time.After(deadline)
	for {
		select {
		case chunk, ok := <-att.Output():
			if !ok {
				return out
			}
			out = append(out, chunk...)
		case <-timeout:
			return out
		}
	}
}

// readChunkContains reads from the attachment until the accumulated bytes
// contain `needle`, or the deadline elapses. Unlike readChunk (which drains
// a fixed window), this is synchronization-by-content so multi-attach tests
// don't race on drain-chunk boundaries.
func readChunkContains(t *testing.T, att Attachment, needle []byte, deadline time.Duration) bool {
	t.Helper()
	out := make([]byte, 0, 256)
	timeout := time.After(deadline)
	for {
		select {
		case chunk, ok := <-att.Output():
			if !ok {
				return bytes.Contains(out, needle)
			}
			out = append(out, chunk...)
			if bytes.Contains(out, needle) {
				return true
			}
		case <-timeout:
			return bytes.Contains(out, needle)
		}
	}
}

func waitUntil(t *testing.T, cond func() bool, deadline time.Duration, msg string) {
	t.Helper()
	start := time.Now()
	for time.Since(start) < deadline {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", msg)
}

func TestAttach_SpawnsFreshSession(t *testing.T) {
	m := newTestManager(t)
	key := SessionKey{Workspace: "ws1", Name: "lead-shell-1"}

	att, reattach, err := m.AttachSession(key, 80, 24, &LaunchSpec{Argv: []string{"-c", "cat"}})
	if err != nil {
		t.Fatalf("AttachSession: %v", err)
	}
	if reattach {
		t.Errorf("reattach=true on fresh session")
	}
	if att.Scrollback() != nil {
		t.Errorf("Scrollback() non-nil on fresh session")
	}
	if got := m.SessionCount(); got != 1 {
		t.Errorf("SessionCount=%d want 1", got)
	}
	m.Detach(key, att.ConnID())
}

func TestAttachSpawnsSessionWhenRecordingCannotStart(t *testing.T) {
	m := newTestManager(t)
	badRoot := filepath.Join(t.TempDir(), "recording-root-is-a-file")
	if err := os.WriteFile(badRoot, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("seed unusable recording root: %v", err)
	}
	m.SetRecordingStore(NewRecordingStore(badRoot, nil))
	key := SessionKey{Workspace: "ws1", Name: "recording-unavailable"}

	att, _, err := m.AttachSession(key, 80, 24, &LaunchSpec{Argv: []string{"-c", "cat"}})
	if err != nil {
		t.Fatalf("AttachSession failed for a recording-only error: %v", err)
	}
	availability, ok := att.(interface{ RecordingAvailable() bool })
	if !ok || availability.RecordingAvailable() {
		t.Fatalf("recording availability = %v, want false", ok && availability.RecordingAvailable())
	}
	if _, err := att.WriteInput([]byte("shell-still-alive\n")); err != nil {
		t.Fatalf("WriteInput: %v", err)
	}
	if !readChunkContains(t, att, []byte("shell-still-alive"), time.Second) {
		t.Fatal("PTY did not remain usable after recording startup failed")
	}
}

func TestDetachDoesNotKillImmediately(t *testing.T) {
	m := newTestManager(t)
	key := SessionKey{Workspace: "ws1", Name: "lead-shell-1"}

	att, _, err := m.AttachSession(key, 80, 24, &LaunchSpec{Argv: []string{"-c", "cat"}})
	if err != nil {
		t.Fatalf("AttachSession: %v", err)
	}
	m.Detach(key, att.ConnID())

	// Session should still be live during the grace window.
	if got := m.SessionCount(); got != 1 {
		t.Errorf("SessionCount after Detach=%d want 1 (still in grace)", got)
	}
}

func TestReattachWithinGraceReplaysScrollback(t *testing.T) {
	m := newTestManager(t)
	m.SetGracePeriod(2 * time.Second) // wide enough to not race the test
	key := SessionKey{Workspace: "ws1", Name: "lead-shell-1"}

	att1, _, err := m.AttachSession(key, 80, 24, &LaunchSpec{Argv: []string{"-c", "cat"}})
	if err != nil {
		t.Fatalf("AttachSession: %v", err)
	}

	// Send data through cat; it echoes back to stdout → scrollback.
	if _, err := att1.WriteInput([]byte("hello-world\n")); err != nil {
		t.Fatalf("WriteInput: %v", err)
	}
	out1 := readChunk(t, att1, 500*time.Millisecond)
	if !bytes.Contains(out1, []byte("hello-world")) {
		t.Fatalf("first attach output missing echo; got %q", string(out1))
	}

	// Detach and immediately reattach.
	m.Detach(key, att1.ConnID())
	att2, reattach, err := m.AttachSession(key, 80, 24, nil)
	if err != nil {
		t.Fatalf("reattach: %v", err)
	}
	if !reattach {
		t.Errorf("reattach=false on existing session")
	}
	replay := att2.Scrollback()
	if !bytes.HasPrefix(replay, []byte("\x1b[2J\x1b[H")) {
		t.Errorf("replay missing reset prefix; got %q", string(replay[:min(8, len(replay))]))
	}
	if !bytes.Contains(replay, []byte("hello-world")) {
		t.Errorf("replay missing prior output; got %q", string(replay))
	}
	m.Detach(key, att2.ConnID())
}

func TestGracePeriodExpiryKillsSession(t *testing.T) {
	m := newTestManager(t)
	m.SetGracePeriod(50 * time.Millisecond)
	key := SessionKey{Workspace: "ws1", Name: "lead-shell-1"}

	att, _, err := m.AttachSession(key, 80, 24, &LaunchSpec{Argv: []string{"-c", "cat"}})
	if err != nil {
		t.Fatalf("AttachSession: %v", err)
	}
	m.Detach(key, att.ConnID())
	waitUntil(t, func() bool { return m.SessionCount() == 0 }, time.Second,
		"session count to reach 0 after grace expiry")
}

func TestExplicitKillTerminatesImmediately(t *testing.T) {
	m := newTestManager(t)
	key := SessionKey{Workspace: "ws1", Name: "lead-shell-1"}

	att, _, err := m.AttachSession(key, 80, 24, &LaunchSpec{Argv: []string{"-c", "cat"}})
	if err != nil {
		t.Fatalf("AttachSession: %v", err)
	}
	if err := m.Kill(key); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if got := m.SessionCount(); got != 0 {
		t.Errorf("SessionCount after Kill=%d want 0", got)
	}
	// Output channel should be closed for the formerly-attached consumer.
	select {
	case _, ok := <-att.Output():
		if ok {
			// One residual frame is acceptable if drained just before close.
			select {
			case _, ok2 := <-att.Output():
				if ok2 {
					t.Errorf("output channel still open after Kill")
				}
			case <-time.After(200 * time.Millisecond):
				t.Errorf("output channel did not close after Kill")
			}
		}
	case <-time.After(200 * time.Millisecond):
		t.Errorf("output channel did not close after Kill")
	}
}

func TestIdleReapClosesDetachedSession(t *testing.T) {
	m := newTestManager(t)
	m.SetGracePeriod(10 * time.Second) // disable grace path
	m.SetIdleTimeout(50 * time.Millisecond)
	key := SessionKey{Workspace: "ws1", Name: "lead-shell-1"}

	att, _, err := m.AttachSession(key, 80, 24, &LaunchSpec{Argv: []string{"-c", "cat"}})
	if err != nil {
		t.Fatalf("AttachSession: %v", err)
	}
	m.Detach(key, att.ConnID())

	// The reaper wakes every defaultReaperTick (60 s) so triggering it by
	// time alone would be slow. Poke reapIdle directly.
	waitUntil(t, func() bool {
		m.reapIdle()
		return m.SessionCount() == 0
	}, 2*time.Second, "idle reap to clear session")
}

func TestSessionCountIncludesDetachedUpToMax(t *testing.T) {
	m := newTestManager(t)
	m.SetGracePeriod(5 * time.Second)

	type attached struct {
		key SessionKey
		att Attachment
	}
	var all []attached
	for i := 0; i < m.MaxSessions(); i++ {
		k := SessionKey{Workspace: "ws", Name: fmt.Sprintf("s-%d", i)}
		att, _, err := m.AttachSession(k, 80, 24, &LaunchSpec{Argv: []string{"-c", "cat"}})
		if err != nil {
			t.Fatalf("attach %d: %v", i, err)
		}
		all = append(all, attached{k, att})
	}
	// Detach every second one. Detached sessions still count toward the cap.
	for i, a := range all {
		if i%2 == 0 {
			m.Detach(a.key, a.att.ConnID())
		}
	}
	if got := m.SessionCount(); got != m.MaxSessions() {
		t.Errorf("SessionCount=%d want %d", got, m.MaxSessions())
	}

	key := SessionKey{Workspace: "ws", Name: "over-cap"}
	_, _, err := m.AttachSession(key, 80, 24, &LaunchSpec{Argv: []string{"-c", "cat"}})
	if err == nil {
		t.Errorf("expected ErrPTYMaxSessionsReached, got nil")
	}
}

// TestSecondAttachCoexistsAndReceivesScrollback verifies the multi-client
// contract: a second AttachSession for the same key joins the existing
// session (does NOT kick the first), and its replay includes output the
// first client has already seen.
func TestSecondAttachCoexistsAndReceivesScrollback(t *testing.T) {
	m := newTestManager(t)
	m.SetGracePeriod(5 * time.Second)
	key := SessionKey{Workspace: "ws1", Name: "lead-shell-1"}

	att1, _, err := m.AttachSession(key, 80, 24, &LaunchSpec{Argv: []string{"-c", "cat"}})
	if err != nil {
		t.Fatalf("AttachSession: %v", err)
	}

	if _, err := att1.WriteInput([]byte("marker-abc\n")); err != nil {
		t.Fatalf("WriteInput: %v", err)
	}
	readChunk(t, att1, 500*time.Millisecond) // ensure drain observed the echo

	att2, reattach, err := m.AttachSession(key, 80, 24, nil)
	if err != nil {
		t.Fatalf("second attach: %v", err)
	}
	if !reattach {
		t.Errorf("reattach=false; want true for second concurrent attach")
	}
	if !bytes.Contains(att2.Scrollback(), []byte("marker-abc")) {
		t.Errorf("second attach replay missing marker; got %q", string(att2.Scrollback()))
	}

	// First attachment must stay open; check that a round-trip input→output
	// after the second attach reaches BOTH clients' channels.
	if _, err := att1.WriteInput([]byte("post-second\n")); err != nil {
		t.Fatalf("WriteInput post-second: %v", err)
	}
	got1 := readChunkContains(t, att1, []byte("post-second"), time.Second)
	got2 := readChunkContains(t, att2, []byte("post-second"), time.Second)
	if !got1 {
		t.Errorf("first attachment did not receive post-second output")
	}
	if !got2 {
		t.Errorf("second attachment did not receive post-second output")
	}

	m.Detach(key, att1.ConnID())
	m.Detach(key, att2.ConnID())
}

// TestMultiAttach_DetachToZeroArmsKillTimer verifies the grace-period
// invariant for multi-client: detaching one of N clients does NOT arm the
// kill timer; only the last detach does.
func TestMultiAttach_DetachToZeroArmsKillTimer(t *testing.T) {
	m := newTestManager(t)
	m.SetGracePeriod(100 * time.Millisecond)
	key := SessionKey{Workspace: "ws1", Name: "multi"}

	att1, _, err := m.AttachSession(key, 80, 24, &LaunchSpec{Argv: []string{"-c", "cat"}})
	if err != nil {
		t.Fatalf("first attach: %v", err)
	}
	att2, _, err := m.AttachSession(key, 80, 24, nil)
	if err != nil {
		t.Fatalf("second attach: %v", err)
	}

	m.Detach(key, att1.ConnID())
	// One client still attached — session must NOT be reaped.
	time.Sleep(250 * time.Millisecond)
	if m.SessionCount() != 1 {
		t.Fatalf("session killed while one client still attached; SessionCount=%d", m.SessionCount())
	}

	// Last detach — grace timer arms and fires within the grace window.
	m.Detach(key, att2.ConnID())
	waitUntil(t, func() bool { return m.SessionCount() == 0 }, 2*time.Second,
		"session to be reaped after last client detaches")
}

// TestMultiAttach_SlowClientDoesNotStallFast exercises the slow-client
// policy: a client whose output channel has backed up must not block the
// drain goroutine or starve other attached clients. Drain fan-out happens
// outside the attach mutex and uses non-blocking sends, so a full channel
// on one attachment just drops frames for that attachment.
func TestMultiAttach_SlowClientDoesNotStallFast(t *testing.T) {
	m := newTestManager(t)
	m.SetGracePeriod(5 * time.Second)
	key := SessionKey{Workspace: "ws1", Name: "slow"}

	slow, _, err := m.AttachSession(key, 80, 24, &LaunchSpec{Argv: []string{"-c", "cat"}})
	if err != nil {
		t.Fatalf("slow attach: %v", err)
	}
	fast, _, err := m.AttachSession(key, 80, 24, nil)
	if err != nil {
		t.Fatalf("fast attach: %v", err)
	}

	// Never read from `slow` — its channel will saturate at attachBufferSize.
	// Pump enough input to overflow it several times over.
	for i := 0; i < attachBufferSize*3; i++ {
		if _, err := fast.WriteInput([]byte("x\n")); err != nil {
			t.Fatalf("WriteInput: %v", err)
		}
	}

	// Fast client must still receive output within the deadline — if the
	// drain goroutine were blocked on the slow channel this would time out.
	if !readChunkContains(t, fast, []byte("x"), 2*time.Second) {
		t.Fatalf("fast client did not receive output (drain may be blocked by slow client)")
	}

	// Slow client is still attached — not kicked.
	if m.SessionCount() != 1 {
		t.Errorf("expected session still live; SessionCount=%d", m.SessionCount())
	}

	m.Detach(key, slow.ConnID())
	m.Detach(key, fast.ConnID())
}

// TestMultiAttach_AttachmentCount verifies the count exposed on PTYSource
// tracks the map size through attach / detach / Kill.
func TestMultiAttach_AttachmentCount(t *testing.T) {
	m := newTestManager(t)
	m.SetGracePeriod(5 * time.Second)
	key := SessionKey{Workspace: "ws1", Name: "count"}

	if got := m.AttachmentCount(key); got != 0 {
		t.Errorf("initial AttachmentCount=%d; want 0", got)
	}

	att1, _, err := m.AttachSession(key, 80, 24, &LaunchSpec{Argv: []string{"-c", "cat"}})
	if err != nil {
		t.Fatalf("attach1: %v", err)
	}
	if got := m.AttachmentCount(key); got != 1 {
		t.Errorf("after att1 AttachmentCount=%d; want 1", got)
	}

	att2, _, err := m.AttachSession(key, 80, 24, nil)
	if err != nil {
		t.Fatalf("attach2: %v", err)
	}
	if got := m.AttachmentCount(key); got != 2 {
		t.Errorf("after att2 AttachmentCount=%d; want 2", got)
	}

	m.Detach(key, att1.ConnID())
	if got := m.AttachmentCount(key); got != 1 {
		t.Errorf("after detach1 AttachmentCount=%d; want 1", got)
	}

	m.Detach(key, att2.ConnID())
	// Session still alive in the grace window but with zero attachments.
	if got := m.AttachmentCount(key); got != 0 {
		t.Errorf("after detach2 AttachmentCount=%d; want 0", got)
	}
}

func TestChildExitRemovesSession(t *testing.T) {
	m := newTestManager(t)
	key := SessionKey{Workspace: "ws1", Name: "lead-shell-1"}

	// A command that exits immediately.
	_, _, err := m.AttachSession(key, 80, 24, &LaunchSpec{Argv: []string{"-c", "true"}})
	if err != nil {
		t.Fatalf("AttachSession: %v", err)
	}
	waitUntil(t, func() bool { return m.SessionCount() == 0 }, 2*time.Second,
		"session to be removed after child exits")
}

// TestShutdown_RejectsFutureAttach — MultiPTYManager.Deregister relies on
// this contract to prevent orphan sessions after the entry is dropped.
func TestShutdown_RejectsFutureAttach(t *testing.T) {
	m := NewPTYManager("cat", 0, t.TempDir())
	if err := m.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	_, _, err := m.AttachSession(SessionKey{Workspace: "ws1", Name: "s"}, 80, 24, &LaunchSpec{Argv: []string{"-c", "cat"}})
	if !errors.Is(err, ErrPTYManagerClosed) {
		t.Errorf("post-Shutdown AttachSession err = %v, want ErrPTYManagerClosed", err)
	}
	if got := m.SessionCount(); got != 0 {
		t.Errorf("post-Shutdown SessionCount=%d want 0 (attach must not have spawned)", got)
	}
}

// TestShutdown_Idempotent — called from both Deregister and Close paths;
// a naive implementation would panic on the second close(reaperStop).
func TestShutdown_Idempotent(t *testing.T) {
	m := NewPTYManager("cat", 0, t.TempDir())
	if err := m.Shutdown(); err != nil {
		t.Fatalf("first Shutdown: %v", err)
	}
	if err := m.Shutdown(); err != nil {
		t.Errorf("second Shutdown: %v, want nil", err)
	}
}

func waitForSessionSelfExit(t *testing.T, m *PTYManager, key SessionKey) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if m.SessionClosed(key) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("session %v did not close after instant-exit command", key)
}

func countRecordingGenerationDirs(t *testing.T, sessionDir string) int {
	t.Helper()
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read session recording dir: %v", err)
	}
	dirs := 0
	for _, entry := range entries {
		if entry.IsDir() {
			dirs++
		}
	}
	return dirs
}

func TestSpawnBreakerStopsInstantExitRespawnLoop(t *testing.T) {
	m := newTestManager(t)
	root := t.TempDir()
	m.SetRecordingStore(NewRecordingStore(root, nil))
	key := SessionKey{Workspace: "ws1", Name: "crash-loop"}
	launch := &LaunchSpec{Argv: []string{"-c", "exit 0"}}

	for i := 0; i < 3; i++ {
		_, _, err := m.AttachSession(key, 80, 24, launch)
		if err != nil && !errors.Is(err, ErrPTYSessionExited) {
			t.Fatalf("attach %d: %v", i+1, err)
		}
		waitForSessionSelfExit(t, m, key)
	}

	sessionDir := filepath.Join(root, "ws1", "crash-loop", "generations")
	beforeRefusal := countRecordingGenerationDirs(t, sessionDir)
	if beforeRefusal > 3 {
		t.Fatalf("generation dirs after 3 attach attempts = %d, want at most one per attempt", beforeRefusal)
	}
	if _, _, err := m.AttachSession(key, 80, 24, launch); !errors.Is(err, ErrPTYSpawnBackoff) {
		t.Fatalf("attach after 3 fast self-exits: err = %v, want ErrPTYSpawnBackoff", err)
	}
	if got := countRecordingGenerationDirs(t, sessionDir); got != beforeRefusal {
		t.Fatalf("generation dirs grew %d -> %d across a refused attach, want no growth", beforeRefusal, got)
	}

	// The breaker is per-session: an unrelated healthy session still spawns.
	other := SessionKey{Workspace: "ws1", Name: "healthy-neighbor"}
	att, _, err := m.AttachSession(other, 80, 24, &LaunchSpec{Argv: []string{"-c", "cat"}})
	if err != nil {
		t.Fatalf("healthy neighbor session blocked: %v", err)
	}
	if _, err := att.WriteInput([]byte("alive\n")); err != nil {
		t.Fatalf("WriteInput to healthy neighbor: %v", err)
	}
}

func TestReattachReplayIsEmulatorRenderedNotRawRing(t *testing.T) {
	m := newTestManager(t)
	m.SetGracePeriod(2 * time.Second)
	m.SetRecordingStore(NewRecordingStore(t.TempDir(), nil))
	key := SessionKey{Workspace: "ws1", Name: "replay-rendered"}

	// The child prints raw bytes ending in a dangling incomplete CSI, which
	// lands verbatim at the tail of the PTY stream and the replay ring.
	launch := &LaunchSpec{Argv: []string{"-c", `printf 'marker-text\ntail\033['; cat`}}
	att1, _, err := m.AttachSession(key, 80, 24, launch)
	if err != nil {
		t.Fatalf("AttachSession: %v", err)
	}
	if !readChunkContains(t, att1, []byte("tail"), 2*time.Second) {
		t.Fatal("child output never arrived")
	}
	// The dangling ESC [ normally arrives in the same PTY read as "tail", but
	// nothing guarantees it; wait until the ring holds it before detaching so
	// the reattach below deterministically sees the torn tail.
	m.mu.Lock()
	ring := m.sessions[key].scrollback
	m.mu.Unlock()
	deadline := time.Now().Add(2 * time.Second)
	for !bytes.HasSuffix(ring.Bytes(), []byte{0x1b, '['}) {
		if time.Now().After(deadline) {
			t.Fatalf("torn escape never reached the ring: %q", ring.Bytes())
		}
		time.Sleep(5 * time.Millisecond)
	}
	m.Detach(key, att1.ConnID())

	att2, reattached, err := m.AttachSession(key, 80, 24, &LaunchSpec{Argv: []string{"-c", "cat"}})
	if err != nil {
		t.Fatalf("re-AttachSession: %v", err)
	}
	if !reattached {
		t.Fatal("expected grace-window reattach, got fresh session")
	}
	replay := att2.Scrollback()
	if !bytes.Contains(replay, []byte("marker-text")) {
		t.Fatalf("replay lost screen content: %q", replay)
	}
	// The raw ring would replay "marker-text\r\ntail\x1b[" verbatim; a
	// rendered replay reconstructs cells with absolute row addressing, so
	// that raw CR/LF juxtaposition must be gone.
	if bytes.Contains(replay, []byte("marker-text\r\ntail")) {
		t.Fatalf("replay contains raw ring bytes, not a rendered screen: %q", replay)
	}
	// The stream's dangling incomplete CSI must be preserved verbatim at the
	// very end: the next live chunk delivered to this attachment begins with
	// the sequence's remaining bytes, and without the fragment the client
	// would print them as literal text.
	if !bytes.HasSuffix(replay, []byte{0x1b, '['}) {
		t.Fatalf("replay dropped the in-flight escape fragment: %q", replay)
	}
	// Everything before that fragment must be fully terminated sequences —
	// the tear at the ring head (eviction trims at arbitrary byte offsets)
	// must never surface in a rendered replay.
	body := replay[:len(replay)-2]
	if esc := bytes.LastIndexByte(body, 0x1b); esc >= 0 && !escapeSequenceComplete(body[esc:]) {
		t.Fatalf("rendered replay contains an interior torn sequence: %q", body[esc:])
	}
}
