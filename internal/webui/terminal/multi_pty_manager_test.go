package terminal

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newTestMultiManager returns a MultiPTYManager with a short grace/idle so
// per-workspace managers created during tests clean up quickly.
func newTestMultiManager(t *testing.T, maxPerWS int) *MultiPTYManager {
	t.Helper()
	mm := NewMultiPTYManager("cat", maxPerWS)
	mm.SetGracePeriod(200 * time.Millisecond)
	mm.SetIdleTimeout(200 * time.Millisecond)
	t.Cleanup(func() { _ = mm.Close() })
	return mm
}

func TestNewMultiPTYManager_Empty(t *testing.T) {
	mm := NewMultiPTYManager("cat", 7)
	t.Cleanup(func() { _ = mm.Close() })
	if got := mm.SessionCount(); got != 0 {
		t.Errorf("SessionCount=%d want 0", got)
	}
	if got := mm.MaxSessions(); got != 7 {
		t.Errorf("MaxSessions=%d want 7", got)
	}
}

func TestRegister_ValidPath(t *testing.T) {
	mm := newTestMultiManager(t, 0)
	dir := t.TempDir()
	if err := mm.Register("ws1", dir); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if mm.hasManager("ws1") {
		t.Errorf("hasManager(ws1)=true immediately after Register; expected lazy")
	}
}

func TestRegister_InvalidPath(t *testing.T) {
	mm := newTestMultiManager(t, 0)
	err := mm.Register("ws1", "/does/not/exist/12345")
	if err == nil {
		t.Fatal("Register: expected error, got nil")
	}
	if !errors.Is(err, ErrInvalidWorkspacePath) {
		t.Errorf("Register error = %v, want errors.Is(ErrInvalidWorkspacePath)", err)
	}
	if mm.hasManager("ws1") {
		t.Errorf("hasManager(ws1)=true after failed Register")
	}

	// Confirm the entry was never stored: AttachSession surfaces
	// ErrWorkspaceNotRegistered, not ErrInvalidWorkspacePath.
	_, _, attErr := mm.AttachSession(SessionKey{Workspace: "ws1", Name: "s"}, 80, 24, nil)
	if !errors.Is(attErr, ErrWorkspaceNotRegistered) {
		t.Errorf("AttachSession error = %v, want errors.Is(ErrWorkspaceNotRegistered)", attErr)
	}
}

func TestRegister_PathIsFile(t *testing.T) {
	mm := newTestMultiManager(t, 0)
	dir := t.TempDir()
	file := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	err := mm.Register("ws1", file)
	if !errors.Is(err, ErrInvalidWorkspacePath) {
		t.Errorf("Register with file path err = %v, want ErrInvalidWorkspacePath", err)
	}
}

func TestRegister_EmptyWorkspaceID(t *testing.T) {
	mm := newTestMultiManager(t, 0)
	err := mm.Register("", t.TempDir())
	if !errors.Is(err, ErrWorkspaceNotRegistered) {
		t.Errorf("Register with empty wsID err = %v, want ErrWorkspaceNotRegistered", err)
	}
}

func TestRegister_EmptyPath(t *testing.T) {
	mm := newTestMultiManager(t, 0)
	err := mm.Register("ws1", "")
	if !errors.Is(err, ErrInvalidWorkspacePath) {
		t.Errorf("Register with empty path err = %v, want ErrInvalidWorkspacePath", err)
	}
}

func TestRegister_ReplacesExisting(t *testing.T) {
	mm := newTestMultiManager(t, 0)
	dirA := t.TempDir()
	dirB := t.TempDir()

	if err := mm.Register("ws1", dirA); err != nil {
		t.Fatalf("Register A: %v", err)
	}
	key := SessionKey{Workspace: "ws1", Name: "s1"}
	att, _, err := mm.AttachSession(key, 80, 24, []string{"-c", "cat"})
	if err != nil {
		t.Fatalf("AttachSession: %v", err)
	}
	if !mm.hasManager("ws1") {
		t.Fatal("hasManager(ws1)=false after AttachSession")
	}

	if err := mm.Register("ws1", dirB); err != nil {
		t.Fatalf("Register B: %v", err)
	}
	if mm.hasManager("ws1") {
		t.Errorf("hasManager(ws1)=true after Register-replace; new entry should be lazy")
	}
	// The prior attachment's output channel should close because the old
	// PTYManager was shut down.
	select {
	case _, ok := <-att.Output():
		if ok {
			// Drain any residual frames, then expect close.
			select {
			case _, ok2 := <-att.Output():
				if ok2 {
					t.Errorf("output channel still open after Register-replace")
				}
			case <-time.After(500 * time.Millisecond):
				t.Errorf("output channel did not close after Register-replace")
			}
		}
	case <-time.After(500 * time.Millisecond):
		t.Errorf("output channel did not close after Register-replace")
	}
}

