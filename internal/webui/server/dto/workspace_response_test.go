package dto

import (
	"encoding/json"
	"testing"
)

func TestWorkspaceResponse_JSONRoundTrip(t *testing.T) {
	resp := WorkspaceResponse{
		ID:   "ws-1",
		Name: "dev",
		Path: "/home/user/dev",
		Repos: []WorkspaceRepo{
			{
				Name:          "frontend",
				Path:          "/home/user/dev/frontend",
				DefaultBranch: "main",
				Remote:        "origin",
				SourceRepoID:  "repo-1",
				Groups:        []string{"ui"},
			},
		},
		Groups: []string{"ui", "backend"},
		Agents: []WorkspaceAgentInfo{
			{
				Name:       "falcon",
				Repos:      []string{"frontend"},
				RepoGroups: []string{"ui"},
				CrossRepo:  true,
			},
		},
		Workspaces: []WorkspaceSummary{
			{
				ID:        "ws-2",
				Name:      "staging",
				Path:      "/home/user/staging",
				Active:    true,
				RepoCount: 3,
				IsDefault: false,
				Backend:   "docker",
			},
		},
		WorkspaceOrder:   []string{"ws-1", "ws-2"},
		DefaultWorkspace: "ws-1",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got WorkspaceResponse
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.ID != resp.ID {
		t.Errorf("ID = %q, want %q", got.ID, resp.ID)
	}
	if got.Name != resp.Name {
		t.Errorf("Name = %q, want %q", got.Name, resp.Name)
	}
	if got.Path != resp.Path {
		t.Errorf("Path = %q, want %q", got.Path, resp.Path)
	}
	if len(got.Repos) != 1 {
		t.Errorf("Repos len = %d, want 1", len(got.Repos))
	}
	if len(got.Groups) != 2 {
		t.Errorf("Groups len = %d, want 2", len(got.Groups))
	}
	if len(got.Agents) != 1 {
		t.Errorf("Agents len = %d, want 1", len(got.Agents))
	}
	if len(got.Workspaces) != 1 {
		t.Errorf("Workspaces len = %d, want 1", len(got.Workspaces))
	}
	if len(got.WorkspaceOrder) != 2 {
		t.Errorf("WorkspaceOrder len = %d, want 2", len(got.WorkspaceOrder))
	}
	if got.DefaultWorkspace != resp.DefaultWorkspace {
		t.Errorf("DefaultWorkspace = %q, want %q", got.DefaultWorkspace, resp.DefaultWorkspace)
	}
	// Verify nested sub-type fields
	if got.Repos[0].SourceRepoID != "repo-1" {
		t.Errorf("Repos[0].SourceRepoID = %q, want %q", got.Repos[0].SourceRepoID, "repo-1")
	}
	if !got.Agents[0].CrossRepo {
		t.Error("Agents[0].CrossRepo = false, want true")
	}
	if got.Workspaces[0].Backend != "docker" {
		t.Errorf("Workspaces[0].Backend = %q, want %q", got.Workspaces[0].Backend, "docker")
	}
}

func TestWorkspaceResponse_EmptyRepos(t *testing.T) {
	resp := WorkspaceResponse{
		Repos:      []WorkspaceRepo{},
		Groups:     []string{"g"},
		Agents:     []WorkspaceAgentInfo{},
		Workspaces: []WorkspaceSummary{},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}

	val, ok := raw["repos"]
	if !ok {
		t.Fatal("repos should be present when empty slice")
	}
	if string(val) != "[]" {
		t.Errorf("repos = %s, want []", val)
	}
}

func TestWorkspaceResponse_EmptyGroups(t *testing.T) {
	resp := WorkspaceResponse{
		Repos:      []WorkspaceRepo{},
		Groups:     []string{},
		Agents:     []WorkspaceAgentInfo{},
		Workspaces: []WorkspaceSummary{},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}

	val, ok := raw["groups"]
	if !ok {
		t.Fatal("groups should be present when empty slice")
	}
	if string(val) != "[]" {
		t.Errorf("groups = %s, want []", val)
	}
}

func TestWorkspaceResponse_EmptyAgents(t *testing.T) {
	resp := WorkspaceResponse{
		Repos:      []WorkspaceRepo{},
		Groups:     []string{},
		Agents:     []WorkspaceAgentInfo{},
		Workspaces: []WorkspaceSummary{},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}

	val, ok := raw["agents"]
	if !ok {
		t.Fatal("agents should be present when empty slice")
	}
	if string(val) != "[]" {
		t.Errorf("agents = %s, want []", val)
	}
}

func TestWorkspaceResponse_EmptyWorkspaces(t *testing.T) {
	resp := WorkspaceResponse{
		Repos:      []WorkspaceRepo{},
		Groups:     []string{},
		Agents:     []WorkspaceAgentInfo{},
		Workspaces: []WorkspaceSummary{},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}

	val, ok := raw["workspaces"]
	if !ok {
		t.Fatal("workspaces should be present when empty slice")
	}
	if string(val) != "[]" {
		t.Errorf("workspaces = %s, want []", val)
	}
}

// TestWorkspaceResponse_NilSlicesSerializeAsNull documents raw Go serialization
// behavior for nil slices. The mapping layer MUST initialize these slices to
// []T{} before constructing WorkspaceResponse. Sending null to the frontend
// is a bug — this test exists to document the footgun.
func TestWorkspaceResponse_NilSlicesSerializeAsNull(t *testing.T) {
	resp := WorkspaceResponse{
		Repos:      nil,
		Groups:     nil,
		Agents:     nil,
		Workspaces: nil,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}

	for _, field := range []string{"repos", "groups", "agents", "workspaces"} {
		val, ok := raw[field]
		if !ok {
			t.Errorf("%s field omitted from JSON output", field)
			continue
		}
		if string(val) != "null" {
			t.Errorf("%s = %s, want null", field, val)
		}
	}
}

func TestWorkspaceResponse_WorkspaceOrderOmitted(t *testing.T) {
	resp := WorkspaceResponse{
		Repos:          []WorkspaceRepo{},
		Groups:         []string{},
		Agents:         []WorkspaceAgentInfo{},
		Workspaces:     []WorkspaceSummary{},
		WorkspaceOrder: nil,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}

	if _, ok := raw["workspace_order"]; ok {
		t.Error("workspace_order should be omitted when nil")
	}
}

func TestWorkspaceResponse_WorkspaceOrderPresent(t *testing.T) {
	resp := WorkspaceResponse{
		Repos:          []WorkspaceRepo{},
		Groups:         []string{},
		Agents:         []WorkspaceAgentInfo{},
		Workspaces:     []WorkspaceSummary{},
		WorkspaceOrder: []string{"ws-1", "ws-2"},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}

	val, ok := raw["workspace_order"]
	if !ok {
		t.Fatal("workspace_order should be present when populated")
	}
	if string(val) != `["ws-1","ws-2"]` {
		t.Errorf("workspace_order = %s, want %s", val, `["ws-1","ws-2"]`)
	}
}

func TestWorkspaceSummary_JSONRoundTrip(t *testing.T) {
	ws := WorkspaceSummary{
		ID:        "ws-1",
		Name:      "dev",
		Path:      "/home/user/dev",
		Active:    true,
		RepoCount: 5,
		IsDefault: true,
		Backend:   "docker",
	}

	data, err := json.Marshal(ws)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got WorkspaceSummary
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got != ws {
		t.Errorf("got %+v, want %+v", got, ws)
	}
}

func TestWorkspaceSummary_BackendOmitted(t *testing.T) {
	ws := WorkspaceSummary{
		ID:   "ws-1",
		Name: "dev",
		Path: "/home/user/dev",
	}

	data, err := json.Marshal(ws)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}

	if _, ok := raw["backend"]; ok {
		t.Error("backend should be omitted when empty")
	}
}

