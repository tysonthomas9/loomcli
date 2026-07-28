package automationroutes

import (
	"errors"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestStoreAgentIdentityCompatibilityRejectsBothAgentNamespaces(t *testing.T) {
	st := memstore.New()
	if _, err := st.Workspaces().Create(t.Context(), store.WorkspaceCreate{Key: "WS", Name: "WS"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Roles().Create(t.Context(), store.RoleCreate{WorkspaceKey: "WS", Name: "task"}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.Agents().Create(t.Context(), store.AgentCreate{
		WorkspaceKey: "WS", Name: "s3-local-review", RoleName: "task", Auto: true,
	}); err != nil {
		t.Fatalf("create supervised agent: %v", err)
	}
	if _, err := st.AgentServices().Create(t.Context(), store.AgentServiceCreate{
		WorkspaceKey: "WS", ServiceID: "s2-review-loop", Name: "Review loop",
		Kind: domain.AgentServiceKindEvent, DesiredState: domain.AgentServiceDesiredPaused, RoleName: "task",
	}); err != nil {
		t.Fatalf("create agent record: %v", err)
	}
	if err := st.AgentServices().Delete(t.Context(), "WS", "s2-review-loop"); err != nil {
		t.Fatalf("archive agent record: %v", err)
	}

	checker := newStoreAgentIdentityCompatibility(st.Agents(), st.AgentServices())
	if checker == nil {
		t.Fatal("identity checker = nil")
	}
	if err := checker.CheckUnattachedBindingID(t.Context(), "WS", "s1-bug-fix"); err != nil {
		t.Fatalf("available identifier rejected: %v", err)
	}

	for _, test := range []struct {
		id      string
		message string
	}{
		{id: "s3-local-review", message: "supervised agent"},
		{id: "s2-review-loop", message: "durable agent record"},
	} {
		t.Run(test.id, func(t *testing.T) {
			err := checker.CheckUnattachedBindingID(t.Context(), "WS", test.id)
			if !errors.Is(err, automation.ErrConflict) || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("CheckUnattachedBindingID error = %v, want %q conflict", err, test.message)
			}
		})
	}
}

func TestStoreAgentIdentityCompatibilityRequiresBothStores(t *testing.T) {
	st := memstore.New()
	if got := newStoreAgentIdentityCompatibility(nil, st.AgentServices()); got != nil {
		t.Fatalf("checker with missing AgentStore = %T, want nil", got)
	}
	if got := newStoreAgentIdentityCompatibility(st.Agents(), nil); got != nil {
		t.Fatalf("checker with missing AgentServiceStore = %T, want nil", got)
	}
}
