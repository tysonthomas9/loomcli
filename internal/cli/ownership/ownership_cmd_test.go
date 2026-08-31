package ownership

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// fakeOwnershipStore is a stand-in for the control-plane ownership lease
// store. Only Get and Release are exercised; the rest satisfy the interface.
type fakeOwnershipStore struct {
	lease  *domain.AgentOwnershipLease
	getErr error

	releaseCalls []releaseCall
	releaseErr   error
}

type releaseCall struct {
	ws      string
	agentID string
	token   string
}

var _ store.AgentOwnershipLeaseStore = (*fakeOwnershipStore)(nil)

func (f *fakeOwnershipStore) Acquire(context.Context, store.AgentOwnershipLeaseAcquire) (*domain.AgentOwnershipLease, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeOwnershipStore) Get(_ context.Context, _, _ string) (*domain.AgentOwnershipLease, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.lease, nil
}

func (f *fakeOwnershipStore) List(context.Context, string, store.AgentOwnershipLeaseFilter) ([]*domain.AgentOwnershipLease, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeOwnershipStore) Heartbeat(context.Context, string, string, string, time.Duration) (*domain.AgentOwnershipLease, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeOwnershipStore) Release(_ context.Context, ws, agentID, token string) (*domain.AgentOwnershipLease, error) {
	f.releaseCalls = append(f.releaseCalls, releaseCall{ws: ws, agentID: agentID, token: token})
	if f.releaseErr != nil {
		return nil, f.releaseErr
	}
	out := *f.lease
	out.Status = domain.AgentLeaseReleased
	return &out, nil
}

func activeLease() *domain.AgentOwnershipLease {
	return &domain.AgentOwnershipLease{
		WorkspaceKey: "PUPPET",
		AgentID:      "worker-3",
		LeaseID:      "lease-abc",
		OwnerID:      "supervisor-1",
		NodeID:       "node-a",
		Token:        "tok-123",
		Status:       domain.AgentLeaseActive,
		ExpiresAt:    time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC),
	}
}

// The release must use the token from the preceding Get — that read is the
// only reason the command exists, since an operator has no other way to learn
// the token of a lease they do not hold.
func TestReleaseOwnershipUsesTokenFromGet(t *testing.T) {
	fake := &fakeOwnershipStore{lease: activeLease()}
	var out bytes.Buffer

	if err := releaseOwnership(context.Background(), fake, &out, "PUPPET", "worker-3"); err != nil {
		t.Fatalf("releaseOwnership: %v", err)
	}

	if len(fake.releaseCalls) != 1 {
		t.Fatalf("want 1 release call, got %d", len(fake.releaseCalls))
	}
	got := fake.releaseCalls[0]
	if got.token != "tok-123" || got.ws != "PUPPET" || got.agentID != "worker-3" {
		t.Fatalf("unexpected release call: %+v", got)
	}
	text := out.String()
	// The holder is printed: the lease belongs to someone else by
	// construction, and the operator needs to see whom it was taken from.
	for _, want := range []string{"supervisor-1", "node-a", "lease-abc", "status=released"} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q:\n%s", want, text)
		}
	}
}

func TestReleaseOwnershipNoLease(t *testing.T) {
	for name, fake := range map[string]*fakeOwnershipStore{
		"not found error": {getErr: fmt.Errorf("fleetdb: get: %w", domain.ErrNotFound)},
		"nil lease":       {},
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			err := releaseOwnership(context.Background(), fake, &out, "PUPPET", "worker-3")
			if err == nil {
				t.Fatal("want an error so the command exits non-zero, got nil")
			}
			if !strings.Contains(err.Error(), "no ownership lease for agent \"worker-3\"") {
				t.Errorf("unclear error message: %v", err)
			}
			if len(fake.releaseCalls) != 0 {
				t.Errorf("released despite no lease: %+v", fake.releaseCalls)
			}
		})
	}
}

// An already-released lease is the desired end state, so it reports success
// rather than failing a retried command.
func TestReleaseOwnershipAlreadyReleased(t *testing.T) {
	lease := activeLease()
	lease.Status = domain.AgentLeaseReleased
	fake := &fakeOwnershipStore{lease: lease}
	var out bytes.Buffer

	if err := releaseOwnership(context.Background(), fake, &out, "PUPPET", "worker-3"); err != nil {
		t.Fatalf("releaseOwnership: %v", err)
	}
	if len(fake.releaseCalls) != 0 {
		t.Errorf("released an inactive lease: %+v", fake.releaseCalls)
	}
	if !strings.Contains(out.String(), "already released") {
		t.Errorf("output does not explain the no-op:\n%s", out.String())
	}
}

func TestReleaseOwnershipMissingToken(t *testing.T) {
	lease := activeLease()
	lease.Token = ""
	fake := &fakeOwnershipStore{lease: lease}

	err := releaseOwnership(context.Background(), fake, &bytes.Buffer{}, "PUPPET", "worker-3")
	if err == nil || !strings.Contains(err.Error(), "carries no token") {
		t.Fatalf("want a token-specific error, got %v", err)
	}
	if len(fake.releaseCalls) != 0 {
		t.Errorf("sent an empty token: %+v", fake.releaseCalls)
	}
}

func TestReleaseCommandRequiresExactlyOneAgent(t *testing.T) {
	if err := releaseCmd.Args(releaseCmd, nil); err == nil {
		t.Error("want an error for no agent argument")
	}
	if err := releaseCmd.Args(releaseCmd, []string{"a", "b"}); err == nil {
		t.Error("want an error for two agent arguments")
	}
	if err := releaseCmd.Args(releaseCmd, []string{"worker-3"}); err != nil {
		t.Errorf("one agent argument should be accepted: %v", err)
	}
}
