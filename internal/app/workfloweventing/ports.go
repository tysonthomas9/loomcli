// Package workfloweventing coordinates events emitted by an already-verified
// workflow execution. HTTP identity verification and Automation persistence
// remain outside this application workflow.
package workfloweventing

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

// VerifiedRun is the minimal server-derived execution envelope this workflow
// needs. The transport maps its legacy run record into this app-local type so
// the workflow does not depend on the global domain package.
type VerifiedRun struct {
	WorkspaceKey string
	RunID        string
	Status       string
	NodeID       string
	LeaseID      string
	FencingToken int64
}

// ExecutionAuthorityProvider derives the one action-scoped authority that may
// enter Automation admission for an already-verified running DriverRun. The
// workflow never accepts an authority or action selector from request data.
type ExecutionAuthorityProvider interface {
	AuthorityForVerifiedRun(context.Context, VerifiedRun) (authority.ExecutionAuthority, error)
}
