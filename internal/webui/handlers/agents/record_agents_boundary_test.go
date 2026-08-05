package agents

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	agentsmodule "github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type recordingAgentRecordAPI struct {
	record       *agentsmodule.Agent
	getErr       error
	listErr      error
	updateErr    error
	desiredErr   error
	archiveErr   error
	lifecycleErr error

	update    agentsmodule.UpdateAgentCommand
	desired   agentsmodule.SetDesiredStateCommand
	archive   agentsmodule.ArchiveAgentCommand
	lifecycle agentsmodule.ApplyLifecycleCommand
}

type recordingInteractiveAgentRuntime struct {
	stopped []string
	err     error
}

func (runtime *recordingInteractiveAgentRuntime) StopAgent(
	_ context.Context,
	workspace,
	agentID string,
) error {
	runtime.stopped = append(runtime.stopped, workspace+"/"+agentID)
	return runtime.err
}

func canonicalRuntimeMetadataForTest(t *testing.T, kind string) map[string]string {
	t.Helper()
	metadata, err := agentsmodule.WithRuntimeMetadata(nil, agentsmodule.RuntimeMetadata{RoleKind: kind})
	if err != nil {
		t.Fatal(err)
	}
	return metadata
}

func TestCanonicalInteractiveLifecycleUsesAgentsAndLocalRuntime(t *testing.T) {
	for _, test := range []struct {
		operation string
		action    agentsmodule.LifecycleAction
		stops     bool
	}{
		{operation: "start", action: agentsmodule.LifecycleEnable},
		{operation: "stop", action: agentsmodule.LifecycleDisable, stops: true},
		{operation: "restart", action: agentsmodule.LifecycleEnable, stops: true},
	} {
		t.Run(test.operation, func(t *testing.T) {
			now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
			api := &recordingAgentRecordAPI{record: &agentsmodule.Agent{
				WorkspaceKey: agentRecordTestWS, AgentID: "reviewer", Name: "reviewer",
				GenerationID: "0123456789abcdef0123456789abcdef",
				Kind:         agentsmodule.AgentKindLead, Behavior: agentsmodule.BehaviorReference{RoleName: "review"},
				DesiredState: agentsmodule.DesiredRunning, MaxInstances: 1,
				Metadata: canonicalRuntimeMetadataForTest(t, "interactive"), CreatedAt: now, UpdatedAt: now,
			}}
			runtime := &recordingInteractiveAgentRuntime{}
			module := newTestAgentsModule(nil, newAgentRecordStore(t), nil, agentRecordTestWS)
			module.agentRecords = api
			module.agentLifecycle = api
			module.interactiveRuntime = runtime
			mux := http.NewServeMux()
			module.Register(mux)

			response := doAgentRequest(t, mux, http.MethodPost,
				"/api/workspaces/WS/agents/reviewer/"+test.operation, `{}`)
			if response.Code != http.StatusOK {
				t.Fatalf("%s status=%d body=%s", test.operation, response.Code, response.Body.String())
			}
			if api.lifecycle.Action != test.action || api.lifecycle.AgentID != "reviewer" ||
				api.lifecycle.ExpectedGenerationID != api.record.GenerationID {
				t.Fatalf("lifecycle command = %#v", api.lifecycle)
			}
			if got := len(runtime.stopped); got != map[bool]int{false: 0, true: 1}[test.stops] {
				t.Fatalf("runtime stops = %v", runtime.stopped)
			}
		})
	}
}

func TestCanonicalAgentYieldRouteIsAbsent(t *testing.T) {
	api := &recordingAgentRecordAPI{}
	module := newTestAgentsModule(nil, newAgentRecordStore(t), nil, agentRecordTestWS)
	module.agentRecords = api
	module.agentLifecycle = api
	mux := http.NewServeMux()
	module.Register(mux)
	response := doAgentRequest(t, mux, http.MethodPost, "/api/workspaces/WS/agents/reviewer/yield", "")
	if response.Code != http.StatusNotFound || api.lifecycle.AgentID != "" {
		t.Fatalf("yield status=%d body=%s lifecycle=%#v", response.Code, response.Body.String(), api.lifecycle)
	}
}

