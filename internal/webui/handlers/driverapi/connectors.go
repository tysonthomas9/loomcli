// Connector egress on the run-scoped driver-op surface (CV9).
//
// The connector-dispatch op lets a running workflow perform an outbound
// connector call while holding ONLY its run token: authentication is the
// same parent-DriverRun header verification every driver op uses
// (verifyParent → fenced heartbeat), and the BindingID used for grant lookup
// is resolved server-side from the verified run's provenance — never from
// the request body — so a workflow can never claim another binding's grants.
// Provider credentials stay inside connector.Dispatcher.Dispatch and never
// appear on this wire in either direction.
//
// Wire shapes are camelCase per the driver-op (SDK v2) convention; refusal
// and upstream failures map onto the structured error envelope with the
// connector-specific codes grant_denied, precondition_required,
// stale_subject, rate_limited and upstream_error.
package driverapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// errConnectorEgressUnavailable is returned when the module has no
// Dispatcher: the server was started without a usable
// LOOM_CONNECTOR_VAULT_KEY, so connector egress fails closed.
var errConnectorEgressUnavailable = errors.New(
	"connector egress is not configured on this server (set " + connectorsmodule.VaultKeyEnvVar + ")")

// connectorDispatchParams is the camelCase connector-dispatch request body.
// BindingID is deliberately absent: it is resolved from the verified parent
// run server-side.
type connectorDispatchParams struct {
	// ConnectorID names the workspace connector to call through (a
	// workspace may hold several named connectors per source kind).
	ConnectorID string `json:"connectorId"`
	// Action is the dotted connector action, e.g. "github.merge".
	Action string `json:"action"`
	// Resource is the grant-resource identifier, e.g. "repo:octocat/hello".
	Resource string `json:"resource"`
	// Args holds the camelCase provider arguments.
	Args map[string]any `json:"args"`
	// Preconditions carries the freshness assertions; irreversible actions
	// are refused with precondition_required when their registered field is
	// missing.
	Preconditions struct {
		ExpectedHeadSha         string `json:"expectedHeadSha"`
		ExpectedIssueRevision   string `json:"expectedIssueRevision"`
		ExpectedMessageTs       string `json:"expectedMessageTs"`
		ExpectedMonitorRevision string `json:"expectedMonitorRevision"`
	} `json:"preconditions"`
	// CallSeq is the run-scoped call sequence number; with the run id and
	// action it derives the deterministic call/idempotency id.
	CallSeq int `json:"callSeq"`
}

// preconditions maps the camelCase wire fields onto the provider-layer
// Preconditions pair (ExpectedHeadSha for git subjects, ExpectedRevision for
// everything else).
func (p connectorDispatchParams) preconditions() connectorsmodule.DispatchPreconditions {
	return connectorsmodule.DispatchPreconditions{
		ExpectedHeadSha: strings.TrimSpace(p.Preconditions.ExpectedHeadSha),
		ExpectedRevision: firstNonEmpty(
			p.Preconditions.ExpectedIssueRevision,
			p.Preconditions.ExpectedMessageTs,
			p.Preconditions.ExpectedMonitorRevision,
		),
	}
}

// connectorDispatchResult is the camelCase success response: the audited
// decision, the upstream status and the provider's sanitized result subset.
type connectorDispatchResult struct {
	CallID   string         `json:"callId"`
	Decision string         `json:"decision"`
	Status   int            `json:"status,omitempty"`
	Body     map[string]any `json:"body,omitempty"`
}

