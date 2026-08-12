// Package systemeventing coordinates internal events emitted by a registered,
// server-owned component. Transport identity and Automation persistence remain
// outside this named application workflow.
package systemeventing

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const (
	IssueJournalBridgeComponentID = "serve-issue-journal-bridge"
	DriverRunOutcomeComponentID   = "serve-driver-run-outcomes"
)

// VerifiedSource is server-derived runtime identity. ActorRef may carry an
// identity read from a trusted durable journal; it is deliberately separate
// from EmitRequest so request/event content cannot forge the admitted actor.
type VerifiedSource struct {
	ComponentID  string
	WorkspaceKey string
	ActorRef     string
}

// AuthorityProvider validates a registered source and issues only the exact
// Automation admission action for its server-derived workspace and actor.
type AuthorityProvider interface {
	AuthorityForVerifiedSource(context.Context, VerifiedSource) (authority.SystemAuthority, error)
}

// IssueJournalEmitter is the pre-bound application port exposed to the issue
// journal bridge. The registered component identity is captured when the port
// is composed; callers can supply only the trusted journal workspace/actor and
// event content, never a component selector.
type IssueJournalEmitter interface {
	EmitIssueJournal(context.Context, string, string, EmitRequest) (*automation.AdmissionResult, error)
}

// RunOutcomeEmitter is the pre-bound application port exposed to Execution's
// composition adapter. Like IssueJournalEmitter, it never accepts a component
// identifier from its caller.
type RunOutcomeEmitter interface {
	EmitRunOutcome(context.Context, string, string, EmitRequest) (*automation.AdmissionResult, error)
}