func TestCanonicalAgentLifecycleRejectsRetiredDaemonCommandFields(t *testing.T) {
	api := &recordingAgentRecordAPI{}
	module := newTestAgentsModule(nil, newAgentRecordStore(t), nil, agentRecordTestWS)
	module.agentRecords = api
	module.agentLifecycle = api
	mux := http.NewServeMux()
	module.Register(mux)

	for _, body := range []string{
		`{"force":true}`,
		`{"task_id":"WS-1"}`,
		`{"payload":{"task_id":"WS-1"}}`,
	} {
		response := doAgentRequest(
			t, mux, http.MethodPost, "/api/workspaces/WS/agents/reviewer/stop", body,
		)
		if response.Code != http.StatusBadRequest || api.lifecycle.AgentID != "" {
			t.Fatalf("body=%s status=%d response=%s lifecycle=%#v", body, response.Code, response.Body.String(), api.lifecycle)
		}
	}
}

func (api *recordingAgentRecordAPI) ApplyLifecycle(
	_ context.Context,
	_ authority.OperatorAuthority,
	command agentsmodule.ApplyLifecycleCommand,
) (*agentsmodule.LifecycleResult, error) {
	api.lifecycle = command
	if api.lifecycleErr != nil {
		return nil, api.lifecycleErr
	}
	out := cloneCanonicalAgentForHandlerTest(api.record)
	switch command.Action {
	case agentsmodule.LifecycleEnable:
		out.DesiredState = agentsmodule.DesiredRunning
	case agentsmodule.LifecycleDisable:
		out.DesiredState = agentsmodule.DesiredPaused
	case agentsmodule.LifecycleDelete:
		out.DesiredState = agentsmodule.DesiredStopped
		deletedAt := command.ExpectedUpdatedAt.Add(time.Second)
		out.DeletedAt = &deletedAt
	}
	out.UpdatedAt = command.ExpectedUpdatedAt.Add(time.Second)
	api.record = cloneCanonicalAgentForHandlerTest(out)
	return &agentsmodule.LifecycleResult{
		WorkspaceKey: command.WorkspaceKey, AgentID: command.AgentID,
		IdempotencyKey: command.IdempotencyKey, Action: command.Action,
		Agent: out, CommittedAt: out.UpdatedAt,
	}, nil
}

func TestUnifiedAgentDisableUsesOneAtomicLifecycleCommand(t *testing.T) {
	st := newAgentRecordStore(t)
	seedRole(t, st, "docs")
	persisted, err := st.AgentServices().Create(t.Context(), store.AgentServiceCreate{
		WorkspaceKey: agentRecordTestWS, ServiceID: "docs-agent", Name: "Docs agent",
		Kind: domain.AgentServiceKindEvent, DesiredState: domain.AgentServiceDesiredRunning,
		RoleName: "docs",
	})
	if err != nil {
		t.Fatal(err)
	}
	canonical := canonicalAgentRecordForTest(persisted)
	canonical.Metadata = canonicalRuntimeMetadataForTest(t, "worker")
	api := &recordingAgentRecordAPI{record: canonical}
	module := newTestAgentsModule(nil, st, nil, agentRecordTestWS)
	module.agentRecords = api
	module.agentLifecycle = api
	mux := http.NewServeMux()
	module.Register(mux)

	response := doAgentRequest(
		t,
		mux,
		http.MethodPost,
		"/api/workspaces/WS/agents/docs-agent/disable",
		"",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("disable status=%d body=%s", response.Code, response.Body.String())
	}
	if api.lifecycle.Action != agentsmodule.LifecycleDisable ||
		api.lifecycle.WorkspaceKey != agentRecordTestWS ||
		api.lifecycle.AgentID != "docs-agent" ||
		!api.lifecycle.ExpectedUpdatedAt.Equal(persisted.UpdatedAt) ||
		!strings.HasPrefix(api.lifecycle.IdempotencyKey, "webui-agent-lifecycle-") {
		t.Fatalf("lifecycle command = %#v", api.lifecycle)
	}
	if api.desired.AgentID != "" || api.archive.AgentID != "" {
		t.Fatalf("sequential legacy commands were called: desired=%#v archive=%#v", api.desired, api.archive)
	}
}