func TestAttachSession_UnknownWorkspace(t *testing.T) {
	mm := newTestMultiManager(t, 0)
	_, _, err := mm.AttachSession(SessionKey{Workspace: "nope", Name: "s"}, 80, 24, nil)
	if !errors.Is(err, ErrWorkspaceNotRegistered) {
		t.Errorf("AttachSession err = %v, want ErrWorkspaceNotRegistered", err)
	}
}

func TestAttachSession_EmptyWorkspace(t *testing.T) {
	mm := newTestMultiManager(t, 0)
	_, _, err := mm.AttachSession(SessionKey{Workspace: "", Name: "s"}, 80, 24, nil)
	if !errors.Is(err, ErrWorkspaceNotRegistered) {
		t.Errorf("AttachSession empty ws err = %v, want ErrWorkspaceNotRegistered", err)
	}
}

func TestAttachSession_LazyCreate(t *testing.T) {
	mm := newTestMultiManager(t, 0)
	dir := t.TempDir()
	if err := mm.Register("ws1", dir); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if mm.hasManager("ws1") {
		t.Fatal("hasManager(ws1)=true before AttachSession")
	}
	key := SessionKey{Workspace: "ws1", Name: "s1"}
	_, _, err := mm.AttachSession(key, 80, 24, []string{"-c", "cat"})
	if err != nil {
		t.Fatalf("AttachSession: %v", err)
	}
	if !mm.hasManager("ws1") {
		t.Errorf("hasManager(ws1)=false after AttachSession; expected lazy create")
	}
	// Verify the per-ws manager's cwd points at the registered path.
	mm.mu.RLock()
	gotCwd := mm.entries["ws1"].mgr.cwd
	mm.mu.RUnlock()
	if gotCwd != dir {
		t.Errorf("per-ws manager cwd = %q, want %q", gotCwd, dir)
	}
}

func TestAttachSession_RoutingByWorkspace(t *testing.T) {
	mm := newTestMultiManager(t, 0)
	if err := mm.Register("ws1", t.TempDir()); err != nil {
		t.Fatalf("Register ws1: %v", err)
	}
	if err := mm.Register("ws2", t.TempDir()); err != nil {
		t.Fatalf("Register ws2: %v", err)
	}

	k1 := SessionKey{Workspace: "ws1", Name: "s"}
	k2 := SessionKey{Workspace: "ws2", Name: "s"}

	if _, _, err := mm.AttachSession(k1, 80, 24, []string{"-c", "cat"}); err != nil {
		t.Fatalf("attach ws1: %v", err)
	}
	if _, _, err := mm.AttachSession(k2, 80, 24, []string{"-c", "cat"}); err != nil {
		t.Fatalf("attach ws2: %v", err)
	}

	if !mm.HasSession(k1) || !mm.HasSession(k2) {
		t.Fatalf("HasSession ws1=%v ws2=%v", mm.HasSession(k1), mm.HasSession(k2))
	}

	if err := mm.Kill(k1); err != nil {
		t.Fatalf("Kill k1: %v", err)
	}
	if mm.HasSession(k1) {
		t.Errorf("HasSession(k1)=true after Kill")
	}
	if !mm.HasSession(k2) {
		t.Errorf("HasSession(k2)=false; sibling workspace was affected by Kill")
	}
}

