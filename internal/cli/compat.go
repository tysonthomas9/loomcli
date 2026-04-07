// compat.go provides backward-compatible type aliases and function wrappers
// for types and functions that moved from the cli package to subpackages during
// the v2 refactoring. This prevents breakage in test files and external callers.
package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// --- Type aliases for config types ---

type AgentEntry = config.AgentEntry
type DaemonConfig = config.DaemonConfig
type DaemonSettings = config.DaemonSettings
type RestartPolicy = config.RestartPolicy
type RoleConfig = config.RoleConfig
type RepoConfig = config.RepoConfig
type WorkspaceConfig = config.WorkspaceConfig
type LoomConfig = config.LoomConfig
type Checkpoint = config.Checkpoint
type ProjectFile = config.ProjectFile
type FleetSettings = config.FleetSettings
type FleetClientConfig = config.FleetClientConfig
type FleetDBSettings = config.FleetDBSettings

// FleetDBServerConfig alias is in fleetdb_server.go

// --- Config constants ---

var CurrentConfigVersion = config.CurrentConfigVersion

// --- Function wrappers for config functions ---

var LoadConfig = config.LoadConfig
var SaveConfig = config.SaveConfig
var LoadDaemonConfig = config.LoadDaemonConfig
var LoadProjectFile = config.LoadProjectFile
var ResolveActiveWorkspace = config.ResolveActiveWorkspace
var IsWorkspaceMode = config.IsWorkspaceMode
var ResolveDaemonStatePath = config.ResolveDaemonStatePath
var ValidateRemoteName = config.ValidateRemoteName
var GetWorkspaceDir = config.GetWorkspaceDir
var LoadCheckpoint = config.LoadCheckpoint
var SaveCheckpoint = config.SaveCheckpoint
var ClearCheckpoint = config.ClearCheckpoint

func intPtr(v int) *int    { return config.IntPtr(v) }
func boolPtr(v bool) *bool { return config.BoolPtr(v) }

var validateAgentRepos = config.ValidateAgentRepos
var overlayDaemonSettings = config.OverlayDaemonSettings
var resolveAgentRepos = config.ResolveAgentRepos
var fetchReadyIssues = FetchReadyIssues
var setDefaultIssueBackend = SetDefaultIssueBackend
var isFleetMode = IsFleetMode
var isFleetModeFromEnv = IsFleetModeFromEnv
var resolveIssueBackendType = ResolveIssueBackendType
var defaultIssueBackend = DefaultIssueBackend
var isFleetActive = IsFleetActive
var isFleetDBActive = IsFleetDBActive
var ensureSignalDir = EnsureSignalDir
var validateSignalDir = ValidateSignalDir
var validateWorktreeName = ValidateWorktreeName
var resetProjectConfigVersionWarnOnce = config.ResetProjectConfigVersionWarnOnce
var branchCompletion = BranchCompletion
var worktreeCompletion = WorktreeCompletion
var worktreeThenBranchCompletion = WorktreeThenBranchCompletion

// PromptData is the template context for custom prompt files.
// Duplicated here for backward compat with tests that reference it from cli.
type PromptData struct {
	AgentName    string
	WorktreeName string
	Role         string
	TaskID       string
}

// LoadPromptTemplate reads a prompt template file and executes it.
// Duplicated here for backward compat with tests that reference it from cli.
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

// InvokeAgentForConflicts invokes the active backend to resolve merge conflicts.
// This was moved during refactoring; this stub maintains backward compat.
func InvokeAgentForConflicts(workDir, featureBranch, targetBranch string, conflicts []string) error {
	prompt := fmt.Sprintf("Resolve merge conflicts in: %s -> %s\n\nConflicted files:\n", featureBranch, targetBranch)
	for _, f := range conflicts {
		prompt += "  - " + f + "\n"
	}
	return InvokeAgent(workDir, prompt, "")
}