func (api *recordingAgentRecordAPI) GetAgent(
	context.Context,
	string,
	string,
) (*agentsmodule.Agent, error) {
	return cloneCanonicalAgentForHandlerTest(api.record), api.getErr
}

func (api *recordingAgentRecordAPI) ListAgents(
	context.Context,
	string,
	agentsmodule.AgentFilter,
) ([]*agentsmodule.Agent, error) {
	if api.listErr != nil {
		return nil, api.listErr
	}
	return []*agentsmodule.Agent{cloneCanonicalAgentForHandlerTest(api.record)}, nil
}

func (api *recordingAgentRecordAPI) UpdateAgent(
	_ context.Context,
	_ authority.OperatorAuthority,
	command agentsmodule.UpdateAgentCommand,
) (*agentsmodule.Agent, error) {
	api.update = command
	if api.updateErr != nil {
		return nil, api.updateErr
	}
	out := cloneCanonicalAgentForHandlerTest(api.record)
	if command.Patch.Name != nil {
		out.Name = *command.Patch.Name
	}
	if command.Patch.BudgetPolicy != nil {
		out.BudgetPolicy = *command.Patch.BudgetPolicy
	}
	out.UpdatedAt = out.UpdatedAt.Add(time.Second)
	api.record = cloneCanonicalAgentForHandlerTest(out)
	return out, nil
}

func (api *recordingAgentRecordAPI) SetDesiredState(
	_ context.Context,
	_ authority.OperatorAuthority,
	command agentsmodule.SetDesiredStateCommand,
) (*agentsmodule.Agent, error) {
	api.desired = command
	if api.desiredErr != nil {
		return nil, api.desiredErr
	}
	out := cloneCanonicalAgentForHandlerTest(api.record)
	out.DesiredState = command.DesiredState
	out.UpdatedAt = command.ExpectedUpdatedAt.Add(time.Second)
	api.record = cloneCanonicalAgentForHandlerTest(out)
	return out, nil
}

func (api *recordingAgentRecordAPI) ArchiveAgent(
	_ context.Context,
	_ authority.OperatorAuthority,
	command agentsmodule.ArchiveAgentCommand,
) (*agentsmodule.Agent, error) {
	api.archive = command
	if api.archiveErr != nil {
		return nil, api.archiveErr
	}
	out := cloneCanonicalAgentForHandlerTest(api.record)
	deletedAt := command.ExpectedUpdatedAt.Add(time.Second)
	out.DeletedAt = &deletedAt
	out.UpdatedAt = deletedAt
	api.record = cloneCanonicalAgentForHandlerTest(out)
	return out, nil
}

func cloneCanonicalAgentForHandlerTest(record *agentsmodule.Agent) *agentsmodule.Agent {
	if record == nil {
		return nil
	}
	out := *record
	out.Metadata = cloneStringMap(record.Metadata)
	out.DeletedAt = cloneAgentRecordTime(record.DeletedAt)
	return &out
}

