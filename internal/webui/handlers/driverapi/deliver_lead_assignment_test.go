package driverapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"

	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
)

func listDueOutboxRows(t *testing.T, st *memstore.Store) []*execution.OutboxDelivery {
	t.Helper()
	rows, err := st.Outbox().ListDue(context.Background(), "WS", execution.OutboxDueFilter{
		Now:   time.Now().UTC(),
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("ListDue: %v", err)
	}
	return rows
}

func TestDeliverLeadAssignmentEnqueuesOutboxOnPending(t *testing.T) {
	h := newTestHarness(t)
	h.module.deliverAssignment = func(context.Context, string, string) (driverpkg.AgentMessageDeliveryResult, error) {
		return driverpkg.AgentMessageDeliveryResult{State: "pending", Reason: "lead has no orchestration session"}, nil
	}

	req := opRequest{
		op:      "deliver-lead-assignment",
		body:    map[string]string{"agent": "lead-1"},
		headers: h.ownerHeaders(t),
	}
	resp, decoded := h.do(t, req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %v", resp.StatusCode, decoded)
	}
	// Response shape is the unchanged AgentMessageDeliveryResult.
	if state, _ := decoded["state"].(string); state != "pending" {
		t.Fatalf("state = %q, want pending", state)
	}
	rows := listDueOutboxRows(t, h.store)
	if len(rows) != 1 {
		t.Fatalf("outbox rows = %d, want exactly 1 enqueued on pending", len(rows))
	}
	row := rows[0]
	if row.Kind != execution.OutboxKindLeadAssignment || row.TargetAgent != "lead-1" {
		t.Fatalf("row = %+v, want leadAssignment for lead-1", row)
	}
	if row.DedupeKey != "lead-assignment:"+h.runID+":lead-1" {
		t.Fatalf("dedupe key = %q, want lead-assignment:%s:lead-1", row.DedupeKey, h.runID)
	}
	if row.DriverRunID != h.runID || row.EpicID != "EPIC-1" {
		t.Fatalf("row provenance = %+v, want driver run %q and epic EPIC-1", row, h.runID)
	}

	// Idempotent across calls: the dedupe key collapses repeat enqueues.
	resp, _ = h.do(t, req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("repeat status = %d, want 200", resp.StatusCode)
	}
	if rows := listDueOutboxRows(t, h.store); len(rows) != 1 {
		t.Fatalf("outbox rows after repeat call = %d, want 1", len(rows))
	}
}

func TestDeliverLeadAssignmentSkipsOutboxOnDeliveredAndUnsupported(t *testing.T) {
	cases := []struct {
		name  string
		state string
	}{
		{"delivered", driverpkg.AgentMessageDeliveryStateDelivered},
		{"unsupported", driverpkg.AgentMessageDeliveryStateUnsupported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHarness(t)
			h.module.deliverAssignment = func(context.Context, string, string) (driverpkg.AgentMessageDeliveryResult, error) {
				return driverpkg.AgentMessageDeliveryResult{State: tc.state}, nil
			}
			resp, decoded := h.do(t, opRequest{
				op:      "deliver-lead-assignment",
				body:    map[string]string{"agent": "lead-1"},
				headers: h.ownerHeaders(t),
			})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200: %v", resp.StatusCode, decoded)
			}
			if state, _ := decoded["state"].(string); state != tc.state {
				t.Fatalf("state = %q, want %q", state, tc.state)
			}
			if rows := listDueOutboxRows(t, h.store); len(rows) != 0 {
				t.Fatalf("outbox rows = %d, want 0 for terminal inline delivery", len(rows))
			}
		})
	}
}