// connectorDispatch is the "connector-dispatch" op handler. Flow: verify the
// parent run owns its lease, resolve the run's trigger binding server-side,
// then hand off to the egress choke point (connector.Dispatcher.Dispatch),
// which evaluates grants deny-by-default, enforces freshness preconditions,
// unseals the credential just-in-time and journals exactly one
// ConnectorCallRecord for granted AND refused outcomes.
func (m *Module) connectorDispatch(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[connectorDispatchParams](body)
	if err != nil {
		return nil, err
	}
	if m.dispatcher == nil {
		return nil, errConnectorEgressUnavailable
	}
	parent, err := m.verifyParent(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	bindingID, err := m.resolveParentBindingID(ctx, ws, parent)
	if err != nil {
		return nil, err
	}
	res, err := m.dispatcher.Dispatch(ctx, connectorsmodule.DispatchCommand{
		WorkspaceKey:  ws,
		RunID:         parent.RunID,
		BindingID:     bindingID,
		ConnectorID:   strings.TrimSpace(params.ConnectorID),
		Action:        strings.TrimSpace(params.Action),
		Resource:      strings.TrimSpace(params.Resource),
		Args:          params.Args,
		Preconditions: params.preconditions(),
		CallSeq:       params.CallSeq,
	})
	if err != nil {
		return nil, err
	}
	return connectorDispatchResult{
		CallID:   res.CallID,
		Decision: string(res.Decision),
		Status:   res.Status,
		Body:     res.Body,
	}, nil
}

// resolveParentBindingID derives the grant-lookup BindingID from the verified
// parent run's server-side provenance (via lookupParentBindingID), applying the
// connector-egress deny-by-default policy: a run with no binding lineage has no
// grants, so it is refused before any store or provider work happens. This
// refusal cannot be journaled: without a binding there is no valid
// ConnectorCallRecord.
func (m *Module) resolveParentBindingID(ctx context.Context, ws string, parent *execution.DriverRun) (string, error) {
	bindingID, err := m.lookupParentBindingID(ctx, ws, parent)
	if errors.Is(err, domain.ErrNotFound) {
		return "", fmt.Errorf(
			"driver run %q has no trigger binding; connector egress is deny-by-default: %w",
			parent.RunID,
			connectorsmodule.ErrGrantDenied,
		)
	}
	if err != nil {
		return "", err
	}
	return bindingID, nil
}

// lookupParentBindingID resolves the trigger binding a VERIFIED parent run was
// dispatched by, from server-side provenance ONLY — never from request input
// (the same confused-deputy class the actor-lock fix closed). Precedence:
//
//  0. the run's own TriggerBindingID, stamped by fleet-db's trigger-dispatch leg
//     (and by the binding-scoped run-now endpoint) — the direct, canonical source
//     for runs the fleet-db decode now carries;
//  1. else its TriggerEvent id in SourceRef → the TriggerDelivery that admitted
//     the run names the binding;
//  2. else a route-key-sourced run (epic runs through epics.runs.create, or a
//     binding-scoped run-now that stamps the binding's route key) carries the
//     binding's route key in SourceRef.
//
// Returns domain.ErrNotFound (wrapped) when the run has no binding lineage;
// callers map that onto their own policy — connector egress denies-by-default,
// binding.config surfaces a clean not_found.
func (m *Module) lookupParentBindingID(ctx context.Context, ws string, parent *execution.DriverRun) (string, error) {
	if id := strings.TrimSpace(parent.TriggerBindingID); id != "" {
		return id, nil
	}
	sourceRef := strings.TrimSpace(parent.SourceRef)
	if sourceRef == "" {
		return "", fmt.Errorf("driver run %q has no binding provenance: %w", parent.RunID, domain.ErrNotFound)
	}
	deliveries, err := m.store.TriggerDeliveries().List(ctx, ws, store.TriggerDeliveryFilter{TriggerEventID: sourceRef})
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return "", fmt.Errorf("resolve trigger delivery for driver run %q: %w", parent.RunID, err)
	}
	for _, delivery := range deliveries {
		if delivery != nil && delivery.DriverRunID == parent.RunID && delivery.TriggerBindingID != "" {
			return delivery.TriggerBindingID, nil
		}
	}
	binding, err := m.store.TriggerBindings().GetByRouteKey(ctx, ws, sourceRef)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return "", fmt.Errorf("driver run %q source ref %q resolves to no binding: %w", parent.RunID, sourceRef, domain.ErrNotFound)
		}
		return "", fmt.Errorf("resolve trigger binding for driver run %q: %w", parent.RunID, err)
	}
	return binding.BindingID, nil
}

// writeConnectorOpError maps connector-specific dispatch errors onto the
// structured error envelope, reporting whether it handled err. It runs ahead
// of the generic domain mapping in writeDomainOpError; non-connector errors
// fall through untouched. Error strings reaching this point are already
// sanitized (providers strip credential material before constructing
// errors), so messages are safe to echo.
func writeConnectorOpError(w http.ResponseWriter, err error) bool {
	if errors.Is(err, errConnectorEgressUnavailable) {
		writeOpError(w, http.StatusServiceUnavailable, "unavailable", err.Error(), false)
		return true
	}
	failure, ok := connectorsmodule.ClassifyDispatchError(err)
	if !ok {
		return false
	}
	switch failure.Kind {
	case connectorsmodule.DispatchFailureGrantDenied:
		writeOpError(w, http.StatusForbidden, string(failure.Kind), err.Error(), false)
	case connectorsmodule.DispatchFailurePreconditionRequired:
		writeOpError(w, http.StatusBadRequest, "precondition_required", err.Error(), false)
	case connectorsmodule.DispatchFailureStaleSubject:
		writeOpError(w, http.StatusConflict, "stale_subject", err.Error(), false)
	case connectorsmodule.DispatchFailureRateLimited:
		writeOpError(w, http.StatusTooManyRequests, "rate_limited", err.Error(), true)
	case connectorsmodule.DispatchFailureUpstream:
		writeOpError(w, http.StatusBadGateway, "upstream_error", err.Error(), failure.Retryable)
	default:
		return false
	}
	return true
}
