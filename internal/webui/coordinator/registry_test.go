package coordinator

import (
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"testing"
)

// mockHook is a test helper implementing LifecycleHook.
type mockHook struct {
	name      string
	critical  bool
	regErr    error                          // returned by OnRegister
	provideFn func(ctx *RegistrationContext) // optional: call Provide in OnRegister
	calls     *[]string                      // shared call log
}

func (h *mockHook) Name() string   { return h.name }
func (h *mockHook) Critical() bool { return h.critical }

func (h *mockHook) OnRegister(ctx *RegistrationContext) error {
	*h.calls = append(*h.calls, "register:"+h.name)
	if h.provideFn != nil {
		h.provideFn(ctx)
	}
	return h.regErr
}

func (h *mockHook) OnDeregister(ctx DeregistrationContext) {
	*h.calls = append(*h.calls, "deregister:"+h.name)
}

func (h *mockHook) OnRollback(ctx DeregistrationContext) {
	*h.calls = append(*h.calls, "rollback:"+h.name)
}

// panicHook panics in OnRollback and/or OnDeregister.
type panicHook struct {
	mockHook
	panicOnRollback   bool
	panicOnDeregister bool
}

func (h *panicHook) OnRollback(ctx DeregistrationContext) {
	*h.calls = append(*h.calls, "rollback:"+h.name)
	if h.panicOnRollback {
		panic("rollback panic from " + h.name)
	}
}

func (h *panicHook) OnDeregister(ctx DeregistrationContext) {
	*h.calls = append(*h.calls, "deregister:"+h.name)
	if h.panicOnDeregister {
		panic("deregister panic from " + h.name)
	}
}

func newRegistry(t *testing.T) *WorkspaceRegistry {
	t.Helper()
	return NewWorkspaceRegistry(slog.Default())
}

func TestRegistry_Register_CallsHooksInOrder(t *testing.T) {
	var calls []string
	r := newRegistry(t)
	r.AddHook(&mockHook{name: "a", calls: &calls})
	r.AddHook(&mockHook{name: "b", calls: &calls})
	r.AddHook(&mockHook{name: "c", calls: &calls})

	if err := r.Register("ws-1", "/tmp/ws1"); err != nil {
		t.Fatal(err)
	}

	want := []string{"register:a", "register:b", "register:c"}
	assertCalls(t, calls, want)
}

func TestRegistry_Register_CriticalHookFailure_TriggersRollback(t *testing.T) {
	var calls []string
	r := newRegistry(t)
	r.AddHook(&mockHook{name: "a", calls: &calls})
	r.AddHook(&mockHook{name: "b", critical: true, regErr: errors.New("fail"), calls: &calls})
	r.AddHook(&mockHook{name: "c", calls: &calls})

	err := r.Register("ws-1", "/tmp/ws1")
	if err == nil {
		t.Fatal("expected error from critical hook failure")
	}
	if !errors.Is(err, errors.New("fail")) {
		// Check the error message wraps the hook name.
		if got := err.Error(); got != `hook "b": fail` {
			t.Fatalf("unexpected error: %s", got)
		}
	}

	want := []string{"register:a", "register:b", "rollback:a"}
	assertCalls(t, calls, want)

	// Workspace should NOT be in active map.
	if ids := r.WorkspaceIDs(); len(ids) != 0 {
		t.Fatalf("expected no registered workspaces, got %v", ids)
	}
}

func TestRegistry_Register_NonCriticalHookFailure_ContinuesChain(t *testing.T) {
	var calls []string
	r := newRegistry(t)
	r.AddHook(&mockHook{name: "a", calls: &calls})
	r.AddHook(&mockHook{name: "b", critical: false, regErr: errors.New("soft fail"), calls: &calls})
	r.AddHook(&mockHook{name: "c", calls: &calls})

	if err := r.Register("ws-1", "/tmp/ws1"); err != nil {
		t.Fatal(err)
	}

	want := []string{"register:a", "register:b", "register:c"}
	assertCalls(t, calls, want)

	// Workspace IS registered, but b is not in the succeeded list.
	ids := r.WorkspaceIDs()
	if len(ids) != 1 || ids[0] != "ws-1" {
		t.Fatalf("expected [ws-1], got %v", ids)
	}
}

