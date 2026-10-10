package git

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit/publish"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

func TestPrCmdRequiresLeadAndChange(t *testing.T) {
	for _, args := range [][]string{nil, {"lead"}, {"lead", "change", "main"}} {
		if err := prCmd.Args(prCmd, args); err == nil {
			t.Fatalf("accepted legacy PR arguments %v", args)
		}
	}
	if err := prCmd.Args(prCmd, []string{"lead", "change"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prCmd.Use, "<change>") || prCmd.Flags().Lookup("all") != nil {
		t.Fatal("CLI still advertises legacy branch or all-workspace PR mode")
	}
}

func TestPrStackCmdRequiresStableStackAndOrderedChanges(t *testing.T) {
	for _, args := range [][]string{nil, {"stack"}} {
		if err := prStackCmd.Args(prStackCmd, args); err == nil {
			t.Fatalf("accepted incomplete stack arguments %v", args)
		}
	}
	if err := prStackCmd.Args(prStackCmd, []string{"feature-1", "lead", "A", "B"}); err != nil {
		t.Fatal(err)
	}
	if err := prStackCmd.Args(prStackCmd, []string{"feature-1", "lead"}); err != nil {
		t.Fatal(err)
	}
}

func TestPrStackCommandCallsPublisherWithOrderedChanges(t *testing.T) {
	originalResolver, originalPublish := prStackResolver, prStackPublish
	t.Cleanup(func() { prStackResolver, prStackPublish = originalResolver, originalPublish })
	prStackResolver = func() (*cli.Resolver, error) {
		return &cli.Resolver{Workspace: "workspace", Config: &config.LoomConfig{Workspaces: map[string]config.WorkspaceConfig{
			"workspace": {ID: "W"},
		}}}, nil
	}
	called := false
	prStackPublish = func(_ context.Context, workspace, stack, lead string, changes []string) ([]publish.Result, error) {
		called = true
		if workspace != "W" || stack != "feature-1" || lead != "lead" || !reflect.DeepEqual(changes, []string{"A", "B"}) {
			t.Fatalf("publisher arguments = %q %q %q %v", workspace, stack, lead, changes)
		}
		return []publish.Result{{PRURL: "https://example.test/1", Backend: "loom", StatusReason: "GitHub native stacks are unavailable for this repository"}}, nil
	}
	var output, status bytes.Buffer
	prStackCmd.SetOut(&output)
	prStackCmd.SetErr(&status)
	t.Cleanup(func() { prStackCmd.SetOut(nil); prStackCmd.SetErr(nil) })
	if err := prStackCmd.RunE(prStackCmd, []string{"feature-1", "lead", "A", "B"}); err != nil {
		t.Fatal(err)
	}
	if !called || output.String() != "https://example.test/1\n" {
		t.Fatalf("publisher called = %v, output = %q", called, output.String())
	}
	if !strings.Contains(status.String(), "GitHub native stacks are unavailable") {
		t.Fatalf("fallback status = %q", status.String())
	}
}

func TestStackCommandsSelectWorkspaceOutsideWorkspace(t *testing.T) {
	setupOutsideWorkspaceForPR(t)
	previousWorkspace, previousPublish := prStackWorkspace, prStackPublish
	t.Cleanup(func() { prStackWorkspace, prStackPublish = previousWorkspace, previousPublish })
	if err := prStackCmd.Flags().Set("workspace", "selected"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prStackCmd.Flags().Set("workspace", previousWorkspace) })
	prStackPublish = func(_ context.Context, workspace, _, _ string, _ []string) ([]publish.Result, error) {
		if workspace != "SELECTED" {
			t.Fatalf("publish workspace = %q", workspace)
		}
		return nil, nil
	}
	if err := runPRStack(prStackCmd, []string{"stack", "lead"}); err != nil {
		t.Fatal(err)
	}
}

func setupOutsideWorkspaceForPR(t *testing.T) {
	t.Helper()
	workspaceDir := t.TempDir()
	setupWorkspaceConfig(t, &config.LoomConfig{Workspaces: map[string]config.WorkspaceConfig{
		"selected": {Path: workspaceDir, ID: "W"},
	}})
	t.Setenv("LOOM_WORKSPACE", "")
	outside := t.TempDir()
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(outside); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previousDir) })
	if _, err := cli.NewResolver(); err == nil || !strings.Contains(err.Error(), "no active workspace") {
		t.Fatalf("outside workspace resolver error = %v", err)
	}
}

func TestDeliveryModeCommandSetsWorkspaceMode(t *testing.T) {
	root := t.TempDir()
	setupWorkspaceConfigInDir(t, root, &config.LoomConfig{
		DefaultWorkspace: "ws1",
		Workspaces: map[string]config.WorkspaceConfig{
			"ws1": {Path: root, Repos: []config.RepoConfig{{Name: "repo", Path: root}}},
		},
	})
	journalDir := filepath.Join(root, "loomgit")
	if err := os.MkdirAll(journalDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(journalDir, "store.db"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	deliveryModeCmd.SetOut(&output)
	t.Cleanup(func() { deliveryModeCmd.SetOut(nil) })
	deliveryModeCmd.SetContext(context.Background())
	if err := deliveryModeCmd.RunE(deliveryModeCmd, []string{"trunk"}); err != nil {
		t.Fatal(err)
	}
	if output.String() != "trunk\n" {
		t.Fatalf("set mode output = %q", output.String())
	}
	output.Reset()
	if err := deliveryModeCmd.RunE(deliveryModeCmd, nil); err != nil {
		t.Fatal(err)
	}
	if output.String() != "trunk\n" {
		t.Fatalf("read mode output = %q", output.String())
	}
}

func TestLeadMayMergeSelectsWorkspaceOutsideWorkspace(t *testing.T) {
	setupOutsideWorkspaceForPR(t)
	journalDir := filepath.Join(os.Getenv("LOOM_CONFIG_DIR"), "loomgit")
	if err := os.MkdirAll(journalDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(journalDir, "store.db"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	previousWorkspace, previousPolicy := prStackWorkspace, setWorkspacePolicy
	t.Cleanup(func() {
		prStackWorkspace, setWorkspacePolicy = previousWorkspace, previousPolicy
		leadMayMergeCmd.SetArgs(nil)
		leadMayMergeCmd.SetOut(nil)
		leadMayMergeCmd.SetErr(nil)
	})
	var output bytes.Buffer
	leadMayMergeCmd.SetOut(&output)
	leadMayMergeCmd.SetErr(&bytes.Buffer{})
	for _, args := range [][]string{
		{"--workspace", "selected", "when_green"},
		{"when_green", "--workspace", "selected"},
	} {
		prStackWorkspace = ""
		output.Reset()
		var selected string
		setWorkspacePolicy = func(ctx context.Context, workspace, leadMayMerge string, actor review.Actor, env []string) (string, error) {
			selected = workspace
			return publish.SetWorkspacePolicyLocal(ctx, workspace, leadMayMerge, actor, nil)
		}
		leadMayMergeCmd.SetArgs(args)
		if err := leadMayMergeCmd.Execute(); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if selected != "SELECTED" || output.String() != "when_green\n" {
			t.Fatalf("%v: workspace=%q output=%q", args, selected, output.String())
		}
	}
}