func TestUnifiedAgentRecordReadsPreferCanonicalAgentsAndFailClosed(t *testing.T) {
	t.Run("canonical record wins over conflicting legacy persistence", func(t *testing.T) {
		st := newAgentRecordStore(t)
		seedRole(t, st, "legacy-role")
		if _, err := st.AgentServices().Create(t.Context(), store.AgentServiceCreate{
			WorkspaceKey: agentRecordTestWS,
			ServiceID:    "docs",
			Name:         "Legacy name",
			Kind:         domain.AgentServiceKindEvent,
			RoleName:     "legacy-role",
		}); err != nil {
			t.Fatal(err)
		}
		now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
		canonical := &recordingAgentRecordAPI{record: &agentsmodule.Agent{
			WorkspaceKey: agentRecordTestWS,
			AgentID:      "docs",
			Name:         "Canonical name",
			Kind:         agentsmodule.AgentKindEvent,
			Behavior:     agentsmodule.BehaviorReference{RoleName: "canonical-role"},
			DesiredState: agentsmodule.DesiredRunning,
			MaxInstances: 1,
			Metadata:     canonicalRuntimeMetadataForTest(t, "worker"),
			CreatedAt:    now,
			UpdatedAt:    now,
		}}
		module := newTestAgentsModule(nil, st, nil, agentRecordTestWS)
		module.agentRecords = canonical
		mux := http.NewServeMux()
		module.Register(mux)

		get := doAgentRequest(t, mux, http.MethodGet, "/api/workspaces/WS/agents/docs", "")
		if get.Code != http.StatusOK {
			t.Fatalf("GET status = %d body=%s", get.Code, get.Body.String())
		}
		var detail agentRecordDTO
		decodeJSON(t, get.Body.Bytes(), &detail)
		if detail.Name != "Canonical name" || detail.Behavior.RoleName != "canonical-role" {
			t.Fatalf("detail = %+v, want canonical Agent projection", detail)
		}

		list := doAgentRequest(t, mux, http.MethodGet, "/api/workspaces/WS/agents", "")
		if list.Code != http.StatusOK {
			t.Fatalf("list status = %d body=%s", list.Code, list.Body.String())
		}
		items := decodeListItems(t, list.Body.Bytes())
		if len(items) != 1 || items[0]["name"] != "Canonical name" {
			t.Fatalf("list = %+v, want canonical Agent only", items)
		}
	})

	t.Run("canonical outage does not fall back to legacy record", func(t *testing.T) {
		st := newAgentRecordStore(t)
		seedRole(t, st, "legacy-role")
		if _, err := st.AgentServices().Create(t.Context(), store.AgentServiceCreate{
			WorkspaceKey: agentRecordTestWS,
			ServiceID:    "docs",
			Name:         "Must not leak through fallback",
			Kind:         domain.AgentServiceKindEvent,
			RoleName:     "legacy-role",
		}); err != nil {
			t.Fatal(err)
		}
		module := newTestAgentsModule(nil, st, nil, agentRecordTestWS)
		module.agentRecords = &recordingAgentRecordAPI{
			getErr:  agentsmodule.ErrUnavailable,
			listErr: agentsmodule.ErrUnavailable,
		}
		mux := http.NewServeMux()
		module.Register(mux)

		for _, path := range []string{
			"/api/workspaces/WS/agents/docs",
			"/api/workspaces/WS/agents",
		} {
			response := doAgentRequest(t, mux, http.MethodGet, path, "")
			if response.Code != http.StatusServiceUnavailable ||
				strings.Contains(response.Body.String(), "Must not leak") {
				t.Fatalf("GET %s = %d %s, want fail-closed 503", path, response.Code, response.Body.String())
			}
		}
	})
}

func TestUnifiedAgentDeleteUsesExactCanonicalCASCommands(t *testing.T) {
	st := newAgentRecordStore(t)
	updatedAt := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	api := &recordingAgentRecordAPI{record: &agentsmodule.Agent{
		WorkspaceKey: agentRecordTestWS,
		AgentID:      "docs",
		Name:         "Docs",
		Kind:         agentsmodule.AgentKindEvent,
		Behavior:     agentsmodule.BehaviorReference{RoleName: "docs"},
		DesiredState: agentsmodule.DesiredRunning,
		MaxInstances: 1,
		GenerationID: "0123456789abcdef0123456789abcdef",
		Metadata:     canonicalRuntimeMetadataForTest(t, "worker"),
		CreatedAt:    updatedAt,
		UpdatedAt:    updatedAt,
	}}
	var actions []authority.Action
	resolver := boundaryOperatorResolverFunc(func(
		_ *http.Request,
		workspace string,
		action authority.Action,
	) (authority.OperatorAuthority, error) {
		if workspace != agentRecordTestWS {
			t.Fatalf("authority workspace = %q", workspace)
		}
		actions = append(actions, action)
		return authority.OperatorAuthority{}, nil
	})
	module := newTestAgentsModule(nil, st, nil, agentRecordTestWS)
	module.agentRecords = api
	module.agentRecordAuthority = resolver
	module.agentLifecycle = api
	mux := http.NewServeMux()
	module.Register(mux)

	response := doAgentRequest(t, mux, http.MethodDelete, "/api/workspaces/WS/agents/docs", "")
	if response.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d body=%s", response.Code, response.Body.String())
	}
	if api.lifecycle.WorkspaceKey != agentRecordTestWS ||
		api.lifecycle.AgentID != "docs" ||
		api.lifecycle.Action != agentsmodule.LifecycleDelete ||
		!api.lifecycle.ExpectedUpdatedAt.Equal(updatedAt) ||
		!strings.HasPrefix(api.lifecycle.IdempotencyKey, "webui-agent-lifecycle-") {
		t.Fatalf("lifecycle command = %+v, want exact delete/revision intent", api.lifecycle)
	}
	if api.desired.AgentID != "" || api.archive.AgentID != "" {
		t.Fatalf(
			"sequential legacy commands were called: desired=%+v archive=%+v",
			api.desired,
			api.archive,
		)
	}
	wantActions := []authority.Action{
		agentsmodule.ActionApplyLifecycle,
	}
	if len(actions) != len(wantActions) {
		t.Fatalf("authority actions = %v, want %v", actions, wantActions)
	}
	for index := range wantActions {
		if actions[index] != wantActions[index] {
			t.Fatalf("authority actions = %v, want %v", actions, wantActions)
		}
	}
}

