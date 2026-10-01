//go:build daemon_bugreplay

package supervisor

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// Found bug #10: missing arbitration must not authorize a daemon spawn.
func TestBugReplay_Found10_OwnershipAcquireFailsClosed(t *testing.T) {
	t.Run("no control store", func(t *testing.T) {
		s := newOwnershipVerifyTestSupervisor(&scriptedOwnershipLeaseStore{})
		s.ControlStore = nil
		if got := s.acquireAgentOwnership(newOwnershipVerifyAgent()); got == ownershipAcquired {
			t.Fatal("missing control store authorized ownership")
		}
	})
	t.Run("lease route missing", func(t *testing.T) {
		fake := &scriptedOwnershipLeaseStore{acquireResults: []scriptedAcquireResult{{err: wrappedSentinel(domain.ErrNotFound)}}}
		s := newOwnershipVerifyTestSupervisor(fake)
		if got := s.acquireAgentOwnership(newOwnershipVerifyAgent()); got == ownershipAcquired {
			t.Fatal("missing ownership lease route authorized ownership")
		}
	})
}