func TestConcurrentAttachLazyCreate(t *testing.T) {
	mm := newTestMultiManager(t, 50)
	mm.SetGracePeriod(5 * time.Second)
	dir := t.TempDir()
	if err := mm.Register("ws1", dir); err != nil {
		t.Fatalf("Register: %v", err)
	}

	const n = 50
	var wg sync.WaitGroup
	var errCount atomic.Int32
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			key := SessionKey{Workspace: "ws1", Name: fmt.Sprintf("s-%d", i)}
			if _, _, err := mm.AttachSession(key, 80, 24, []string{"-c", "cat"}); err != nil {
				errCount.Add(1)
				t.Logf("attach %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	if errCount.Load() != 0 {
		t.Errorf("%d concurrent attaches returned errors; want 0", errCount.Load())
	}

	// Exactly one PTYManager must have been created for the workspace.
	mm.mu.RLock()
	entry := mm.entries["ws1"]
	mm.mu.RUnlock()
	if entry == nil || entry.mgr == nil {
		t.Fatal("no per-ws manager created")
	}
	if got := entry.mgr.SessionCount(); got != n {
		t.Errorf("per-ws SessionCount=%d want %d", got, n)
	}
}

// TestConcurrentAttachAndGraceUpdate exercises the race between goroutines
// hammering AttachSession (which creates per-ws managers under mm.mu) and
// SetGracePeriod/SetIdleTimeout (which mutate every existing per-ws manager).
// Catches any lock-ordering bug under `go test -race`.
func TestConcurrentAttachAndGraceUpdate(t *testing.T) {
	mm := newTestMultiManager(t, 20)
	mm.SetGracePeriod(5 * time.Second)
	for i := 0; i < 5; i++ {
		if err := mm.Register(fmt.Sprintf("ws-%d", i), t.TempDir()); err != nil {
			t.Fatalf("Register: %v", err)
		}
	}

	var wg sync.WaitGroup
	done := make(chan struct{})

	// Attacher goroutines create per-ws managers on first attach.
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := SessionKey{Workspace: fmt.Sprintf("ws-%d", i%5), Name: fmt.Sprintf("s-%d", i)}
			if _, _, err := mm.AttachSession(key, 80, 24, []string{"-c", "cat"}); err != nil {
				t.Errorf("attach %d: %v", i, err)
			}
		}(i)
	}

	// Grace/idle updater — mutates every already-created per-ws manager.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				mm.SetGracePeriod(time.Duration(100+time.Now().UnixNano()%100) * time.Millisecond)
				mm.SetIdleTimeout(time.Duration(200+time.Now().UnixNano()%100) * time.Millisecond)
			}
		}
	}()

	// Let the race run briefly, then tell the updater to stop and wait for
	// all goroutines to finish.
	time.Sleep(50 * time.Millisecond)
	close(done)
	wg.Wait()
}

func TestPerWorkspaceCap(t *testing.T) {
	mm := newTestMultiManager(t, 3)
	mm.SetGracePeriod(5 * time.Second)
	if err := mm.Register("ws1", t.TempDir()); err != nil {
		t.Fatalf("Register ws1: %v", err)
	}
	if err := mm.Register("ws2", t.TempDir()); err != nil {
		t.Fatalf("Register ws2: %v", err)
	}

	// Fill ws1 to cap.
	for i := 0; i < 3; i++ {
		k := SessionKey{Workspace: "ws1", Name: fmt.Sprintf("s-%d", i)}
		if _, _, err := mm.AttachSession(k, 80, 24, []string{"-c", "cat"}); err != nil {
			t.Fatalf("ws1 attach %d: %v", i, err)
		}
	}
	// ws1 over-cap.
	over := SessionKey{Workspace: "ws1", Name: "over"}
	if _, _, err := mm.AttachSession(over, 80, 24, []string{"-c", "cat"}); !errors.Is(err, ErrPTYMaxSessionsReached) {
		t.Errorf("ws1 over-cap err = %v, want ErrPTYMaxSessionsReached", err)
	}
	// ws2 still has full cap of its own.
	for i := 0; i < 3; i++ {
		k := SessionKey{Workspace: "ws2", Name: fmt.Sprintf("s-%d", i)}
		if _, _, err := mm.AttachSession(k, 80, 24, []string{"-c", "cat"}); err != nil {
			t.Errorf("ws2 attach %d: %v (per-ws cap leaked across workspaces)", i, err)
		}
	}
}

