package leadcontrol

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// serverClaimedBy is what the recording store stamps on a claim. It is
// deliberately unlike materializingTurnDeliverer.claimedBy ("test-deliverer"),
// so a completion that re-derives the value instead of echoing the claim's
// answer is visible.
const serverClaimedBy = "codex:lead-session:thread-42"

// claimRecordingStore wraps a store and intercepts the inbox: it stamps its own
// claimed_by on a claim (as fleet-db does), records every Complete update, and
// can refuse a completion the way fleet-db #322 refuses a lost claim.
type claimRecordingStore struct {
	store.Store
	inbox *claimRecordingInbox
}

func (s *claimRecordingStore) AgentInboxMessages() store.AgentInboxMessageStore { return s.inbox }

type claimRecordingInbox struct {
	store.AgentInboxMessageStore
	completeErr error
	completes   []store.AgentInboxMessageComplete
}

func (s *claimRecordingInbox) ClaimNext(
	ctx context.Context, in store.AgentInboxMessageClaim,
) (*domain.AgentInboxMessage, error) {
	msg, err := s.AgentInboxMessageStore.ClaimNext(ctx, in)
	if err != nil {
		return nil, err
	}
	// fleet-db returns the claim it recorded; the client must not assume it
	// equals what it asked for.
	msg.ClaimedBy = serverClaimedBy
	return msg, nil
}

func (s *claimRecordingInbox) Complete(
	ctx context.Context, ws, inboxMessageID string, update store.AgentInboxMessageComplete,
) (*domain.AgentInboxMessage, error) {
	s.completes = append(s.completes, update)
	if s.completeErr != nil {
		return nil, s.completeErr
	}
	return s.AgentInboxMessageStore.Complete(ctx, ws, inboxMessageID, update)
}

func newClaimRecordingStore(inner store.Store) *claimRecordingStore {
	return &claimRecordingStore{Store: inner, inbox: &claimRecordingInbox{AgentInboxMessageStore: inner.AgentInboxMessages()}}
}

// refusingTurnDeliverer delivers, or refuses to, on demand.
type refusingTurnDeliverer struct {
	materializingTurnDeliverer
	pendingReasonText string
}

func (d *refusingTurnDeliverer) deliverTurn(
	_ context.Context, _ store.Store, _, _ string, result *DeliveryResult, _, _ string,
) (*DeliveryResult, error) {
	d.turns++
	if d.pendingReasonText != "" {
		result.State = DeliveryStatePending
		result.Reason = d.pendingReasonText
		return result, nil
	}
	result.State = DeliveryStateDelivered
	return result, nil
}

// fleet-db #322: only the holder of the live claim may complete a message, and
// the holder is identified by the claimed_by the claim itself recorded. The
// completion must echo that value verbatim on every outcome.
func TestLeadInboxCompletionEchoesTheClaimFromClaimNext(t *testing.T) {
	tests := []struct {
		name        string
		pending     string
		wantOutcome string
	}{
		{name: "delivered", wantOutcome: "delivered"},
		{name: "retry", pending: "runtime busy", wantOutcome: "retry"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			st := newClaimRecordingStore(memstore.New())
			session, _ := createMaterializingDeliveryFixture(t, st)
			var events []string
			d := &refusingTurnDeliverer{
				materializingTurnDeliverer: materializingTurnDeliverer{events: &events},
				pendingReasonText:          tt.pending,
			}
			installLeadTurnMaterializer(t, func(context.Context, store.Store, string, string, string) error { return nil })

			if _, err := deliverNextLeadInboxMessage(
				ctx, st, "WS", "nova", session, d, &DeliveryResult{State: DeliveryStatePending},
			); err != nil {
				t.Fatalf("deliverNextLeadInboxMessage: %v", err)
			}
			if len(st.inbox.completes) != 1 {
				t.Fatalf("completes = %+v, want exactly one", st.inbox.completes)
			}
			got := st.inbox.completes[0]
			if got.Outcome != tt.wantOutcome {
				t.Fatalf("outcome = %q, want %q", got.Outcome, tt.wantOutcome)
			}
			if got.ClaimedBy != serverClaimedBy {
				t.Fatalf("complete claimed_by = %q, want the claim's own %q", got.ClaimedBy, serverClaimedBy)
			}
		})
	}
}

