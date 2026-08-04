package connector

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/connector/providers"
	"github.com/tysonthomas9/loomcli/internal/domain"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// This file is the single enforcement choke point for invariant §9.5: every
// outbound connector call flows through Dispatcher.Dispatch, which resolves
// the named connector, evaluates grants (deny-by-default), refuses
// irreversible actions lacking freshness preconditions, unseals the
// credential just-in-time, invokes the provider, and appends exactly one
// ConnectorCallRecord for BOTH granted and refused outcomes.
//
// Placement rules baked in here:
//
//   - Refusal audits are appended BEFORE the refusal error is returned, so a
//     denied probe is always journaled even when the caller drops the error.
//   - Grants and connector state are re-resolved from the stores on EVERY
//     call — never cached across retries — so a grant revoked mid-run denies
//     the very next call.
//   - The plaintext credential exists only inside Dispatch: the byte slice is
//     defer-zeroed, and the string copy handed to the provider lives on the
//     provider call's stack (placed in the Authorization header only).

// Dispatch sentinel errors. Both wrap domain.ErrInvalid; the refusals are
// additionally journaled with decision "denied" before they are returned.
var (
	// ErrConnectorDisabled indicates the resolved connector exists but is
	// not active; egress through a disabled connector is refused.
	ErrConnectorDisabled = fmt.Errorf("connector: connector disabled: %w", domain.ErrInvalid)

	// ErrNoOutboundCredential indicates the connector has no sealed outbound
	// credential, so there is nothing to authenticate egress with.
	ErrNoOutboundCredential = fmt.Errorf("connector: no outbound credential: %w", domain.ErrInvalid)
)

// maxSummaryLen caps audit summaries defensively; provider errors are already
// sanitized and capped upstream of this layer.
const maxSummaryLen = 240

// Request describes one egress call. Per the cardinality decision a
// workspace holds multiple named connectors per source kind, so the request
// references its connector explicitly by ConnectorID (the source kind is
// derived from the resolved connector record, never trusted from callers).
type Request = connectorsmodule.DispatchCommand

// Result is the sanitized dispatch outcome. It is populated as far as the
// flow progressed even when an error is returned, so callers can surface the
// decision without re-deriving it.
type Result = connectorsmodule.DispatchResult

// Dispatcher wires the connector stores, the vault sealer, and the provider
// registry into the egress choke point. All fields are required except Now,
// which defaults to time.Now and exists for deterministic tests.
type Dispatcher struct {
	Connectors store.ConnectorStore
	Grants     store.ConnectorGrantStore
	Audit      store.ConnectorAuditStore
	// Vault unseals the connector's outbound credential just-in-time. It is
	// the Sealer interface (not the concrete *Vault) so a KMS-backed sealer
	// can replace the default AES-256-GCM implementation.
	Vault     Sealer
	Providers *providers.Registry
	Now       func() time.Time
}

func (d *Dispatcher) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// Dispatch performs one connector egress call end to end:
//
//  1. resolve the named connector (NotFound and disabled fail closed),
//  2. load the binding's active grants for THIS connector and Evaluate —
//     a deny is journaled with decision "denied" before it returns,
//  3. refuse irreversible actions missing their registered precondition
//     field with decision "precondition_required", journaled,
//  4. resolve + unseal the outbound credential (AAD-bound to the workspace
//     and connector identity; plaintext never escapes this call),
//  5. invoke the provider with IdempotencyKey = runID#action#callSeq,
//  6. append the audit record with the decision, upstream status, and
//     sanitized summary; a duplicate CallID append is treated as success so
//     retries under the same key stay idempotent.
//
// Grants and connector state are re-read from the stores on every invocation;
// nothing is cached across retries.
func (d *Dispatcher) Dispatch(ctx context.Context, req Request) (Result, error) {
	if err := req.Validate(); err != nil {
		return Result{}, errors.Join(domain.ErrInvalid, err)
	}
	res := Result{CallID: domain.ConnectorCallID(req.RunID, req.Action, req.CallSeq)}

	// (1) Resolve the connector by its explicit connector_id. Get returns a
	// redacted copy — only identity, kind, and status are needed here.
	conn, err := d.Connectors.Get(ctx, req.WorkspaceKey, req.ConnectorID)
	if err != nil {
		return res, fmt.Errorf("connector dispatch resolve %q: %w", req.ConnectorID, err)
	}
	kind := conn.SourceKind
	if conn.Status != domain.ConnectorStatusActive {
		derr := fmt.Errorf("connector %q status %q: %w", req.ConnectorID, conn.Status, ErrConnectorDisabled)
		return d.refuse(ctx, req, res, kind, domain.ConnectorCallDenied, derr)
	}

	// (2)+(3) Deny-by-default grant evaluation, then the irreversible-action
	// precondition gate — both refuse before any egress or credential access.
	if refusal, rerr := d.authorizeCall(ctx, req, res, kind); rerr != nil || refusal != nil {
		if refusal != nil {
			return *refusal, rerr
		}
		return res, rerr
	}

	// Resolve the provider before touching the credential: a missing adapter
	// refuses the call without ever unsealing anything.
	prov, err := d.Providers.Get(kind)
	if err != nil {
		derr := fmt.Errorf("connector dispatch provider for %q: %w", kind, err)
		return d.refuse(ctx, req, res, kind, domain.ConnectorCallDenied, derr)
	}

	// (4) Just-in-time unseal. The plaintext slice is zeroed on the way out;
	// the string copy below lives only on this call's stack and the
	// provider's Authorization header.
	plaintext, refusal, err := d.unsealCredential(ctx, req, res, kind)
	if err != nil || refusal != nil {
		if refusal != nil {
			return *refusal, err
		}
		return res, err
	}
	defer zeroBytes(plaintext)

	// (5) Provider call. The deterministic CallID is the run-level fencing
	// key: a task retry re-dispatches under the same Idempotency-Key.
	callRes, callErr := prov.Call(ctx, providers.CallSpec{
		Action:         req.Action,
		Resource:       req.Resource,
		Args:           req.Args,
		Preconditions:  req.Preconditions,
		IdempotencyKey: res.CallID,
		Credential:     string(plaintext),
	})

	return d.journalOutcome(ctx, req, res, kind, callRes, callErr)
}

