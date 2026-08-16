package memstore

import (
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"

	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

func taskRunEventAppend(taskRunID string, attempt int, eventType execution.TaskRunEventType) execution.TaskRunEventAppend {
	return execution.TaskRunEventAppend{
		WorkspaceKey: "WS",
		EpicID:       "epic-1",
		DriverRunID:  "run-1",
		TaskID:       "task-1",
		TaskRunID:    taskRunID,
		Type:         eventType,
		Attempt:      attempt,
		OccurredAt:   time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	}
}

func TestTaskRunEventAppendIdempotency(t *testing.T) {
	ctx := t.Context()
	s := New()

	first, err := s.TaskRunEvents().Append(ctx, taskRunEventAppend("tr-1", 1, execution.TaskRunEventQueued))
	if err != nil {
		t.Fatalf("first append: %v", err)
	}
	wantEventID := execution.TaskRunEventID("tr-1", 1, execution.TaskRunEventQueued)
	if first.EventID != wantEventID {
		t.Fatalf("derived EventID = %q, want %q", first.EventID, wantEventID)
	}
	if first.Seq != 1 {
		t.Fatalf("first Seq = %d, want 1", first.Seq)
	}

	second, err := s.TaskRunEvents().Append(ctx, taskRunEventAppend("tr-1", 1, execution.TaskRunEventQueued))
	if err != nil {
		t.Fatalf("duplicate append: %v", err)
	}
	if second.Seq != first.Seq || second.EventID != first.EventID {
		t.Fatalf("duplicate append returned (%q, seq %d), want (%q, seq %d)",
			second.EventID, second.Seq, first.EventID, first.Seq)
	}

	events, err := s.TaskRunEvents().ListSince(ctx, "WS", execution.TaskRunEventFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("journal has %d events after duplicate append, want 1", len(events))
	}
}

func TestTaskRunEventListSince(t *testing.T) {
	ctx := t.Context()
	s := New()

	appends := []execution.TaskRunEventAppend{
		taskRunEventAppend("tr-1", 1, execution.TaskRunEventQueued),
		taskRunEventAppend("tr-1", 1, execution.TaskRunEventClaimed),
		taskRunEventAppend("tr-2", 1, execution.TaskRunEventQueued),
		taskRunEventAppend("tr-1", 1, execution.TaskRunEventCompleted),
	}
	appends[2].EpicID = "epic-2"
	appends[2].DriverRunID = "run-2"
	for _, in := range appends {
		if _, err := s.TaskRunEvents().Append(ctx, in); err != nil {
			t.Fatalf("append %s: %v", in.Type, err)
		}
	}

	tests := []struct {
		name     string
		filter   execution.TaskRunEventFilter
		wantSeqs []int64
	}{
		{name: "all from zero", filter: execution.TaskRunEventFilter{}, wantSeqs: []int64{1, 2, 3, 4}},
		{name: "after cursor", filter: execution.TaskRunEventFilter{AfterSeq: 2}, wantSeqs: []int64{3, 4}},
		{name: "limit window", filter: execution.TaskRunEventFilter{AfterSeq: 1, Limit: 2}, wantSeqs: []int64{2, 3}},
		{name: "epic filter", filter: execution.TaskRunEventFilter{EpicID: "epic-2"}, wantSeqs: []int64{3}},
		{name: "driver run filter", filter: execution.TaskRunEventFilter{DriverRunID: "run-1"}, wantSeqs: []int64{1, 2, 4}},
		{name: "past the end", filter: execution.TaskRunEventFilter{AfterSeq: 4}, wantSeqs: []int64{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			events, err := s.TaskRunEvents().ListSince(ctx, "WS", tc.filter)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if len(events) != len(tc.wantSeqs) {
				t.Fatalf("got %d events, want %d", len(events), len(tc.wantSeqs))
			}
			for i, event := range events {
				if event.Seq != tc.wantSeqs[i] {
					t.Errorf("events[%d].Seq = %d, want %d", i, event.Seq, tc.wantSeqs[i])
				}
			}
		})
	}
}

func outboxCreate(dedupeKey string) execution.OutboxCreate {
	return execution.OutboxCreate{
		WorkspaceKey: "WS",
		Kind:         execution.OutboxKindLeadTaskMessage,
		EpicID:       "epic-1",
		DriverRunID:  "run-1",
		TaskRunID:    "tr-1",
		TargetAgent:  "lead-1",
		Body:         "task tr-1 completed",
		DedupeKey:    dedupeKey,
	}
}

func TestOutboxCreateDedupe(t *testing.T) {
	ctx := t.Context()
	s := New()

	first, err := s.Outbox().Create(ctx, outboxCreate("dedupe-1"))
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	if first.Status != execution.OutboxDeliveryStatusPending || first.Attempt != 0 {
		t.Fatalf("new record (status %q, attempt %d), want (pending, 0)", first.Status, first.Attempt)
	}
	if first.OutboxID == "" || first.Seq == 0 {
		t.Fatalf("new record missing identity: id %q seq %d", first.OutboxID, first.Seq)
	}

	dup, err := s.Outbox().Create(ctx, outboxCreate("dedupe-1"))
	if err != nil {
		t.Fatalf("duplicate create: %v", err)
	}
	if dup.OutboxID != first.OutboxID || dup.Seq != first.Seq {
		t.Fatalf("duplicate create returned (%q, seq %d), want existing (%q, seq %d)",
			dup.OutboxID, dup.Seq, first.OutboxID, first.Seq)
	}

	other, err := s.Outbox().Create(ctx, outboxCreate("dedupe-2"))
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	if other.OutboxID == first.OutboxID {
		t.Fatalf("distinct dedupe keys share OutboxID %q", other.OutboxID)
	}
}

func TestOutboxListDue(t *testing.T) {
	ctx := t.Context()
	s := New()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Minute)
	past := now.Add(-time.Minute)

	due, err := s.Outbox().Create(ctx, outboxCreate("due"))
	if err != nil {
		t.Fatalf("create due: %v", err)
	}
	retryLater, err := s.Outbox().Create(ctx, outboxCreate("retry-later"))
	if err != nil {
		t.Fatalf("create retry-later: %v", err)
	}
	if _, err := s.Outbox().MarkResult(ctx, "WS", retryLater.OutboxID, execution.OutboxDeliveryUpdate{
		Status: execution.OutboxDeliveryStatusPending, Attempt: 1, NextRetryAt: &future, LastError: "inbox busy",
	}); err != nil {
		t.Fatalf("mark retry-later: %v", err)
	}
	retryDue, err := s.Outbox().Create(ctx, outboxCreate("retry-due"))
	if err != nil {
		t.Fatalf("create retry-due: %v", err)
	}
	if _, err := s.Outbox().MarkResult(ctx, "WS", retryDue.OutboxID, execution.OutboxDeliveryUpdate{
		Status: execution.OutboxDeliveryStatusPending, Attempt: 1, NextRetryAt: &past, LastError: "inbox busy",
	}); err != nil {
		t.Fatalf("mark retry-due: %v", err)
	}
	delivered, err := s.Outbox().Create(ctx, outboxCreate("delivered"))
	if err != nil {
		t.Fatalf("create delivered: %v", err)
	}
	if _, err := s.Outbox().MarkResult(ctx, "WS", delivered.OutboxID, execution.OutboxDeliveryUpdate{
		Status: execution.OutboxDeliveryStatusDelivered, Attempt: 1, InboxMessageID: "msg-1",
	}); err != nil {
		t.Fatalf("mark delivered: %v", err)
	}

	tests := []struct {
		name    string
		filter  execution.OutboxDueFilter
		wantIDs []string
	}{
		{name: "due now", filter: execution.OutboxDueFilter{Now: now}, wantIDs: []string{due.OutboxID, retryDue.OutboxID}},
		{name: "limit", filter: execution.OutboxDueFilter{Now: now, Limit: 1}, wantIDs: []string{due.OutboxID}},
		{name: "all retries elapsed", filter: execution.OutboxDueFilter{Now: now.Add(time.Hour)}, wantIDs: []string{due.OutboxID, retryLater.OutboxID, retryDue.OutboxID}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			records, err := s.Outbox().ListDue(ctx, "WS", tc.filter)
			if err != nil {
				t.Fatalf("list due: %v", err)
			}
			if len(records) != len(tc.wantIDs) {
				t.Fatalf("got %d records, want %d", len(records), len(tc.wantIDs))
			}
			for i, record := range records {
				if record.OutboxID != tc.wantIDs[i] {
					t.Errorf("records[%d].OutboxID = %q, want %q", i, record.OutboxID, tc.wantIDs[i])
				}
			}
		})
	}
}

