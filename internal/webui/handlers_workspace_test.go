package webui

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleActiveWorkspace_NilConfigFn(t *testing.T) {
	handler := handleActiveWorkspace(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/active", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp workspaceResponse
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

func TestHandleActiveWorkspace_EmptyRepos(t *testing.T) {
	handler := handleActiveWorkspace(func() (*WorkspaceData, error) {
		return nil, nil
	})
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/active", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp workspaceResponse
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
}

func TestHandleActiveWorkspace_WithRepos(t *testing.T) {
	handler := handleActiveWorkspace(func() (*WorkspaceData, error) {
		return &WorkspaceData{
			Name: "myworkspace",
			Path: "/workspaces/myworkspace",
			Repos: []WorkspaceRepo{
				{Name: "payments/api", Path: "/workspaces/payments/api", DefaultBranch: "main", Remote: "origin"},
				{Name: "auth/service", Path: "/workspaces/auth/service", DefaultBranch: "develop", Remote: "upstream"},
			},
		}, nil
	})
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/active", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp workspaceResponse
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
	handler := handleActiveWorkspace(func() (*WorkspaceData, error) {
		return nil, errors.New("config broken")
	})
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/active", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}

	var resp workspaceResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Success {
		t.Fatal("expected success=false")
	}
}

func TestHandleActiveWorkspace_FullResponse(t *testing.T) {
	handler := handleActiveWorkspace(func() (*WorkspaceData, error) {
		return &WorkspaceData{
			Name: "prod",
			Path: "/workspaces/prod",
			Repos: []WorkspaceRepo{
				{Name: "api", Path: "/code/api", SourceRepoID: "api", Groups: []string{"backend"}},
				{Name: "web", Path: "/code/web", SourceRepoID: "web", Groups: []string{"frontend"}},
			},
			Groups: []string{"backend", "frontend"},
			Agents: []WorkspaceAgentInfo{
				{Name: "agent-1", Repos: []string{"api"}, RepoGroups: []string{"backend"}, CrossRepo: false},
				{Name: "agent-2", Repos: []string{"web"}, RepoGroups: []string{"frontend"}, CrossRepo: true},
			},
		}, nil
	})
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/active", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp workspaceResponse
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
	if data.Repos[0].SourceRepoID != "api" {
		t.Errorf("expected source_repo_id api, got %s", data.Repos[0].SourceRepoID)
	}
	if len(data.Repos[0].Groups) != 1 || data.Repos[0].Groups[0] != "backend" {
		t.Errorf("expected repo groups [backend], got %v", data.Repos[0].Groups)
	}
	if len(data.Groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(data.Groups))
	}
	if len(data.Agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(data.Agents))
	}
	if data.Agents[0].Name != "agent-1" {
		t.Errorf("expected agent name agent-1, got %s", data.Agents[0].Name)
	}
	if !data.Agents[1].CrossRepo {
		t.Error("expected agent-2 CrossRepo=true")
	}
}

func TestHandleActiveWorkspace_EmptyGroups(t *testing.T) {
	handler := handleActiveWorkspace(func() (*WorkspaceData, error) {
		return &WorkspaceData{
			Name: "ws",
			Path: "/ws",
			Repos: []WorkspaceRepo{
				{Name: "api", Path: "/code/api", Groups: []string{}},
			},
			Groups: []string{},
		}, nil
	})
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/active", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp workspaceResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !resp.Success {
		t.Fatal("expected success=true")
	}
	if len(resp.Data.Groups) != 0 {
		t.Errorf("expected empty groups, got %v", resp.Data.Groups)
	}
	if len(resp.Data.Repos[0].Groups) != 0 {
		t.Errorf("expected empty repo groups, got %v", resp.Data.Repos[0].Groups)
	}
	assertJSONArraysNotNull(t, resp, "groups", "agents")
}

func TestHandleActiveWorkspace_AgentsWithRepoAssignments(t *testing.T) {
	handler := handleActiveWorkspace(func() (*WorkspaceData, error) {
		return &WorkspaceData{
			Name: "ws",
			Path: "/ws",
			Agents: []WorkspaceAgentInfo{
				{Name: "worker-1", Repos: []string{"api", "auth"}, RepoGroups: []string{"backend"}, CrossRepo: false},
				{Name: "worker-2", Repos: []string{}, RepoGroups: []string{"frontend", "infra"}, CrossRepo: true},
			},
		}, nil
	})
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/active", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp workspaceResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !resp.Success {
		t.Fatal("expected success=true")
	}
	agents := resp.Data.Agents
	if len(agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(agents))
	}
	if agents[0].Name != "worker-1" {
		t.Errorf("expected worker-1, got %s", agents[0].Name)
	}
	if len(agents[0].Repos) != 2 {
		t.Errorf("expected 2 repos for worker-1, got %d", len(agents[0].Repos))
	}
	if len(agents[0].RepoGroups) != 1 || agents[0].RepoGroups[0] != "backend" {
		t.Errorf("expected repo_groups [backend], got %v", agents[0].RepoGroups)
	}
	if agents[0].CrossRepo {
		t.Error("expected worker-1 CrossRepo=false")
	}
	if !agents[1].CrossRepo {
		t.Error("expected worker-2 CrossRepo=true")
	}
	if len(agents[1].RepoGroups) != 2 {
		t.Errorf("expected 2 repo_groups for worker-2, got %d", len(agents[1].RepoGroups))
	}
}

func TestHandleActiveWorkspace_NilSlicesNormalized(t *testing.T) {
	handler := handleActiveWorkspace(func() (*WorkspaceData, error) {
		return &WorkspaceData{
			Name: "ws",
			Path: "/ws",
			Repos: []WorkspaceRepo{
				{Name: "api", Path: "/code/api"}, // Groups is nil
			},
			Agents: []WorkspaceAgentInfo{
				{Name: "agent-1"}, // Repos and RepoGroups are nil
			},
		}, nil
	})
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/active", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Parse raw JSON to check for null vs []
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	var data map[string]json.RawMessage
	if err := json.Unmarshal(raw["data"], &data); err != nil {
		t.Fatalf("decode data error: %v", err)
	}

	// Check repos[0].groups is [] not null
	var repos []map[string]json.RawMessage
	if err := json.Unmarshal(data["repos"], &repos); err != nil {
		t.Fatalf("decode repos error: %v", err)
	}
	if string(repos[0]["groups"]) == "null" {
		t.Error("expected repos[0].groups to be [] not null")
	}

	// Check agents[0].repos and agents[0].repo_groups are [] not null
	var agents []map[string]json.RawMessage
	if err := json.Unmarshal(data["agents"], &agents); err != nil {
		t.Fatalf("decode agents error: %v", err)
	}
	if string(agents[0]["repos"]) == "null" {
		t.Error("expected agents[0].repos to be [] not null")
	}
	if string(agents[0]["repo_groups"]) == "null" {
		t.Error("expected agents[0].repo_groups to be [] not null")
	}
}

// assertJSONArraysNotNull re-marshals the response data and checks that specified fields are [] not null.
func assertJSONArraysNotNull(t *testing.T, resp workspaceResponse, fields ...string) {
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
