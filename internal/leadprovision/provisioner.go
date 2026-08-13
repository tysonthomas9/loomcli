// Package leadprovision builds eager placement requests for interactive leads.
package leadprovision

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/backendnames"
	"github.com/tysonthomas9/loomcli/internal/domain"
	runtimesettings "github.com/tysonthomas9/loomcli/internal/localsettings"
	"github.com/tysonthomas9/loomcli/internal/placement"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const codexAuthJSONPath = "/root/.codex/auth.json"

var (
	defaultAllowlist = []string{
		"app.daytona.io",
		"github.com",
		"registry.npmjs.org",
		"chatgpt.com",
		"auth.openai.com",
		"api.openai.com",
	}
	defaultResource = placement.ResourceSize{VCPU: 2, MemGiB: 4}
)

// Broker is the narrow placement dependency. placement.Broker satisfies it;
// tests use a fake so provisioning never reaches Daytona.
type Broker interface {
	Provision(context.Context, placement.ProvisionRequest) (*placement.ProvisionResult, error)
}

// Provisioner eagerly provisions Daytona sandboxes for interactive lead agents.
type Provisioner struct {
	broker           Broker
	store            store.Store
	localSettingsDir string
	allowlist        []string
	snapshotRef      string
	resource         placement.ResourceSize
}

type provisionTarget struct {
	agent   *domain.Agent
	role    *domain.Role
	profile *domain.DaemonProfile
}

// New constructs an eager lead provisioner.
func New(broker Broker, st store.Store, localSettingsDir string, allowlist []string, snapshotRef string, resource placement.ResourceSize) *Provisioner {
	if len(allowlist) == 0 {
		allowlist = defaultAllowlist
	}
	if resource.VCPU <= 0 {
		resource.VCPU = defaultResource.VCPU
	}
	if resource.MemGiB <= 0 {
		resource.MemGiB = defaultResource.MemGiB
	}
	return &Provisioner{
		broker:           broker,
		store:            st,
		localSettingsDir: strings.TrimSpace(localSettingsDir),
		allowlist:        append([]string(nil), allowlist...),
		snapshotRef:      strings.TrimSpace(snapshotRef),
		resource:         resource,
	}
}

// DefaultAllowlist returns the default network domain allowlist for Daytona
// lead sandboxes.
func DefaultAllowlist() []string {
	return append([]string(nil), defaultAllowlist...)
}

// DefaultResource returns the default per-lead reservation.
func DefaultResource() placement.ResourceSize {
	return defaultResource
}

// ProvisionForAgent is a no-op unless the agent is an interactive lead on the
// Daytona runtime. Missing required credentials fail closed.
func (p *Provisioner) ProvisionForAgent(ctx context.Context, workspaceKey, agentName string) error {
	return p.provisionForAgent(ctx, workspaceKey, agentName, false)
}

// ReviveForAgent re-drives provisioning for an existing Daytona lead and
// forces the broker to verify the provider-side lead PTY.
func (p *Provisioner) ReviveForAgent(ctx context.Context, workspaceKey, agentName string) error {
	return p.provisionForAgent(ctx, workspaceKey, agentName, true)
}

func (p *Provisioner) provisionForAgent(ctx context.Context, workspaceKey, agentName string, forceLeadProbe bool) error {
	if p == nil {
		return fmt.Errorf("lead provisioner is not configured")
	}
	if p.store == nil {
		return fmt.Errorf("lead provisioner store is not configured")
	}
	ws := strings.TrimSpace(workspaceKey)
	name := strings.TrimSpace(agentName)
	target, err := p.loadProvisionTarget(ctx, ws, name)
	if err != nil {
		return err
	}
	if !target.needsDaytonaLeadProvision() {
		return nil
	}
	if p.broker == nil {
		return fmt.Errorf("lead provisioner broker is not configured")
	}

	backend := resolvedBackend(target.agent, target.role, target.profile)
	// Post-POC: wire the claude arm instead of seeding Codex credentials into
	// non-Codex leads. Ticket 08's boot probe refuses boot on wrong creds.
	if backend != backendnames.Codex {
		return fmt.Errorf("daytona lead provisioning supports only the codex backend; agent %q resolves to %q (post-POC: wire the claude arm)", name, backend)
	}

	authJSON, gitToken, err := p.runtimeCredentials()
	if err != nil {
		return err
	}
	promptText, err := placement.LeadPromptText(target.role)
	if err != nil {
		return fmt.Errorf("resolve lead prompt text for Daytona lead provisioning: %w", err)
	}

	req := p.provisionRequest(ws, name, authJSON, gitToken, promptText)
	req.ForceLeadProbe = forceLeadProbe
	result, err := p.broker.Provision(ctx, req)
	if err != nil {
		return err
	}
	if result != nil && strings.TrimSpace(result.LeadStartError) != "" {
		return fmt.Errorf("start Daytona lead process for agent %q: %w", name, errors.New(result.LeadStartError))
	}
	return nil
}

