package webhooks

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	"github.com/tysonthomas9/loomcli/internal/app/webhookingestion"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
)

// inboundSecretCandidate is one signing secret the inbound verifier may match,
// tagged with provenance so a previous-secret match can emit the stale-secret
// audit signal.
type inboundSecretCandidate struct {
	secret string
	// connectorID is the owning connector.
	connectorID string
	// stale marks a connector's previous inbound secret inside its
	// dual-secret rotation window.
	stale bool
}

// errInboundUnverified is the single uniform 401 returned for EVERY inbound
// verification failure — bad signature, no resolvable secret, expired
// rotation window, or a secret-resolution error. A uniform body leaks
// nothing about which secrets or connectors exist (security.html S2).
func errInboundUnverified() error {
	return unverified("webhook signature verification failed")
}

// VerifierConfig supplies the owner APIs used only inside inbound signature
// verification. Neither resolved secrets nor a binding DTO cross into
// webhookingestion.
type VerifierConfig struct {
	Bindings   bindingRouteQueries
	Connectors connectorSecretSource
	Now        func() time.Time
}

type bindingRouteQueries interface {
	ListBindings(context.Context, string, automation.BindingFilter) ([]*automation.Binding, error)
}

type connectorSecretSource interface {
	ListConnectorRecords(context.Context, string, connectorsmodule.ConnectorFilter) ([]*connectorsmodule.Connector, error)
	ResolveInboundSecretsRecord(context.Context, string, string) (*connectorsmodule.InboundSecrets, error)
}

// Verifier resolves route ownership through Automation and signing material
// exclusively through Connectors. Verify returns only success or a uniform
// denial; plaintext secret material never leaves this adapter.
type Verifier struct {
	bindings   bindingRouteQueries
	connectors connectorSecretSource
	adapters   registry
	now        func() time.Time
}

var _ webhookingestion.Verifier = (*Verifier)(nil)

func NewVerifier(config VerifierConfig) *Verifier {
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &Verifier{
		bindings: config.Bindings, connectors: config.Connectors,
		adapters: defaultRegistry(), now: now,
	}
}

// Verify resolves the exact enabled route binding, finds the eligible secret
// candidates, and compares the presented signature in constant time through
// the selected source adapter. Missing/disabled routes, bad signatures, and
// secret-resolution failures deliberately share one 401 response.
func (v *Verifier) Verify(ctx context.Context, request webhookingestion.VerificationRequest) error {
	if v == nil || v.bindings == nil {
		return errInboundUnverified()
	}
	adapter, ok := v.adapters[request.SourceKind]
	if !ok {
		return errInboundUnverified()
	}
	enabled := true
	bindings, err := v.bindings.ListBindings(ctx, request.WorkspaceKey, automation.BindingFilter{
		RouteKey: request.RouteKey,
		Enabled:  &enabled,
		Limit:    2,
	})
	if err != nil || len(bindings) != 1 || bindings[0] == nil || bindings[0].SourceKind != request.SourceKind {
		return errInboundUnverified()
	}
	binding := bindings[0]

	candidates, err := v.connectorSecretCandidates(ctx, request.WorkspaceKey, binding.SourceKind, v.now().UTC())
	if err != nil {
		slog.Error("webhook inbound secret resolution failed",
			"workspace", request.WorkspaceKey, "binding_id", binding.BindingID, "source_kind", binding.SourceKind, "err", err)
		return errInboundUnverified()
	}
	for _, cand := range candidates {
		if adapter.VerifySignature(request.Payload, request.PresentedSignature, cand.secret) != nil {
			continue
		}
		if cand.stale {
			// Stale-secret audit signal (locked decision): the sender is
			// still signing with the pre-rotation secret. Deliveries stop
			// verifying when the rotation window closes.
			slog.Warn("webhook verified with previous (stale) connector inbound secret",
				"audit", "connector_stale_inbound_secret",
				"workspace", request.WorkspaceKey, "binding_id", binding.BindingID, "connector_id", cand.connectorID)
		}
		return nil
	}
	return errInboundUnverified()
}

// connectorSecretCandidates resolves the inbound secrets of every active
// connector for the source kind. A missing connector, missing secret, or
// unsupported source kind yields no candidates and therefore fails closed.
func (v *Verifier) connectorSecretCandidates(ctx context.Context, ws, sourceKind string, now time.Time) ([]inboundSecretCandidate, error) {
	kind := connectorsmodule.ConnectorSourceKind(sourceKind)
	if !kind.Valid() {
		return nil, nil
	}
	cs := v.connectors
	if cs == nil {
		return nil, nil
	}
	conns, err := cs.ListConnectorRecords(ctx, ws, connectorsmodule.ConnectorFilter{
		SourceKind: kind, Status: connectorsmodule.ConnectorStatusActive,
	})
	if err != nil {
		return nil, err
	}
	candidates := make([]inboundSecretCandidate, 0, len(conns)*2)
	for _, conn := range conns {
		secrets, err := cs.ResolveInboundSecretsRecord(ctx, ws, conn.ConnectorID)
		if err != nil {
			if errors.Is(err, connectorsmodule.ErrNotFound) {
				continue
			}
			return nil, err
		}
		if secrets == nil {
			return nil, errors.New("connector inbound secret resolution returned no result")
		}
		if secrets.Current != "" {
			candidates = append(candidates, inboundSecretCandidate{secret: secrets.Current, connectorID: conn.ConnectorID})
		}
		// Defensive re-check of the rotation window even though stores
		// already blank Previous outside it: a previous secret with no
		// usable window fails closed.
		if secrets.Previous != "" && !secrets.PreviousValidUntil.IsZero() && now.Before(secrets.PreviousValidUntil) {
			candidates = append(candidates, inboundSecretCandidate{secret: secrets.Previous, connectorID: conn.ConnectorID, stale: true})
		}
	}
	return candidates, nil
}
