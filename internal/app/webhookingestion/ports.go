// Package webhookingestion coordinates verified external webhook admission.
// It owns the narrow ports needed by that workflow; HTTP request handling,
// connector secret storage, and Automation persistence remain outside it.
package webhookingestion

import (
	"context"
	"encoding/json"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

// VerificationRequest is the bounded, transport-neutral input a connector
// verifier needs. PresentedSignature is caller-provided proof, never the
// server-side signing secret. Verify returns only an error so a successful
// verification cannot leak plaintext connector credentials back to the
// workflow.
type VerificationRequest struct {
	WorkspaceKey string
	SourceKind   string
	// SourceRef is optional for compatibility with legacy GitHub bindings;
	// RouteKey remains the exact ingress binding address in that lane.
	SourceRef          string
	RouteKey           string
	PresentedSignature string
	Payload            json.RawMessage
}

// Verifier verifies the presented proof against connector-owned secret state.
// Implementations must keep that secret inside the adapter boundary.
type Verifier interface {
	Verify(context.Context, VerificationRequest) error
}

// AuthorityRequest identifies the already-verified webhook source for which
// server composition must derive authority. It deliberately contains neither
// caller credentials nor a general-purpose action selector.
type AuthorityRequest struct {
	WorkspaceKey string
	SourceKind   string
	SourceRef    string
	RouteKey     string
}

// AuthorityProvider returns server-derived authority scoped to Automation
// event admission. The workflow invokes it only after Verifier succeeds.
type AuthorityProvider interface {
	AuthorityForVerifiedWebhook(context.Context, AuthorityRequest) (authority.WebhookAuthority, error)
}
