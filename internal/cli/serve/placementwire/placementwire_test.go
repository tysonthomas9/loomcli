package placementwire

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/placement"
)

func TestNewServePlacementBrokerSetsMaxLive(t *testing.T) {
	t.Setenv(envLoomLeadMaxVCPU, "2")
	t.Setenv(envLoomLeadMaxMemGiB, "4")
	t.Setenv("LOOM_DEPLOYMENT_ID", "test-deployment")
	ctx := context.Background()
	st := memstore.New()
	broker, err := newBroker(st, placement.ProviderRegistry{domain.RuntimeProviderDaytona: newServeTestPlacementProvider()}, []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("newServePlacementBroker: %v", err)
	}

	if _, err := broker.Provision(ctx, serveTestProvisionRequest("nova")); err != nil {
		t.Fatalf("first Provision: %v", err)
	}
	_, err = broker.Provision(ctx, serveTestProvisionRequest("orion"))
	if !errors.Is(err, domain.ErrUnschedulable) {
		t.Fatalf("second Provision = %v, want quota rejection", err)
	}
}

func TestNewServePlacementBrokerInjectsLeadAPIBaseURL(t *testing.T) {
	t.Setenv("LOOM_DEPLOYMENT_ID", "test-deployment")
	t.Setenv(envLoomLeadAPIBaseURL, "https://serve.example.com")
	st := memstore.New()
	provider := newServeTestPlacementProvider()
	broker, err := newBroker(st, placement.ProviderRegistry{domain.RuntimeProviderDaytona: provider}, []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("newServePlacementBroker: %v", err)
	}

	if _, err := broker.Provision(context.Background(), serveTestProvisionRequest("nova")); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	call := provider.createRequest(t, 0)
	if got := call.Env["LOOM_LEAD_API_URL"]; got != "https://serve.example.com" {
		t.Fatalf("LOOM_LEAD_API_URL = %q, want public serve URL", got)
	}
}

func TestNewServePlacementBrokerOmitsLeadAPIBaseURLWhenUnset(t *testing.T) {
	t.Setenv("LOOM_DEPLOYMENT_ID", "test-deployment")
	t.Setenv(envLoomLeadAPIBaseURL, "")
	st := memstore.New()
	provider := newServeTestPlacementProvider()
	broker, err := newBroker(st, placement.ProviderRegistry{domain.RuntimeProviderDaytona: provider}, []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("newServePlacementBroker: %v", err)
	}

	if _, err := broker.Provision(context.Background(), serveTestProvisionRequest("nova")); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	call := provider.createRequest(t, 0)
	if _, ok := call.Env["LOOM_LEAD_API_URL"]; ok {
		t.Fatalf("LOOM_LEAD_API_URL injected despite empty %s: %#v", envLoomLeadAPIBaseURL, call.Env)
	}
}

func TestBuildLeadProvisionerRequiresBroker(t *testing.T) {
	t.Setenv("LOOM_DEPLOYMENT_ID", "test-deployment")
	st := memstore.New()
	if got := LeadProvisioner(st, nil, "snapshot"); got != nil {
		t.Fatalf("LeadProvisioner(nil broker) = %#v, want nil", got)
	}
	broker, err := newBroker(st, placement.ProviderRegistry{domain.RuntimeProviderDaytona: newServeTestPlacementProvider()}, []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("newServePlacementBroker: %v", err)
	}
	if got := LeadProvisioner(st, broker, "snapshot"); got == nil {
		t.Fatal("LeadProvisioner(with broker) = nil, want provisioner")
	}
}

type serveTestPlacementProvider struct {
	mu             sync.Mutex
	next           int
	createRequests []placement.CreateRequest
	sandboxes      map[string]placement.ProviderSandbox
	names          map[string]string
	ptys           map[string][]placement.PtySession
}

func newServeTestPlacementProvider() *serveTestPlacementProvider {
	return &serveTestPlacementProvider{
		sandboxes: make(map[string]placement.ProviderSandbox),
		names:     make(map[string]string),
		ptys:      make(map[string][]placement.PtySession),
	}
}

func (p *serveTestPlacementProvider) Create(_ context.Context, req placement.CreateRequest) (placement.CreateResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.next++
	id := "sandbox-" + strconv.Itoa(p.next)
	p.createRequests = append(p.createRequests, cloneServeCreateRequest(req))
	p.sandboxes[id] = placement.ProviderSandbox{
		ID:       id,
		Labels:   copyStringMap(req.Labels),
		State:    placement.ProviderSandboxRunning,
		RawState: placement.ProviderSandboxRawStarted,
	}
	if name := strings.TrimSpace(req.Name); name != "" {
		p.names[name] = id
	}
	return placement.CreateResult{SandboxID: id, Outcome: placement.CreateOutcomeCreated}, nil
}

