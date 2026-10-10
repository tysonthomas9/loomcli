package git

import (
	"bytes"
	"context"
	"os"
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

