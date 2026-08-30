package placement

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type storeWithNodes struct {
	store.Store
	nodes store.NodeStore
}

func (s storeWithNodes) Nodes() store.NodeStore {
	return s.nodes
}

type failingUpdateNodeStore struct {
	store.NodeStore
	fail func(context.Context, string, string, store.NodeUpdate) error
}

func (s failingUpdateNodeStore) Update(ctx context.Context, workspaceKey, nodeID string, patch store.NodeUpdate) (*domain.Node, error) {
	if s.fail != nil {
		if err := s.fail(ctx, workspaceKey, nodeID, patch); err != nil {
			return nil, err
		}
	}
	return s.NodeStore.Update(ctx, workspaceKey, nodeID, patch)
}

type fakeProvider struct {
	mu                     sync.Mutex
	createCalls            []CreateRequest
	prepCalls              []LeadBootPrep
	prepSandboxIDs         []string
	startProcessCalls      []ProcessSpec
	startProcessSandboxIDs []string
	deleteCalls            []string
	getCalls               []string
	ensureRunningCalls     []string
	listCalls              []map[string]string
	listPtySessionCalls    []string
	findByNameCalls        []string
	// allCalls records EVERY entry into a Provider method, before any error or
	// existence check can return early. The per-method counters below record
	// only successful paths (SetAutostopInterval, for example, returns
	// ErrSandboxNotFound before recording), so a stray call against an empty
	// non-owning provider would otherwise be invisible to totalCalls.
	allCalls            []string
	updateActivityCalls []string
	killPtyCalls        []string
	setAutostopCalls    []time.Duration
	events              []string
	sandboxes           map[string]ProviderSandbox
	listOnlySandboxes   []ProviderSandbox
	listInvisible       map[string]bool
	names               map[string]string
	ptySessions         map[string]map[string]PtySession
	createResult        *CreateResult
	createErr           error
	// createLosesResponse simulates a create whose response is lost after
	// dispatch: the sandbox is made (with its labels and name) but the call
	// returns an empty CreateResult alongside createErr.
	createLosesResponse bool
	findByNameErr       error
	createCtxErr        error
	deleteErr           error
	prepErr             error
	getErr              error
	getHook             func(string, int)
	ensureRunningErr    error
	listErr             error
	listPtySessionsErr  error
	setAutostopErr      error
	startProcessErr     error
	startProcessErrs    []error
	createDelay         time.Duration
	deleteLeavesSandbox bool
	dropPtyAfterCreate  bool
	createHook          func(string)
	deleteHook          func(string)
}

func (f *fakeProvider) Create(ctx context.Context, req CreateRequest) (CreateResult, error) {
	f.recordCall("Create")
	if f.createDelay > 0 {
		time.Sleep(f.createDelay)
	}
	f.mu.Lock()
	f.createCalls = append(f.createCalls, cloneCreateRequest(req))
	result := CreateResult{}
	if f.createResult != nil {
		result = *f.createResult
	} else if f.createErr == nil {
		result.SandboxID = fmt.Sprintf("sandbox-%d", len(f.createCalls))
		result.Outcome = CreateOutcomeCreated
	}
	made := result.SandboxID
	if made == "" && f.createLosesResponse {
		made = fmt.Sprintf("sandbox-%d", len(f.createCalls))
	}
	if made != "" {
		f.ensureSandboxesLocked()
		f.sandboxes[made] = ProviderSandbox{
			ID:     made,
			Labels: copyMap(req.Labels),
			State:  ProviderSandboxRunning,
		}
		if name := strings.TrimSpace(req.Name); name != "" {
			if f.names == nil {
				f.names = make(map[string]string)
			}
			f.names[name] = made
		}
		f.events = append(f.events, "create:"+made)
	}
	hook := f.createHook
	err := f.createErr
	f.mu.Unlock()
	if hook != nil {
		hook(made)
	}
	f.mu.Lock()
	f.createCtxErr = ctx.Err()
	f.mu.Unlock()
	return result, err
}

