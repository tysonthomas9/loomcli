package leadcontrol

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/skillmat"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type materializingTurnDeliverer struct {
	events *[]string
	turns  int
}

func (d *materializingTurnDeliverer) provider() string                               { return RuntimeProviderCodex }
func (d *materializingTurnDeliverer) hasRuntimeMetadata(map[string]string) bool      { return true }
func (d *materializingTurnDeliverer) notReadyReason() string                         { return "" }
func (d *materializingTurnDeliverer) unsupportedReason(map[string]string) string     { return "" }
func (d *materializingTurnDeliverer) pendingReason() string                          { return "" }
func (d *materializingTurnDeliverer) claimedBy(string) string                        { return "test-deliverer" }
func (d *materializingTurnDeliverer) populate(*DeliveryResult, *domain.AgentSession) {}
func (d *materializingTurnDeliverer) deliveredThreadID() string                      { return "thread-1" }
func (d *materializingTurnDeliverer) deliverTurn(_ context.Context, _ store.Store, _, _ string, result *DeliveryResult, _, _ string) (*DeliveryResult, error) {
	d.turns++
	*d.events = append(*d.events, "deliver")
	result.State = DeliveryStateDelivered
	return result, nil
}

func TestLeadTurnMaterializesImmediatelyBeforeDelivery(t *testing.T) {
	ctx := t.Context()
	st := memstore.New()
	session, inbox := createMaterializingDeliveryFixture(t, st)
	var events []string
	d := &materializingTurnDeliverer{events: &events}
	installLeadTurnMaterializer(t, func(_ context.Context, got store.Store, workspace, roleName, targetDir string) error {
		if got != st || workspace != "WS" || roleName != "operator" || targetDir != "/repo" {
			t.Fatalf("materializer args = %T, %q, %q, %q", got, workspace, roleName, targetDir)
		}
		events = append(events, "materialize")
		return nil
	})

	result, err := deliverNextLeadInboxMessage(ctx, st, "WS", "nova", session, d, &DeliveryResult{State: DeliveryStatePending})
	if err != nil {
		t.Fatalf("deliverNextLeadInboxMessage: %v", err)
	}
	if result.State != DeliveryStateDelivered || d.turns != 1 {
		t.Fatalf("result/turns = %+v/%d", result, d.turns)
	}
	if want := []string{"materialize", "deliver"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	stored, err := st.AgentInboxMessages().Get(ctx, "WS", inbox.InboxMessageID)
	if err != nil || stored.Status != domain.AgentInboxMessageDelivered {
		t.Fatalf("inbox = %+v, %v", stored, err)
	}
}

func TestLeadTurnMaterializationRefusalBlocksDeliveryAndLeavesMessageVisible(t *testing.T) {
	ctx := t.Context()
	st := memstore.New()
	session, inbox := createMaterializingDeliveryFixture(t, st)
	var events []string
	d := &materializingTurnDeliverer{events: &events}
	installLeadTurnMaterializer(t, func(context.Context, store.Store, string, string, string) error {
		return errors.New("symlink escapes target")
	})

	_, err := deliverNextLeadInboxMessage(ctx, st, "WS", "nova", session, d, &DeliveryResult{State: DeliveryStatePending})
	if err == nil || !strings.Contains(err.Error(), "skill materialization refused: symlink escapes target") {
		t.Fatalf("error = %v", err)
	}
	if d.turns != 0 {
		t.Fatalf("turns = %d, want 0", d.turns)
	}
	stored, getErr := st.AgentInboxMessages().Get(ctx, "WS", inbox.InboxMessageID)
	if getErr != nil {
		t.Fatalf("get inbox: %v", getErr)
	}
	if stored.Status != domain.AgentInboxMessageQueued || stored.ClaimedBy == "" {
		t.Fatalf("inbox = %+v, want queued under the existing delivery-failure claim", stored)
	}
}

func TestLeadTurnDeliversWhenSkillStoreIsUnavailable(t *testing.T) {
	ctx := t.Context()
	st := memstore.New()
	session, _ := createMaterializingDeliveryFixture(t, st)
	var events []string
	d := &materializingTurnDeliverer{events: &events}
	installLeadTurnMaterializer(t, func(context.Context, store.Store, string, string, string) error {
		events = append(events, "materialize")
		return &skillmat.StoreUnavailableError{Err: errors.New("fleet-db unavailable")}
	})

	result, err := deliverNextLeadInboxMessage(ctx, st, "WS", "nova", session, d, &DeliveryResult{State: DeliveryStatePending})
	if err != nil {
		t.Fatalf("deliverNextLeadInboxMessage: %v", err)
	}
	if result.State != DeliveryStateDelivered || d.turns != 1 {
		t.Fatalf("result/turns = %+v/%d", result, d.turns)
	}
	if want := []string{"materialize", "deliver"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

// Sessions written before lead_role was recorded in metadata, and any session
// whose registration did not resolve a role, take the fallback path instead.
// The fixture above always stamps lead_role, so without this the fallback is
// never reached from a delivery.
func TestLeadTurnResolvesRoleFromTheAgentWhenMetadataOmitsIt(t *testing.T) {
	tests := []struct {
		name     string
		agent    string
		wantRole string
	}{
		{name: "registered agent supplies its role", agent: "nova", wantRole: "operator"},
		{name: "unregistered agent falls back to lead", agent: "ghost", wantRole: "lead"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			st := memstore.New()
			session, _ := createMaterializingDeliveryFixtureWithMetadata(t, st, tt.agent, map[string]string{
				MetadataLeadWorkDir: "/repo",
			})
			var events []string
			d := &materializingTurnDeliverer{events: &events}
			var gotRole string
			installLeadTurnMaterializer(t, func(_ context.Context, _ store.Store, _, roleName, _ string) error {
				gotRole = roleName
				events = append(events, "materialize")
				return nil
			})

			if _, err := deliverNextLeadInboxMessage(
				ctx, st, "WS", tt.agent, session, d, &DeliveryResult{State: DeliveryStatePending},
			); err != nil {
				t.Fatalf("deliverNextLeadInboxMessage: %v", err)
			}
			if gotRole != tt.wantRole {
				t.Fatalf("materialized for role %q, want %q", gotRole, tt.wantRole)
			}
		})
	}
}

func createMaterializingDeliveryFixture(t *testing.T, st store.Store) (*domain.AgentSession, *domain.AgentInboxMessage) {
	t.Helper()
	return createMaterializingDeliveryFixtureWithMetadata(t, st, "nova", map[string]string{
		MetadataLeadWorkDir: "/repo",
		MetadataLeadRole:    "operator",
	})
}

func createMaterializingDeliveryFixtureWithMetadata(
	t *testing.T, st store.Store, agentID string, metadata map[string]string,
) (*domain.AgentSession, *domain.AgentInboxMessage) {
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
		AgentID:      agentID,
		Kind:         domain.AgentSessionKindOrchestration,
		Status:       domain.AgentSessionRunning,
		Metadata:     metadata,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	inbox, err := createLeadInboxMessage(ctx, st, "WS", agentID, session.SessionID, "next turn", LeadMessageDeliveryOptions{})
	if err != nil {
		t.Fatalf("create inbox: %v", err)
	}
	return session, inbox
}

func installLeadTurnMaterializer(t *testing.T, materialize leadSkillMaterializer) {
	t.Helper()
	original := materializeLeadSkillsBeforeTurn
	materializeLeadSkillsBeforeTurn = materialize
	t.Cleanup(func() { materializeLeadSkillsBeforeTurn = original })
}