func TestOutboxMarkResult(t *testing.T) {
	ctx := t.Context()
	retryAt := time.Date(2026, 6, 1, 13, 0, 0, 0, time.UTC)

	tests := []struct {
		name            string
		update          execution.OutboxDeliveryUpdate
		wantDeliveredAt bool
	}{
		{
			name: "pending to delivered",
			update: execution.OutboxDeliveryUpdate{
				Status: execution.OutboxDeliveryStatusDelivered, Attempt: 1, InboxMessageID: "msg-1",
			},
			wantDeliveredAt: true,
		},
		{
			name: "pending retry with backoff",
			update: execution.OutboxDeliveryUpdate{
				Status: execution.OutboxDeliveryStatusPending, Attempt: 2, NextRetryAt: &retryAt, LastError: "inbox busy",
			},
		},
		{
			name: "pending to unsupported",
			update: execution.OutboxDeliveryUpdate{
				Status: execution.OutboxDeliveryStatusUnsupported, Attempt: 1, LastError: "agent has no inbox",
			},
		},
		{
			name: "pending to failed",
			update: execution.OutboxDeliveryUpdate{
				Status: execution.OutboxDeliveryStatusFailed, Attempt: 5, LastError: "retries exhausted",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := New()
			created, err := s.Outbox().Create(ctx, outboxCreate("dedupe-1"))
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			updated, err := s.Outbox().MarkResult(ctx, "WS", created.OutboxID, tc.update)
			if err != nil {
				t.Fatalf("mark result: %v", err)
			}
			if updated.Status != tc.update.Status || updated.Attempt != tc.update.Attempt {
				t.Errorf("got (status %q, attempt %d), want (%q, %d)",
					updated.Status, updated.Attempt, tc.update.Status, tc.update.Attempt)
			}
			if updated.LastError != tc.update.LastError {
				t.Errorf("LastError = %q, want %q", updated.LastError, tc.update.LastError)
			}
			if updated.InboxMessageID != tc.update.InboxMessageID {
				t.Errorf("InboxMessageID = %q, want %q", updated.InboxMessageID, tc.update.InboxMessageID)
			}
			if (updated.DeliveredAt != nil) != tc.wantDeliveredAt {
				t.Errorf("DeliveredAt = %v, want set=%v", updated.DeliveredAt, tc.wantDeliveredAt)
			}
			if tc.update.NextRetryAt != nil &&
				(updated.NextRetryAt == nil || !updated.NextRetryAt.Equal(*tc.update.NextRetryAt)) {
				t.Errorf("NextRetryAt = %v, want %v", updated.NextRetryAt, tc.update.NextRetryAt)
			}
			got, err := s.Outbox().Get(ctx, "WS", created.OutboxID)
			if err != nil {
				t.Fatalf("get after mark: %v", err)
			}
			if got.Status != tc.update.Status {
				t.Errorf("persisted status = %q, want %q", got.Status, tc.update.Status)
			}
		})
	}
}

func TestOutboxNotFound(t *testing.T) {
	ctx := t.Context()
	s := New()

	if _, err := s.Outbox().MarkResult(ctx, "WS", "missing", execution.OutboxDeliveryUpdate{
		Status: execution.OutboxDeliveryStatusDelivered, Attempt: 1,
	}); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("MarkResult missing record error = %v, want persistence.ErrNotFound", err)
	}
	if _, err := s.Outbox().Get(ctx, "WS", "missing"); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("Get missing record error = %v, want persistence.ErrNotFound", err)
	}
}
