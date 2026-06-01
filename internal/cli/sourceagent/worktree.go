package sourceagent

import (
	"fmt"
	"strings"

	workspacecli "github.com/tysonthomas9/loomcli/internal/cli/workspace"
)

type Worktree struct {
	Instance string `json:"instance"`
	Repo     string `json:"repo"`
	Path     string `json:"path"`
}

func ProvisionWorktree(instanceName string, repos []string) (*Worktree, error) {
	repo, ok := SingleRepo(repos)
	if !ok {
		return nil, nil
	}
	target, err := workspacecli.ResolveAgentTarget(instanceName, repo)
	if err != nil {
		return nil, fmt.Errorf("prepare worktree for agent %q repo %q: %w", instanceName, repo, err)
	}
	return &Worktree{Instance: instanceName, Repo: repo, Path: target.WorkDir}, nil
}

func SingleRepo(repos []string) (string, bool) {
	var selected string
	for _, repo := range repos {
		repo = strings.TrimSpace(repo)
		if repo == "" || repo == "." {
			continue
		}
		if selected != "" && selected != repo {
			return "", false
		}
		selected = repo
	}
	return selected, selected != ""
}
