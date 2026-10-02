package git

import (
	"context"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
)

func TestPushCmd_RoutesRevisionToApply(t *testing.T) {
	previousApply, previousResolver := pushApply, pushResolver
	previousWorkspace, previousLead, previousRequestID, previousAll := pushWorkspace, pushLead, pushRequestID, pushAll
	t.Cleanup(func() {
		pushApply, pushResolver = previousApply, previousResolver
		pushWorkspace, pushLead, pushRequestID, pushAll = previousWorkspace, previousLead, previousRequestID, previousAll
	})
	pushWorkspace, pushLead, pushRequestID, pushAll = "", "lead", "request-1", false
	pushResolver = func() (*cli.Resolver, error) {
		return &cli.Resolver{Workspace: "workspace", Config: &config.LoomConfig{
			Workspaces: map[string]config.WorkspaceConfig{"workspace": {ID: "workspace-1"}},
		}}, nil
	}
	called := false
	pushApply = func(_ context.Context, request apply.Request) (apply.Result, error) {
		called = true
		if request.Workspace != "workspace-1" || request.Change != "change-1" || request.Revision != 2 ||
			request.Lead != "lead" || request.RequestID != "request-1" {
			t.Fatalf("unexpected Apply request: %+v", request)
		}
		return apply.Result{}, nil
	}
	if err := runPush(pushCmd, []string{"change-1", "2"}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("Push did not call Apply")
	}
}

func TestPushCmd_ArgsValidation(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantError bool
		errorMsg  string
	}{
		{
			name:      "without --all, no args",
			args:      []string{},
			wantError: true,
			errorMsg:  "accepts 2 arg(s)",
		},
		{
			name:      "one arg",
			args:      []string{"feature/branch"},
			wantError: true,
			errorMsg:  "accepts 2 arg(s)",
		},
		{
			name:      "change and revision",
			args:      []string{"change-1", "2"},
			wantError: false,
		},
		{
			name:      "without --all, three args",
			args:      []string{"feature/branch", "main", "extra"},
			wantError: true,
			errorMsg:  "accepts 2 arg(s)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Call the Args validation function directly
			err := pushCmd.Args(pushCmd, tc.args)

			if tc.wantError {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tc.errorMsg)
					return
				}
				if tc.errorMsg != "" && !strings.Contains(err.Error(), tc.errorMsg) {
					t.Errorf("expected error containing %q, got %q", tc.errorMsg, err.Error())
				}
			} else if err != nil {
				t.Errorf("expected no error, got %v", err)
			}
		})
	}
}

func TestPushCmd_RejectsLegacyBranchInput(t *testing.T) {
	err := runPush(pushCmd, []string{"agent", "main"})
	if err == nil || !strings.Contains(err.Error(), "branch Push is unavailable") {
		t.Fatalf("legacy branch Push error = %v", err)
	}
}

func TestPushCmd_ConflictPrintsPathsAndFails(t *testing.T) {
	err := applyError(apply.Result{Paths: []string{"src/conflict.go"}}, loomgit.NewError(loomgit.Conflict, "conflict", nil))
	if err == nil || !strings.Contains(err.Error(), "src/conflict.go") {
		t.Fatalf("conflict paths missing: %v", err)
	}
}

func TestPushCmd_GroupID(t *testing.T) {
	t.Parallel()
	// Verify command is in the "git" group
	if pushCmd.GroupID != "git" {
		t.Errorf("expected push command to be in 'git' group, got %q", pushCmd.GroupID)
	}
}

func TestPushCmd_Flags(t *testing.T) {
	t.Parallel()
	// Verify flags are registered
	if allFlag := pushCmd.Flags().Lookup("all"); allFlag == nil {
		t.Error("expected --all flag to be registered")
	}
	if wsFlag := pushCmd.Flags().Lookup("workspace"); wsFlag == nil {
		t.Error("expected --workspace flag to be registered")
	}

	// Verify shorthand flags
	if allFlag := pushCmd.Flags().ShorthandLookup("a"); allFlag == nil {
		t.Error("expected -a shorthand flag to be registered")
	}
	if wsFlag := pushCmd.Flags().ShorthandLookup("W"); wsFlag == nil {
		t.Error("expected -W shorthand flag to be registered")
	}
}
