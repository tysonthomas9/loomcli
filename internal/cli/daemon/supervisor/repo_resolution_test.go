package supervisor

import (
	"testing"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

func TestEntryWithRuntimeRepoUsesSingleReposAffinity(t *testing.T) {
	entry, repo := entryWithRuntimeRepo(cfgpkg.AgentEntry{
		Worktree: "worker",
		Role:     "task",
		Repos:    []string{"slack-src"},
	})
	if repo != "slack-src" {
		t.Fatalf("repo = %q, want slack-src", repo)
	}
	if entry.Repo != "" {
		t.Fatalf("entry.Repo = %q, want empty so claim routing does not add a legacy repo label", entry.Repo)
	}
}

func TestEntryWithRuntimeRepoKeepsAmbiguousRepoEmpty(t *testing.T) {
	entry, repo := entryWithRuntimeRepo(cfgpkg.AgentEntry{
		Worktree: "worker",
		Role:     "task",
		Repos:    []string{"frontend", "backend"},
	})
	if repo != "" || entry.Repo != "" {
		t.Fatalf("repo = %q entry.Repo = %q, want empty", repo, entry.Repo)
	}
}
