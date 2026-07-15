package agents

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/svcimpl"
)

func TestUnifiedSupervisedCreateNormalizesAgentRecordCollisionLookup(t *testing.T) {
	st := newAgentRecordStore(t)
	ctx := context.Background()
	seedRole(t, st, "docs-assistant")
	if _, err := st.AgentServices().Create(ctx, store.AgentServiceCreate{
		WorkspaceKey: agentRecordTestWS,
		ServiceID:    "agt-reserved",
		Name:         "Reserved",
		Kind:         domain.AgentServiceKindEvent,
		RoleName:     "docs-assistant",
	}); err != nil {
		t.Fatalf("create reserved agent record: %v", err)
	}

	mux := http.NewServeMux()
	NewModule(svcimpl.NewAgentService(nil, nil, nil, st), st, nil).Register(mux)
	rec := doAgentRequest(t, mux, http.MethodPost, "/api/workspaces/WS/agents",
		`{"name":"AGT-RESERVED","role_name":"task","kind":"supervised","backend":"codex"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "already used by an agent record") {
		t.Fatalf("mixed-case collision status = %d body=%s, want 409", rec.Code, rec.Body.String())
	}

	agents, err := st.Agents().List(ctx, agentRecordTestWS)
	if err != nil || len(agents) != 0 {
		t.Fatalf("supervised agents after rejected create = %+v err=%v, want none", agents, err)
	}
	if _, err := st.Roles().Get(ctx, agentRecordTestWS, "task"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("rejected create mutated roles, task role err=%v", err)
	}
	record, err := st.AgentServices().Get(ctx, agentRecordTestWS, "agt-reserved")
	if err != nil || record.Name != "Reserved" || record.DeletedAt != nil {
		t.Fatalf("reserved record mutated: %+v err=%v", record, err)
	}

	listRec := doAgentRequest(t, mux, http.MethodGet, "/api/workspaces/WS/agents", "")
	if listRec.Code != http.StatusOK {
		t.Fatalf("list after rejected collision status = %d body=%s, want 200", listRec.Code, listRec.Body.String())
	}
	items := decodeListItems(t, listRec.Body.Bytes())
	if len(items) != 1 || items[0]["id"] != "agt-reserved" || items[0]["kind"] != agentRecordKindPrompt {
		t.Fatalf("list after rejected collision = %+v, want only reserved prompt record", items)
	}
	detailRec := doAgentRequest(t, mux, http.MethodGet, "/api/workspaces/WS/agents/agt-reserved", "")
	if detailRec.Code != http.StatusOK {
		t.Fatalf("reserved detail route status = %d body=%s, want 200", detailRec.Code, detailRec.Body.String())
	}
}
