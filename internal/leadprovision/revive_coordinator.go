package leadprovision

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

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

type AgentProvisioner interface {
	ReviveForAgent(context.Context, string, string) error
}

type ReviveCoordinator struct {
	provider    SandboxStateProvider
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

func NewReviveCoordinator(provider SandboxStateProvider, provisioner AgentProvisioner) *ReviveCoordinator {
	return &ReviveCoordinator{
		provider:    provider,
		provisioner: provisioner,
		entries:     make(map[reviveKey]*reviveEntry),
	}
}

func (c *ReviveCoordinator) EnsureAttachable(ctx context.Context, workspace, agent, sandboxID string) error {
	if c == nil || c.provider == nil || c.provisioner == nil {
		return fmt.Errorf("lead revive coordinator is not configured")
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

	needsRevive, err := c.sandboxRequiresRevive(ctx, sandboxID)
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
func (c *ReviveCoordinator) sandboxRequiresRevive(ctx context.Context, sandboxID string) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	stateCtx, cancel := context.WithTimeout(ctx, reviveStateTimeout)
	defer cancel()
	sandbox, err := c.provider.Get(stateCtx, sandboxID)
	if err != nil {
		return false, fmt.Errorf("get lead sandbox %q state before attach: %w", sandboxID, err)
	}
	needsRevive, err := sandboxNeedsRevive(sandbox)
	if err != nil {
		return false, err
	}
	if sandbox.RawState == placement.ProviderSandboxRawStarted {
		sessions, listErr := c.provider.ListPtySessions(stateCtx, sandboxID)
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
