package supervisor

import (
	"os/exec"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/agent"
)

// buildSandboxCommand provisions an OpenShell sandbox for ap and returns the
// unstarted `sandbox exec` command, recording the sandbox name + credential-revoke
// func on ap so postExitCleanup can fetch the work back, revoke, and delete it.
//
// The heavy sandbox / fleet-db / bootstrap logic lives in internal/cli/agent (the
// package that also owns the one-shot sandbox flow), reached via the existing
// supervisor→agent dependency. This keeps the supervisor package's import fan-out
// bounded; the method only marshals AgentProcess → agent.SandboxExecSpec.
func (s *Supervisor) buildSandboxCommand(ap *AgentProcess) (*exec.Cmd, error) {
	ap.Mu.Lock()
	epicID := ap.AssignedEpicID
	ap.Mu.Unlock()

	spec := agent.SandboxExecSpec{
		Worktree:      ap.Entry.Worktree,
		WorktreePath:  ap.WorktreePath,
		WorkspaceID:   s.WorkspaceID,
		ProjectDir:    s.ProjectDir,
		Role:          ap.Entry.Role,
		IsBuiltinRole: BuiltInRoles[ap.Entry.Role],
		PromptFile:    ap.RoleConfig.PromptFile,
		TaskFilter:    ap.RoleConfig.TaskFilter,
		EpicID:        epicID,
		Backend:       s.GetEffectiveBackend(ap),
	}
	if ap.RepoConfig != nil {
		spec.RepoRemoteURL = ap.RepoConfig.RemoteURL
	}

	cmd, name, revoke, err := agent.BuildSandboxExecCommand(spec)
	if err != nil {
		return nil, err
	}
	ap.Mu.Lock()
	ap.SandboxName = name
	ap.sandboxRevoke = revoke
	ap.Mu.Unlock()
	return cmd, nil
}

// cleanupSandbox fetches the branch the sandbox pushed, fast-forwards the host
// worktree, revokes the scoped credential, and deletes the sandbox. Called from
// postExitCleanup for execution:sandbox agents after the exec process exits.
func (s *Supervisor) cleanupSandbox(ap *AgentProcess) {
	ap.Mu.Lock()
	name := ap.SandboxName
	revoke := ap.sandboxRevoke
	ap.SandboxName = ""
	ap.sandboxRevoke = nil
	ap.Mu.Unlock()

	branch, _ := cli.GetCurrentBranch(ap.WorktreePath)
	agent.CleanupSandboxExec(s.ProjectDir, ap.WorktreePath, branch, name, revoke)
}