// authorizeCall is Dispatch steps (2) and (3): deny-by-default grant
// evaluation (re-resolved on every call — only grants issued against THIS
// connector count; a grant on another named connector of the same kind never
// authorizes egress here), then the registered freshness-field gate for
// irreversible actions. A non-nil refusal is the journaled refusal Result to
// return alongside the error.
func (d *Dispatcher) authorizeCall(ctx context.Context, req Request, res Result, kind domain.ConnectorSourceKind) (*Result, error) {
	grants, err := d.Grants.ListByBinding(ctx, req.WorkspaceKey, req.BindingID)
	if err != nil {
		return nil, fmt.Errorf("connector dispatch grants for binding %q: %w", req.BindingID, err)
	}
	scoped := make([]*domain.ConnectorGrant, 0, len(grants))
	for _, g := range grants {
		if g != nil && g.ConnectorID == req.ConnectorID {
			scoped = append(scoped, g)
		}
	}
	if decision := Evaluate(req.BindingID, scoped, req.Action, req.Resource); !decision.Allowed {
		refused, rerr := d.refuse(ctx, req, res, kind, domain.ConnectorCallDenied, decision.Denied)
		return &refused, rerr
	}
	if missing := missingPreconditions(req.Action, req.Preconditions); len(missing) > 0 {
		perr := &providers.PreconditionRequired{Action: req.Action, Fields: missing}
		refused, rerr := d.refuse(ctx, req, res, kind, domain.ConnectorCallPreconditionRequired, perr)
		return &refused, rerr
	}
	return nil, nil
}

// unsealCredential resolves and unseals the connector's outbound credential
// just-in-time for Dispatch step (4). A non-nil refusal is the journaled
// deny Result to return alongside err; the caller owns zeroing plaintext.
func (d *Dispatcher) unsealCredential(ctx context.Context, req Request, res Result, kind domain.ConnectorSourceKind) (plaintext []byte, refusal *Result, err error) {
	sealed, err := d.Connectors.ResolveOutboundCredentialSealed(ctx, req.WorkspaceKey, req.ConnectorID)
	if err != nil {
		return nil, nil, fmt.Errorf("connector dispatch sealed credential for %q: %w", req.ConnectorID, err)
	}
	if len(sealed) == 0 {
		derr := fmt.Errorf("connector %q: %w", req.ConnectorID, ErrNoOutboundCredential)
		refused, rerr := d.refuse(ctx, req, res, kind, domain.ConnectorCallDenied, derr)
		return nil, &refused, rerr
	}
	plaintext, err = d.Vault.Unseal(sealed, CredentialAAD(req.WorkspaceKey, req.ConnectorID))
	if err != nil {
		derr := fmt.Errorf("connector %q: %w", req.ConnectorID, err)
		refused, rerr := d.refuse(ctx, req, res, kind, domain.ConnectorCallDenied, derr)
		return nil, &refused, rerr
	}
	return plaintext, nil, nil
}

