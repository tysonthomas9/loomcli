// Package ownership implements the `loom ownership` command tree: the
// operator escape hatch for agent-ownership leases.
//
// A supervisor that dies without releasing its lease leaves the agent
// unclaimable for the remainder of the lease TTL (30 minutes), which stalls
// the fleet. Clearing it by hand means a GET to read the lease token
// followed by a POST to the release endpoint — and that POST 415s with
// `missing_content_type` unless it carries `Content-Type: application/json`
// and a body, which reads like a bad token rather than a malformed request.
// `loom ownership release <agent>` does the same two calls through the
// control-plane store, so the content type, workspace, base URL and auth all
// come from the same place every other command uses.
package ownership

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

var ownershipCmd = &cobra.Command{
	Use:     "ownership",
	Short:   "Inspect and release agent-ownership leases",
	GroupID: "workspace",
}

var releaseCmd = &cobra.Command{
	Use:   "release <agent>",
	Short: "Release the agent-ownership lease held on an agent",
	Long: `Release the agent-ownership lease held on an agent.

This is an operator escape hatch. It reads the current lease to obtain its
token and then releases it, so a lease orphaned by a supervisor that died
without shutting down cleanly does not keep the agent unclaimable for the
rest of its TTL.

The lease is released even though it belongs to another owner — that is the
point of the command — and the owner it was taken from is printed.

The workspace comes from --workspace or LOOM_WORKSPACE, like every other
control-plane command.

EXAMPLES
  loom ownership release worker-3
  loom ownership release worker-3 --workspace PUPPET`,
	Args: cobra.ExactArgs(1),
	RunE: runRelease,
}

func runRelease(cmd *cobra.Command, args []string) error {
	agentID := strings.TrimSpace(args[0])
	if agentID == "" {
		return errors.New("agent id must not be empty")
	}
	out := cmd.OutOrStdout()
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		return releaseOwnership(ctx, h.Store.AgentOwnershipLeases(), out, ws, agentID)
	})
}

// releaseOwnership is the whole command minus store construction, so it can be
// exercised against a fake AgentOwnershipLeaseStore.
func releaseOwnership(ctx context.Context, leases store.AgentOwnershipLeaseStore, out io.Writer, ws, agentID string) error {
	lease, err := leases.Get(ctx, ws, agentID)
	if err != nil {
		if cmdstore.IsNotFound(err) {
			return noLeaseError(ws, agentID)
		}
		return fmt.Errorf("get ownership lease for agent %q: %w", agentID, err)
	}
	if lease == nil {
		return noLeaseError(ws, agentID)
	}
	if lease.Status != domain.AgentLeaseActive {
		_, _ = fmt.Fprintf(out, "Ownership lease for agent %s in workspace %s is already %s; nothing to release.\n",
			agentID, ws, lease.Status)
		return nil
	}
	// The token is what the release endpoint authenticates against. fleet-db
	// returns it on the lease read; if it ever stops doing so, say why the
	// release cannot proceed instead of sending an empty token and reporting
	// the resulting 400 as a lease problem.
	if strings.TrimSpace(lease.Token) == "" {
		return fmt.Errorf("ownership lease for agent %q in workspace %q carries no token; cannot release it", agentID, ws)
	}

	_, _ = fmt.Fprintf(out, "Releasing ownership lease %s for agent %s (held by %s, expires %s).\n",
		lease.LeaseID, agentID, ownerLabel(lease), formatExpiry(lease.ExpiresAt))

	released, err := leases.Release(ctx, ws, agentID, lease.Token)
	if err != nil {
		return fmt.Errorf("release ownership lease for agent %q: %w", agentID, err)
	}
	status := domain.AgentLeaseReleased
	if released != nil && released.Status != "" {
		status = released.Status
	}
	_, _ = fmt.Fprintf(out, "Released ownership lease for agent %s in workspace %s (status=%s).\n", agentID, ws, status)
	return nil
}

func noLeaseError(ws, agentID string) error {
	return fmt.Errorf("no ownership lease for agent %q in workspace %q; nothing to release", agentID, ws)
}

// ownerLabel names the holder of a lease for the operator. Owner and node are
// both optional on the wire, so fall back rather than printing empty quotes.
func ownerLabel(lease *domain.AgentOwnershipLease) string {
	owner := strings.TrimSpace(lease.OwnerID)
	if owner == "" {
		owner = "unknown owner"
	}
	if node := strings.TrimSpace(lease.NodeID); node != "" {
		return owner + " on node " + node
	}
	return owner
}

func formatExpiry(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	return t.UTC().Format(time.RFC3339)
}

func init() {
	ownershipCmd.AddCommand(releaseCmd)
	cli.RegisterCommand(ownershipCmd)
}
