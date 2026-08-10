package leadprovision

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	runtimesettings "github.com/tysonthomas9/loomcli/internal/localsettings"
	"github.com/tysonthomas9/loomcli/internal/placement"
	"github.com/tysonthomas9/loomcli/internal/placement/daytona"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type fakeBroker struct {
	mu    sync.Mutex
	calls []placement.ProvisionRequest
}

func (f *fakeBroker) Provision(_ context.Context, req placement.ProvisionRequest) (*placement.ProvisionResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req)
	return &placement.ProvisionResult{}, nil
}

func (f *fakeBroker) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeBroker) onlyCall(t *testing.T) placement.ProvisionRequest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) != 1 {
		t.Fatalf("Provision calls = %d, want 1", len(f.calls))
	}
	return f.calls[0]
}

func TestProvisionForAgentNoOpsForNonInteractiveOrNonDaytona(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name     string
		roleKind domain.RoleKind
		runtime  domain.RuntimeProvider
	}{
		{name: "worker", roleKind: domain.RoleKindWorker, runtime: domain.RuntimeProviderDaytona},
		{name: "local interactive", roleKind: domain.RoleKindInteractive, runtime: domain.RuntimeProviderLocal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, broker := newProvisionFixture(t, tc.roleKind, tc.runtime, "codex")
			provisioner := New(broker, st, t.TempDir(), DefaultAllowlist(), daytona.DefaultSnapshotName, DefaultResource())

			if err := provisioner.ProvisionForAgent(ctx, "WS", "nova"); err != nil {
				t.Fatalf("ProvisionForAgent: %v", err)
			}
			if got := broker.callCount(); got != 0 {
				t.Fatalf("Provision calls = %d, want 0", got)
			}
		})
	}
}

func TestProvisionForAgentBuildsCodexDaytonaRequest(t *testing.T) {
	ctx := context.Background()
	st, broker := newProvisionFixture(t, domain.RoleKindInteractive, domain.RuntimeProviderDaytona, "")
	dir := t.TempDir()
	authJSON := `{"tokens":{"access":"codex-secret"}}`
	sealRuntimeCredential(t, dir, runtimesettings.RuntimeCredentialProviderCodex, authJSON)
	provisioner := New(broker, st, dir, nil, daytona.DefaultSnapshotName, placement.ResourceSize{})

	if err := provisioner.ProvisionForAgent(ctx, "WS", "nova"); err != nil {
		t.Fatalf("ProvisionForAgent: %v", err)
	}

	req := broker.onlyCall(t)
	if req.WorkspaceKey != "WS" || req.AgentName != "nova" {
		t.Fatalf("request identity = %q/%q, want WS/nova", req.WorkspaceKey, req.AgentName)
	}
	if req.SnapshotRef != daytona.DefaultSnapshotName {
		t.Fatalf("SnapshotRef = %q, want %q", req.SnapshotRef, daytona.DefaultSnapshotName)
	}
	if req.Resource != DefaultResource() {
		t.Fatalf("Resource = %+v, want %+v", req.Resource, DefaultResource())
	}
	wantCaps := []string{placement.CapLeadSession, placement.CapLeadAssignment, placement.CapLeadInbox}
	if len(req.Caps) != len(wantCaps) {
		t.Fatalf("Caps = %v, want %v", req.Caps, wantCaps)
	}
	for i, cap := range wantCaps {
		if req.Caps[i] != cap {
			t.Fatalf("Caps = %v, want %v", req.Caps, wantCaps)
		}
	}
	if req.Backend != "codex" {
		t.Fatalf("Backend = %q, want codex", req.Backend)
	}
	if len(req.SeedFiles) != 1 ||
		req.SeedFiles[0].Path != "/root/.codex/auth.json" ||
		string(req.SeedFiles[0].Content) != authJSON ||
		req.SeedFiles[0].Mode != "600" {
		t.Fatalf("SeedFiles = %+v, want codex auth.json drop", req.SeedFiles)
	}
	if !stringSlicesEqual(req.NetworkDomainAllowlist, DefaultAllowlist()) {
		t.Fatalf("allowlist = %v, want %v", req.NetworkDomainAllowlist, DefaultAllowlist())
	}
	if req.RepoName != "" {
		t.Fatalf("RepoName = %q, want empty", req.RepoName)
	}
	if req.GitToken != nil {
		t.Fatalf("GitToken configured without github slot")
	}
}

