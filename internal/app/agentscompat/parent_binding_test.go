package agentscompat

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type parentBindingAPIFake struct {
	agents.CompatibilityAPI
	calls   int
	auth    authority.SystemAuthority
	command agents.BindSupervisedAssignmentParentCommand
}

func (api *parentBindingAPIFake) BindSupervisedAssignmentParent(
	_ context.Context,
	auth authority.SystemAuthority,
	command agents.BindSupervisedAssignmentParentCommand,
) (*agents.SupervisedAssignment, error) {
	api.calls++
	api.auth = auth
	api.command = command
	return &agents.SupervisedAssignment{
		WorkspaceKey: command.WorkspaceKey,
		Name:         command.AgentName,
		Parent:       command.Parent,
	}, nil
}

func TestParentBindingCommandsRequireExactVerifiedExecutionGeneration(t *testing.T) {
	api := &parentBindingAPIFake{}
	agentsIssuer := authority.NewIssuer()
	commands, err := NewParentBindingCommands(api, agentsIssuer)
	if err != nil {
		t.Fatal(err)
	}
	command := agents.BindSupervisedAssignmentParentCommand{
		WorkspaceKey: "WS",
		AgentName:    "docs",
		Parent:       "run-7",
		Proof: agents.ParentBindingProof{
			DriverRunID:  "run-7",
			NodeID:       "node-a",
			LeaseID:      "lease-7",
			FencingToken: 7,
		},
	}

	for name, auth := range map[string]authority.ExecutionAuthority{
		"wrong action": issueDriverRunAuthority(
			t, execution.ActionFinalizeDriverRun, "WS", "run-7", "node-a", "lease-7", 7,
		),
		"wrong workspace": issueDriverRunAuthority(
			t, execution.ActionHeartbeatDriverRun, "OTHER", "run-7", "node-a", "lease-7", 7,
		),
		"wrong run": issueDriverRunAuthority(
			t, execution.ActionHeartbeatDriverRun, "WS", "run-8", "node-a", "lease-7", 7,
		),
		"wrong node": issueDriverRunAuthority(
			t, execution.ActionHeartbeatDriverRun, "WS", "run-7", "node-b", "lease-7", 7,
		),
		"wrong lease": issueDriverRunAuthority(
			t, execution.ActionHeartbeatDriverRun, "WS", "run-7", "node-a", "lease-8", 7,
		),
		"stale fence": issueDriverRunAuthority(
			t, execution.ActionHeartbeatDriverRun, "WS", "run-7", "node-a", "lease-7", 6,
		),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := commands.BindVerifiedDriverRunParent(
				t.Context(),
				auth,
				command,
			); !errors.Is(err, agents.ErrNotOwner) {
				t.Fatalf("error = %v, want not owner", err)
			}
		})
	}
	if api.calls != 0 {
		t.Fatalf("mismatched proofs reached Agents API %d times", api.calls)
	}

	executionAuth := issueDriverRunAuthority(
		t,
		execution.ActionHeartbeatDriverRun,
		"WS",
		"run-7",
		"node-a",
		"lease-7",
		7,
	)
	bound, err := commands.BindVerifiedDriverRunParent(t.Context(), executionAuth, command)
	if err != nil {
		t.Fatal(err)
	}
	if bound.Parent != "run-7" || api.calls != 1 {
		t.Fatalf("bound=%+v calls=%d", bound, api.calls)
	}
	if api.auth.Action() != agents.ActionBindSupervisedAssignmentParent ||
		api.auth.Workspace() != "WS" ||
		api.auth.Subject() != "driver-run:run-7" {
		t.Fatalf("derived system authority = action %q workspace %q subject %q",
			api.auth.Action(), api.auth.Workspace(), api.auth.Subject())
	}
}

func issueDriverRunAuthority(
	t *testing.T,
	action authority.Action,
	workspace, runID, nodeID, leaseID string,
	fence int64,
) authority.ExecutionAuthority {
	t.Helper()
	issuer := authority.NewIssuer()
	principal, err := issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject:   "driver-run:" + runID,
		Class:     authority.ClassExecution,
		Workspace: workspace,
		Actions:   []authority.Action{action},
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := issuer.IssueExecutionForOwner(
		principal,
		workspace,
		action,
		authority.ExecutionOwner{
			ResourceKind: authority.ExecutionResourceDriverRun,
			ResourceID:   runID,
			NodeID:       nodeID,
			LeaseID:      leaseID,
			FencingToken: fence,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return auth
}
