package leadprovision

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/placement"
)

func TestReviveCoordinatorSingleflightsConcurrentEnsures(t *testing.T) {
	provider := &fakeReviveProvider{sandbox: placement.ProviderSandbox{
		ID:       "sandbox-1",
		State:    placement.ProviderSandboxStopped,
		RawState: placement.ProviderSandboxRawStopped,
	}}
	provisioner := newFakeReviveProvisioner()
	provisioner.block = make(chan struct{})
	coordinator := NewReviveCoordinator(provider, provisioner)

	const callers = 12
	start := make(chan struct{})
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- coordinator.EnsureAttachable(context.Background(), "WS", "nova", "sandbox-1")
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if !errors.Is(err, ErrReviveStarting) {
			t.Fatalf("EnsureAttachable error = %v, want ErrReviveStarting", err)
		}
	}
	provisioner.waitForCalls(t, 1)
	if got := provisioner.callCount(); got != 1 {
		t.Fatalf("ProvisionForAgent calls = %d, want 1", got)
	}
	if got := provider.callCount(); got != 1 {
		t.Fatalf("provider Get calls = %d, want 1", got)
	}
	status := coordinator.Status("WS", "nova")
	if status.State != ReviveStateWaking || status.Err != nil {
		t.Fatalf("status = %+v, want waking without error", status)
	}
	deadline := provisioner.onlyDeadline(t)
	remaining := time.Until(deadline)
	if remaining < 11*time.Minute || remaining > 12*time.Minute {
		t.Fatalf("detached provision deadline remaining = %v, want about 12m", remaining)
	}
	close(provisioner.block)
	provisioner.waitForCompletions(t, 1)
	if status := coordinator.Status("WS", "nova"); status.State != ReviveStateIdle || status.Err != nil {
		t.Fatalf("status after success = %+v, want idle", status)
	}
}

func TestReviveCoordinatorRetainsFailureAndKicksOneFreshRetry(t *testing.T) {
	provider := &fakeReviveProvider{sandbox: placement.ProviderSandbox{
		ID:       "sandbox-1",
		State:    placement.ProviderSandboxStopped,
		RawState: placement.ProviderSandboxRawArchived,
	}}
	firstErr := errors.New("archive restore failed")
	provisioner := newFakeReviveProvisioner()
	provisioner.errs = []error{firstErr, nil}
	provisioner.blockAfter = 1
	provisioner.block = make(chan struct{})
	coordinator := NewReviveCoordinator(provider, provisioner)

	if err := coordinator.EnsureAttachable(context.Background(), "WS", "nova", "sandbox-1"); !errors.Is(err, ErrReviveStarting) {
		t.Fatalf("first EnsureAttachable = %v, want ErrReviveStarting", err)
	}
	provisioner.waitForCompletions(t, 1)
	status := coordinator.Status("WS", "nova")
	if status.State != ReviveStateFailed || !errors.Is(status.Err, firstErr) {
		t.Fatalf("status after failure = %+v, want retained failure", status)
	}

	err := coordinator.EnsureAttachable(context.Background(), "WS", "nova", "sandbox-1")
	if !errors.Is(err, firstErr) {
		t.Fatalf("retry EnsureAttachable = %v, want retained error", err)
	}
	provisioner.waitForCalls(t, 2)
	if got := provisioner.callCount(); got != 2 {
		t.Fatalf("ProvisionForAgent calls = %d, want one fresh retry", got)
	}
	if status := coordinator.Status("WS", "nova"); status.State != ReviveStateWaking || status.Err != nil {
		t.Fatalf("status during retry = %+v, want waking", status)
	}
	close(provisioner.block)
	provisioner.waitForCompletions(t, 2)
}

func TestReviveCoordinatorRevivesStartedSandboxWithoutLeadPTY(t *testing.T) {
	provider := &fakeReviveProvider{sandbox: placement.ProviderSandbox{
		ID:       "sandbox-1",
		State:    placement.ProviderSandboxRunning,
		RawState: placement.ProviderSandboxRawStarted,
	}}
	provisioner := newFakeReviveProvisioner()
	provisioner.block = make(chan struct{})
	coordinator := NewReviveCoordinator(provider, provisioner)

	err := coordinator.EnsureAttachable(context.Background(), "WS", "nova", "sandbox-1")
	if !errors.Is(err, ErrReviveStarting) {
		t.Fatalf("EnsureAttachable = %v, want ErrReviveStarting", err)
	}
	provisioner.waitForCalls(t, 1)
	if got := provider.listPtySessionCallCount(); got != 1 {
		t.Fatalf("ListPtySessions calls = %d, want 1", got)
	}
	close(provisioner.block)
	provisioner.waitForCompletions(t, 1)
}

