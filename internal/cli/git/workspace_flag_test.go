package git

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
	"github.com/tysonthomas9/loomcli/internal/loomgit/pull"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

// Each loom git command that takes -W must select that workspace even when
// no workspace is active, with the flag before or after its arguments.
func TestGitCommandsSelectWorkspaceWithoutActiveWorkspace(t *testing.T) {
	root := t.TempDir()
	repoDir := filepath.Join(root, "repo")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	setupWorkspaceConfig(t, &config.LoomConfig{Workspaces: map[string]config.WorkspaceConfig{
		"selected": {Path: root, Repos: []config.RepoConfig{{Name: "repo", Path: repoDir}}},
	}})
	t.Setenv("LOOM_WORKSPACE", "")
	journalDir := filepath.Join(os.Getenv("LOOM_CONFIG_DIR"), "loomgit")
	if err := os.MkdirAll(journalDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(journalDir, "store.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previousDir) })
	if _, err := cli.NewResolver(); !errors.Is(err, bootstrap.ErrNoActiveWorkspace) {
		t.Fatalf("resolver without a selection: %v", err)
	}

	oldApply, oldApprove, oldPull, oldRestack, oldUnapply := pushApply, approveLocal, pullLocal, restackLocal, unapplyLocal
	oldPR, oldPush, oldApproveWS, oldPull2, oldSync := prWorkspace, pushWorkspace, approveWorkspace, pullWorkspace, syncWorkspaceFlag
	t.Cleanup(func() {
		pushApply, approveLocal, pullLocal, restackLocal, unapplyLocal = oldApply, oldApprove, oldPull, oldRestack, oldUnapply
		prWorkspace, pushWorkspace, approveWorkspace, pullWorkspace, syncWorkspaceFlag = oldPR, oldPush, oldApproveWS, oldPull2, oldSync
	})

	var got string
	pushApply = func(_ context.Context, request apply.Request) (apply.Result, error) {
		got = request.Workspace
		return apply.Result{}, nil
	}
	approveLocal = func(_ context.Context, workspace, _, _ string, _ int, _ review.Actor) (apply.FollowResult, error) {
		got = workspace
		return apply.FollowResult{}, nil
	}
	pullLocal = func(_ context.Context, path, _, _, _ string) (pull.PullResult, error) {
		got = path
		return pull.PullResult{}, nil
	}
	restackLocal = func(_ context.Context, path, _ string, _ []string, _ string) (pull.PullResult, error) {
		got = path
		return pull.PullResult{}, nil
	}
	unapplyLocal = func(_ context.Context, path, _, _ string) (pull.PullResult, error) {
		got = path
		return pull.PullResult{}, nil
	}

	cases := []struct {
		name string
		cmd  *cobra.Command
		args []string
		want string // value the command's backend must receive; "" = only check resolution
		out  string
	}{
		{name: "delivery-mode", cmd: deliveryModeCmd, args: []string{"trunk"}, out: "trunk\n"},
		{name: "pr", cmd: prCmd, args: []string{"lead", "change"}},
		{name: "push", cmd: pushCmd, args: []string{"change", "2"}, want: "SELECTED"},
		{name: "approve", cmd: approveCmd, args: []string{"change", "2"}, want: "SELECTED"},
		{name: "pull", cmd: pullCmd, args: []string{"repo"}, want: repoDir},
		{name: "restack", cmd: restackCmd, args: []string{"repo", "base"}, want: repoDir},
		{name: "unapply", cmd: unapplyCmd, args: []string{"repo", "change"}, want: repoDir},
		{name: "sync", cmd: syncCmd, want: repoDir},
	}
	for _, tc := range cases {
		for _, position := range []string{"before", "after"} {
			args := append([]string{"--workspace", "selected"}, tc.args...)
			if position == "after" {
				args = append(append([]string{}, tc.args...), "--workspace", "selected")
			}
			prWorkspace, pushWorkspace, approveWorkspace, pullWorkspace, syncWorkspaceFlag = "", "", "", "", ""
			got = ""
			var output bytes.Buffer
			tc.cmd.SetArgs(args)
			tc.cmd.SetOut(&output)
			tc.cmd.SetErr(&bytes.Buffer{})
			tc.cmd.SetContext(context.Background())
			err := tc.cmd.Execute()
			tc.cmd.SetArgs(nil)
			tc.cmd.SetOut(nil)
			tc.cmd.SetErr(nil)
			_ = tc.cmd.Flags().Set("workspace", "")
			if errors.Is(err, bootstrap.ErrNoActiveWorkspace) {
				t.Fatalf("%s (flag %s): %v", tc.name, position, err)
			}
			if tc.name == "pr" {
				continue // no publish seam; resolving past the workspace is the property
			}
			if err != nil {
				t.Fatalf("%s (flag %s): %v", tc.name, position, err)
			}
			if got != tc.want || (tc.out != "" && output.String() != tc.out) {
				t.Fatalf("%s (flag %s): backend got %q want %q, output %q", tc.name, position, got, tc.want, output.String())
			}
		}
	}
}