func TestPublicAgentRecordProductionPathsHaveNoLegacyMutations(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	allowedReads := map[string]int{
		"prompt_agent_create_helpers.go":  1,
		"prompt_agent_create_response.go": 1,
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		text := string(source)
		if strings.Contains(text, "AgentServices().Update") ||
			strings.Contains(text, "AgentServices().Delete") ||
			strings.Contains(text, "AgentServices().Create") {
			t.Fatalf("%s contains a legacy AgentService mutation", file)
		}
		count := strings.Count(text, "AgentServices().")
		if count != allowedReads[filepath.Base(file)] {
			t.Fatalf(
				"%s has %d direct AgentService reads, want explicit compatibility count %d",
				file,
				count,
				allowedReads[filepath.Base(file)],
			)
		}
		delete(allowedReads, filepath.Base(file))
	}
	if len(allowedReads) != 0 {
		t.Fatalf("explicit prompt-agent compatibility reads were not found: %v", allowedReads)
	}
}

func TestUnifiedAgentIdentityUpdateDoesNotFallbackAfterCanonicalConflict(t *testing.T) {
	st := newAgentRecordStore(t)
	seedRole(t, st, "legacy-role")
	legacy, err := st.AgentServices().Create(t.Context(), store.AgentServiceCreate{
		WorkspaceKey: agentRecordTestWS,
		ServiceID:    "docs",
		Name:         "Legacy name",
		Kind:         domain.AgentServiceKindEvent,
		RoleName:     "legacy-role",
	})
	if err != nil {
		t.Fatal(err)
	}
	api := &recordingAgentRecordAPI{
		record:    canonicalAgentRecordForTest(legacy),
		updateErr: agentsmodule.ErrConflict,
	}
	module := newTestAgentsModule(nil, st, nil, agentRecordTestWS)
	module.agentRecords = api
	mux := http.NewServeMux()
	module.Register(mux)

	response := doAgentRequest(
		t,
		mux,
		http.MethodPatch,
		"/api/workspaces/WS/agents/docs",
		`{"name":"Must not persist"}`,
	)
	if response.Code != http.StatusConflict {
		t.Fatalf("PATCH status = %d body=%s, want 409", response.Code, response.Body.String())
	}
	unchanged, err := st.AgentServices().Get(t.Context(), agentRecordTestWS, "docs")
	if err != nil || unchanged.Name != "Legacy name" {
		t.Fatalf("legacy record mutated after canonical conflict: %+v err=%v", unchanged, err)
	}
	if !api.update.ExpectedUpdatedAt.Equal(legacy.UpdatedAt) {
		t.Fatalf("update command revision = %s, want %s", api.update.ExpectedUpdatedAt, legacy.UpdatedAt)
	}
	if api.update.Patch.Name == nil || *api.update.Patch.Name != "Must not persist" {
		t.Fatalf("update command patch = %+v", api.update.Patch)
	}
	if !errors.Is(api.updateErr, agentsmodule.ErrConflict) {
		t.Fatalf("test setup lost conflict: %v", api.updateErr)
	}
}
