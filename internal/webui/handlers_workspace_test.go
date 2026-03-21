package webui

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleWorkspace_NilConfigFn(t *testing.T) {
	handler := handleWorkspace(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/workspace", nil)
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
	if len(resp.Data.Groups) != 0 {
		t.Fatalf("expected empty groups, got %d", len(resp.Data.Groups))
	}
	if len(resp.Data.Agents) != 0 {
		t.Fatalf("expected empty agents, got %d", len(resp.Data.Agents))
	}
	// Verify JSON has [] not null for arrays
	assertJSONArraysNotNull(t, resp, "repos", "groups", "agents", "workspaces")
}

func TestHandleWorkspace_EmptyRepos(t *testing.T) {
	handler := handleWorkspace(func() (*WorkspaceData, error) {
		return nil, nil
	})
	req := httptest.NewRequest(http.MethodGet, "/api/workspace", nil)
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

func TestHandleWorkspace_WithRepos(t *testing.T) {
	handler := handleWorkspace(func() (*WorkspaceData, error) {
		return &WorkspaceData{
			Name: "myworkspace",
			Path: "/workspaces/myworkspace",
			Repos: []WorkspaceRepo{
				{Name: "payments/api", Path: "/workspaces/payments/api", DefaultBranch: "main", Remote: "origin"},
				{Name: "auth/service", Path: "/workspaces/auth/service", DefaultBranch: "develop", Remote: "upstream"},
			},
		}, nil
	})
	req := httptest.NewRequest(http.MethodGet, "/api/workspace", nil)
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
	if resp.Data.Path != "/workspaces/myworkspace" {
		t.Errorf("expected path /workspaces/myworkspace, got %s", resp.Data.Path)
	}
	if len(resp.Data.Repos) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(resp.Data.Repos))
	}
	if resp.Data.Repos[0].Name != "payments/api" {
		t.Errorf("expected name payments/api, got %s", resp.Data.Repos[0].Name)
	}
	if resp.Data.Repos[1].Path != "/workspaces/auth/service" {
		t.Errorf("expected path /workspaces/auth/service, got %s", resp.Data.Repos[1].Path)
	}
	if resp.Data.Repos[1].DefaultBranch != "develop" {
		t.Errorf("expected default_branch develop, got %s", resp.Data.Repos[1].DefaultBranch)
	}
	if resp.Data.Repos[1].Remote != "upstream" {
		t.Errorf("expected remote upstream, got %s", resp.Data.Repos[1].Remote)
	}
}

func TestHandleWorkspace_ConfigError(t *testing.T) {
	handler := handleWorkspace(func() (*WorkspaceData, error) {
		return nil, errors.New("config broken")
	})
	req := httptest.NewRequest(http.MethodGet, "/api/workspace", nil)
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
	if resp.Error != "failed to load workspace config" {
		t.Errorf("unexpected error message: %s", resp.Error)
	}
}

func TestHandleWorkspace_NilRepos(t *testing.T) {
	handler := handleWorkspace(func() (*WorkspaceData, error) {
		return &WorkspaceData{Name: "ws", Path: "/ws"}, nil
	})
	req := httptest.NewRequest(http.MethodGet, "/api/workspace", nil)
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
	if resp.Data.Repos == nil {
		t.Fatal("expected non-nil repos slice")
	}
	if len(resp.Data.Repos) != 0 {
		t.Fatalf("expected empty repos, got %d", len(resp.Data.Repos))
	}
	// Verify all nil slices are normalized
	assertJSONArraysNotNull(t, resp, "repos", "groups", "agents", "workspaces")
}

