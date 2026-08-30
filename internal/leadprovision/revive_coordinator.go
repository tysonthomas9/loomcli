package leadprovision

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/placement"
)

const (
	reviveStateTimeout     = 5 * time.Second
	reviveProvisionTimeout = 12 * time.Minute
)

var (
	ErrReviveStarting      = errors.New("lead sandbox revive is in progress")
	ErrReviveTerminalState = errors.New("lead sandbox is in a terminal provider state")
)

type ReviveState string

const (
	ReviveStateIdle   ReviveState = "idle"
	ReviveStateWaking ReviveState = "waking"
	ReviveStateFailed ReviveState = "failed"
)

type ReviveStatus struct {
	State ReviveState
	Err   error
}

type SandboxStateProvider interface {
	Get(context.Context, string) (placement.ProviderSandbox, error)
	ListPtySessions(context.Context, string) ([]placement.PtySession, error)
}

// SandboxStateProviderRegistry maps a runtime provider onto the adapter that
// owns its sandboxes, mirroring placement.ProviderRegistry.
//
// The coordinator previously held ONE provider and took a bare sandbox id. A
// sandbox id is unique only within a provider, so with a second provider
// registered that would inspect -- and revive -- whatever sandbox happened to
// carry the same id on the wrong platform.
type SandboxStateProviderRegistry map[domain.RuntimeProvider]SandboxStateProvider

type AgentProvisioner interface {
	ReviveForAgent(context.Context, string, string) error
}

type ReviveCoordinator struct {
	providers   SandboxStateProviderRegistry
	provisioner AgentProvisioner
	mu          sync.Mutex
	entries     map[reviveKey]*reviveEntry
}

type reviveKey struct {
	workspace string
	agent     string
}

type reviveEntry struct {
	mu    sync.Mutex
	state ReviveState
	err   error
}

func NewReviveCoordinator(providers SandboxStateProviderRegistry, provisioner AgentProvisioner) *ReviveCoordinator {
	// Snapshot: a registry the caller can still mutate would let routing change
	// under a running coordinator.
	snapshot := make(SandboxStateProviderRegistry, len(providers))
	for kind, p := range providers {
		if kind == "" || p == nil {
			continue
		}
		snapshot[kind] = p
	}
	return &ReviveCoordinator{
		providers:   snapshot,
		provisioner: provisioner,
		entries:     make(map[reviveKey]*reviveEntry),
	}
}

// Supports reports whether revive can act on a runtime provider, so callers can
// gate on the registry instead of hardcoding a provider name.
func (c *ReviveCoordinator) Supports(kind domain.RuntimeProvider) bool {
	if c == nil {
		return false
	}
	_, err := c.providerFor(kind)
	return err == nil
}

// providerFor resolves the adapter owning a sandbox, FAIL-CLOSED. An
// unregistered or unset provider is an error, never a fallback to the only
// registered adapter -- that fallback is precisely how a revive lands on
// another platform's sandbox.
func (c *ReviveCoordinator) providerFor(kind domain.RuntimeProvider) (SandboxStateProvider, error) {
	if strings.TrimSpace(string(kind)) == "" {
		return nil, fmt.Errorf("runtime provider not set on lead placement")
	}
	p, ok := c.providers[kind]
	if !ok || p == nil {
		return nil, fmt.Errorf("no sandbox state provider registered for runtime provider %q", kind)
	}
	return p, nil
}

func (c *ReviveCoordinator) EnsureAttachable(ctx context.Context, workspace, agent string, kind domain.RuntimeProvider, sandboxID string) error {
	if c == nil || len(c.providers) == 0 || c.provisioner == nil {
		return fmt.Errorf("lead revive coordinator is not configured")
	}
	prov, err := c.providerFor(kind)
	if err != nil {
		return err
	}
	key := reviveKey{workspace: strings.TrimSpace(workspace), agent: strings.TrimSpace(agent)}
	sandboxID = strings.TrimSpace(sandboxID)
	if key.workspace == "" || key.agent == "" || sandboxID == "" {
		return fmt.Errorf("workspace, agent, and sandbox id are required for lead revive")
	}
	entry := c.entry(key)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	switch entry.state {
	case ReviveStateWaking:
		return ErrReviveStarting
	case ReviveStateFailed:
		retained := entry.err
		entry.state = ReviveStateWaking
		entry.err = nil
		go c.provision(key, entry)
		return retained
	}

	needsRevive, err := c.sandboxRequiresRevive(ctx, prov, sandboxID)
	if err != nil {
		return err
	}
	if !needsRevive {
		return nil
	}
	entry.state = ReviveStateWaking
	entry.err = nil
	go c.provision(key, entry)
	return ErrReviveStarting
}

