package webhooks

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

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

// verifyInboundSignature resolves the inbound signing secret(s) for the
// binding and verifies the request signature against each candidate (the
// adapter compares in constant time). Secret-resolution failures are logged
// server-side and fail closed with the same uniform 401 as a bad signature.
// A match against a rotation-window previous secret emits the stale-secret
// audit signal before returning success.
func (m *Module) verifyInboundSignature(r *http.Request, adapter Adapter, ws string, binding *domain.TriggerBinding, body []byte) error {
	candidates, err := m.resolveInboundSecretCandidates(r.Context(), ws, binding, time.Now().UTC())
	if err != nil {
		slog.Error("webhook inbound secret resolution failed",
			"workspace", ws, "binding_id", binding.BindingID, "source_kind", binding.SourceKind, "err", err)
		return errInboundUnverified()
	}
	for _, cand := range candidates {
		if adapter.Verify(r, body, cand.secret) != nil {
			continue
		}
		if cand.stale {
			// Stale-secret audit signal (locked decision): the sender is
			// still signing with the pre-rotation secret. Deliveries stop
			// verifying when the rotation window closes.
			slog.Warn("webhook verified with previous (stale) connector inbound secret",
				"audit", "connector_stale_inbound_secret",
				"workspace", ws, "binding_id", binding.BindingID, "connector_id", cand.connectorID)
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
func (m *Module) resolveInboundSecretCandidates(ctx context.Context, ws string, binding *domain.TriggerBinding, now time.Time) ([]inboundSecretCandidate, error) {
	candidates, connectorFound, err := m.connectorSecretCandidates(ctx, ws, binding.SourceKind, now)
	if err != nil {
		return nil, err
	}
	if connectorFound {
		return candidates, nil
	}
	secret, err := m.store.TriggerBindings().ResolveWebhookSecret(ctx, ws, binding.BindingID)
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
func (m *Module) connectorSecretCandidates(ctx context.Context, ws, sourceKind string, now time.Time) (candidates []inboundSecretCandidate, found bool, err error) {
	kind := domain.ConnectorSourceKind(sourceKind)
	if !kind.Valid() {
		// Source kinds outside the connector enum (e.g. cron) never have
		// connectors.
		return nil, false, nil
	}
	cs := m.store.Connectors()
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
