package git

import (
	"errors"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

func TestPushCommandContract(t *testing.T) {
	if len(pushCmd.Aliases) != 0 {
		t.Fatalf("push retains merge alias: %v", pushCmd.Aliases)
	}
	if err := pushCmd.Args(pushCmd, []string{"runner", "main"}); err == nil {
		t.Fatal("target branch positional argument accepted")
	}
	if pushCmd.Flags().Lookup("all") != nil || pushCmd.Flags().Lookup("workspace") != nil {
		t.Fatal("bulk/workspace push flags remain")
	}
}

func TestFeatureBranchRejectsDefaultAndDetachedWorktrees(t *testing.T) {
	for _, wt := range []cli.WorktreeInfo{
		{Branch: "main", Repo: &config.RepoConfig{DefaultBranch: "main"}},
		{Branch: ""},
	} {
		if _, err := featureBranch(wt); err == nil {
			t.Fatalf("featureBranch(%+v) accepted a non-feature branch", wt)
		}
	}
}

func TestFeaturePublicationReturnsUpstreamPushFailure(t *testing.T) {
	var command string
	deps := &cli.Deps{Git: gitRunner(func(_ string, args ...string) cli.CommandResult {
		command = strings.Join(args, " ")
		return cli.CommandResult{Err: errors.New("remote rejected")}
	})}

	err := gitPushRemote(deps, "/repo", "upstream", "feature/safe")
	if err == nil || !strings.Contains(err.Error(), "remote rejected") {
		t.Fatalf("push error = %v, want upstream failure", err)
	}
	if command != "push upstream feature/safe" {
		t.Fatalf("push command = %q", command)
	}
}

func TestResolveFeatureWorktreeRejectsAmbiguousRepository(t *testing.T) {
	worktrees := []cli.WorktreeInfo{
		{Name: "runner", Branch: "stack/a", Repo: &config.RepoConfig{Name: "one"}},
		{Name: "runner", Branch: "stack/b", Repo: &config.RepoConfig{Name: "two"}},
	}
	if _, err := resolveFeatureWorktree(worktrees, "runner", ""); err == nil {
		t.Fatal("ambiguous repository was accepted")
	}
	wt, err := resolveFeatureWorktree(worktrees, "runner", "two")
	if err != nil || wt.Branch != "stack/b" {
		t.Fatalf("explicit repository resolution = %+v, %v", wt, err)
	}
}

func TestResolveFeatureRemoteRequiresExplicitChoice(t *testing.T) {
	deps := &cli.Deps{Git: gitRunner(func(_ string, _ ...string) cli.CommandResult {
		return cli.CommandResult{Stdout: "origin\nupstream\n"}
	})}
	_, err := resolveFeatureRemote(deps, cli.WorktreeInfo{Path: "/repo", Repo: &config.RepoConfig{}})
	if err == nil || !contains(err.Error(), "multiple remotes") {
		t.Fatalf("got %v", err)
	}
}

func TestStackedRunnerPublicationTouchesOnlySelectedFeatureHead(t *testing.T) {
	oldRemote := pushRemote
	pushRemote = ""
	t.Cleanup(func() { pushRemote = oldRemote })

	worktrees := []cli.WorktreeInfo{
		{Name: "runner-base", Branch: "stack/pr-22", Repo: &config.RepoConfig{Name: "loomcli", Remote: "origin"}},
		{Name: "runner-middle", Branch: "stack/pr-23", Repo: &config.RepoConfig{Name: "loomcli", Remote: "origin"}},
		{Name: "runner", Path: "/repo/runner", Branch: "stack/pr-24", Repo: &config.RepoConfig{Name: "loomcli", Remote: "origin"}},
	}
	wt, err := resolveFeatureWorktree(worktrees, "runner", "loomcli")
	if err != nil {
		t.Fatal(err)
	}
	var commands []string
	deps := &cli.Deps{Git: gitRunner(func(_ string, args ...string) cli.CommandResult {
		command := strings.Join(args, " ")
		commands = append(commands, command)
		return cli.CommandResult{}
	})}
	remote, err := resolveFeatureRemote(deps, wt)
	if err != nil {
		t.Fatal(err)
	}
	if err := gitPushRemote(deps, wt.Path, remote, wt.Branch); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 1 || commands[0] != "push origin stack/pr-24" {
		t.Fatalf("publication commands = %v", commands)
	}
	for _, forbidden := range []string{"stash", "checkout", "merge", " main", "--force"} {
		if strings.Contains(commands[0], forbidden) {
			t.Fatalf("publication used forbidden mutation %q: %s", forbidden, commands[0])
		}
	}
}

type gitRunner func(string, ...string) cli.CommandResult

func (r gitRunner) Run(dir string, args ...string) cli.CommandResult {
	return r(dir, args...)
}
func (r gitRunner) RunWithOutput(dir string, args ...string) error { return r(dir, args...).Err }
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
