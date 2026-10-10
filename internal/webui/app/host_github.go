package app

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/modbuilder"
)

// GitHubHost is the host GitHub connector as prwatch.Host sees it: the repo
// an agent's clone is, the viewer login, and github_read ops on a GitHub
// repo registered in the workspace. It is declared here, not imported, to
// keep this package's import fanout.
type GitHubHost interface {
	Repo(ctx context.Context, ws, repoPath string) (owner, repo string, err error)
	Viewer(ctx context.Context, ws, owner, repo string) (string, error)
	Read(ctx context.Context, ws, owner, repo, op string, args map[string]any) (map[string]any, error)
}

// HostGitHub is the host GitHub connector the agents' PR watches read
// through (OR10), as the host's own viewer, with no agent or bridge. nil
// without a store or vault key; PR watches are then unavailable.
func HostGitHub(cfg webui.ServerConfig) GitHubHost {
	disp := (&Server{config: cfg}).buildConnectorDispatcher()
	if disp == nil {
		return nil
	}
	return modbuilder.NewHostGitHub(cfg.Store, disp, cfg.LocalSettingsDir)
}
