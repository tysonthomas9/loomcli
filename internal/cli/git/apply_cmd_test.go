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

func TestApplyCmd_RoutesRevisionToApply(t *testing.T) {
	previousApply, previousResolver := applyRevision, applyResolver
	previousWorkspace, previousLead, previousRequestID, previousAll := applyWorkspace, applyLead, applyRequestID, applyAll
	t.Cleanup(func() {
		applyRevision, applyResolver = previousApply, previousResolver
		applyWorkspace, applyLead, applyRequestID, applyAll = previousWorkspace, previousLead, previousRequestID, previousAll
	})
	applyWorkspace, applyLead, applyRequestID, applyAll = "", "lead", "request-1", false
	applyResolver = func() (*cli.Resolver, error) {
		return &cli.Resolver{Workspace: "workspace", Config: &config.LoomConfig{
			Workspaces: map[string]config.WorkspaceConfig{"workspace": {ID: "workspace-1"}},
		}}, nil
	}
	called := false
	applyRevision = func(_ context.Context, request apply.Request) (apply.Result, error) {
		called = true
		if request.Workspace != "workspace-1" || request.Change != "change-1" || request.Revision != 2 ||
			request.Lead != "lead" || request.RequestID != "request-1" {
			t.Fatalf("unexpected Apply request: %+v", request)
		}
		return apply.Result{}, nil
	}
	if err := runApply(applyCmd, []string{"change-1", "2"}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("apply did not call Apply")
	}
}

func TestApplyCmd_ArgsValidation(t *testing.T) {
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
			err := applyCmd.Args(applyCmd, tc.args)

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

func TestApplyCmd_RejectsLegacyBranchInput(t *testing.T) {
	err := runApply(applyCmd, []string{"agent", "main"})
	if err == nil || !strings.Contains(err.Error(), "branch input is unavailable") {
		t.Fatalf("legacy branch input error = %v", err)
	}
}

func TestApplyCmd_ConflictPrintsPathsAndFails(t *testing.T) {
	err := applyError(apply.Result{Paths: []string{"src/conflict.go"}}, loomgit.NewError(loomgit.Conflict, "conflict", nil))
	if err == nil || !strings.Contains(err.Error(), "src/conflict.go") {
		t.Fatalf("conflict paths missing: %v", err)
	}
}

func TestApplyCmd_GroupID(t *testing.T) {
	t.Parallel()
	// Verify command is in the "git" group
	if applyCmd.GroupID != "git" {
		t.Errorf("expected apply command to be in 'git' group, got %q", applyCmd.GroupID)
	}
}

func TestApplyCmd_Flags(t *testing.T) {
	t.Parallel()
	// Verify flags are registered
	if allFlag := applyCmd.Flags().Lookup("all"); allFlag == nil {
		t.Error("expected --all flag to be registered")
	}
	if wsFlag := applyCmd.Flags().Lookup("workspace"); wsFlag == nil {
		t.Error("expected --workspace flag to be registered")
	}

	// Verify shorthand flags
	if allFlag := applyCmd.Flags().ShorthandLookup("a"); allFlag == nil {
		t.Error("expected -a shorthand flag to be registered")
	}
	if wsFlag := applyCmd.Flags().ShorthandLookup("W"); wsFlag == nil {
		t.Error("expected -W shorthand flag to be registered")
	}
}
