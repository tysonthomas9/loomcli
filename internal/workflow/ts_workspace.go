package workflow

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type tsContextWorkspace struct {
	Key           string                     `json:"key"`
	Name          string                     `json:"name,omitempty"`
	State         string                     `json:"state,omitempty"`
	DefaultBranch string                     `json:"defaultBranch,omitempty"`
	Workflow      tsContextWorkspaceWorkflow `json:"workflow"`
	Runtime       tsContextWorkspaceRuntime  `json:"runtime,omitempty"`
	Repos         []tsContextWorkspaceRepo   `json:"repos,omitempty"`
	SelectedRepos []string                   `json:"selectedRepos,omitempty"`
	Env           []string                   `json:"env,omitempty"`
}

type tsContextWorkspaceWorkflow struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type tsContextWorkspaceRuntime struct {
	ProfileName string   `json:"profileName,omitempty"`
	Provider    string   `json:"provider,omitempty"`
	Version     string   `json:"version,omitempty"`
	Repos       []string `json:"repos,omitempty"`
	Env         []string `json:"env,omitempty"`
}

type tsContextWorkspaceRepo struct {
	Name          string   `json:"name"`
	SourceRepoID  string   `json:"sourceRepoId,omitempty"`
	DefaultBranch string   `json:"defaultBranch,omitempty"`
	Groups        []string `json:"groups,omitempty"`
	Found         bool     `json:"found"`
}

func tsContextWorkspaceForRun(ctx context.Context, st store.Store, run *domain.WorkflowRun, def *domain.WorkflowDefinition, profile *tsContextRuntimeProfile) tsContextWorkspace {
	workspace := tsContextWorkspace{
		Key: run.WorkspaceKey,
		Workflow: tsContextWorkspaceWorkflow{
			Name:    run.WorkflowName,
			Version: run.WorkflowVersion,
		},
		Env: workflowManifestEnv(def),
	}
	if def != nil {
		workspace.Workflow.Name = firstNonEmptyString(workspace.Workflow.Name, def.Name)
		workspace.Workflow.Version = firstNonEmptyString(workspace.Workflow.Version, def.Version)
		workspace.Runtime.ProfileName = def.RuntimeProfileName
	}
	if st != nil && st.Workspaces() != nil {
		if loaded, err := st.Workspaces().Get(ctx, run.WorkspaceKey); err == nil && loaded != nil {
			workspace.Name = loaded.Name
			workspace.State = string(loaded.State)
			workspace.DefaultBranch = loaded.DefaultBranch
		}
	}
	workflowRepos := workflowManifestRepos(def)
	if profile != nil {
		workspace.Runtime.ProfileName = firstNonEmptyString(workspace.Runtime.ProfileName, profile.Name)
		workspace.Runtime.Provider = profile.Provider
		workspace.Runtime.Version = profile.Version
		workspace.Runtime.Repos = cloneStrings(profile.Repos)
		workspace.Runtime.Env = cloneStrings(profile.Env)
		workspace.SelectedRepos = cloneStrings(profile.Repos)
	}
	if len(workspace.SelectedRepos) == 0 {
		workspace.SelectedRepos = workflowRepos
	}
	workspace.SelectedRepos = compactUniqueStrings(workspace.SelectedRepos)
	workspace.Repos = tsContextWorkspaceRepos(ctx, st, run.WorkspaceKey, workspace.SelectedRepos)
	return workspace
}

func tsContextWorkspaceRepos(ctx context.Context, st store.Store, workspaceKey string, selected []string) []tsContextWorkspaceRepo {
	if st == nil || st.Repos() == nil {
		return unresolvedWorkspaceRepos(selected)
	}
	repos, err := st.Repos().List(ctx, workspaceKey)
	if err != nil {
		return unresolvedWorkspaceRepos(selected)
	}
	if len(selected) == 0 {
		out := make([]tsContextWorkspaceRepo, 0, len(repos))
		for _, repo := range repos {
			out = append(out, tsContextWorkspaceRepoFromDomain(repo, true))
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		return out
	}
	byName := make(map[string]*domain.Repo, len(repos)*2)
	for _, repo := range repos {
		if repo == nil {
			continue
		}
		if repo.Name != "" {
			byName[repo.Name] = repo
		}
		if repo.SourceRepoID != "" {
			byName[repo.SourceRepoID] = repo
		}
	}
	out := make([]tsContextWorkspaceRepo, 0, len(selected))
	for _, name := range selected {
		if repo := byName[name]; repo != nil {
			out = append(out, tsContextWorkspaceRepoFromDomain(repo, true))
			continue
		}
		out = append(out, tsContextWorkspaceRepo{Name: name, Found: false})
	}
	return out
}

func tsContextWorkspaceRepoFromDomain(repo *domain.Repo, found bool) tsContextWorkspaceRepo {
	if repo == nil {
		return tsContextWorkspaceRepo{Found: found}
	}
	return tsContextWorkspaceRepo{
		Name:          repo.Name,
		SourceRepoID:  repo.SourceRepoID,
		DefaultBranch: repo.DefaultBranch,
		Groups:        cloneStrings(repo.Groups),
		Found:         found,
	}
}

func unresolvedWorkspaceRepos(selected []string) []tsContextWorkspaceRepo {
	out := make([]tsContextWorkspaceRepo, 0, len(selected))
	for _, name := range compactUniqueStrings(selected) {
		out = append(out, tsContextWorkspaceRepo{Name: name, Found: false})
	}
	return out
}

func workflowManifestRepos(def *domain.WorkflowDefinition) []string {
	repos, _ := workflowManifestRuntimePolicy(def)
	return repos
}

func workflowManifestEnv(def *domain.WorkflowDefinition) []string {
	_, env := workflowManifestRuntimePolicy(def)
	return env
}

func workflowManifestRuntimePolicy(def *domain.WorkflowDefinition) ([]string, []string) {
	if def == nil || len(def.Manifest) == 0 {
		return nil, nil
	}
	var manifest struct {
		Repos []string `json:"repos"`
		Env   []string `json:"env"`
	}
	if err := json.Unmarshal(def.Manifest, &manifest); err != nil {
		return nil, nil
	}
	return compactUniqueStrings(manifest.Repos), compactUniqueStrings(manifest.Env)
}

func appendRuntimeWorkspaceReadEvent(ctx context.Context, st store.Store, run *domain.WorkflowRun, params map[string]any) {
	data := copyAnyMap(params)
	data["workflow_run_id"] = run.RunID
	_, _ = st.RunEvents().Append(ctx, store.RunEventAppend{
		WorkspaceKey:  run.WorkspaceKey,
		WorkflowRunID: run.RunID,
		Type:          "runtime_workspace_read",
		Message:       "workflow runtime workspace read from TypeScript WorkflowContext",
		Data:          mustJSON(data),
	})
}

func compactUniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
