package daemon

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestValidateIPCLease_HeartbeatSuccess_HappyPath(t *testing.T) {
	leases := &scriptedAgentLeaseStore{
		heartbeatLease: validIPCLease(),
	}
	d := newLeaseValidationDaemon(leases)

	resp, ok := d.validateIPCLease(t.Context(), validIPCRequest())
	if !ok {
		t.Fatalf("validateIPCLease failed: %+v", resp)
	}
	assertLeaseStoreCalls(t, leases.calls, []string{"heartbeat"})
}

func TestValidateIPCLease_Heartbeat409_GetSucceeds(t *testing.T) {
	leases := &scriptedAgentLeaseStore{
		heartbeatErr: fmt.Errorf("fleetdb heartbeat: %w", domain.ErrAlreadyExists),
		getLease:     validIPCLease(),
	}
	d := newLeaseValidationDaemon(leases)

	resp, ok := d.validateIPCLease(t.Context(), validIPCRequest())
	if !ok {
		t.Fatalf("validateIPCLease failed: %+v", resp)
	}
	assertLeaseStoreCalls(t, leases.calls, []string{"heartbeat", "get"})
}

// Heartbeat 410 (fleet-db lease_expired → domain.ErrGone) takes the same
// verify-via-get fast path as the legacy 409 mislabel: a live, token-matched
// record still validates the fenced mutation.
func TestValidateIPCLease_Heartbeat410Gone_GetSucceeds(t *testing.T) {
	leases := &scriptedAgentLeaseStore{
		heartbeatErr: fmt.Errorf("fleetdb heartbeat: %w", domain.ErrGone),
		getLease:     validIPCLease(),
	}
	d := newLeaseValidationDaemon(leases)

	resp, ok := d.validateIPCLease(t.Context(), validIPCRequest())
	if !ok {
		t.Fatalf("validateIPCLease failed: %+v", resp)
	}
	assertLeaseStoreCalls(t, leases.calls, []string{"heartbeat", "get"})
}

func TestValidateIPCLease_Heartbeat409_GetReturnsExpiredLease_Rejects(t *testing.T) {
	lease := validIPCLease()
	lease.ExpiresAt = time.Now().Add(-time.Minute)
	leases := &scriptedAgentLeaseStore{
		heartbeatErr: fmt.Errorf("fleetdb heartbeat: %w", domain.ErrAlreadyExists),
		getLease:     lease,
	}
	d := newLeaseValidationDaemon(leases)

	resp, ok := d.validateIPCLease(t.Context(), validIPCRequest())
	if ok {
		t.Fatal("validateIPCLease succeeded for expired lease")
	}
	if resp.Kind != string(backend.KindConflict) {
		t.Fatalf("response kind = %q, want %q: %+v", resp.Kind, backend.KindConflict, resp)
	}
}

func TestValidateIPCLease_Heartbeat409_GetReturnsReleasedLease_Rejects(t *testing.T) {
	lease := validIPCLease()
	lease.Status = domain.AgentLeaseReleased
	leases := &scriptedAgentLeaseStore{
		heartbeatErr: fmt.Errorf("fleetdb heartbeat: %w", domain.ErrAlreadyExists),
		getLease:     lease,
	}
	d := newLeaseValidationDaemon(leases)

	resp, ok := d.validateIPCLease(t.Context(), validIPCRequest())
	if ok {
		t.Fatal("validateIPCLease succeeded for released lease")
	}
	if resp.Kind != string(backend.KindConflict) {
		t.Fatalf("response kind = %q, want %q: %+v", resp.Kind, backend.KindConflict, resp)
	}
}

func TestValidateIPCLease_Heartbeat409_GetReturnsMismatchedToken_Rejects(t *testing.T) {
	lease := validIPCLease()
	lease.Token = "other-token"
	leases := &scriptedAgentLeaseStore{
		heartbeatErr: fmt.Errorf("fleetdb heartbeat: %w", domain.ErrAlreadyExists),
		getLease:     lease,
	}
	d := newLeaseValidationDaemon(leases)

	resp, ok := d.validateIPCLease(t.Context(), validIPCRequest())
	if ok {
		t.Fatal("validateIPCLease succeeded for mismatched token")
	}
	if resp.Kind != string(backend.KindConflict) {
		t.Fatalf("response kind = %q, want %q: %+v", resp.Kind, backend.KindConflict, resp)
	}
}

