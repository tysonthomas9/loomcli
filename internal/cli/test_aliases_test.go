package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

type AgentEntry = config.AgentEntry
type RuntimeConfig = config.RuntimeConfig
type RoleConfig = config.RoleConfig
type RepoConfig = config.RepoConfig
type WorkspaceConfig = config.WorkspaceConfig
type LoomConfig = config.LoomConfig
type Checkpoint = config.Checkpoint
type FleetClientConfig = config.FleetClientConfig

var LoadConfig = config.LoadConfig
var LoadRuntimeConfig = config.LoadRuntimeConfig
var ResolveActiveWorkspace = config.ResolveActiveWorkspace
var ValidateRemoteName = config.ValidateRemoteName
var GetWorkspaceDir = config.GetWorkspaceDir
var LoadCheckpoint = config.LoadCheckpoint
var SaveCheckpoint = config.SaveCheckpoint
var ClearCheckpoint = config.ClearCheckpoint

func intPtr(v int) *int    { return config.IntPtr(v) }
func boolPtr(v bool) *bool { return config.BoolPtr(v) }

var validateAgentRepos = config.ValidateAgentRepos
var resolveAgentRepos = config.ResolveAgentRepos
var fetchReadyIssues = FetchReadyIssues
var setDefaultIssueBackend = SetDefaultIssueBackend
var isFleetMode = IsFleetMode
var isFleetModeFromEnv = IsFleetModeFromEnv
var resolveIssueBackendType = ResolveIssueBackendType
var defaultIssueBackend = DefaultIssueBackend
var resetDefaultIssueBackend = ResetDefaultIssueBackend
var isFleetActive = IsFleetActive
var isFleetDBActive = IsFleetDBActive
var ensureSignalDir = EnsureSignalDir
var validateSignalDir = ValidateSignalDir
var validateWorktreeName = ValidateWorktreeName
var branchCompletion = BranchCompletion
var worktreeCompletion = WorktreeCompletion
var worktreeThenBranchCompletion = WorktreeThenBranchCompletion

type PromptData struct {
	AgentName    string
	WorktreeName string
	Role         string
	TaskID       string
}

func LoadPromptTemplate(path string, data PromptData) (string, error) {
	content, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		return "", fmt.Errorf("reading prompt template %s: %w", path, err)
	}
	tmpl, err := template.New(filepath.Base(path)).Parse(string(content))
	if err != nil {
		return "", fmt.Errorf("parsing prompt template %s: %w", path, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("executing prompt template %s: %w", path, err)
	}
	return buf.String(), nil
}

func InvokeAgentForConflicts(workDir, featureBranch, targetBranch string, conflicts []string) error {
	prompt := fmt.Sprintf("Resolve merge conflicts in: %s -> %s\n\nConflicted files:\n", featureBranch, targetBranch)
	for _, f := range conflicts {
		prompt += "  - " + f + "\n"
	}
	return InvokeAgent(workDir, prompt, "")
}
