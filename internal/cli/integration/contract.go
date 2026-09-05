// Package integration reads the workspace's integration.yaml contract.
//
// It exists for one fact that no other loom code path can supply: the shared
// `local/union` worktrees. Those live in linked worktrees of clones that appear
// in no loom config at all — neither cli.DiscoverWorktrees (workspace repo
// clones) nor cli.DiscoverAgentWorktrees (agent worktrees) can reach them. The
// only place their paths are written down is
// `repos.<name>.local_integration.worktree` in the workspace's
// integration.yaml.
//
// An absent contract is not an error. Most installs have none, and health
// checks that read this must stay silent there.
package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

// SharedWorktree is one declared local-integration worktree.
type SharedWorktree struct {
	Repo   string // repo key in the contract
	Path   string // worktree path on disk
	Branch string // integration branch, e.g. "local/union"
	Clone  string // clone the worktree is linked from
}

// contractFile is the subset of integration.yaml this package understands.
// yaml.v3 ignores unknown fields by default, so the rest of the contract —
// which is large, hand-written and changes often — is not this package's
// problem.
type contractFile struct {
	Repos map[string]struct {
		LocalIntegration *struct {
			Branch   string `yaml:"branch"`
			Clone    string `yaml:"clone"`
			Worktree string `yaml:"worktree"`
		} `yaml:"local_integration"`
	} `yaml:"repos"`
}

// ContractPath returns the integration.yaml this workspace uses.
// $LOOM_INTEGRATION_CONTRACT overrides it, which is how tests and one-off
// operator runs point the scan at a scratch contract.
func ContractPath() string {
	if p := os.Getenv("LOOM_INTEGRATION_CONTRACT"); p != "" {
		return p
	}
	return filepath.Join(cli.GetWorktreesDir(), "integration.yaml")
}

// SharedWorktrees returns the declared local-integration worktrees that exist
// on disk, sorted by repo name.
//
// Returns (nil, nil) when there is no contract file: an install without one has
// no shared worktrees, which is a fact, not a failure.
func SharedWorktrees() ([]SharedWorktree, error) {
	return sharedWorktreesFrom(ContractPath())
}

func sharedWorktreesFrom(path string) ([]SharedWorktree, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: the workspace's own contract path, chosen by the operator
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read integration contract %s: %w", path, err)
	}

	var cf contractFile
	if err := yaml.Unmarshal(data, &cf); err != nil {
		return nil, fmt.Errorf("parse integration contract %s: %w", path, err)
	}

	names := make([]string, 0, len(cf.Repos))
	for name := range cf.Repos {
		names = append(names, name)
	}
	sort.Strings(names)

	var out []SharedWorktree
	for _, name := range names {
		li := cf.Repos[name].LocalIntegration
		if li == nil || li.Worktree == "" {
			continue
		}
		// A contract can outlive the worktree it names. Skipping the missing
		// ones keeps a stale entry from being reported as a problem forever.
		if _, statErr := os.Stat(li.Worktree); statErr != nil {
			continue
		}
		out = append(out, SharedWorktree{
			Repo:   name,
			Path:   li.Worktree,
			Branch: li.Branch,
			Clone:  li.Clone,
		})
	}
	return out, nil
}
