package webhooks

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	"github.com/tysonthomas9/loomcli/internal/app/webhookingestion"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// inboundSecretCandidate is one signing secret the inbound verifier may match,
// tagged with provenance so a previous-secret match can emit the stale-secret
// audit signal.
type inboundSecretCandidate struct {
	secret string
	// connectorID is the owning connector; empty for the per-binding
	// back-compat fallback secret.
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

// CompatibilityVerifierConfig supplies the privileged persistence seams used
// only inside inbound signature verification. Neither resolved secrets nor a
// binding DTO cross into webhookingestion or Automation.
type CompatibilityVerifierConfig struct {
	Bindings   store.TriggerBindingStore
	Connectors store.ConnectorStore
	Now        func() time.Time
}

// CompatibilityVerifier adapts the legacy binding/connector secret stores to
// webhookingestion.Verifier. Verify returns only success or a uniform denial;
// plaintext secret material never leaves this adapter.
type CompatibilityVerifier struct {
	bindings   store.TriggerBindingStore
	connectors store.ConnectorStore
	adapters   registry
	now        func() time.Time
}

var _ webhookingestion.Verifier = (*CompatibilityVerifier)(nil)

func NewCompatibilityVerifier(config CompatibilityVerifierConfig) *CompatibilityVerifier {
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &CompatibilityVerifier{
		bindings: config.Bindings, connectors: config.Connectors,
		adapters: defaultRegistry(), now: now,
	}
}

// Verify resolves the exact enabled route binding, finds the eligible secret
// candidates, and compares the presented signature in constant time through
// the selected source adapter. Missing/disabled routes, bad signatures, and
// secret-resolution failures deliberately share one 401 response.
func (v *CompatibilityVerifier) Verify(ctx context.Context, request webhookingestion.VerificationRequest) error {
	if v == nil || v.bindings == nil {
		return errInboundUnverified()
	}
	adapter, ok := v.adapters[request.SourceKind]
	if !ok {
		return errInboundUnverified()
	}
	binding, err := v.bindings.GetByRouteKey(ctx, request.WorkspaceKey, request.RouteKey)
	if err != nil || binding == nil || !binding.Enabled || binding.SourceKind != request.SourceKind {
		return errInboundUnverified()
	}

	candidates, err := v.resolveInboundSecretCandidates(ctx, request.WorkspaceKey, binding, v.now().UTC())
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

// resolveInboundSecretCandidates returns the ordered secrets to verify the
// request against. Per-source connectors are the verification root once they
// exist (M1 contract — one rotation point per source): every active connector
// matching the binding's source kind contributes its current inbound secret,
// plus its previous secret while now < PreviousSecretValidUntil. Only when NO
// connector exists for the source kind does resolution fall back to the
// exact-RouteKey binding's webhook secret (back-compat — no flag day). A
// connector that exists but yields no usable secret does NOT fall back:
// verification fails closed.
func (v *CompatibilityVerifier) resolveInboundSecretCandidates(ctx context.Context, ws string, binding *automation.Binding, now time.Time) ([]inboundSecretCandidate, error) {
	candidates, connectorFound, err := v.connectorSecretCandidates(ctx, ws, binding.SourceKind, now)
	if err != nil {
		return nil, err
	}
	if connectorFound {
		return candidates, nil
	}
	secret, err := v.bindings.ResolveWebhookSecret(ctx, ws, binding.BindingID)
	if err != nil {
		return nil, err
	}
	return []inboundSecretCandidate{{secret: secret}}, nil
}

// connectorSecretCandidates resolves the inbound secrets of every active
// connector for the source kind. found reports whether any connector
// actually resolved — false means the workspace has no connector for this
// source (or the store has no connector wiring at all) and the caller may
// use the back-compat binding-secret path.
func (v *CompatibilityVerifier) connectorSecretCandidates(ctx context.Context, ws, sourceKind string, now time.Time) (candidates []inboundSecretCandidate, found bool, err error) {
	kind := domain.ConnectorSourceKind(sourceKind)
	if !kind.Valid() {
		// Source kinds outside the connector enum (e.g. cron) never have
		// connectors.
		return nil, false, nil
	}
	cs := v.connectors
	if cs == nil {
		return nil, false, nil
	}
	conns, err := cs.List(ctx, ws, store.ConnectorFilter{SourceKind: kind, Status: domain.ConnectorStatusActive})
	if err != nil {
		if errors.Is(err, errors.ErrUnsupported) {
			// Store without connector persistence (fail-closed placeholder):
			// keep the pre-connector binding-secret path working.
			return nil, false, nil
		}
		return nil, false, err
	}
	for _, conn := range conns {
		secrets, err := cs.ResolveInboundSecret(ctx, ws, conn.ConnectorID)
		if err != nil {
			if errors.Is(err, domain.ErrConnectorNotFound) {
				// Deleted between List and Resolve: treat as absent.
				continue
			}
			return nil, true, err
		}
		if secrets == nil {
			return nil, true, errors.New("connector inbound secret resolution returned no result")
		}
		found = true
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
	return candidates, found, nil
}
