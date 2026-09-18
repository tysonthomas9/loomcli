package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

var (
	seedWorktreeWorkspace string
	seedWorktreeAgent     string
	seedWorktreeRepo      string
	seedWorktreeFile      string
	seedWorktreeContent   string
	seedWorktreeMessage   string
)

// daemonSeedWorktreeCmd is part of the TEST-ONLY seeding seam (docs/adr/0001).
// It creates an agent's worktrees through the same composition the runtime's
// agent start uses — storeadapter.BuildWorkspaceDataForKey +
// localworkspace.{SelectAgentRepos,AgentWorktreePath,EnsureGitWorktree,
// RememberAgentWorktree} — so harnesses never construct worktree paths or
// branch names by hand. Optionally commits a file into one worktree, standing
// in for the change an agent's own git usage would produce. Hidden: never in
// help output; refuses to run without LOOM_TESTSUPPORT=1.
var daemonSeedWorktreeCmd = &cobra.Command{
	Use:    "seed-worktree",
	Short:  "TEST-ONLY: create an agent's worktrees via the product's own layout owners",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE:   runDaemonSeedWorktree,
}

func init() {
	f := daemonSeedWorktreeCmd.Flags()
	f.StringVar(&seedWorktreeWorkspace, "workspace", "", "Workspace key (default: active)")
	f.StringVar(&seedWorktreeAgent, "agent", "", "Agent name (required; must exist in the workspace)")
	f.StringVar(&seedWorktreeRepo, "repo", "", "Repo to commit into (default: the agent's only repo)")
	f.StringVar(&seedWorktreeFile, "file", "", "Path relative to the worktree root to write and commit")
	f.StringVar(&seedWorktreeContent, "content", "", "Content file for --file (default: stdin)")
	f.StringVar(&seedWorktreeMessage, "message", "seed worktree content", "Commit message for --file")
	daemonCmd.AddCommand(daemonSeedWorktreeCmd)
}

func runDaemonSeedWorktree(_ *cobra.Command, _ []string) error {
	if err := requireTestSupport(); err != nil {
		return err
	}
	if seedWorktreeAgent == "" {
		return fmt.Errorf("--agent is required")
	}
	var content []byte
	if seedWorktreeFile != "" {
		data, err := readSeedContent(seedWorktreeContent)
		if err != nil {
			return fmt.Errorf("read worktree file content: %w", err)
		}
		content = data
	}
	return cmdstore.WithStore(func(ctx context.Context, h *bootstrap.StoreHandle) error {
		return seedAgentWorktrees(ctx, h, content)
	})
}

// seedAgentWorktrees is the store-scoped body of seed-worktree: it mirrors the
// runtime's agent-start worktree flow, then applies the optional --file commit.
func seedAgentWorktrees(ctx context.Context, h *bootstrap.StoreHandle, content []byte) error {
	ws := seedWorktreeWorkspace
	if ws == "" {
		active, aerr := cmdstore.ActiveWorkspace(ctx, h.Store)
		if aerr != nil {
			return aerr
		}
		ws = active
	}
	agent, err := h.Store.Agents().Get(ctx, ws, seedWorktreeAgent)
	if err != nil {
		return fmt.Errorf("load agent %q: %w", seedWorktreeAgent, err)
	}
	wsData, err := storeadapter.BuildWorkspaceDataForKey(ctx, h.Store, ws)
	if err != nil {
		return fmt.Errorf("load workspace %q: %w", ws, err)
	}
	if wsData.Path == "" {
		return fmt.Errorf("workspace %q has no local path on this machine", ws)
	}

	created, err := ensureSeedWorktrees(wsData, agent)
	if err != nil {
		return err
	}
	if err := localworkspace.RememberAgentWorktree(ws, agent.Name, localworkspace.FirstWorktreePath(created)); err != nil {
		return fmt.Errorf("update local agent state: %w", err)
	}

	if seedWorktreeFile != "" {
		worktree, cerr := seedCommitWorktree(created)
		if cerr != nil {
			return cerr
		}
		if cerr := seedCommitFile(worktree, seedWorktreeFile, content, seedWorktreeMessage); cerr != nil {
			return cerr
		}
	}
	names := make([]string, 0, len(created))
	for name := range created {
		names = append(names, name)
	}
	fmt.Printf("seeded worktree: ws=%s agent=%s repos=%s\n", ws, agent.Name, strings.Join(names, ","))
	return nil
}

// ensureSeedWorktrees runs the runtime's exact worktree flow for each of the
// agent's repos and returns repo name → worktree path.
func ensureSeedWorktrees(wsData *ops.WorkspaceData, agent *domain.Agent) (map[string]string, error) {
	localRepos := make([]localworkspace.Repo, 0, len(wsData.Repos))
	for _, repo := range wsData.Repos {
		localRepos = append(localRepos, localworkspace.Repo{
			Name:   repo.Name,
			Path:   repo.Path,
			Groups: append([]string(nil), repo.Groups...),
		})
	}
	repos, err := localworkspace.SelectAgentRepos(localRepos, *agent)
	if err != nil {
		return nil, err
	}
	if len(repos) == 0 {
		return nil, fmt.Errorf("workspace %q has no repos for agent %q", wsData.ID, agent.Name)
	}
	created := make(map[string]string, len(repos))
	for _, repo := range repos {
		if repo.Path == "" {
			return nil, fmt.Errorf("repo %q has no local path on this machine", repo.Name)
		}
		target := localworkspace.AgentWorktreePath(wsData.Path, repo.Name, agent.Name)
		if werr := localworkspace.EnsureGitWorktree(repo.Path, target, agent.Name); werr != nil {
			return nil, fmt.Errorf("create worktree for repo %q: %w", repo.Name, werr)
		}
		created[repo.Name] = target
	}
	return created, nil
}

// seedCommitWorktree picks the worktree the --file commit lands in: the
// --repo flag when given, otherwise the agent's only repo.
func seedCommitWorktree(created map[string]string) (string, error) {
	if seedWorktreeRepo != "" {
		wt, ok := created[seedWorktreeRepo]
		if !ok {
			return "", fmt.Errorf("--repo %q is not among the agent's repos", seedWorktreeRepo)
		}
		return wt, nil
	}
	if len(created) != 1 {
		return "", fmt.Errorf("agent spans %d repos; pass --repo to pick the commit target", len(created))
	}
	for _, wt := range created {
		return wt, nil
	}
	return "", fmt.Errorf("no worktree created")
}

// seedCommitFile writes relPath inside the worktree and commits it, standing
// in for the change an agent's own git usage would produce.
func seedCommitFile(worktree, relPath string, content []byte, message string) error {
	dest := filepath.Join(worktree, relPath)
	if !strings.HasPrefix(filepath.Clean(dest), filepath.Clean(worktree)+string(filepath.Separator)) {
		return fmt.Errorf("--file must stay inside the worktree")
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("create --file parent dir: %w", err)
	}
	if err := os.WriteFile(dest, content, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", relPath, err)
	}
	for _, args := range [][]string{
		{"add", relPath},
		{"-c", "user.email=seed@loom.test", "-c", "user.name=loom-seed", "commit", "-m", message},
	} {
		cmd := exec.Command("git", append([]string{"-C", worktree}, args...)...) //nolint:gosec // G204: test-only CLI, fixed argv
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}