func TestRegistry_Deregister_CallsHooksInReverseOrder(t *testing.T) {
	var calls []string
	r := newRegistry(t)
	r.AddHook(&mockHook{name: "a", calls: &calls})
	r.AddHook(&mockHook{name: "b", calls: &calls})
	r.AddHook(&mockHook{name: "c", calls: &calls})
	r.Register("ws-1", "/tmp/ws1")

	calls = nil // reset
	r.Deregister("ws-1")

	want := []string{"deregister:c", "deregister:b", "deregister:a"}
	assertCalls(t, calls, want)
}

func TestRegistry_Deregister_UnknownWorkspace_NoOp(t *testing.T) {
	r := newRegistry(t)
	r.Deregister("nonexistent") // should not panic
}

func TestRegistry_Deregister_EmptyID_NoOp(t *testing.T) {
	r := newRegistry(t)
	r.Deregister("") // should not panic
}

func TestRegistry_Register_EmptyID_Error(t *testing.T) {
	r := newRegistry(t)
	err := r.Register("", "/tmp/ws1")
	if !errors.Is(err, ErrEmptyWorkspaceID) {
		t.Fatalf("expected ErrEmptyWorkspaceID, got %v", err)
	}
}

func TestRegistry_Register_EmptyPath_Error(t *testing.T) {
	r := newRegistry(t)
	err := r.Register("ws-1", "")
	if !errors.Is(err, ErrEmptyWorkspacePath) {
		t.Fatalf("expected ErrEmptyWorkspacePath, got %v", err)
	}
}

func TestRegistry_Close_PreventsNewRegistrations(t *testing.T) {
	r := newRegistry(t)
	r.Close()

	err := r.Register("ws-1", "/tmp/ws1")
	if !errors.Is(err, ErrRegistryClosed) {
		t.Fatalf("expected ErrRegistryClosed, got %v", err)
	}
}

func TestRegistry_AddHook_DuplicateName_Error(t *testing.T) {
	var calls []string
	r := newRegistry(t)
	r.AddHook(&mockHook{name: "a", calls: &calls})

	err := r.AddHook(&mockHook{name: "a", calls: &calls})
	if !errors.Is(err, ErrDuplicateHookName) {
		t.Fatalf("expected ErrDuplicateHookName, got %v", err)
	}
}

func TestRegistry_AddHook_AfterClose_Error(t *testing.T) {
	var calls []string
	r := newRegistry(t)
	r.Close()

	err := r.AddHook(&mockHook{name: "a", calls: &calls})
	if !errors.Is(err, ErrRegistryClosed) {
		t.Fatalf("expected ErrRegistryClosed, got %v", err)
	}
}

func TestRegistry_Register_ResourceBag_PassesBetweenHooks(t *testing.T) {
	var calls []string
	var resolved any
	var resolveOK bool

	r := newRegistry(t)
	r.AddHook(&mockHook{
		name:  "provider",
		calls: &calls,
		provideFn: func(ctx *RegistrationContext) {
			ctx.Provide("mykey", 42)
		},
	})
	r.AddHook(&mockHook{
		name:  "consumer",
		calls: &calls,
		provideFn: func(ctx *RegistrationContext) {
			resolved, resolveOK = ctx.Resolve("mykey")
		},
	})

	if err := r.Register("ws-1", "/tmp/ws1"); err != nil {
		t.Fatal(err)
	}

	if !resolveOK || resolved != 42 {
		t.Fatalf("expected (42, true), got (%v, %v)", resolved, resolveOK)
	}
}