func (p *serveTestPlacementProvider) FindByName(_ context.Context, name string) (placement.ProviderSandbox, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	id, ok := p.names[strings.TrimSpace(name)]
	if !ok {
		return placement.ProviderSandbox{}, placement.ErrSandboxNotFound
	}
	sandbox, ok := p.sandboxes[id]
	if !ok {
		return placement.ProviderSandbox{}, placement.ErrSandboxNotFound
	}
	return sandbox, nil
}

func (p *serveTestPlacementProvider) createRequest(t *testing.T, idx int) placement.CreateRequest {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.createRequests) <= idx {
		t.Fatalf("create requests = %d, want index %d", len(p.createRequests), idx)
	}
	return cloneServeCreateRequest(p.createRequests[idx])
}

func (p *serveTestPlacementProvider) Get(_ context.Context, sandboxID string) (placement.ProviderSandbox, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	sandbox, ok := p.sandboxes[sandboxID]
	if !ok {
		return placement.ProviderSandbox{}, placement.ErrSandboxNotFound
	}
	return sandbox, nil
}

func (p *serveTestPlacementProvider) EnsureRunning(_ context.Context, sandboxID string) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	sandbox, ok := p.sandboxes[sandboxID]
	if !ok {
		return false, placement.ErrSandboxNotFound
	}
	if sandbox.State != placement.ProviderSandboxStopped {
		return false, nil
	}
	sandbox.State = placement.ProviderSandboxRunning
	sandbox.RawState = placement.ProviderSandboxRawStarted
	p.sandboxes[sandboxID] = sandbox
	delete(p.ptys, sandboxID)
	return true, nil
}

func (p *serveTestPlacementProvider) ListManaged(_ context.Context, labels map[string]string) ([]placement.ProviderSandbox, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []placement.ProviderSandbox
	for _, sandbox := range p.sandboxes {
		if stringMapContains(sandbox.Labels, labels) {
			out = append(out, sandbox)
		}
	}
	return out, nil
}

func (p *serveTestPlacementProvider) Delete(_ context.Context, sandboxID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.sandboxes, sandboxID)
	delete(p.ptys, sandboxID)
	return nil
}

func (p *serveTestPlacementProvider) UpdateLastActivity(context.Context, string) error {
	return nil
}

func (p *serveTestPlacementProvider) SetAutostopInterval(context.Context, string, time.Duration) error {
	return nil
}

func (p *serveTestPlacementProvider) PrepareLeadBoot(context.Context, string, placement.LeadBootPrep) error {
	return nil
}

func (p *serveTestPlacementProvider) CreatePty(_ context.Context, sandboxID string, spec placement.ProcessSpec) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.sandboxes[sandboxID]; !ok {
		return placement.ErrSandboxNotFound
	}
	sessionID := strings.TrimSpace(spec.SessionID)
	if sessionID == "" {
		sessionID = placement.LeadPTYSessionID
	}
	p.ptys[sandboxID] = append(p.ptys[sandboxID], placement.PtySession{SessionID: sessionID})
	return nil
}

func (p *serveTestPlacementProvider) ListPtySessions(_ context.Context, sandboxID string) ([]placement.PtySession, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.sandboxes[sandboxID]; !ok {
		return nil, placement.ErrSandboxNotFound
	}
	return append([]placement.PtySession(nil), p.ptys[sandboxID]...), nil
}

func (p *serveTestPlacementProvider) KillPtySession(context.Context, string, string) error {
	return nil
}

func serveTestProvisionRequest(agent string) placement.ProvisionRequest {
	return placement.ProvisionRequest{
		WorkspaceKey:    "WS",
		AgentName:       agent,
		SnapshotRef:     "snapshot",
		Caps:            []string{placement.CapLeadSession},
		Resource:        placement.ResourceSize{VCPU: 2, MemGiB: 4},
		RuntimeProvider: domain.RuntimeProviderDaytona,
		Backend:         "codex",
	}
}

func cloneServeCreateRequest(in placement.CreateRequest) placement.CreateRequest {
	out := in
	out.Labels = copyStringMap(in.Labels)
	out.Env = copyStringMap(in.Env)
	out.NetworkDomainAllowlist = append([]string(nil), in.NetworkDomainAllowlist...)
	return out
}

func copyStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func stringMapContains(have, want map[string]string) bool {
	for k, v := range want {
		if have[k] != v {
			return false
		}
	}
	return true
}
