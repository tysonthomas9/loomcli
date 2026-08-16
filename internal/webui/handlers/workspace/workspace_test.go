package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/app/query/operationalview"
	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
)

func TestHandleActiveWorkspace_EmptyResponse(t *testing.T) {
	svc := &mockWorkspaceService{
		getActiveWorkspaceFn: func(_ context.Context) (*operationalview.Workspace, error) {
			return &operationalview.Workspace{
				Repos:      []operationalview.Repository{},
				Groups:     []string{},
				Agents:     []operationalview.Agent{},
				Workspaces: []operationalview.Summary{},
			}, nil
		},
	}
	handler := handleActiveWorkspace(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/active", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp WorkspaceResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !resp.Success {
		t.Fatal("expected success=true")
	}
	if resp.Data == nil {
		t.Fatal("expected non-nil data")
	}
	if len(resp.Data.Repos) != 0 {
		t.Fatalf("expected empty repos, got %d", len(resp.Data.Repos))
	}
	assertJSONArraysNotNull(t, resp, "repos", "groups", "agents", "workspaces")
}

func TestHandleActiveWorkspace_WithRepos(t *testing.T) {
	svc := &mockWorkspaceService{
		getActiveWorkspaceFn: func(_ context.Context) (*operationalview.Workspace, error) {
			return &operationalview.Workspace{
				Name: "myworkspace",
				Path: "/workspaces/myworkspace",
				Repos: []operationalview.Repository{
					{Name: "payments/api", Path: "/workspaces/payments/api", DefaultBranch: "main", Remote: "origin"},
					{Name: "auth/service", Path: "/workspaces/auth/service", DefaultBranch: "develop", Remote: "upstream"},
				},
				Groups:     []string{},
				Agents:     []operationalview.Agent{},
				Workspaces: []operationalview.Summary{},
			}, nil
		},
	}
	handler := handleActiveWorkspace(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/active", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp WorkspaceResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !resp.Success {
		t.Fatal("expected success=true")
	}
	if resp.Data.Name != "myworkspace" {
		t.Errorf("expected name myworkspace, got %s", resp.Data.Name)
	}
	if len(resp.Data.Repos) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(resp.Data.Repos))
	}
}

func TestHandleActiveWorkspace_ConfigError(t *testing.T) {
	svc := &mockWorkspaceService{
		getActiveWorkspaceFn: func(_ context.Context) (*operationalview.Workspace, error) {
			return nil, apperrors.ErrInternal("config broken", nil)
		},
	}
	handler := handleActiveWorkspace(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/active", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestHandleActiveWorkspace_FullResponse(t *testing.T) {
	svc := &mockWorkspaceService{
		getActiveWorkspaceFn: func(_ context.Context) (*operationalview.Workspace, error) {
			return &operationalview.Workspace{
				Name: "prod",
				Path: "/workspaces/prod",
				Repos: []operationalview.Repository{
					{Name: "api", Path: "/code/api", SourceRepoID: "api", Groups: []string{"backend"}},
					{Name: "web", Path: "/code/web", SourceRepoID: "web", Groups: []string{"frontend"}},
				},
				Groups: []string{"backend", "frontend"},
				Agents: []operationalview.Agent{
					{Name: "agent-1", Repos: []string{"api"}, RepoGroups: []string{"backend"}, CrossRepo: false},
					{Name: "agent-2", Repos: []string{"web"}, RepoGroups: []string{"frontend"}, CrossRepo: true},
				},
				Workspaces: []operationalview.Summary{},
			}, nil
		},
	}
	handler := handleActiveWorkspace(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/active", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp WorkspaceResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !resp.Success {
		t.Fatal("expected success=true")
	}
	data := resp.Data
	if data.Name != "prod" {
		t.Errorf("expected name prod, got %s", data.Name)
	}
	if len(data.Repos) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(data.Repos))
	}
	if len(data.Groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(data.Groups))
	}
	if len(data.Agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(data.Agents))
	}
	if !data.Agents[1].CrossRepo {
		t.Error("expected agent-2 CrossRepo=true")
	}
}

// assertJSONArraysNotNull re-marshals the response data and checks that specified fields are [] not null.
func assertJSONArraysNotNull(t *testing.T, resp WorkspaceResponse, fields ...string) {
	t.Helper()
	dataBytes, err := json.Marshal(resp.Data)
	if err != nil {
		t.Fatalf("assertJSONArraysNotNull: marshal error: %v", err)
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(dataBytes, &data); err != nil {
		t.Fatalf("assertJSONArraysNotNull: decode data error: %v", err)
	}
	for _, f := range fields {
		if string(data[f]) == "null" {
			t.Errorf("expected data.%s to be [] not null", f)
		}
	}
}
