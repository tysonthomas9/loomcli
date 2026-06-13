package fleetdb

import (
	"context"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type triggerRouteStore struct{ client *Client }

var _ store.TriggerRouteDispatcher = (*triggerRouteStore)(nil)

// DispatchTriggerRoute is the legacy single-run lane over the router-v2 wire:
// it dispatches via DispatchTriggerRouteV2 and then fetches the primary leg's
// run, so existing webhook callers keep receiving the admitted run.
func (s *triggerRouteStore) DispatchTriggerRoute(ctx context.Context, ws, routeKey string, in store.TriggerRouteDispatch) (*domain.DriverRun, error) {
	result, err := s.DispatchTriggerRouteV2(ctx, ws, routeKey, in)
	if err != nil {
		return nil, err
	}
	if len(result.Deliveries) == 0 {
		return nil, fmt.Errorf("trigger route %q in workspace %q dispatched no deliveries: %w", routeKey, ws, domain.ErrNotFound)
	}
	var run domain.DriverRun
	runPath := "/api/v1/" + pathEscape(ws) + "/driver-runs/" + pathEscape(result.Deliveries[0].RunID)
	if err := s.client.do(ctx, "GET", runPath, nil, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

// DispatchTriggerRouteV2 posts the trigger-routes endpoint and decodes the
// BREAKING router-v2 fan-out wire: 201 {"deliveries":[...]} with NO top-level
// driver_run_id. PrimaryRun stays nil — the wire no longer returns run bodies;
// callers needing the run fetch it by Deliveries[0].RunID. SubjectAttrs is
// deliberately not sent: fleet-db's strict decoder rejects unknown fields and
// the server-side subject-key templating lane lands with a later chunk.
func (s *triggerRouteStore) DispatchTriggerRouteV2(ctx context.Context, ws, routeKey string, in store.TriggerRouteDispatch) (*store.TriggerRouteDispatchResult, error) {
	body := map[string]any{
		"run_id":             in.RunID,
		"idempotency_key":    in.IdempotencyKey,
		"source_event_id":    in.SourceEventID,
		"event_type":         in.EventType,
		"subject_ref":        in.SubjectRef,
		"actor_ref":          in.ActorRef,
		"epic_id":            in.EpicID,
		"raw_payload_ref":    in.RawPayloadRef,
		"raw_payload_digest": in.RawPayloadDigest,
		"signature_status":   in.SignatureStatus,
		"replay_of_event_id": in.ReplayOfEventID,
		"payload":            in.Payload,
	}
	headers := map[string]string{}
	if in.IdempotencyKey != "" {
		headers["Idempotency-Key"] = in.IdempotencyKey
	}
	var resp struct {
		Deliveries []store.TriggerRouteDelivery `json:"deliveries"`
	}
	path := "/api/v1/" + pathEscape(ws) + "/trigger-routes/" + pathEscape(routeKey)
	if err := s.client.doWithHeaders(ctx, "POST", path, body, &resp, headers); err != nil {
		return nil, err
	}
	return &store.TriggerRouteDispatchResult{Deliveries: resp.Deliveries}, nil
}
