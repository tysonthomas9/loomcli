package leadcontrol

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backend/fleet"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// TestDeliverCurrentAssignmentCompletesInboxOnRealFleetDB drives the
// server's deliver-lead-assignment path (DeliverCurrentAssignment over the
// fleet-db HTTP store) against a real embedded fleet-db. fleet-db lets only
// the claim holder complete an inbox message; a completion without
// claimed_by is 403 not-owner, which left the assignment leased and pending
// and redelivered it after the lease expired.
//
// Env-gated like the other embedded fleet-db tests: set
// LOOM_RUN_EMBEDDED_SMOKE=1 and point FLEET_DB_BIN (or PATH) at a fleet-db
// built with the inbox owner guard.
func TestDeliverCurrentAssignmentCompletesInboxOnRealFleetDB(t *testing.T) {
	if os.Getenv("LOOM_RUN_EMBEDDED_SMOKE") != "1" {
		t.Skip("set LOOM_RUN_EMBEDDED_SMOKE=1 (with a fleet-db binary) to run against real fleet-db")
	}
	if diag := bootstrap.DiagnoseFleetDBBinary(); diag.Err != nil {
		t.Skipf("fleet-db binary unavailable: %v", diag.Err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	emb, err := bootstrap.StartEmbedded(ctx, t.TempDir(), slog.Default())
	if err != nil {
		t.Fatalf("StartEmbedded: %v", err)
	}
	t.Cleanup(func() { _ = emb.Stop() })
	st, err := fleetdb.New(fleetdb.Config{BaseURL: emb.URL(), Actor: "loom-server"})
	if err != nil {
		t.Fatalf("fleetdb client: %v", err)
	}
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "WS"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: "WS", Name: "lead", Kind: string(domain.RoleKindInteractive)}); err != nil {
		t.Fatalf("create lead role: %v", err)
	}
	issues, err := fleet.New(fleet.Config{BaseURL: emb.URL(), WorkspaceID: "WS", Actor: "loom-server"})
	if err != nil {
		t.Fatalf("fleet issue backend: %v", err)
	}
	epic, err := issues.Create(ctx, backend.CreateParams{Title: "Epic", IssueType: "epic", Priority: 1, CreatedBy: "loom-server"})
	if err != nil {
		t.Fatalf("create epic: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "WS", Name: "nova", RoleName: "lead", Backend: "codex", Parent: epic.ID,
	}); err != nil {
		t.Fatalf("create lead: %v", err)
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS", SessionID: "lead-session", AgentID: "nova",
		Kind: domain.AgentSessionKindOrchestration, Status: domain.AgentSessionRunning,
		Metadata: map[string]string{"actor": "test"},
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	fake := installFakeCodexClient(t, CodexThreadStatus{Type: "idle"})
	setCodexRuntimeMetadata(t, st, "WS", "lead-session", "ws://codex.test", "thread-1")

	result, err := DeliverCurrentAssignment(ctx, st, "WS", "nova")
	if err != nil {
		t.Fatalf("DeliverCurrentAssignment() error = %v", err)
	}
	if result.State != DeliveryStateDelivered || result.InboxMessageID == "" {
		t.Fatalf("delivery = %+v, want delivered with an inbox message", result)
	}
	if fake.turns != 1 {
		t.Fatalf("turns = %d, want 1", fake.turns)
	}
	msg, err := st.AgentInboxMessages().Get(ctx, "WS", result.InboxMessageID)
	if err != nil {
		t.Fatalf("get inbox message: %v", err)
	}
	if msg.Status != domain.AgentInboxMessageDelivered {
		t.Fatalf("inbox status = %q (claimed_by %q), want delivered", msg.Status, msg.ClaimedBy)
	}
	session, err := st.AgentSessions().Get(ctx, "WS", "lead-session")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.Metadata[MetadataDeliveryVersion] == "" || session.Metadata[MetadataDeliveryError] != "" {
		t.Fatalf("session delivery metadata = %#v, want delivered version and no error", session.Metadata)
	}
	// A drain tick (the lead's 2 s loop) finds nothing left to deliver.
	again, err := DeliverPendingLeadMessages(ctx, st, "WS", "nova")
	if err != nil {
		t.Fatalf("DeliverPendingLeadMessages() error = %v", err)
	}
	if again.State != DeliveryStateNone || fake.turns != 1 {
		t.Fatalf("drain after delivery = %+v, turns %d; want none and no second turn", again, fake.turns)
	}
}
