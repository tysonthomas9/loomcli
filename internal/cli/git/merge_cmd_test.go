package git

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

func TestMergeDelegatesToGitHubAfterProtectionCheck(t *testing.T) {
	oldSquash, oldMerge, oldRebase := mergeSquash, mergeCommit, mergeRebase
	mergeSquash, mergeCommit, mergeRebase = true, false, false
	t.Cleanup(func() { mergeSquash, mergeCommit, mergeRebase = oldSquash, oldMerge, oldRebase })

	deps, _, execRunner, _, _ := NewTestDeps(t)
	execRunner.RunFunc = func(_ string, name string, args ...string) cli.CommandResult {
		command := name + " " + strings.Join(args, " ")
		switch command {
		case "gh --version":
			return cli.CommandResult{Stdout: "gh version 2.80.0"}
		case "gh pr view 17 --json baseRefName":
			return cli.CommandResult{Stdout: `{"baseRefName":"main"}`}
		case "gh repo view --json nameWithOwner":
			return cli.CommandResult{Stdout: `{"nameWithOwner":"acme/project"}`}
		case "gh api repos/acme/project/branches/main/protection":
			return cli.CommandResult{Stdout: `{}`}
		case "gh pr merge 17 --squash":
			return cli.CommandResult{}
		default:
			return cli.CommandResult{Err: errors.New("unexpected command: " + command)}
		}
	}
	cmd := &cobra.Command{}
	cmd.SetContext(cli.WithDeps(context.Background(), deps))

	if err := runMerge(cmd, []string{"17"}); err != nil {
		t.Fatalf("runMerge: %v", err)
	}
	if len(execRunner.Calls) != 5 {
		t.Fatalf("commands = %d, want gh version, PR view, repo view, protection, merge", len(execRunner.Calls))
	}
}

func TestMergeFailsClosedBeforeGitHubMerge(t *testing.T) {
	oldSquash, oldMerge, oldRebase := mergeSquash, mergeCommit, mergeRebase
	mergeSquash, mergeCommit, mergeRebase = true, false, false
	t.Cleanup(func() { mergeSquash, mergeCommit, mergeRebase = oldSquash, oldMerge, oldRebase })

	deps, _, execRunner, _, _ := NewTestDeps(t)
	execRunner.RunFunc = func(_ string, name string, args ...string) cli.CommandResult {
		command := name + " " + strings.Join(args, " ")
		switch command {
		case "gh --version":
			return cli.CommandResult{}
		case "gh pr view 17 --json baseRefName":
			return cli.CommandResult{Stdout: `{"baseRefName":"main"}`}
		case "gh repo view --json nameWithOwner":
			return cli.CommandResult{Stdout: `{"nameWithOwner":"acme/project"}`}
		case "gh api repos/acme/project/branches/main/protection":
			return cli.CommandResult{Stderr: "404 Not Found", Err: errors.New("exit 1")}
		default:
			return cli.CommandResult{Err: errors.New("unexpected command: " + command)}
		}
	}
	cmd := &cobra.Command{}
	cmd.SetContext(cli.WithDeps(context.Background(), deps))

	err := runMerge(cmd, []string{"17"})
	if err == nil || !strings.Contains(err.Error(), "merge disabled") {
		t.Fatalf("expected merge-disabled error, got %v", err)
	}
	for _, call := range execRunner.Calls {
		if len(call.Args) >= 2 && call.Args[0] == "pr" && call.Args[1] == "merge" {
			t.Fatalf("merge ran after failed protection check: %+v", call)
		}
	}
}

func TestMergeRequiresExactlyOneMethod(t *testing.T) {
	oldSquash, oldMerge, oldRebase := mergeSquash, mergeCommit, mergeRebase
	t.Cleanup(func() { mergeSquash, mergeCommit, mergeRebase = oldSquash, oldMerge, oldRebase })
	for _, methods := range [][3]bool{{false, false, false}, {true, true, false}} {
		mergeSquash, mergeCommit, mergeRebase = methods[0], methods[1], methods[2]
		if err := runMerge(&cobra.Command{}, []string{"17"}); err == nil || !strings.Contains(err.Error(), "exactly one") {
			t.Fatalf("methods %v: error = %v", methods, err)
		}
	}
}

func TestMergeReportsMissingGhAndUpstreamFailure(t *testing.T) {
	oldSquash, oldMerge, oldRebase := mergeSquash, mergeCommit, mergeRebase
	mergeSquash, mergeCommit, mergeRebase = true, false, false
	t.Cleanup(func() { mergeSquash, mergeCommit, mergeRebase = oldSquash, oldMerge, oldRebase })

	t.Run("missing gh", func(t *testing.T) {
		deps, _, execRunner, _, _ := NewTestDeps(t)
		execRunner.Result = cli.CommandResult{Err: errors.New("executable not found")}
		cmd := &cobra.Command{}
		cmd.SetContext(cli.WithDeps(context.Background(), deps))
		if err := runMerge(cmd, []string{"17"}); err == nil || !strings.Contains(err.Error(), "gh") {
			t.Fatalf("error = %v, want missing gh", err)
		}
		if len(execRunner.Calls) != 1 {
			t.Fatalf("calls = %#v, want only gh version", execRunner.Calls)
		}
	})

	t.Run("merge rejected", func(t *testing.T) {
		deps, _, execRunner, _, _ := NewTestDeps(t)
		execRunner.RunFunc = func(_ string, name string, args ...string) cli.CommandResult {
			command := name + " " + strings.Join(args, " ")
			switch command {
			case "gh --version", "gh api repos/acme/project/branches/main/protection":
				return cli.CommandResult{}
			case "gh pr view 17 --json baseRefName":
				return cli.CommandResult{Stdout: `{"baseRefName":"main"}`}
			case "gh repo view --json nameWithOwner":
				return cli.CommandResult{Stdout: `{"nameWithOwner":"acme/project"}`}
			case "gh pr merge 17 --squash":
				return cli.CommandResult{Stderr: "required check failed", Err: errors.New("exit 1")}
			default:
				return cli.CommandResult{Err: errors.New("unexpected command: " + command)}
			}
		}
		cmd := &cobra.Command{}
		cmd.SetContext(cli.WithDeps(context.Background(), deps))
		if err := runMerge(cmd, []string{"17"}); err == nil || !strings.Contains(err.Error(), "required check failed") {
			t.Fatalf("error = %v, want GitHub rejection", err)
		}
	})
}
