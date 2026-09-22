package stack

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

type recordingExec struct {
	calls []string
	run   func(string) cli.CommandResult
}

func (r *recordingExec) Run(_ string, name string, args ...string) cli.CommandResult {
	command := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, command)
	return r.run(command)
}

func TestStackMergeUsesOfficialExtensionNonInteractively(t *testing.T) {
	exec := &recordingExec{}
	exec.run = func(command string) cli.CommandResult {
		switch command {
		case "gh --version":
			return cli.CommandResult{}
		case "gh extension list":
			return cli.CommandResult{Stdout: "gh stack github/gh-stack v0.1.0"}
		case "gh repo view --json nameWithOwner,defaultBranchRef":
			return cli.CommandResult{Stdout: `{"nameWithOwner":"acme/project","defaultBranchRef":{"name":"main"}}`}
		case "gh api repos/acme/project/branches/main/protection":
			return cli.CommandResult{}
		case "gh stack merge 42 --yes --squash":
			return cli.CommandResult{Stdout: "queued\n"}
		default:
			return cli.CommandResult{Err: errors.New("unexpected command: " + command)}
		}
	}
	deps := &cli.Deps{Exec: exec}
	cmd := mergeCmd()
	cmd.SetContext(cli.WithDeps(context.Background(), deps))
	if err := cmd.Flags().Set("squash", "true"); err != nil {
		t.Fatal(err)
	}

	if err := cmd.RunE(cmd, []string{"42"}); err != nil {
		t.Fatalf("stack merge: %v", err)
	}
	if got := exec.calls[len(exec.calls)-1]; got != "gh stack merge 42 --yes --squash" {
		t.Fatalf("last command = %q", got)
	}
}

func TestStackMergeRejectsMissingOfficialExtension(t *testing.T) {
	exec := &recordingExec{run: func(command string) cli.CommandResult {
		switch command {
		case "gh --version":
			return cli.CommandResult{}
		case "gh extension list":
			return cli.CommandResult{Stdout: "gh stack someone/gh-stack v9.9.9"}
		default:
			return cli.CommandResult{Err: errors.New("unexpected command: " + command)}
		}
	}}
	deps := &cli.Deps{Exec: exec}
	cmd := mergeCmd()
	cmd.SetContext(cli.WithDeps(context.Background(), deps))
	if err := cmd.Flags().Set("squash", "true"); err != nil {
		t.Fatal(err)
	}

	err := cmd.RunE(cmd, []string{"42"})
	if err == nil || !strings.Contains(err.Error(), "official github/gh-stack") {
		t.Fatalf("expected official extension error, got %v", err)
	}
	if len(exec.calls) != 2 {
		t.Fatalf("commands after missing extension = %v", exec.calls)
	}
}

func TestStackMergeRequiresExactlyOneMethod(t *testing.T) {
	for _, methods := range [][3]bool{{false, false, false}, {true, true, false}} {
		cmd := mergeCmd()
		for i, flag := range []string{"squash", "merge", "rebase"} {
			if methods[i] {
				if err := cmd.Flags().Set(flag, "true"); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := cmd.RunE(cmd, []string{"42"}); err == nil || !strings.Contains(err.Error(), "exactly one") {
			t.Fatalf("methods %v: error = %v", methods, err)
		}
	}
}

func TestStackMergeReportsMissingGhAndUpstreamFailure(t *testing.T) {
	t.Run("missing gh", func(t *testing.T) {
		exec := &recordingExec{run: func(string) cli.CommandResult {
			return cli.CommandResult{Err: errors.New("executable not found")}
		}}
		cmd := mergeCmd()
		cmd.SetContext(cli.WithDeps(context.Background(), &cli.Deps{Exec: exec}))
		_ = cmd.Flags().Set("squash", "true")
		if err := cmd.RunE(cmd, []string{"42"}); err == nil || !strings.Contains(err.Error(), "gh CLI") {
			t.Fatalf("error = %v, want missing gh", err)
		}
		if len(exec.calls) != 1 {
			t.Fatalf("calls = %v, want only gh version", exec.calls)
		}
	})

	t.Run("merge rejected", func(t *testing.T) {
		exec := &recordingExec{run: func(command string) cli.CommandResult {
			switch command {
			case "gh --version":
				return cli.CommandResult{}
			case "gh extension list":
				return cli.CommandResult{Stdout: "gh stack github/gh-stack v0.1.0"}
			case "gh repo view --json nameWithOwner,defaultBranchRef":
				return cli.CommandResult{Stdout: `{"nameWithOwner":"acme/project","defaultBranchRef":{"name":"main"}}`}
			case "gh api repos/acme/project/branches/main/protection":
				return cli.CommandResult{}
			case "gh stack merge 42 --yes --squash":
				return cli.CommandResult{Stderr: "required check failed", Err: errors.New("exit 1")}
			default:
				return cli.CommandResult{Err: errors.New("unexpected command: " + command)}
			}
		}}
		cmd := mergeCmd()
		cmd.SetContext(cli.WithDeps(context.Background(), &cli.Deps{Exec: exec}))
		_ = cmd.Flags().Set("squash", "true")
		if err := cmd.RunE(cmd, []string{"42"}); err == nil || !strings.Contains(err.Error(), "required check failed") {
			t.Fatalf("error = %v, want GitHub rejection", err)
		}
	})
}