func TestReviveCoordinatorStartedSandboxWithLeadPTYIsAttachable(t *testing.T) {
	provider := &fakeReviveProvider{
		sandbox: placement.ProviderSandbox{
			ID:       "sandbox-1",
			State:    placement.ProviderSandboxRunning,
			RawState: placement.ProviderSandboxRawStarted,
		},
		sessions: []placement.PtySession{{SessionID: placement.LeadPTYSessionID}},
	}
	provisioner := newFakeReviveProvisioner()
	coordinator := NewReviveCoordinator(provider, provisioner)

	if err := coordinator.EnsureAttachable(context.Background(), "WS", "nova", "sandbox-1"); err != nil {
		t.Fatalf("EnsureAttachable: %v", err)
	}
	if got := provisioner.callCount(); got != 0 {
		t.Fatalf("ReviveForAgent calls = %d, want 0", got)
	}
}

func TestReviveCoordinatorRejectsProviderWithoutRawState(t *testing.T) {
	provider := &fakeReviveProvider{sandbox: placement.ProviderSandbox{
		ID:    "sandbox-1",
		State: placement.ProviderSandboxRunning,
	}}
	coordinator := NewReviveCoordinator(provider, newFakeReviveProvisioner())

	err := coordinator.EnsureAttachable(context.Background(), "WS", "nova", "sandbox-1")
	if err == nil || !strings.Contains(err.Error(), "provider does not expose raw state") {
		t.Fatalf("EnsureAttachable = %v, want missing raw-state error", err)
	}
}

type fakeReviveProvider struct {
	mu           sync.Mutex
	sandbox      placement.ProviderSandbox
	sessions     []placement.PtySession
	err          error
	listErr      error
	calls        int
	listPtyCalls int
}

func (f *fakeReviveProvider) Get(context.Context, string) (placement.ProviderSandbox, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.sandbox, f.err
}

func (f *fakeReviveProvider) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeReviveProvider) ListPtySessions(context.Context, string) ([]placement.PtySession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listPtyCalls++
	return append([]placement.PtySession(nil), f.sessions...), f.listErr
}

func (f *fakeReviveProvider) listPtySessionCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listPtyCalls
}

type fakeReviveProvisioner struct {
	mu         sync.Mutex
	calls      int
	completed  int
	deadlines  []time.Time
	errs       []error
	block      chan struct{}
	blockAfter int
	callSignal chan struct{}
	doneSignal chan struct{}
}

func newFakeReviveProvisioner() *fakeReviveProvisioner {
	return &fakeReviveProvisioner{
		callSignal: make(chan struct{}, 16),
		doneSignal: make(chan struct{}, 16),
	}
}

func (f *fakeReviveProvisioner) ReviveForAgent(ctx context.Context, _, _ string) error {
	f.mu.Lock()
	f.calls++
	call := f.calls
	if deadline, ok := ctx.Deadline(); ok {
		f.deadlines = append(f.deadlines, deadline)
	}
	var err error
	if len(f.errs) >= call {
		err = f.errs[call-1]
	}
	block := f.block
	blockAfter := f.blockAfter
	f.mu.Unlock()
	f.callSignal <- struct{}{}
	if block != nil && call > blockAfter {
		<-block
	}
	f.mu.Lock()
	f.completed++
	f.mu.Unlock()
	f.doneSignal <- struct{}{}
	return err
}

func (f *fakeReviveProvisioner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeReviveProvisioner) onlyDeadline(t *testing.T) time.Time {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.deadlines) != 1 {
		t.Fatalf("deadlines = %d, want 1", len(f.deadlines))
	}
	return f.deadlines[0]
}

func (f *fakeReviveProvisioner) waitForCalls(t *testing.T, want int) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		if f.callCount() >= want {
			return
		}
		select {
		case <-f.callSignal:
		case <-deadline:
			t.Fatalf("timed out waiting for provision call %d", want)
		}
	}
}

func (f *fakeReviveProvisioner) waitForCompletions(t *testing.T, want int) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		f.mu.Lock()
		completed := f.completed
		f.mu.Unlock()
		if completed >= want {
			return
		}
		select {
		case <-f.doneSignal:
		case <-deadline:
			t.Fatalf("timed out waiting for provision completion %d", want)
		}
	}
}