func TestWorkspaceRepo_JSONRoundTrip(t *testing.T) {
	repo := WorkspaceRepo{
		Name:          "frontend",
		Path:          "/home/user/frontend",
		DefaultBranch: "main",
		Remote:        "origin",
		SourceRepoID:  "repo-1",
		Groups:        []string{"ui", "web"},
	}

	data, err := json.Marshal(repo)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got WorkspaceRepo
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.Name != repo.Name {
		t.Errorf("Name = %q, want %q", got.Name, repo.Name)
	}
	if got.SourceRepoID != repo.SourceRepoID {
		t.Errorf("SourceRepoID = %q, want %q", got.SourceRepoID, repo.SourceRepoID)
	}
	if len(got.Groups) != 2 {
		t.Errorf("Groups len = %d, want 2", len(got.Groups))
	}
}

func TestWorkspaceRepo_EmptyGroups(t *testing.T) {
	repo := WorkspaceRepo{Groups: []string{}}

	data, err := json.Marshal(repo)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}

	val, ok := raw["groups"]
	if !ok {
		t.Fatal("groups should be present when empty slice")
	}
	if string(val) != "[]" {
		t.Errorf("groups = %s, want []", val)
	}
}

func TestWorkspaceRepo_SourceRepoIDOmitted(t *testing.T) {
	repo := WorkspaceRepo{Groups: []string{}}

	data, err := json.Marshal(repo)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}

	if _, ok := raw["source_repo_id"]; ok {
		t.Error("source_repo_id should be omitted when empty")
	}
}

func TestWorkspaceAgentInfo_JSONRoundTrip(t *testing.T) {
	agent := WorkspaceAgentInfo{
		Name:       "falcon",
		Repos:      []string{"frontend", "backend"},
		RepoGroups: []string{"ui"},
		CrossRepo:  true,
	}

	data, err := json.Marshal(agent)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got WorkspaceAgentInfo
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.Name != agent.Name {
		t.Errorf("Name = %q, want %q", got.Name, agent.Name)
	}
	if len(got.Repos) != 2 {
		t.Errorf("Repos len = %d, want 2", len(got.Repos))
	}
	if len(got.RepoGroups) != 1 {
		t.Errorf("RepoGroups len = %d, want 1", len(got.RepoGroups))
	}
	if !got.CrossRepo {
		t.Error("CrossRepo = false, want true")
	}
}

func TestWorkspaceAgentInfo_EmptyRepos(t *testing.T) {
	agent := WorkspaceAgentInfo{
		Repos:      []string{},
		RepoGroups: []string{},
	}

	data, err := json.Marshal(agent)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}

	val, ok := raw["repos"]
	if !ok {
		t.Fatal("repos should be present when empty slice")
	}
	if string(val) != "[]" {
		t.Errorf("repos = %s, want []", val)
	}
}

func TestWorkspaceAgentInfo_EmptyRepoGroups(t *testing.T) {
	agent := WorkspaceAgentInfo{
		Repos:      []string{},
		RepoGroups: []string{},
	}

	data, err := json.Marshal(agent)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}

	val, ok := raw["repo_groups"]
	if !ok {
		t.Fatal("repo_groups should be present when empty slice")
	}
	if string(val) != "[]" {
		t.Errorf("repo_groups = %s, want []", val)
	}
}
