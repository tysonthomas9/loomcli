package agents

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
)

var errInjectedPostCommitRead = errors.New("injected post-commit read failure")

type failGetAfterCreateAgentServiceStore struct {
	store.AgentServiceStore
	created bool
}

func (s *failGetAfterCreateAgentServiceStore) Create(
	ctx context.Context,
	in store.AgentServiceCreate,
) (*domain.AgentService, error) {
	created, err := s.AgentServiceStore.Create(ctx, in)
	if err == nil {
		s.created = true
	}
	return created, err
}

func (s *failGetAfterCreateAgentServiceStore) Get(
	ctx context.Context,
	workspaceKey, serviceID string,
) (*domain.AgentService, error) {
	if s.created {
		return nil, errInjectedPostCommitRead
	}
	return s.AgentServiceStore.Get(ctx, workspaceKey, serviceID)
}

type agentStoreWithPostCommitGetFailure struct {
	store.Store
	agentServices store.AgentServiceStore
}

func (s agentStoreWithPostCommitGetFailure) AgentServices() store.AgentServiceStore {
	return s.agentServices
}

type failListAfterCreateBindingOperations struct {
	automation.BindingOperations
	created bool
}

func (o *failListAfterCreateBindingOperations) CreateManagedBinding(
	ctx context.Context,
	auth authority.OperatorAuthority,
	command automation.CreateManagedBindingCommand,
) (*automation.Binding, error) {
	created, err := o.BindingOperations.CreateManagedBinding(ctx, auth, command)
	if err == nil {
		o.created = true
	}
	return created, err
}

func (o *failListAfterCreateBindingOperations) ListBindings(
	ctx context.Context,
	workspace string,
	filter automation.BindingFilter,
) ([]*automation.Binding, error) {
	if o.created {
		return nil, errInjectedPostCommitRead
	}
	return o.BindingOperations.ListBindings(ctx, workspace, filter)
}

func TestPromptAgentCreatePostCommitReadFailureReturnsCommittedSuccess(t *testing.T) {
	tests := []struct {
		name      string
		failStage string
	}{
		{name: "agent service get", failStage: "get_record"},
		{name: "binding list", failStage: "list_bindings"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := newAgentRecordStore(t)
			seedPromptAgentRole(t, base, "docs-assistant")

			var st store.Store = base
			var bindings automation.BindingOperations = &testBindingOperations{store: base}
			switch tt.failStage {
			case "get_record":
				failingAgentServices := &failGetAfterCreateAgentServiceStore{
					AgentServiceStore: base.AgentServices(),
				}
				st = agentStoreWithPostCommitGetFailure{
					Store:         base,
					agentServices: failingAgentServices,
				}
			case "list_bindings":
				bindings = &failListAfterCreateBindingOperations{BindingOperations: bindings}
			default:
				t.Fatalf("unknown failure stage %q", tt.failStage)
			}

			mux := http.NewServeMux()
			New(Config{
				Store:             st,
				Bindings:          bindings,
				OperatorAuthority: testOperatorAuthorityResolver{},
				WorkspaceFromContext: func(context.Context) string {
					return agentRecordTestWS
				},
			}).Register(mux)

			var logs bytes.Buffer
			previousLogger := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(previousLogger) })

			body := `{
				"kind":"prompt",
				"name":"Docs assistant",
				"backend":"codex",
				"behavior":{"role_name":"docs-assistant"},
				"trigger":{"source_kind":"internal"},
				"grants":[{
					"connector_id":"github",
					"action":"issues.read",
					"resource_pattern":"repo:example/docs"
				}],
				"enabled":true
			}`
			rec := doAgentRequest(t, mux, http.MethodPost, "/api/workspaces/WS/agents", body)
			if rec.Code != http.StatusCreated {
				t.Fatalf("POST /agents status = %d body=%s, want committed 201 response", rec.Code, rec.Body.String())
			}

			var response agentRecordDTO
			decodeJSON(t, rec.Body.Bytes(), &response)
			if response.ID == "" || response.Name != "Docs assistant" || response.Kind != agentRecordKindPrompt ||
				!response.Enabled || response.Behavior.RoleName != "docs-assistant" {
				t.Fatalf("created response = %+v, want accurate committed prompt agent", response)
			}
			if len(response.Bindings) != 1 || response.Bindings[0].BindingID == "" ||
				response.Bindings[0].TargetAgentServiceID != response.ID || !response.Bindings[0].Enabled {
				t.Fatalf("created response bindings = %+v, want one enabled committed binding", response.Bindings)
			}

			records, err := base.AgentServices().List(context.Background(), agentRecordTestWS, store.AgentServiceFilter{
				IncludeDeleted: true,
			})
			if err != nil {
				t.Fatalf("list committed agent services: %v", err)
			}
			if len(records) != 1 || records[0].ServiceID != response.ID ||
				records[0].DesiredState != domain.AgentServiceDesiredRunning {
				t.Fatalf("committed agent services = %+v, want exactly one running record", records)
			}
			persistedBindings, err := base.TriggerBindings().List(
				context.Background(),
				agentRecordTestWS,
				store.TriggerBindingFilter{},
			)
			if err != nil {
				t.Fatalf("list committed bindings: %v", err)
			}
			if len(persistedBindings) != 1 || persistedBindings[0].BindingID != response.Bindings[0].BindingID ||
				persistedBindings[0].TargetAgentServiceID != response.ID || !persistedBindings[0].Enabled {
				t.Fatalf("committed bindings = %+v, want exactly one enabled owned binding", persistedBindings)
			}
			grants, err := base.ConnectorGrants().ListByBinding(
				context.Background(),
				agentRecordTestWS,
				response.Bindings[0].BindingID,
			)
			if err != nil || len(grants) != 1 {
				t.Fatalf("committed grants = %+v err=%v, want exactly one", grants, err)
			}

			logOutput := logs.String()
			if !strings.Contains(logOutput, "post-commit response refresh failed") ||
				!strings.Contains(logOutput, "stage="+tt.failStage) ||
				!strings.Contains(logOutput, "agent_id="+response.ID) {
				t.Fatalf("fallback observability log = %q, want stage and committed agent id", logOutput)
			}
		})
	}
}
