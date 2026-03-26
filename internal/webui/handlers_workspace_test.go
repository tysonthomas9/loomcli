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

// TestResolveWorkspaceOverride_ByUUID tests that resolveWorkspaceOverride
// finds the correct workspace when the target is a UUID matching the summary's
// ID field (post-T2 behavior).
func TestResolveWorkspaceOverride_ByUUID(t *testing.T) {
	orig := &WorkspaceData{
		Name: "default-ws",
		Path: "/workspaces/default",
		ID:   "default-uuid",
		Workspaces: []WorkspaceSummary{
			{ID: "default-uuid", Name: "default-ws", Path: "/workspaces/default", Active: true, RepoCount: 1},
			{ID: "target-uuid-1234", Name: "target-ws", Path: "/tmp/test-resolve-uuid", Active: false, RepoCount: 2},
		},
	}

	result := resolveWorkspaceOverride(orig, "target-uuid-1234")
	// resolveWorkspaceOverride will return nil if the path doesn't exist on
	// disk (it tries to load loom.yaml). But it should still find the summary
	// and return a non-nil result with the correct name/id/path even if repos
	// are empty.
	if result == nil {
		// The function loads from disk, which won't have data. That's fine --
		// but it should still return a result because the summary was found.
		t.Fatal("expected non-nil result when target UUID matches a summary")
	}
	if result.Name != "target-ws" {
		t.Errorf("expected name 'target-ws', got %q", result.Name)
	}
	if result.ID != "target-uuid-1234" {
		t.Errorf("expected ID 'target-uuid-1234', got %q", result.ID)
	}
	if result.Path != "/tmp/test-resolve-uuid" {
		t.Errorf("expected path '/tmp/test-resolve-uuid', got %q", result.Path)
	}
}

// TestResolveWorkspaceOverride_ByName tests that resolveWorkspaceOverride
// still works when matching by human-readable name (pre-T2 / backward compat).
func TestResolveWorkspaceOverride_ByName(t *testing.T) {
	orig := &WorkspaceData{
		Name: "default-ws",
		Path: "/workspaces/default",
		Workspaces: []WorkspaceSummary{
			{Name: "default-ws", Path: "/workspaces/default", Active: true, RepoCount: 1},
			{ID: "some-uuid", Name: "other-ws", Path: "/tmp/test-resolve-name", Active: false, RepoCount: 3},
		},
	}

	result := resolveWorkspaceOverride(orig, "other-ws")
	if result == nil {
		t.Fatal("expected non-nil result when target name matches a summary")
	}
	if result.Name != "other-ws" {
		t.Errorf("expected name 'other-ws', got %q", result.Name)
	}
	if result.ID != "some-uuid" {
		t.Errorf("expected ID 'some-uuid', got %q", result.ID)
	}
	if result.Path != "/tmp/test-resolve-name" {
		t.Errorf("expected path '/tmp/test-resolve-name', got %q", result.Path)
	}
}

// TestResolveWorkspaceOverride_NotFound tests that resolveWorkspaceOverride
// returns nil when the target matches neither ID nor Name.
func TestResolveWorkspaceOverride_NotFound(t *testing.T) {
	orig := &WorkspaceData{
		Name: "default-ws",
		Workspaces: []WorkspaceSummary{
			{ID: "uuid-1", Name: "ws-one", Path: "/ws/one"},
		},
	}

	result := resolveWorkspaceOverride(orig, "nonexistent")
	if result != nil {
		t.Errorf("expected nil for unknown target, got %+v", result)
	}
}

// TestHandleWorkspace_UUIDHeaderMatchesCurrentID tests that when the Workspace
// header carries a UUID that matches the current workspace's ID, the handler
// does NOT trigger override (requestedWS != data.ID check). The response
// should return the default workspace data unchanged.
func TestHandleWorkspace_UUIDHeaderMatchesCurrentID(t *testing.T) {
	handler := handleWorkspace(func() (*WorkspaceData, error) {
		return &WorkspaceData{
			ID:   "ws-uuid-current",
			Name: "current-ws",
			Path: "/workspaces/current",
			Repos: []WorkspaceRepo{
				{Name: "api", Path: "/code/api", DefaultBranch: "main", Remote: "origin"},
			},
			Workspaces: []WorkspaceSummary{
				{ID: "ws-uuid-current", Name: "current-ws", Path: "/workspaces/current", Active: true, RepoCount: 1},
				{ID: "ws-uuid-other", Name: "other-ws", Path: "/workspaces/other", Active: false, RepoCount: 2},
			},
		}, nil
	})

	// Set Workspace header to the current workspace's UUID -- should NOT override
	req := httptest.NewRequest(http.MethodGet, "/api/workspace", nil)
	req.Header.Set("Workspace", "ws-uuid-current")
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
	// Should still be the current workspace, not overridden
	if resp.Data.Name != "current-ws" {
		t.Errorf("expected name 'current-ws', got %q", resp.Data.Name)
	}
	if resp.Data.ID != "ws-uuid-current" {
		t.Errorf("expected ID 'ws-uuid-current', got %q", resp.Data.ID)
	}
	// Repos from the current workspace should be preserved
	if len(resp.Data.Repos) != 1 {
		t.Fatalf("expected 1 repo (current ws), got %d", len(resp.Data.Repos))
	}
	if resp.Data.Repos[0].Name != "api" {
		t.Errorf("expected repo name 'api', got %q", resp.Data.Repos[0].Name)
	}
}

// TestHandleWorkspace_NameHeaderMatchesCurrentName tests that when the Workspace
// header carries a name that matches the current workspace's Name, the handler
// does NOT trigger override. This is the pre-T2 equivalent of the UUID test above.
func TestHandleWorkspace_NameHeaderMatchesCurrentName(t *testing.T) {
	handler := handleWorkspace(func() (*WorkspaceData, error) {
		return &WorkspaceData{
			ID:   "ws-uuid-111",
			Name: "my-workspace",
			Path: "/workspaces/mine",
			Repos: []WorkspaceRepo{
				{Name: "svc", Path: "/code/svc", DefaultBranch: "main", Remote: "origin"},
			},
			Workspaces: []WorkspaceSummary{
				{ID: "ws-uuid-111", Name: "my-workspace", Path: "/workspaces/mine", Active: true},
			},
		}, nil
	})

	req := httptest.NewRequest(http.MethodGet, "/api/workspace", nil)
	req.Header.Set("Workspace", "my-workspace")
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
	if resp.Data.Name != "my-workspace" {
		t.Errorf("expected name 'my-workspace', got %q", resp.Data.Name)
	}
	// Repos should be from the current workspace (not overridden)
	if len(resp.Data.Repos) != 1 || resp.Data.Repos[0].Name != "svc" {
		t.Errorf("expected original repos preserved, got %v", resp.Data.Repos)
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
