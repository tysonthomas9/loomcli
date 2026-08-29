package supervisor

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/discovery"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// countingAgentStore records every agent-state Update the gate issues. The
// PUPPET-54 flap was measured in exactly this unit: one control-plane PATCH per
// 30s recheck, forever, for a backend that was missing for 1-2s.
type countingAgentStore struct {
	store.AgentStore
	mu     sync.Mutex
	states []domain.AgentState
}

func (s *countingAgentStore) Update(ctx context.Context, workspaceKey, name string, patch store.AgentUpdate) (*domain.Agent, error) {
	s.mu.Lock()
	if patch.State != nil {
		s.states = append(s.states, *patch.State)
	}
	s.mu.Unlock()
	return s.AgentStore.Update(ctx, workspaceKey, name, patch)
}

func (s *countingAgentStore) snapshot() []domain.AgentState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]domain.AgentState(nil), s.states...)
}

// newFlapSupervisor wires a real control plane into the backend-unavailable
// fixture. newBackendUnavailableSupervisor has no ControlStore or WorkspaceID,
// so markControlPlaneAgentState short-circuits there — a flap test built on it
// would count zero PATCHes for the wrong reason.
func newFlapSupervisor(t *testing.T) (*Supervisor, *AgentProcess, *countingAgentStore) {
	t.Helper()
	base := memstore.New()
	counter := &countingAgentStore{AgentStore: base.Agents()}

	s := newBackendUnavailableSupervisor()
	s.WorkspaceID = "WS"
	s.ControlStore = &controlPlaneStoreOverrides{Store: base, agents: counter}

	ap := newBackendUnavailableAgentProcess()
	if _, err := base.Agents().Create(context.Background(), store.AgentCreate{
		WorkspaceKey: s.WorkspaceID,
		Name:         ap.Entry.Worktree,
		RoleName:     ap.Entry.Role,
	}); err != nil {
		t.Fatalf("seeding control-plane agent row: %v", err)
	}
	return s, ap, counter
}

func installedInfo(name string) discovery.Info {
	return discovery.Info{Name: name, Binary: name, Installed: true}
}

func missingInfo(name string) discovery.Info {
	return discovery.Info{
		Name: name, Binary: name, Installed: false,
		InstallHint: `"codex" not on PATH`,
	}
}

// TestGateBackendAvailable_TransientMiss_NoStateChange is the PUPPET-54
// regression test: a single lookup miss — the window where the auto-updater is
// rewriting the nvm symlink — must not park the agent or touch the control
// plane.
func TestGateBackendAvailable_TransientMiss_NoStateChange(t *testing.T) {
	seen := 0
	stubCheckBackend(t, func(name string) (discovery.Info, error) {
		seen++
		if seen == 1 {
			return missingInfo(name), nil
		}
		return installedInfo(name), nil
	})

	s, ap, counter := newFlapSupervisor(t)

	if err := s.gateBackendAvailable(context.Background(), ap); err != nil {
		t.Fatalf("gate returned %v, want nil — a transient miss must not block spawn", err)
	}
	if ap.StopReason != "" {
		t.Errorf("StopReason = %q, want empty", ap.StopReason)
	}
	if ap.LastError != nil {
		t.Errorf("LastError = %v, want nil", ap.LastError)
	}
	if got := counter.snapshot(); len(got) != 0 {
		t.Errorf("control-plane agent-state updates = %v, want none", got)
	}
}

// TestGateBackendAvailable_PersistentMiss_PatchesOnce is the positive control
// for the test above: a genuinely missing backend must still park the agent,
// and must PATCH exactly once across repeated rechecks rather than once per
// recheck.
func TestGateBackendAvailable_PersistentMiss_PatchesOnce(t *testing.T) {
	stubCheckBackend(t, func(name string) (discovery.Info, error) {
		return missingInfo(name), nil
	})

	s, ap, counter := newFlapSupervisor(t)

	for i := 0; i < 3; i++ {
		if err := s.gateBackendAvailable(context.Background(), ap); !errors.Is(err, ErrBackendUnavailable) {
			t.Fatalf("call %d: err = %v, want ErrBackendUnavailable", i+1, err)
		}
	}

	if ap.StopReason != StopReasonBackendUnavailable {
		t.Errorf("StopReason = %q, want %q", ap.StopReason, StopReasonBackendUnavailable)
	}
	got := counter.snapshot()
	if len(got) != 1 {
		t.Fatalf("agent-state updates = %v (%d), want exactly 1 across 3 rechecks", got, len(got))
	}
	if got[0] != domain.AgentStateBackendUnavailable {
		t.Errorf("state = %v, want %v", got[0], domain.AgentStateBackendUnavailable)
	}
}

func TestGateBackendAvailable_PersistentMiss_ReassertsAfterInterval(t *testing.T) {
	stubCheckBackend(t, func(name string) (discovery.Info, error) {
		return missingInfo(name), nil
	})

	s, ap, counter := newFlapSupervisor(t)

	for i := 0; i < 2; i++ {
		if err := s.gateBackendAvailable(context.Background(), ap); !errors.Is(err, ErrBackendUnavailable) {
			t.Fatalf("call %d: err = %v, want ErrBackendUnavailable", i+1, err)
		}
	}
	if got := counter.snapshot(); len(got) != 1 {
		t.Fatalf("before the interval elapses: updates = %d, want 1", len(got))
	}

	// Rewind the stamp past the re-assert interval: a control-plane row that is
	// recreated or reset under a parked agent still has to converge.
	ap.Mu.Lock()
	ap.BackendStatePatchedAt = time.Now().Add(-backendStateReassertInterval - time.Second)
	ap.Mu.Unlock()

	if err := s.gateBackendAvailable(context.Background(), ap); !errors.Is(err, ErrBackendUnavailable) {
		t.Fatalf("third call: err = %v, want ErrBackendUnavailable", err)
	}
	if got := counter.snapshot(); len(got) != 2 {
		t.Errorf("after the interval elapses: updates = %d, want 2 (level re-assert)", len(got))
	}
}

func TestGateBackendAvailable_Recovery_PatchesActiveOnce(t *testing.T) {
	installed := false
	stubCheckBackend(t, func(name string) (discovery.Info, error) {
		if installed {
			return installedInfo(name), nil
		}
		return missingInfo(name), nil
	})

	s, ap, counter := newFlapSupervisor(t)

	if err := s.gateBackendAvailable(context.Background(), ap); !errors.Is(err, ErrBackendUnavailable) {
		t.Fatalf("park: err = %v, want ErrBackendUnavailable", err)
	}

	installed = true
	for i := 0; i < 2; i++ {
		if err := s.gateBackendAvailable(context.Background(), ap); err != nil {
			t.Fatalf("recovery call %d: err = %v, want nil", i+1, err)
		}
	}

	if ap.StopReason != "" {
		t.Errorf("StopReason = %q, want cleared", ap.StopReason)
	}
	if ap.LastError != nil {
		t.Errorf("LastError = %v, want cleared", ap.LastError)
	}
	got := counter.snapshot()
	want := []domain.AgentState{domain.AgentStateBackendUnavailable, domain.AgentStateActive}
	if len(got) != len(want) {
		t.Fatalf("agent-state updates = %v, want %v (recovery patches Active exactly once)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("update[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}