// sandboxRequiresRevive inspects raw provider state; callers hold the entry
// lock so concurrent ensures cannot both observe idle and kick a revive.
func (c *ReviveCoordinator) sandboxRequiresRevive(ctx context.Context, prov SandboxStateProvider, sandboxID string) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	stateCtx, cancel := context.WithTimeout(ctx, reviveStateTimeout)
	defer cancel()
	sandbox, err := prov.Get(stateCtx, sandboxID)
	if err != nil {
		return false, fmt.Errorf("get lead sandbox %q state before attach: %w", sandboxID, err)
	}
	needsRevive, err := sandboxNeedsRevive(sandbox)
	if err != nil {
		return false, err
	}
	if sandbox.RawState == placement.ProviderSandboxRawStarted {
		sessions, listErr := prov.ListPtySessions(stateCtx, sandboxID)
		if listErr != nil {
			return false, fmt.Errorf("list lead sandbox %q PTY sessions before attach: %w", sandboxID, listErr)
		}
		needsRevive = !hasLeadPTYSession(sessions)
	}
	return needsRevive, nil
}

func (c *ReviveCoordinator) Status(workspace, agent string) ReviveStatus {
	if c == nil {
		return ReviveStatus{State: ReviveStateIdle}
	}
	key := reviveKey{workspace: strings.TrimSpace(workspace), agent: strings.TrimSpace(agent)}
	c.mu.Lock()
	entry := c.entries[key]
	c.mu.Unlock()
	if entry == nil {
		return ReviveStatus{State: ReviveStateIdle}
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	return ReviveStatus{State: entry.state, Err: entry.err}
}

func (c *ReviveCoordinator) entry(key reviveKey) *reviveEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[key]
	if entry == nil {
		entry = &reviveEntry{state: ReviveStateIdle}
		c.entries[key] = entry
	}
	return entry
}

func (c *ReviveCoordinator) provision(key reviveKey, entry *reviveEntry) {
	var provisionErr error
	defer func() {
		entry.mu.Lock()
		defer entry.mu.Unlock()
		if provisionErr != nil {
			entry.state = ReviveStateFailed
			entry.err = provisionErr
			return
		}
		entry.state = ReviveStateIdle
		entry.err = nil
	}()

	ctx, cancel := context.WithTimeout(context.Background(), reviveProvisionTimeout)
	defer cancel()
	provisionErr = c.provisioner.ReviveForAgent(ctx, key.workspace, key.agent)
}

func sandboxNeedsRevive(sandbox placement.ProviderSandbox) (bool, error) {
	switch sandbox.RawState {
	case placement.ProviderSandboxRawStarted:
		return false, nil
	case placement.ProviderSandboxRawStopped,
		placement.ProviderSandboxRawArchived,
		placement.ProviderSandboxRawPaused,
		placement.ProviderSandboxRawStarting,
		placement.ProviderSandboxRawRestoring,
		placement.ProviderSandboxRawResuming,
		placement.ProviderSandboxRawStopping,
		placement.ProviderSandboxRawPausing,
		placement.ProviderSandboxRawArchiving:
		return true, nil
	case placement.ProviderSandboxRawError, placement.ProviderSandboxRawBuildFailed:
		return false, fmt.Errorf("%w: lead sandbox %q has raw state %q", ErrReviveTerminalState, sandbox.ID, sandbox.RawState)
	case placement.ProviderSandboxRawDestroyed:
		return false, fmt.Errorf("%w: lead sandbox %q is destroyed", placement.ErrSandboxNotFound, sandbox.ID)
	case "":
		return false, fmt.Errorf("lead sandbox %q provider does not expose raw state", sandbox.ID)
	}
	return false, fmt.Errorf("lead sandbox %q has unsupported provider state %q", sandbox.ID, sandbox.RawState)
}

func hasLeadPTYSession(sessions []placement.PtySession) bool {
	for _, session := range sessions {
		if strings.TrimSpace(session.SessionID) == placement.LeadPTYSessionID {
			return true
		}
	}
	return false
}