// A refused completion (403 not-owner, 410 lease-expired) must surface as a
// real error rather than being swallowed, so the caller can log it instead of
// re-delivering the same message every tick forever.
func TestLeadInboxCompletionRefusalIsSurfaced(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		pending string
	}{
		{name: "delivered not owner", err: fmt.Errorf("HTTP 403: %w", domain.ErrNotOwner)},
		{name: "delivered lease expired", err: fmt.Errorf("HTTP 410: %w", domain.ErrGone)},
		{name: "retry not owner", err: fmt.Errorf("HTTP 403: %w", domain.ErrNotOwner), pending: "runtime busy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			st := newClaimRecordingStore(memstore.New())
			st.inbox.completeErr = tt.err
			session, _ := createMaterializingDeliveryFixture(t, st)
			var events []string
			d := &refusingTurnDeliverer{
				materializingTurnDeliverer: materializingTurnDeliverer{events: &events},
				pendingReasonText:          tt.pending,
			}
			installLeadTurnMaterializer(t, func(context.Context, store.Store, string, string, string) error { return nil })

			_, err := deliverNextLeadInboxMessage(
				ctx, st, "WS", "nova", session, d, &DeliveryResult{State: DeliveryStatePending},
			)
			if err == nil {
				t.Fatal("deliverNextLeadInboxMessage returned no error on a refused completion")
			}
			if !errors.Is(err, tt.err) {
				t.Fatalf("error = %v, want it to wrap %v", err, tt.err)
			}
			if !strings.Contains(err.Error(), "inbox") {
				t.Fatalf("error = %v, want it to name the inbox completion", err)
			}
		})
	}
}

// A lost claim on a turn that did land must still advance the assignment's
// delivered marker: otherwise the same assignment is re-delivered to the lead
// on every drain tick, which is exactly the loop #322 exposed.
func TestLeadInboxAssignmentIsMarkedDeliveredEvenWhenTheClaimIsLost(t *testing.T) {
	ctx := t.Context()
	inner := memstore.New()
	st := newClaimRecordingStore(inner)
	st.inbox.completeErr = fmt.Errorf("HTTP 403: %w", domain.ErrNotOwner)
	session := createAssignmentDeliveryFixture(t, st)
	var events []string
	d := &refusingTurnDeliverer{materializingTurnDeliverer: materializingTurnDeliverer{events: &events}}
	installLeadTurnMaterializer(t, func(context.Context, store.Store, string, string, string) error { return nil })

	if _, err := deliverNextLeadInboxMessage(
		ctx, st, "WS", "nova", session, d, &DeliveryResult{State: DeliveryStatePending},
	); err == nil {
		t.Fatal("expected the refused completion to surface")
	}
	stored, err := st.AgentSessions().Get(ctx, "WS", session.SessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if stored.Metadata[MetadataDeliveryVersion] != "v3" {
		t.Fatalf("session metadata = %v, want the assignment marked delivered at v3", stored.Metadata)
	}
}

// The drain loop must not hide a delivery failure at debug level: a message
// that a refused completion re-delivers every tick has to be visible in the
// lead runtime's log.
func TestLeadMessageDrainLogsDeliveryFailuresAboveDebug(t *testing.T) {
	recorder := &levelRecorder{}
	logLeadDrainFailure(slog.New(recorder), "nova", fmt.Errorf("HTTP 403: %w", domain.ErrNotOwner))
	if len(recorder.records) != 1 {
		t.Fatalf("records = %v, want exactly one", recorder.records)
	}
	if recorder.maxLevel < slog.LevelWarn {
		t.Fatalf("drain failure logged at %v, want at least WARN", recorder.maxLevel)
	}
}

type levelRecorder struct {
	maxLevel slog.Level
	records  []string
}

func (h *levelRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (h *levelRecorder) Handle(_ context.Context, r slog.Record) error {
	if len(h.records) == 0 || r.Level > h.maxLevel {
		h.maxLevel = r.Level
	}
	h.records = append(h.records, r.Message)
	return nil
}
func (h *levelRecorder) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *levelRecorder) WithGroup(string) slog.Handler      { return h }

func createAssignmentDeliveryFixture(t *testing.T, st store.Store) *domain.AgentSession {
	t.Helper()
	ctx := t.Context()
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "WS", Name: "nova", RoleName: "operator",
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	session, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "lead-session",
		AgentID:      "nova",
		Kind:         domain.AgentSessionKindOrchestration,
		Status:       domain.AgentSessionRunning,
		Metadata: map[string]string{
			MetadataLeadWorkDir: "/repo",
			MetadataLeadRole:    "operator",
		},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := st.AgentInboxMessages().Create(ctx, store.AgentInboxMessageCreate{
		WorkspaceKey:  "WS",
		TargetAgentID: "nova",
		SessionID:     session.SessionID,
		Body:          "assignment turn",
		SourceKind:    assignmentInboxSourceKind,
		SourceRef:     assignmentInboxSourceRefPrefix + "EPIC-1/v3",
		DedupeKey:     "assignment-1",
	}); err != nil {
		t.Fatalf("create assignment inbox message: %v", err)
	}
	return session
}
