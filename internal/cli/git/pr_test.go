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

func TestMergeUpToCommandRequiresExactHumanConfirmation(t *testing.T) {
	oldResolver, oldPreview, oldRequest := prStackResolver, prMergePreview, prMergeRequest
	t.Cleanup(func() { prStackResolver, prMergePreview, prMergeRequest = oldResolver, oldPreview, oldRequest })
	prStackResolver = func() (*cli.Resolver, error) {
		return &cli.Resolver{Workspace: "workspace", Config: &config.LoomConfig{Workspaces: map[string]config.WorkspaceConfig{
			"workspace": {ID: "W"},
		}}}, nil
	}
	prMergePreview = func(_ context.Context, workspace, lead, stack, target string) (publish.MergeStackView, error) {
		if workspace != "W" || lead != "L" || stack != "feature" || target != "C" {
			t.Fatalf("preview %s %s %s %s", workspace, lead, stack, target)
		}
		return publish.MergeStackView{StackID: stack, Target: target, Layers: []publish.MergeLayerView{
			{Change: "A", Head: "head-A"}, {Change: "B", Head: "head-B"}, {Change: "C", Head: "head-C"}, {Change: "D", Head: "head-D"},
		}}, nil
	}
	called := 0
	prMergeRequest = func(_ context.Context, _, _, _, _ string, heads []string) (publish.MergeStackView, error) {
		called++
		if !reflect.DeepEqual(heads, []string{"head-A", "head-B", "head-C", "head-D"}) {
			t.Fatalf("heads: %v", heads)
		}
		return publish.MergeStackView{Phase: "ready"}, nil
	}
	cmd := *mergeUpToCmd
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetIn(strings.NewReader("merge B\n"))
	if err := runMergeUpTo(&cmd, []string{"feature", "L", "C"}); err == nil || called != 0 {
		t.Fatalf("incorrect confirmation: err=%v calls=%d", err, called)
	}
	cmd.SetIn(strings.NewReader("merge C\n"))
	if err := runMergeUpTo(&cmd, []string{"feature", "L", "C"}); err != nil || called != 1 {
		t.Fatalf("confirmed merge: err=%v calls=%d", err, called)
	}
}

func TestStackCommandsSelectWorkspaceOutsideWorkspace(t *testing.T) {
	setupOutsideWorkspaceForPR(t)
	previousWorkspace, previousPublish, previousPreview := prStackWorkspace, prStackPublish, prMergePreview
	t.Cleanup(func() {
		prStackWorkspace, prStackPublish, prMergePreview = previousWorkspace, previousPublish, previousPreview
	})
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
	prMergePreview = func(_ context.Context, workspace, _, _, _ string) (publish.MergeStackView, error) {
		if workspace != "SELECTED" {
			t.Fatalf("merge workspace = %q", workspace)
		}
		return publish.MergeStackView{}, nil
	}
	if err := runPRStack(prStackCmd, []string{"stack", "lead"}); err != nil {
		t.Fatal(err)
	}
	if err := mergeUpToCmd.Flags().Set("status", "true"); err != nil {
		t.Fatal(err)
	}
	if err := mergeUpToCmd.Flags().Set("workspace", "selected"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mergeUpToCmd.Flags().Set("status", "false") })
	if err := runMergeUpTo(mergeUpToCmd, []string{"stack", "lead", "layer"}); err != nil {
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