func (f *fakeProvider) Get(_ context.Context, sandboxID string) (ProviderSandbox, error) {
	f.recordCall("Get")
	f.mu.Lock()
	f.getCalls = append(f.getCalls, sandboxID)
	call := len(f.getCalls)
	hook := f.getHook
	if f.getErr != nil {
		err := f.getErr
		f.mu.Unlock()
		if hook != nil {
			hook(sandboxID, call)
		}
		return ProviderSandbox{}, err
	}
	sandbox, ok := f.sandboxes[sandboxID]
	f.mu.Unlock()
	if hook != nil {
		hook(sandboxID, call)
	}
	if !ok {
		return ProviderSandbox{}, ErrSandboxNotFound
	}
	return cloneProviderSandbox(sandbox), nil
}

func (f *fakeProvider) FindByName(_ context.Context, name string) (ProviderSandbox, error) {
	f.recordCall("FindByName")
	f.mu.Lock()
	f.findByNameCalls = append(f.findByNameCalls, name)
	err := f.findByNameErr
	var sandbox ProviderSandbox
	ok := false
	if err == nil {
		if id, exists := f.names[strings.TrimSpace(name)]; exists {
			sandbox, ok = f.sandboxes[id]
		}
	}
	f.mu.Unlock()
	if err != nil {
		return ProviderSandbox{}, err
	}
	if !ok {
		return ProviderSandbox{}, ErrSandboxNotFound
	}
	return cloneProviderSandbox(sandbox), nil
}

func (f *fakeProvider) setSandboxName(name, sandboxID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.names == nil {
		f.names = make(map[string]string)
	}
	f.names[name] = sandboxID
}

func (f *fakeProvider) EnsureRunning(_ context.Context, sandboxID string) (bool, error) {
	f.recordCall("EnsureRunning")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureRunningCalls = append(f.ensureRunningCalls, sandboxID)
	f.events = append(f.events, "ensureRunning:"+sandboxID)
	if f.ensureRunningErr != nil {
		return false, f.ensureRunningErr
	}
	sandbox, ok := f.sandboxes[sandboxID]
	if !ok || sandbox.State == ProviderSandboxAbsent {
		return false, ErrSandboxNotFound
	}
	if sandbox.State != ProviderSandboxStopped {
		return false, nil
	}
	sandbox.State = ProviderSandboxRunning
	f.sandboxes[sandboxID] = sandbox
	delete(f.ptySessions, sandboxID)
	return true, nil
}

func (f *fakeProvider) Delete(_ context.Context, sandboxID string) error {
	f.recordCall("Delete")
	if f.deleteHook != nil {
		f.deleteHook(sandboxID)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls = append(f.deleteCalls, sandboxID)
	f.events = append(f.events, "delete:"+sandboxID)
	if f.deleteErr != nil {
		return f.deleteErr
	}
	if !f.deleteLeavesSandbox {
		delete(f.sandboxes, sandboxID)
		delete(f.ptySessions, sandboxID)
	}
	return nil
}

func (f *fakeProvider) UpdateLastActivity(_ context.Context, sandboxID string) error {
	f.recordCall("UpdateLastActivity")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateActivityCalls = append(f.updateActivityCalls, sandboxID)
	return nil
}

func (f *fakeProvider) SetAutostopInterval(_ context.Context, sandboxID string, interval time.Duration) error {
	f.recordCall("SetAutostopInterval")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.setAutostopErr != nil {
		return f.setAutostopErr
	}
	if _, ok := f.sandboxes[sandboxID]; !ok {
		return ErrSandboxNotFound
	}
	f.setAutostopCalls = append(f.setAutostopCalls, interval)
	f.events = append(f.events, "autostop:"+interval.String())
	return nil
}

func (f *fakeProvider) PrepareLeadBoot(_ context.Context, sandboxID string, prep LeadBootPrep) error {
	f.recordCall("PrepareLeadBoot")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prepSandboxIDs = append(f.prepSandboxIDs, sandboxID)
	f.prepCalls = append(f.prepCalls, cloneLeadBootPrep(prep))
	f.events = append(f.events, "prep:"+sandboxID)
	if f.prepErr != nil {
		return f.prepErr
	}
	if _, ok := f.sandboxes[sandboxID]; !ok {
		return ErrSandboxNotFound
	}
	return nil
}

func (f *fakeProvider) CreatePty(_ context.Context, sandboxID string, spec ProcessSpec) error {
	f.recordCall("CreatePty")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startProcessCalls = append(f.startProcessCalls, cloneProcessSpec(spec))
	f.startProcessSandboxIDs = append(f.startProcessSandboxIDs, sandboxID)
	f.events = append(f.events, "startProcess:"+sandboxID)
	if len(f.startProcessErrs) > 0 {
		err := f.startProcessErrs[0]
		f.startProcessErrs = f.startProcessErrs[1:]
		if err != nil {
			if errors.Is(err, ErrSandboxNotFound) {
				delete(f.sandboxes, sandboxID)
				delete(f.ptySessions, sandboxID)
			}
			if errors.Is(err, ErrPtySessionAlreadyExists) {
				f.addPtySessionLocked(sandboxID, LeadPTYSessionID)
			}
			return err
		}
	}
	if f.startProcessErr != nil {
		if errors.Is(f.startProcessErr, ErrSandboxNotFound) {
			delete(f.sandboxes, sandboxID)
			delete(f.ptySessions, sandboxID)
		}
		if errors.Is(f.startProcessErr, ErrPtySessionAlreadyExists) {
			f.addPtySessionLocked(sandboxID, LeadPTYSessionID)
		}
		return f.startProcessErr
	}
	if _, ok := f.sandboxes[sandboxID]; !ok {
		return ErrSandboxNotFound
	}
	if f.dropPtyAfterCreate {
		return nil
	}
	sessionID := strings.TrimSpace(spec.SessionID)
	if sessionID == "" {
		sessionID = LeadPTYSessionID
	}
	if f.addPtySessionLocked(sandboxID, sessionID) {
		return ErrPtySessionAlreadyExists
	}
	return nil
}

func (f *fakeProvider) ListManaged(_ context.Context, labels map[string]string) ([]ProviderSandbox, error) {
	f.recordCall("ListManaged")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls = append(f.listCalls, copyMap(labels))
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]ProviderSandbox, 0, len(f.sandboxes)+len(f.listOnlySandboxes))
	for _, sandbox := range f.sandboxes {
		if f.listInvisible[sandbox.ID] || !providerSandboxHasLabels(sandbox, labels) {
			continue
		}
		out = append(out, cloneProviderSandbox(sandbox))
	}
	for _, sandbox := range f.listOnlySandboxes {
		if providerSandboxHasLabels(sandbox, labels) {
			out = append(out, cloneProviderSandbox(sandbox))
		}
	}
	return out, nil
}

