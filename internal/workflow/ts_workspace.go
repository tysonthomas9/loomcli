package workflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
	Skills        []tsContextWorkspaceSkill  `json:"skills,omitempty"`
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

type tsContextWorkspaceSkill struct {
	Name          string            `json:"name"`
	Description   string            `json:"description,omitempty"`
	Source        string            `json:"source"`
	Compatibility string            `json:"compatibility,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
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
	workspace.Skills = tsContextWorkspaceSkills(profile)
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

func appendRuntimeWorkspaceSkillsReadEvent(ctx context.Context, st store.Store, run *domain.WorkflowRun, params map[string]any) {
	data := copyAnyMap(params)
	data["workflow_run_id"] = run.RunID
	_, _ = st.RunEvents().Append(ctx, store.RunEventAppend{
		WorkspaceKey:  run.WorkspaceKey,
		WorkflowRunID: run.RunID,
		Type:          "runtime_workspace_skills_read",
		Message:       "workflow runtime workspace skills read from TypeScript WorkflowContext",
		Data:          mustJSON(data),
	})
}

func tsContextWorkspaceSkills(profile *tsContextRuntimeProfile) []tsContextWorkspaceSkill {
	roots := runtimeWorkspaceSkillRoots(profile)
	if len(roots) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	out := make([]tsContextWorkspaceSkill, 0)
	for _, root := range roots {
		for _, skill := range discoverRuntimeWorkspaceSkills(root) {
			if skill.Name == "" || seen[skill.Name] {
				continue
			}
			seen[skill.Name] = true
			out = append(out, skill)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func runtimeWorkspaceSkillRoots(profile *tsContextRuntimeProfile) []string {
	if profile == nil {
		return nil
	}
	cwd := strings.TrimSpace(profile.CWD)
	dirs := compactUniqueStrings(profile.WorkspaceSkillDirs)
	if cwd == "" && len(dirs) == 0 {
		return nil
	}
	base := resolveRuntimeWorkspacePath(runtimeProfileProjectRoot(profile.SourcePath), cwd)
	if base == "" {
		return nil
	}
	if len(dirs) == 0 {
		dirs = []string{filepath.Join(".agents", "skills")}
	}
	roots := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" || filepath.IsAbs(dir) {
			continue
		}
		roots = append(roots, filepath.Clean(filepath.Join(base, dir)))
	}
	return compactUniqueStrings(roots)
}

func runtimeProfileProjectRoot(sourcePath string) string {
	sourcePath = strings.TrimSpace(sourcePath)
	if sourcePath == "" || strings.HasPrefix(sourcePath, "control-plane:") || strings.Contains(sourcePath, "://") {
		return ""
	}
	if !filepath.IsAbs(sourcePath) {
		abs, err := filepath.Abs(sourcePath)
		if err != nil {
			return ""
		}
		sourcePath = abs
	}
	dir := filepath.Dir(filepath.Clean(sourcePath))
	for {
		if filepath.Base(dir) == ".loom" {
			return filepath.Dir(dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return filepath.Dir(filepath.Clean(sourcePath))
}

func resolveRuntimeWorkspacePath(projectRoot, cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return projectRoot
	}
	if filepath.IsAbs(cwd) {
		clean := filepath.Clean(cwd)
		if isPathWithin(projectRoot, clean) {
			return clean
		}
		return ""
	}
	if projectRoot == "" {
		return ""
	}
	resolved := filepath.Clean(filepath.Join(projectRoot, cwd))
	if !isPathWithin(projectRoot, resolved) {
		return ""
	}
	return resolved
}

func isPathWithin(parent, child string) bool {
	parent = filepath.Clean(strings.TrimSpace(parent))
	child = filepath.Clean(strings.TrimSpace(child))
	if parent == "" || child == "" {
		return false
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

func discoverRuntimeWorkspaceSkills(root string) []tsContextWorkspaceSkill {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	out := make([]tsContextWorkspaceSkill, 0, len(entries))
	for _, entry := range entries {
		if entry == nil || !entry.IsDir() {
			continue
		}
		skill, ok := readRuntimeWorkspaceSkill(filepath.Join(root, entry.Name(), "SKILL.md"), entry.Name())
		if ok {
			out = append(out, skill)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func readRuntimeWorkspaceSkill(path, fallbackName string) (tsContextWorkspaceSkill, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return tsContextWorkspaceSkill{}, false
	}
	fields := skillFrontmatterFields(data)
	name := firstNonEmptyString(fields["name"], fallbackName)
	if name == "" {
		return tsContextWorkspaceSkill{}, false
	}
	metadata := make(map[string]string)
	for key, value := range fields {
		switch key {
		case "name", "description", "compatibility":
			continue
		default:
			metadata[key] = value
		}
	}
	skill := tsContextWorkspaceSkill{
		Name:          name,
		Description:   fields["description"],
		Source:        "runtime_workspace",
		Compatibility: fields["compatibility"],
	}
	if len(metadata) > 0 {
		skill.Metadata = metadata
	}
	return skill, true
}

func skillFrontmatterFields(data []byte) map[string]string {
	text := strings.TrimPrefix(string(data), "\ufeff")
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(strings.TrimSuffix(lines[0], "\r")) != "---" {
		return nil
	}
	fields := map[string]string{}
	for _, raw := range lines[1:] {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "---" {
			break
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = trimFrontmatterString(value)
		if key == "" || value == "" {
			continue
		}
		fields[key] = value
	}
	return fields
}

func trimFrontmatterString(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		first, last := value[0], value[len(value)-1]
		if (first == '\'' && last == '\'') || (first == '"' && last == '"') {
			return strings.TrimSpace(value[1 : len(value)-1])
		}
	}
	return value
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