func TestRegistry_ConcurrentOperations(t *testing.T) {
	var calls []string
	var mu sync.Mutex

	// Use a thread-safe mock.
	safeMock := func(name string) *mockHook {
		return &mockHook{
			name:  name,
			calls: &calls,
			provideFn: func(_ *RegistrationContext) {
				mu.Lock()
				defer mu.Unlock()
			},
		}
	}

	r := newRegistry(t)
	r.AddHook(safeMock("a"))

	var wg sync.WaitGroup
	for i := range 10 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := fmt.Sprintf("ws-%d", n)
			r.Register(id, "/tmp/"+id)
		}(i)
	}
	wg.Wait()

	ids := r.WorkspaceIDs()
	if len(ids) != 10 {
		t.Fatalf("expected 10 workspaces, got %d", len(ids))
	}

	// Deregister all concurrently.
	for i := range 10 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			r.Deregister(fmt.Sprintf("ws-%d", n))
		}(i)
	}
	wg.Wait()

	if ids := r.WorkspaceIDs(); len(ids) != 0 {
		t.Fatalf("expected 0 workspaces after deregister, got %d", len(ids))
	}
}

func TestRegistry_Deregister_OnlyCallsHooksThatSucceeded(t *testing.T) {
	var calls []string
	r := newRegistry(t)
	r.AddHook(&mockHook{name: "a", calls: &calls})
	r.AddHook(&mockHook{name: "b", critical: false, regErr: errors.New("fail"), calls: &calls})
	r.AddHook(&mockHook{name: "c", calls: &calls})
	r.Register("ws-1", "/tmp/ws1")

	calls = nil
	r.Deregister("ws-1")

	// b failed (non-critical), so only c and a should be deregistered.
	want := []string{"deregister:c", "deregister:a"}
	assertCalls(t, calls, want)
}

func TestRegistry_WorkspaceIDs(t *testing.T) {
	r := newRegistry(t)
	r.Register("ws-a", "/tmp/a")
	r.Register("ws-b", "/tmp/b")

	ids := r.WorkspaceIDs()
	sort.Strings(ids)
	if len(ids) != 2 || ids[0] != "ws-a" || ids[1] != "ws-b" {
		t.Fatalf("expected [ws-a ws-b], got %v", ids)
	}
}

func TestRegistry_HookNames(t *testing.T) {
	var calls []string
	r := newRegistry(t)
	r.AddHook(&mockHook{name: "x", calls: &calls})
	r.AddHook(&mockHook{name: "y", calls: &calls})

	names := r.HookNames()
	if len(names) != 2 || names[0] != "x" || names[1] != "y" {
		t.Fatalf("expected [x y], got %v", names)
	}
}

func TestRegistry_RollbackOrder(t *testing.T) {
	var calls []string
	r := newRegistry(t)
	r.AddHook(&mockHook{name: "a", calls: &calls})
	r.AddHook(&mockHook{name: "b", calls: &calls})
	r.AddHook(&mockHook{name: "c", critical: true, regErr: errors.New("fail"), calls: &calls})

	r.Register("ws-1", "/tmp/ws1")

	// a and b succeeded, then c failed (critical). Rollback in reverse: b, a.
	want := []string{"register:a", "register:b", "register:c", "rollback:b", "rollback:a"}
	assertCalls(t, calls, want)
}

func TestRegistry_Register_AllNonCriticalFail_StillRegistered(t *testing.T) {
	var calls []string
	r := newRegistry(t)
	r.AddHook(&mockHook{name: "a", critical: false, regErr: errors.New("fail"), calls: &calls})
	r.AddHook(&mockHook{name: "b", critical: false, regErr: errors.New("fail"), calls: &calls})

	if err := r.Register("ws-1", "/tmp/ws1"); err != nil {
		t.Fatal(err)
	}

	ids := r.WorkspaceIDs()
	if len(ids) != 1 || ids[0] != "ws-1" {
		t.Fatalf("expected [ws-1], got %v", ids)
	}
}