func (f *fakeProvider) ListPtySessions(_ context.Context, sandboxID string) ([]PtySession, error) {
	f.recordCall("ListPtySessions")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listPtySessionCalls = append(f.listPtySessionCalls, sandboxID)
	if f.listPtySessionsErr != nil {
		return nil, f.listPtySessionsErr
	}
	if _, ok := f.sandboxes[sandboxID]; !ok {
		return nil, ErrSandboxNotFound
	}
	out := make([]PtySession, 0, len(f.ptySessions[sandboxID]))
	for _, session := range f.ptySessions[sandboxID] {
		out = append(out, session)
	}
	return out, nil
}

func (f *fakeProvider) KillPtySession(_ context.Context, sandboxID, sessionID string) error {
	f.recordCall("KillPtySession")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.killPtyCalls = append(f.killPtyCalls, sandboxID)
	if _, ok := f.sandboxes[sandboxID]; !ok {
		return ErrSandboxNotFound
	}
	delete(f.ptySessions[sandboxID], sessionID)
	return nil
}

// recordCall must be the FIRST statement of every Provider method on this fake.
func (f *fakeProvider) recordCall(method string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.allCalls = append(f.allCalls, method)
}

func (f *fakeProvider) addSandbox(sandbox ProviderSandbox) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureSandboxesLocked()
	f.sandboxes[sandbox.ID] = cloneProviderSandbox(sandbox)
}

func (f *fakeProvider) addListOnlySandbox(sandbox ProviderSandbox) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listOnlySandboxes = append(f.listOnlySandboxes, cloneProviderSandbox(sandbox))
}

// addListInvisibleSandbox registers a sandbox that point reads (Get,
// FindByName via setSandboxName) can see but ListManaged omits — the
// eventually-consistent-list gap the deterministic name closes.
func (f *fakeProvider) addListInvisibleSandbox(sandbox ProviderSandbox) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureSandboxesLocked()
	f.sandboxes[sandbox.ID] = cloneProviderSandbox(sandbox)
	if f.listInvisible == nil {
		f.listInvisible = make(map[string]bool)
	}
	f.listInvisible[sandbox.ID] = true
}