func (p *Provisioner) loadProvisionTarget(ctx context.Context, ws, name string) (provisionTarget, error) {
	agent, err := p.store.Agents().Get(ctx, ws, name)
	if err != nil {
		return provisionTarget{}, fmt.Errorf("load agent %q in workspace %q for Daytona lead provisioning: %w", name, ws, err)
	}
	if agent == nil {
		return provisionTarget{}, fmt.Errorf("load agent %q in workspace %q for Daytona lead provisioning: nil agent", name, ws)
	}
	role, err := p.store.Roles().Get(ctx, ws, agent.RoleName)
	if err != nil {
		return provisionTarget{}, fmt.Errorf("load role %q for Daytona lead provisioning: %w", agent.RoleName, err)
	}
	return provisionTarget{agent: agent, role: role, profile: p.daemonProfile(ctx, ws)}, nil
}

func (t provisionTarget) needsDaytonaLeadProvision() bool {
	if t.agent == nil {
		return false
	}
	roleKind := domain.ResolveRoleKind(t.role, t.agent.RoleName)
	runtimeProvider := domain.ResolveRuntimeProvider(t.agent, t.profile)
	return roleKind == domain.RoleKindInteractive && runtimeProvider == domain.RuntimeProviderDaytona
}

func (p *Provisioner) runtimeCredentials() (string, func() (string, error), error) {
	settings, err := runtimesettings.Load(p.localSettingsDir)
	if err != nil {
		return "", nil, fmt.Errorf("load local settings for Daytona lead provisioning: %w", err)
	}
	if strings.TrimSpace(settings.RuntimeCredentials.Codex.Sealed) == "" {
		return "", nil, fmt.Errorf("codex runtime credential not configured; seal it via /api/local/settings before provisioning a Daytona lead")
	}
	authJSON, err := runtimesettings.UnsealRuntimeCredential(p.localSettingsDir, settings, runtimesettings.RuntimeCredentialProviderCodex)
	if err != nil {
		return "", nil, fmt.Errorf("unseal codex runtime credential for Daytona lead provisioning: %w", err)
	}
	return authJSON, p.gitTokenCallback(settings), nil
}

func (p *Provisioner) provisionRequest(ws, name, authJSON string, gitToken func() (string, error), promptText string) placement.ProvisionRequest {
	return placement.ProvisionRequest{
		WorkspaceKey:           ws,
		AgentName:              name,
		SnapshotRef:            p.snapshotRef,
		Caps:                   []string{placement.CapLeadSession, placement.CapLeadAssignment, placement.CapLeadInbox, placement.CapLeadData},
		Resource:               p.resource,
		Backend:                backendnames.Codex,
		GitToken:               gitToken,
		PromptText:             promptText,
		SeedFiles:              []placement.SandboxFile{{Path: codexAuthJSONPath, Content: []byte(authJSON), Mode: "600"}},
		NetworkDomainAllowlist: append([]string(nil), p.allowlist...),
	}
}

func (p *Provisioner) daemonProfile(ctx context.Context, ws string) *domain.DaemonProfile {
	if p.store == nil {
		return nil
	}
	profile, err := p.store.Daemon().Get(ctx, ws)
	if err != nil {
		return nil
	}
	return profile
}

func (p *Provisioner) gitTokenCallback(settings runtimesettings.Settings) func() (string, error) {
	if strings.TrimSpace(settings.RuntimeCredentials.GitHub.Sealed) == "" {
		return nil
	}
	return func() (string, error) {
		latest, err := runtimesettings.Load(p.localSettingsDir)
		if err != nil {
			return "", err
		}
		return runtimesettings.UnsealRuntimeCredential(p.localSettingsDir, latest, runtimesettings.RuntimeCredentialProviderGitHub)
	}
}

func resolvedBackend(agent *domain.Agent, role *domain.Role, profile *domain.DaemonProfile) string {
	backend := ""
	if agent != nil {
		backend = strings.TrimSpace(agent.Backend)
	}
	if backend == "" && role != nil {
		backend = strings.TrimSpace(role.Backend)
	}
	if backend == "" && profile != nil {
		backend = strings.TrimSpace(profile.AgentBackend)
	}
	backend = strings.ToLower(strings.TrimSpace(backend))
	if backend == "" {
		return backendnames.Codex
	}
	return backend
}