func TestValidateIPCLease_Heartbeat409_GetReturnsNotFound_PropagatesError(t *testing.T) {
	leases := &scriptedAgentLeaseStore{
		heartbeatErr: fmt.Errorf("fleetdb heartbeat: %w", domain.ErrAlreadyExists),
		getErr:       fmt.Errorf("get lease: %w", domain.ErrNotFound),
	}
	d := newLeaseValidationDaemon(leases)

	resp, ok := d.validateIPCLease(t.Context(), validIPCRequest())
	if ok {
		t.Fatal("validateIPCLease succeeded when Get returned not found")
	}
	if resp.Error == "" {
		t.Fatalf("response did not include propagated error: %+v", resp)
	}
	if !strings.Contains(resp.Error, "not found") {
		t.Fatalf("response error = %q, want not found", resp.Error)
	}
	assertLeaseStoreCalls(t, leases.calls, []string{"heartbeat", "get"})
}

func TestValidateIPCLease_HeartbeatOtherErrorPropagates(t *testing.T) {
	leases := &scriptedAgentLeaseStore{
		heartbeatErr: fmt.Errorf("fleetdb heartbeat: %w", domain.ErrInvalid),
	}
	d := newLeaseValidationDaemon(leases)

	resp, ok := d.validateIPCLease(t.Context(), validIPCRequest())
	if ok {
		t.Fatal("validateIPCLease succeeded when Heartbeat returned invalid")
	}
	if resp.Error == "" {
		t.Fatalf("response did not include propagated error: %+v", resp)
	}
	assertLeaseStoreCalls(t, leases.calls, []string{"heartbeat"})
}

func TestValidateIPCLease_HeartbeatSuccess_TokenMismatch_Rejects(t *testing.T) {
	lease := validIPCLease()
	lease.Token = "other-token"
	leases := &scriptedAgentLeaseStore{
		heartbeatLease: lease,
	}
	d := newLeaseValidationDaemon(leases)

	resp, ok := d.validateIPCLease(t.Context(), validIPCRequest())
	if ok {
		t.Fatal("validateIPCLease succeeded for mismatched heartbeat lease token")
	}
	if resp.Kind != string(backend.KindConflict) {
		t.Fatalf("response kind = %q, want %q: %+v", resp.Kind, backend.KindConflict, resp)
	}
	assertLeaseStoreCalls(t, leases.calls, []string{"heartbeat"})
}

type leaseValidationStore struct {
	*memstore.Store
	leases store.AgentLeaseStore
}

func (s *leaseValidationStore) AgentLeases() store.AgentLeaseStore {
	return s.leases
}

type scriptedAgentLeaseStore struct {
	store.AgentLeaseStore
	heartbeatLease *domain.AgentLease
	heartbeatErr   error
	getLease       *domain.AgentLease
	getErr         error
	calls          []string
}

func (s *scriptedAgentLeaseStore) Heartbeat(context.Context, string, string, string, time.Duration) (*domain.AgentLease, error) {
	s.calls = append(s.calls, "heartbeat")
	return s.heartbeatLease, s.heartbeatErr
}

func (s *scriptedAgentLeaseStore) Get(context.Context, string, string) (*domain.AgentLease, error) {
	s.calls = append(s.calls, "get")
	return s.getLease, s.getErr
}

func newLeaseValidationDaemon(leases store.AgentLeaseStore) *Daemon {
	d := newTestIPCDaemon(&mockIPCBackend{})
	d.store = &leaseValidationStore{
		Store:  memstore.New(),
		leases: leases,
	}
	d.sup.WorkspaceID = "WS"
	return d
}

func validIPCRequest() AgentIPCRequest {
	return AgentIPCRequest{
		AgentName:  "planner",
		SessionID:  "session-1",
		LeaseID:    "lease-1",
		LeaseToken: "token-1",
	}
}

func validIPCLease() *domain.AgentLease {
	return &domain.AgentLease{
		WorkspaceKey: "WS",
		LeaseID:      "lease-1",
		SessionID:    "session-1",
		AgentID:      "planner",
		Token:        "token-1",
		Status:       domain.AgentLeaseActive,
		ExpiresAt:    time.Now().Add(time.Hour),
	}
}

func assertLeaseStoreCalls(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("calls = %v, want %v", got, want)
		}
	}
}