func (f *fakeProvider) addPtySession(sandboxID, sessionID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addPtySessionLocked(sandboxID, sessionID)
}

func (f *fakeProvider) addPtySessionLocked(sandboxID, sessionID string) bool {
	f.ensurePtySessionsLocked()
	if f.ptySessions[sandboxID] == nil {
		f.ptySessions[sandboxID] = make(map[string]PtySession)
	}
	if _, ok := f.ptySessions[sandboxID][sessionID]; ok {
		return true
	}
	f.ptySessions[sandboxID][sessionID] = PtySession{SessionID: sessionID}
	return false
}

func (f *fakeProvider) resetEvents() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = nil
}

func (f *fakeProvider) createCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.createCalls)
}

func (f *fakeProvider) prepCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.prepCalls)
}

func (f *fakeProvider) deleteCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.deleteCalls)
}

func (f *fakeProvider) startProcessCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.startProcessCalls)
}

func (f *fakeProvider) listCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.listCalls)
}

func (f *fakeProvider) listPtySessionCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.listPtySessionCalls)
}

func (f *fakeProvider) getCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.getCalls)
}

func (f *fakeProvider) ensureRunningCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.ensureRunningCalls)
}

func (f *fakeProvider) createContextErr() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.createCtxErr
}

func (f *fakeProvider) createCall(t *testing.T, idx int) CreateRequest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.createCalls) <= idx {
		t.Fatalf("create calls = %d, want index %d", len(f.createCalls), idx)
	}
	return cloneCreateRequest(f.createCalls[idx])
}

func (f *fakeProvider) startProcessCall(t *testing.T, idx int) ProcessSpec {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.startProcessCalls) <= idx {
		t.Fatalf("start process calls = %d, want index %d", len(f.startProcessCalls), idx)
	}
	return cloneProcessSpec(f.startProcessCalls[idx])
}

func (f *fakeProvider) prepCall(t *testing.T, idx int) LeadBootPrep {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.prepCalls) <= idx {
		t.Fatalf("prep calls = %d, want index %d", len(f.prepCalls), idx)
	}
	return cloneLeadBootPrep(f.prepCalls[idx])
}

func (f *fakeProvider) deleteCallsSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deleteCalls...)
}

func (f *fakeProvider) eventsSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.events...)
}

func (f *fakeProvider) setAutostopCallsSnapshot() []time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Duration(nil), f.setAutostopCalls...)
}

func (f *fakeProvider) startProcessSandboxIDsSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.startProcessSandboxIDs...)
}

func (f *fakeProvider) ensureSandboxesLocked() {
	if f.sandboxes == nil {
		f.sandboxes = make(map[string]ProviderSandbox)
	}
}

func (f *fakeProvider) ensurePtySessionsLocked() {
	if f.ptySessions == nil {
		f.ptySessions = make(map[string]map[string]PtySession)
	}
}

func cloneCreateRequest(in CreateRequest) CreateRequest {
	in.Labels = copyMap(in.Labels)
	in.Env = copyMap(in.Env)
	in.NetworkDomainAllowlist = append([]string(nil), in.NetworkDomainAllowlist...)
	return in
}

func cloneProcessSpec(in ProcessSpec) ProcessSpec {
	in.Command = append([]string(nil), in.Command...)
	in.Env = copyMap(in.Env)
	return in
}

func countPromptArgs(command []string) int {
	count := 0
	for _, arg := range command {
		if strings.TrimSpace(arg) == "--prompt" || strings.HasPrefix(strings.TrimSpace(arg), "--prompt=") {
			count++
		}
	}
	return count
}

func cloneLeadBootPrep(in LeadBootPrep) LeadBootPrep {
	out := in
	if in.Repo != nil {
		repo := *in.Repo
		out.Repo = &repo
	}
	if in.BootstrapBinary != nil {
		spec := *in.BootstrapBinary
		out.BootstrapBinary = &spec
	}
	return out
}

func cloneProviderSandbox(in ProviderSandbox) ProviderSandbox {
	in.Labels = copyMap(in.Labels)
	return in
}

func providerSandboxHasLabels(sandbox ProviderSandbox, labels map[string]string) bool {
	for key, value := range labels {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if sandbox.Labels[key] != value {
			return false
		}
	}
	return true
}

func (f *fakeProvider) allCallsSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.allCalls...)
}