func TestProvisionForAgentFailsClosedWhenCodexCredentialMissing(t *testing.T) {
	ctx := context.Background()
	st, broker := newProvisionFixture(t, domain.RoleKindInteractive, domain.RuntimeProviderDaytona, "codex")
	provisioner := New(broker, st, t.TempDir(), DefaultAllowlist(), daytona.DefaultSnapshotName, DefaultResource())

	err := provisioner.ProvisionForAgent(ctx, "WS", "nova")
	if err == nil || !strings.Contains(err.Error(), "codex runtime credential not configured; seal it via /api/local/settings before provisioning a Daytona lead") {
		t.Fatalf("ProvisionForAgent = %v, want missing codex credential error", err)
	}
	if got := broker.callCount(); got != 0 {
		t.Fatalf("Provision calls = %d, want 0", got)
	}
}

func TestProvisionForAgentRejectsNonCodexBackend(t *testing.T) {
	ctx := context.Background()
	st, broker := newProvisionFixture(t, domain.RoleKindInteractive, domain.RuntimeProviderDaytona, "claude")
	dir := t.TempDir()
	sealRuntimeCredential(t, dir, runtimesettings.RuntimeCredentialProviderCodex, `{"tokens":{"access":"codex-secret"}}`)
	provisioner := New(broker, st, dir, DefaultAllowlist(), daytona.DefaultSnapshotName, DefaultResource())

	err := provisioner.ProvisionForAgent(ctx, "WS", "nova")
	if err == nil || !strings.Contains(err.Error(), `daytona lead provisioning supports only the codex backend; agent "nova" resolves to "claude"`) {
		t.Fatalf("ProvisionForAgent = %v, want non-codex backend error", err)
	}
	if got := broker.callCount(); got != 0 {
		t.Fatalf("Provision calls = %d, want 0", got)
	}
}

func TestProvisionForAgentGitTokenCallback(t *testing.T) {
	ctx := context.Background()
	st, broker := newProvisionFixture(t, domain.RoleKindInteractive, domain.RuntimeProviderDaytona, "codex")
	dir := t.TempDir()
	sealRuntimeCredential(t, dir, runtimesettings.RuntimeCredentialProviderCodex, `{"tokens":{"access":"codex-secret"}}`)
	sealRuntimeCredential(t, dir, runtimesettings.RuntimeCredentialProviderGitHub, "gh-secret")
	provisioner := New(broker, st, dir, DefaultAllowlist(), daytona.DefaultSnapshotName, DefaultResource())

	if err := provisioner.ProvisionForAgent(ctx, "WS", "nova"); err != nil {
		t.Fatalf("ProvisionForAgent: %v", err)
	}
	req := broker.onlyCall(t)
	if req.GitToken == nil {
		t.Fatal("GitToken = nil, want callback")
	}
	got, err := req.GitToken()
	if err != nil || got != "gh-secret" {
		t.Fatalf("GitToken() = %q, %v; want gh-secret", got, err)
	}
}

func newProvisionFixture(t *testing.T, roleKind domain.RoleKind, runtime domain.RuntimeProvider, backend string) (*memstore.Store, *fakeBroker) {
	t.Helper()
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "WS",
		Name:         "lead",
		Kind:         string(roleKind),
		Backend:      backend,
		Prompt:       "lead prompt",
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey:    "WS",
		Name:            "nova",
		RoleName:        "lead",
		RuntimeProvider: runtime,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return st, &fakeBroker{}
}

func sealRuntimeCredential(t *testing.T, dir, provider, plaintext string) {
	t.Helper()
	settings, err := runtimesettings.Load(dir)
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	credential, err := runtimesettings.SealRuntimeCredential(dir, provider, plaintext, time.Now().UTC())
	if err != nil {
		t.Fatalf("seal %s credential: %v", provider, err)
	}
	switch provider {
	case runtimesettings.RuntimeCredentialProviderCodex:
		settings.RuntimeCredentials.Codex = credential
	case runtimesettings.RuntimeCredentialProviderGitHub:
		settings.RuntimeCredentials.GitHub = credential
	default:
		t.Fatalf("unsupported test provider %q", provider)
	}
	if err := runtimesettings.Save(dir, settings); err != nil {
		t.Fatalf("save settings: %v", err)
	}
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