func TestRegistry_DoubleRegister_DeregistersFirst(t *testing.T) {
	var calls []string
	r := newRegistry(t)
	r.AddHook(&mockHook{name: "a", calls: &calls})
	r.Register("ws-1", "/tmp/ws1")

	calls = nil
	r.Register("ws-1", "/tmp/ws1-new")

	// Should deregister first, then register again.
	want := []string{"deregister:a", "register:a"}
	assertCalls(t, calls, want)

	ids := r.WorkspaceIDs()
	if len(ids) != 1 || ids[0] != "ws-1" {
		t.Fatalf("expected [ws-1], got %v", ids)
	}
}

func TestRegistry_ZeroHooks_RegisterSucceeds(t *testing.T) {
	r := newRegistry(t)
	if err := r.Register("ws-1", "/tmp/ws1"); err != nil {
		t.Fatal(err)
	}

	ids := r.WorkspaceIDs()
	if len(ids) != 1 || ids[0] != "ws-1" {
		t.Fatalf("expected [ws-1], got %v", ids)
	}
}

func TestRegistry_PanicInOnRollback_ContinuesRollback(t *testing.T) {
	var calls []string
	r := newRegistry(t)
	r.AddHook(&mockHook{name: "a", calls: &calls})
	r.AddHook(&panicHook{
		mockHook:        mockHook{name: "b", calls: &calls},
		panicOnRollback: true,
	})
	r.AddHook(&mockHook{name: "c", critical: true, regErr: errors.New("fail"), calls: &calls})

	r.Register("ws-1", "/tmp/ws1")

	// c fails critically. Rollback: b (panics), then a (still called).
	want := []string{"register:a", "register:b", "register:c", "rollback:b", "rollback:a"}
	assertCalls(t, calls, want)
}

func TestRegistry_PanicInOnDeregister_ContinuesDeregister(t *testing.T) {
	var calls []string
	r := newRegistry(t)
	r.AddHook(&mockHook{name: "a", calls: &calls})
	r.AddHook(&panicHook{
		mockHook:          mockHook{name: "b", calls: &calls},
		panicOnDeregister: true,
	})
	r.AddHook(&mockHook{name: "c", calls: &calls})

	r.Register("ws-1", "/tmp/ws1")
	calls = nil
	r.Deregister("ws-1")

	// Reverse order: c, b (panics), a (still called).
	want := []string{"deregister:c", "deregister:b", "deregister:a"}
	assertCalls(t, calls, want)
}

func TestRegistry_ForWorkspace_RegisteredWorkspace(t *testing.T) {
	var calls []string
	sentinel := "pool-sentinel"
	r := newRegistry(t)
	r.AddHook(&mockHook{
		name:  "provider",
		calls: &calls,
		provideFn: func(ctx *RegistrationContext) {
			ctx.Provide(ResourceKeyPool, sentinel)
		},
	})

	if err := r.Register("ws-1", "/tmp/ws-1"); err != nil {
		t.Fatal(err)
	}

	h := r.ForWorkspace("ws-1")
	if h == nil {
		t.Fatal("expected non-nil handle for registered workspace")
	}
	if h.ID() != "ws-1" {
		t.Errorf("ID() = %q, want %q", h.ID(), "ws-1")
	}
	if h.Path() != "/tmp/ws-1" {
		t.Errorf("Path() = %q, want %q", h.Path(), "/tmp/ws-1")
	}
	pool, ok := h.Resource(ResourceKeyPool)
	if !ok {
		t.Fatal("expected ResourceKeyPool to be provided")
	}
	if pool != sentinel {
		t.Errorf("Resource(ResourceKeyPool) = %v, want %v", pool, sentinel)
	}
}

func TestRegistry_ForWorkspace_UnregisteredWorkspace(t *testing.T) {
	r := newRegistry(t)
	if h := r.ForWorkspace("nonexistent"); h != nil {
		t.Fatalf("expected nil for unregistered workspace, got %v", h)
	}
}