// journalOutcome is Dispatch step (6): it normalizes the provider decision,
// appends the audit record (duplicate CallID appends are success), and
// returns the provider error joined with any audit failure.
func (d *Dispatcher) journalOutcome(ctx context.Context, req Request, res Result, kind domain.ConnectorSourceKind, callRes providers.CallResult, callErr error) (Result, error) {
	// Providers populate CallResult.Decision even on error; fall back to the
	// error mapping defensively.
	decision := callRes.Decision
	if !decision.Valid() {
		if callErr != nil {
			decision = providers.DecisionForError(callErr)
		} else {
			decision = domain.ConnectorCallGranted
		}
	}
	res.Decision = connectorsmodule.ConnectorCallDecision(decision)
	res.Status = callRes.Status
	res.Body = callRes.Body
	summary := ""
	if callErr != nil {
		summary = callErr.Error() // provider errors are pre-sanitized
	}
	if aerr := d.appendAudit(ctx, req, kind, decision, callRes.Status, errorClass(callErr), summary); aerr != nil {
		if callErr != nil {
			return res, errors.Join(callErr, aerr)
		}
		return res, aerr
	}
	return res, callErr
}

// refuse journals a pre-egress refusal and then returns cause. The audit
// append happens BEFORE the return so a denied probe is always recorded; an
// audit failure is joined onto the refusal rather than masking it.
func (d *Dispatcher) refuse(ctx context.Context, req Request, res Result, kind domain.ConnectorSourceKind, decision domain.ConnectorCallDecision, cause error) (Result, error) {
	res.Decision = connectorsmodule.ConnectorCallDecision(decision)
	if aerr := d.appendAudit(ctx, req, kind, decision, 0, "", cause.Error()); aerr != nil {
		return res, errors.Join(cause, aerr)
	}
	return res, cause
}

// appendAudit appends one ConnectorCallRecord. A duplicate CallID means a
// retry already journaled this exact call, so domain.ErrAlreadyExists is
// treated as success — the deterministic id keeps the journal append-once.
func (d *Dispatcher) appendAudit(ctx context.Context, req Request, kind domain.ConnectorSourceKind, decision domain.ConnectorCallDecision, status int, errClass, summary string) error {
	rec := &domain.ConnectorCallRecord{
		WorkspaceKey:     req.WorkspaceKey,
		CallID:           domain.ConnectorCallID(req.RunID, req.Action, req.CallSeq),
		Seq:              req.CallSeq,
		RunID:            req.RunID,
		BindingID:        req.BindingID,
		ConnectorID:      req.ConnectorID,
		SourceKind:       kind,
		Action:           req.Action,
		Resource:         req.Resource,
		Decision:         decision,
		UpstreamStatus:   status,
		ErrorClass:       errClass,
		SanitizedSummary: capSummary(summary),
		OccurredAt:       d.now().UTC(),
	}
	if err := d.Audit.Append(ctx, rec); err != nil && !errors.Is(err, domain.ErrAlreadyExists) {
		return fmt.Errorf("connector dispatch audit %q: %w", rec.CallID, err)
	}
	return nil
}

// missingPreconditions returns the registered precondition fields the request
// failed to supply for an irreversible action; empty for reversible actions
// and for fully pinned irreversible ones.
func missingPreconditions(action string, p providers.Preconditions) []string {
	var missing []string
	for _, field := range RequiredPreconditions(action) {
		if preconditionValue(field, p) == "" {
			missing = append(missing, field)
		}
	}
	return missing
}

// preconditionValue maps a registry field name (camelCase, per wire
// convention) to its value on the request. Unknown field names report empty
// so an unmapped registry entry fails closed rather than slipping through.
func preconditionValue(field string, p providers.Preconditions) string {
	switch field {
	case "expectedHeadSha":
		return p.ExpectedHeadSha
	case "expectedIssueRevision", "expectedMessageTs", "expectedMonitorRevision":
		return p.ExpectedRevision
	default:
		return ""
	}
}

// errorClass extracts the audit ErrorClass from a provider error: the
// upstream class for UpstreamError, "rate_limited" for RateLimited, empty
// otherwise (refusals carry their reason in the summary instead).
func errorClass(err error) string {
	var up *providers.UpstreamError
	if errors.As(err, &up) {
		return up.Class
	}
	var rl *providers.RateLimited
	if errors.As(err, &rl) {
		return "rate_limited"
	}
	return ""
}

// capSummary length-caps audit summaries defensively.
func capSummary(s string) string {
	if len(s) > maxSummaryLen {
		return s[:maxSummaryLen] + "..."
	}
	return s
}

// zeroBytes wipes a plaintext credential slice before it is garbage
// collected. Best effort: the string copy handed to the provider cannot be
// zeroed, which is the accepted Go trade-off documented on CallSpec.
func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