func TestDeregister_KillsSessions(t *testing.T) {
	mm := newTestMultiManager(t, 0)
	mm.SetGracePeriod(5 * time.Second)
	if err := mm.Register("ws1", t.TempDir()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	var keys []SessionKey
	for i := 0; i < 3; i++ {
		k := SessionKey{Workspace: "ws1", Name: fmt.Sprintf("s-%d", i)}
		if _, _, err := mm.AttachSession(k, 80, 24, []string{"-c", "cat"}); err != nil {
			t.Fatalf("attach %d: %v", i, err)
		}
		keys = append(keys, k)
	}
	if got := mm.SessionCount(); got != 3 {
		t.Fatalf("pre-deregister SessionCount=%d want 3", got)
	}

	mm.Deregister("ws1")

	if got := mm.SessionCount(); got != 0 {
		t.Errorf("post-deregister SessionCount=%d want 0", got)
	}
	for _, k := range keys {
		if mm.HasSession(k) {
			t.Errorf("HasSession(%v)=true after Deregister", k)
		}
	}
	_, _, err := mm.AttachSession(keys[0], 80, 24, nil)
	if !errors.Is(err, ErrWorkspaceNotRegistered) {
		t.Errorf("post-deregister AttachSession err = %v, want ErrWorkspaceNotRegistered", err)
	}
}

func TestDeregister_Unknown(t *testing.T) {
	mm := newTestMultiManager(t, 0)
	// Must not panic.
	mm.Deregister("does-not-exist")
}

func TestDeregister_NeverAttached(t *testing.T) {
	mm := newTestMultiManager(t, 0)
	if err := mm.Register("ws1", t.TempDir()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	// No AttachSession yet — per-ws manager is still nil. Deregister must
	// not panic and must drop the entry.
	mm.Deregister("ws1")
	_, _, err := mm.AttachSession(SessionKey{Workspace: "ws1", Name: "s"}, 80, 24, nil)
	if !errors.Is(err, ErrWorkspaceNotRegistered) {
		t.Errorf("post-Deregister AttachSession err = %v, want ErrWorkspaceNotRegistered", err)
	}
}

func TestSessionCount_Aggregates(t *testing.T) {
	mm := newTestMultiManager(t, 0)
	mm.SetGracePeriod(5 * time.Second)
	if err := mm.Register("ws1", t.TempDir()); err != nil {
		t.Fatalf("Register ws1: %v", err)
	}
	if err := mm.Register("ws2", t.TempDir()); err != nil {
		t.Fatalf("Register ws2: %v", err)
	}
	for i := 0; i < 2; i++ {
		k := SessionKey{Workspace: "ws1", Name: fmt.Sprintf("s-%d", i)}
		if _, _, err := mm.AttachSession(k, 80, 24, []string{"-c", "cat"}); err != nil {
			t.Fatalf("ws1 attach: %v", err)
		}
	}
	for i := 0; i < 3; i++ {
		k := SessionKey{Workspace: "ws2", Name: fmt.Sprintf("s-%d", i)}
		if _, _, err := mm.AttachSession(k, 80, 24, []string{"-c", "cat"}); err != nil {
			t.Fatalf("ws2 attach: %v", err)
		}
	}
	if got := mm.SessionCount(); got != 5 {
		t.Errorf("SessionCount=%d want 5", got)
	}
}

func TestMaxSessions_ReturnsPerWS(t *testing.T) {
	mm := NewMultiPTYManager("cat", 7)
	t.Cleanup(func() { _ = mm.Close() })
	if got := mm.MaxSessions(); got != 7 {
		t.Errorf("MaxSessions=%d want 7", got)
	}

	// Registering workspaces does not change the returned cap.
	if err := mm.Register("ws1", t.TempDir()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := mm.Register("ws2", t.TempDir()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if got := mm.MaxSessions(); got != 7 {
		t.Errorf("MaxSessions after Register=%d want 7 (per-ws cap, not sum)", got)
	}
}

func TestMaxSessions_ZeroMaxReturnsDefault(t *testing.T) {
	mm := NewMultiPTYManager("cat", 0)
	t.Cleanup(func() { _ = mm.Close() })
	if got := mm.MaxSessions(); got != defaultPTYMaxSessions {
		t.Errorf("MaxSessions with 0 cap = %d, want %d", got, defaultPTYMaxSessions)
	}
}

func TestClose_ShutsDownAll(t *testing.T) {
	mm := NewMultiPTYManager("cat", 0)
	mm.SetGracePeriod(5 * time.Second)
	if err := mm.Register("ws1", t.TempDir()); err != nil {
		t.Fatalf("Register ws1: %v", err)
	}
	if err := mm.Register("ws2", t.TempDir()); err != nil {
		t.Fatalf("Register ws2: %v", err)
	}
	for _, ws := range []string{"ws1", "ws2"} {
		k := SessionKey{Workspace: ws, Name: "s"}
		if _, _, err := mm.AttachSession(k, 80, 24, []string{"-c", "cat"}); err != nil {
			t.Fatalf("attach %s: %v", ws, err)
		}
	}
	if err := mm.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := mm.SessionCount(); got != 0 {
		t.Errorf("post-Close SessionCount=%d want 0", got)
	}
	// Second Close is a no-op.
	if err := mm.Close(); err != nil {
		t.Errorf("second Close err = %v, want nil", err)
	}
	// Register after Close fails.
	if err := mm.Register("ws3", t.TempDir()); !errors.Is(err, ErrPTYManagerClosed) {
		t.Errorf("post-Close Register err = %v, want ErrPTYManagerClosed", err)
	}
}

func TestGraceAndIdle_Forward(t *testing.T) {
	mm := NewMultiPTYManager("cat", 0)
	t.Cleanup(func() { _ = mm.Close() })
	mm.SetGracePeriod(123 * time.Millisecond)
	mm.SetIdleTimeout(234 * time.Millisecond)

	// Registering-then-Attaching picks up the values.
	if err := mm.Register("ws1", t.TempDir()); err != nil {
		t.Fatalf("Register ws1: %v", err)
	}
	if _, _, err := mm.AttachSession(SessionKey{Workspace: "ws1", Name: "s"}, 80, 24, []string{"-c", "cat"}); err != nil {
		t.Fatalf("AttachSession: %v", err)
	}
	mm.mu.RLock()
	entry := mm.entries["ws1"]
	mm.mu.RUnlock()
	if entry == nil || entry.mgr == nil {
		t.Fatal("no per-ws manager created")
	}
	if got := entry.mgr.GracePeriod(); got != 123*time.Millisecond {
		t.Errorf("per-ws GracePeriod=%v want 123ms", got)
	}
	if got := entry.mgr.IdleTimeout(); got != 234*time.Millisecond {
		t.Errorf("per-ws IdleTimeout=%v want 234ms", got)
	}

	// Updating after the per-ws manager exists propagates.
	mm.SetGracePeriod(345 * time.Millisecond)
	mm.SetIdleTimeout(456 * time.Millisecond)
	if got := entry.mgr.GracePeriod(); got != 345*time.Millisecond {
		t.Errorf("post-update GracePeriod=%v want 345ms", got)
	}
	if got := entry.mgr.IdleTimeout(); got != 456*time.Millisecond {
		t.Errorf("post-update IdleTimeout=%v want 456ms", got)
	}
	if got := mm.GracePeriod(); got != 345*time.Millisecond {
		t.Errorf("MM GracePeriod=%v want 345ms", got)
	}
	if got := mm.IdleTimeout(); got != 456*time.Millisecond {
		t.Errorf("MM IdleTimeout=%v want 456ms", got)
	}
}

func TestDetach_UnknownWorkspace_NoOp(t *testing.T) {
	mm := newTestMultiManager(t, 0)
	// Must not panic.
	mm.Detach(SessionKey{Workspace: "nope", Name: "s"}, "conn")
}

func TestKill_UnknownWorkspace_ReturnsNil(t *testing.T) {
	mm := newTestMultiManager(t, 0)
	if err := mm.Kill(SessionKey{Workspace: "nope", Name: "s"}); err != nil {
		t.Errorf("Kill unknown ws err = %v, want nil", err)
	}
}

func TestHasSession_UnknownWorkspace_False(t *testing.T) {
	mm := newTestMultiManager(t, 0)
	if mm.HasSession(SessionKey{Workspace: "nope", Name: "s"}) {
		t.Errorf("HasSession unknown ws = true, want false")
	}
}

func TestAttachmentCount_UnknownWorkspace_Zero(t *testing.T) {
	mm := newTestMultiManager(t, 0)
	if got := mm.AttachmentCount(SessionKey{Workspace: "nope", Name: "s"}); got != 0 {
		t.Errorf("AttachmentCount unknown ws = %d, want 0", got)
	}
}

func TestDispatch_LazyUncreated_NoOps(t *testing.T) {
	// After Register but before any AttachSession, no per-ws PTYManager
	// exists. The no-op-on-miss dispatch methods must not create one.
	mm := newTestMultiManager(t, 0)
	if err := mm.Register("ws1", t.TempDir()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	key := SessionKey{Workspace: "ws1", Name: "s"}
	mm.Detach(key, "conn")
	if err := mm.Kill(key); err != nil {
		t.Errorf("Kill err = %v, want nil", err)
	}
	if mm.HasSession(key) {
		t.Errorf("HasSession=true on uncreated ws")
	}
	if got := mm.AttachmentCount(key); got != 0 {
		t.Errorf("AttachmentCount=%d want 0", got)
	}
	if mm.hasManager("ws1") {
		t.Errorf("hasManager=true — Detach/Kill/etc should not create the manager")
	}
}