func TestRegistry_ForWorkspace_AfterDeregister(t *testing.T) {
	var calls []string
	r := newRegistry(t)
	r.AddHook(&mockHook{name: "a", calls: &calls})
	r.Register("ws-1", "/tmp/ws-1")
	r.Deregister("ws-1")

	if h := r.ForWorkspace("ws-1"); h != nil {
		t.Fatalf("expected nil after deregister, got %v", h)
	}
}

func TestRegistry_ForWorkspace_EmptyID(t *testing.T) {
	r := newRegistry(t)
	if h := r.ForWorkspace(""); h != nil {
		t.Fatalf("expected nil for empty ID, got %v", h)
	}
}

func TestRegistry_ForWorkspace_ConcurrentWithRegister(t *testing.T) {
	var calls []string
	r := newRegistry(t)
	r.AddHook(&mockHook{name: "a", calls: &calls})

	var wg sync.WaitGroup
	for i := range 10 {
		id := fmt.Sprintf("ws-%d", i)
		wg.Add(2)
		go func() {
			defer wg.Done()
			r.Register(id, "/tmp/"+id)
		}()
		go func() {
			defer wg.Done()
			r.ForWorkspace(id) // may return nil or valid handle — no panic/race
		}()
	}
	wg.Wait()

	// After all goroutines, all workspaces should be registered.
	for i := range 10 {
		id := fmt.Sprintf("ws-%d", i)
		if h := r.ForWorkspace(id); h == nil {
			t.Errorf("expected non-nil handle for %q after concurrent registration", id)
		}
	}
}

func TestRegistry_ForWorkspace_NilSafeChaining(t *testing.T) {
	r := newRegistry(t)

	// ForWorkspace returns nil; chained Resource should not panic.
	h := r.ForWorkspace("nonexistent")
	pool, ok := h.Resource(ResourceKeyPool)
	if ok {
		t.Error("expected ok=false from nil handle Resource")
	}
	if pool != nil {
		t.Errorf("expected nil from nil handle Resource, got %v", pool)
	}
	if h.ID() != "" {
		t.Errorf("expected empty ID from nil handle, got %q", h.ID())
	}
	if h.Path() != "" {
		t.Errorf("expected empty Path from nil handle, got %q", h.Path())
	}
}

func TestRegistry_ForWorkspace_AfterDoubleRegister(t *testing.T) {
	var calls []string
	callCount := 0
	r := newRegistry(t)
	r.AddHook(&mockHook{
		name:  "marker",
		calls: &calls,
		provideFn: func(ctx *RegistrationContext) {
			callCount++
			ctx.Provide("marker", fmt.Sprintf("call-%d", callCount))
		},
	})

	r.Register("ws-1", "/tmp/ws-1") // marker = "call-1"
	r.Register("ws-1", "/tmp/ws-1") // triggers deregister + re-register, marker = "call-2"

	h := r.ForWorkspace("ws-1")
	if h == nil {
		t.Fatal("expected non-nil handle after double register")
	}
	marker, ok := h.Resource("marker")
	if !ok {
		t.Fatal("expected marker resource to be provided")
	}
	if marker != "call-2" {
		t.Errorf("expected marker %q, got %q", "call-2", marker)
	}
}

func TestRegistry_ForWorkspace_AfterClose(t *testing.T) {
	var calls []string
	r := newRegistry(t)
	r.AddHook(&mockHook{name: "a", calls: &calls})
	r.Register("ws-1", "/tmp/ws-1")
	r.Close()

	// ForWorkspace should still return the handle after Close.
	h := r.ForWorkspace("ws-1")
	if h == nil {
		t.Fatal("expected non-nil handle after Close — existing handles should be accessible")
	}
	if h.ID() != "ws-1" {
		t.Errorf("ID() = %q, want %q", h.ID(), "ws-1")
	}
}

func assertCalls(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("call count mismatch: got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("call[%d]: got %q, want %q\nfull: %v", i, got[i], want[i], got)
		}
	}
}
