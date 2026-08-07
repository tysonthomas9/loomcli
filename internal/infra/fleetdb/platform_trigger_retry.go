// platform_trigger_retry.go implements the retry-sweeper surface of
// store.TriggerDeliveryStore against fleet-db's platform v1 routes (branch
// feat/trigger-supersede):
//
//	GET  /api/v1/{ws}/trigger-deliveries/due
//	POST /api/v1/{ws}/trigger-deliveries/{delivery_id}/result
//
// fleet-db's models.TriggerDelivery JSON shape is snake_case and matches
// automation.Delivery's tags field-for-field, so responses decode
// directly into the domain struct — same as the Get/List methods in
// platform.go. The terminal-failure rule (failed at the binding's retry
// budget forces error_class retries_exhausted) is enforced server-side; the
// client only reports outcomes.
package fleetdb

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	"github.com/tysonthomas9/loomcli/internal/store"
)

// ListDue returns deliveries awaiting the retry sweeper whose due score is
// <= filter.Now, in due order. A zero Now is omitted from the query; the
// server then cuts off at its own current time.
func (s *triggerDeliveryStore) ListDue(ctx context.Context, ws string, filter store.TriggerDeliveryDueFilter) ([]*automation.Delivery, error) {
	q := url.Values{}
	if !filter.Now.IsZero() {
		q.Set("now", filter.Now.UTC().Format(time.RFC3339Nano))
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	path := withQuery("/api/v1/"+pathEscape(ws)+"/trigger-deliveries/due", q)
	var resp struct {
		TriggerDeliveries []*automation.Delivery `json:"trigger_deliveries"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.TriggerDeliveries == nil {
		resp.TriggerDeliveries = []*automation.Delivery{}
	}
	return resp.TriggerDeliveries, nil
}

// UpdateResult records one attempt outcome. next_retry_at is only sent when
// set (mirrors the outbox MarkResult wire); a final delivery transitioning
// to a different status surfaces as domain.ErrInvalidTransition via the 409
// invalid_transition mapping in classifyHTTPError.
func (s *triggerDeliveryStore) UpdateResult(ctx context.Context, ws, deliveryID string, update store.TriggerDeliveryResultUpdate) (*automation.Delivery, error) {
	body := map[string]any{
		"status":        update.Status,
		"attempt":       update.Attempt,
		"error_class":   update.ErrorClass,
		"driver_run_id": update.DriverRunID,
	}
	if update.NextRetryAt != nil {
		body["next_retry_at"] = update.NextRetryAt
	}
	path := "/api/v1/" + pathEscape(ws) + "/trigger-deliveries/" + pathEscape(deliveryID) + "/result"
	var out automation.Delivery
	if err := s.client.do(ctx, "POST", path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