func TestHandleWorkspace_FullResponse(t *testing.T) {
	handler := handleWorkspace(func() (*WorkspaceData, error) {
		return &WorkspaceData{
			Name: "myworkspace",
			Path: "/workspaces/myworkspace",
			Repos: []WorkspaceRepo{
				{
					Name:          "api",
					Path:          "/code/api",
					DefaultBranch: "main",
					Remote:        "origin",
					SourceRepoID:  "api",
					Groups:        []string{"backend"},
				},
				{
					Name:          "web",
					Path:          "/code/web",
					DefaultBranch: "main",
					Remote:        "origin",
					SourceRepoID:  "web",
					Groups:        []string{"frontend"},
				},
			},
			Groups: []string{"backend", "frontend"},
			Agents: []WorkspaceAgentInfo{
				{Name: "agent-1", Repos: []string{"api"}, RepoGroups: []string{"backend"}, CrossRepo: false},
				{Name: "agent-2", Repos: []string{"web"}, RepoGroups: []string{"frontend"}, CrossRepo: true},
			},
			Workspaces: []WorkspaceSummary{
				{Name: "myworkspace", Path: "/workspaces/myworkspace", Active: true, RepoCount: 2},
				{Name: "other", Path: "/workspaces/other", Active: false, RepoCount: 1},
			},
		}, nil
	})
	req := httptest.NewRequest(http.MethodGet, "/api/workspace", nil)
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

	// Verify repos
	if len(resp.Data.Repos) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(resp.Data.Repos))
	}
	if resp.Data.Repos[0].SourceRepoID != "api" {
		t.Errorf("expected source_repo_id api, got %s", resp.Data.Repos[0].SourceRepoID)
	}
	if len(resp.Data.Repos[0].Groups) != 1 || resp.Data.Repos[0].Groups[0] != "backend" {
		t.Errorf("expected groups [backend], got %v", resp.Data.Repos[0].Groups)
	}

	// Verify groups
	if len(resp.Data.Groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(resp.Data.Groups))
	}

	// Verify agents
	if len(resp.Data.Agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(resp.Data.Agents))
	}
	if resp.Data.Agents[0].Name != "agent-1" {
		t.Errorf("expected agent name agent-1, got %s", resp.Data.Agents[0].Name)
	}
	if !resp.Data.Agents[1].CrossRepo {
		t.Error("expected agent-2 cross_repo=true")
	}

	// Verify workspaces
	if len(resp.Data.Workspaces) != 2 {
		t.Fatalf("expected 2 workspaces, got %d", len(resp.Data.Workspaces))
	}
	if resp.Data.Workspaces[0].Name != "myworkspace" {
		t.Errorf("expected workspace name myworkspace, got %s", resp.Data.Workspaces[0].Name)
	}
	if !resp.Data.Workspaces[0].Active {
		t.Error("expected myworkspace active=true")
	}
	if resp.Data.Workspaces[0].RepoCount != 2 {
		t.Errorf("expected repo_count 2, got %d", resp.Data.Workspaces[0].RepoCount)
	}
	if resp.Data.Workspaces[1].Active {
		t.Error("expected other workspace active=false")
	}
}

func TestHandleWorkspace_NilSlicesAsEmptyArrays(t *testing.T) {
	// Return data with nil slices inside repos and agents
	handler := handleWorkspace(func() (*WorkspaceData, error) {
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
	req := httptest.NewRequest(http.MethodGet, "/api/workspace", nil)
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

func TestHandleWorkspace_WorkspacesSummary(t *testing.T) {
	handler := handleWorkspace(func() (*WorkspaceData, error) {
		return &WorkspaceData{
			Name: "active-ws",
			Path: "/workspaces/active",
			Repos: []WorkspaceRepo{
				{Name: "api", Path: "/code/api", DefaultBranch: "main", Remote: "origin"},
			},
			Workspaces: []WorkspaceSummary{
				{Name: "active-ws", Path: "/workspaces/active", Active: true, RepoCount: 1},
				{Name: "staging", Path: "/workspaces/staging", Active: false, RepoCount: 3},
				{Name: "testing", Path: "/workspaces/testing", Active: false, RepoCount: 2},
			},
		}, nil
	})
	req := httptest.NewRequest(http.MethodGet, "/api/workspace", nil)
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
	if len(resp.Data.Workspaces) != 3 {
		t.Fatalf("expected 3 workspaces, got %d", len(resp.Data.Workspaces))
	}

	// Verify only one workspace is active
	activeCount := 0
	for _, ws := range resp.Data.Workspaces {
		if ws.Active {
			activeCount++
			if ws.Name != "active-ws" {
				t.Errorf("expected active workspace to be active-ws, got %s", ws.Name)
			}
		}
	}
	if activeCount != 1 {
		t.Errorf("expected exactly 1 active workspace, got %d", activeCount)
	}
}

func TestHandleWorkspace_SingleWorkspace(t *testing.T) {
	handler := handleWorkspace(func() (*WorkspaceData, error) {
		return &WorkspaceData{
			Name: "only-ws",
			Path: "/workspaces/only",
			Workspaces: []WorkspaceSummary{
				{Name: "only-ws", Path: "/workspaces/only", Active: true, RepoCount: 0},
			},
		}, nil
	})
	req := httptest.NewRequest(http.MethodGet, "/api/workspace", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp workspaceResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(resp.Data.Workspaces) != 1 {
		t.Fatalf("expected 1 workspace, got %d", len(resp.Data.Workspaces))
	}
	if !resp.Data.Workspaces[0].Active {
		t.Error("expected single workspace to be active")
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
