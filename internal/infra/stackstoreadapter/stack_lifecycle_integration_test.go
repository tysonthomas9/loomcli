package stackstoreadapter_test

import (
	"context"
	"errors"
	"testing"

	stackstoreadapter "github.com/tysonthomas9/loomcli/internal/infra/stackstoreadapter"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
)

func TestStackLifecycleOwnsAppendAndTopologyReconciliation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service := newStackLifecycle(t)

	stack, err := service.EnsureStack(ctx, sourcecontrol.EnsureStackCommand{
		WorkspaceKey: "WS", StackID: "manual:proof", Repository: "loomcli", RootBase: "main",
	})
	if err != nil {
		t.Fatalf("ensure stack: %v", err)
	}
	if stack.ID != "manual:proof" || stack.Repository != "loomcli" || stack.RootBase != "main" {
		t.Fatalf("unexpected stack projection: %+v", stack)
	}

	root, err := service.AddStackNode(ctx, sourcecontrol.AddStackNodeCommand{
		WorkspaceKey: "WS", StackID: stack.ID, TaskID: "TASK-1",
	})
	if err != nil {
		t.Fatalf("add root: %v", err)
	}
	tip, err := service.AddStackNode(ctx, sourcecontrol.AddStackNodeCommand{
		WorkspaceKey: "WS", StackID: stack.ID, TaskID: "TASK-2",
	})
	if err != nil {
		t.Fatalf("append tip: %v", err)
	}
	if root.BaseTaskID != "" || tip.BaseTaskID != root.TaskID {
		t.Fatalf("append policy not applied: root=%+v tip=%+v", root, tip)
	}
	if root.OutputBranch == "" || tip.OutputBranch == "" {
		t.Fatalf("stable output branches were not assigned: root=%+v tip=%+v", root, tip)
	}

	result, err := service.ReconcileStack(ctx, sourcecontrol.ReconcileStackCommand{
		Stack: sourcecontrol.EnsureStackCommand{
			WorkspaceKey: "WS", StackID: stack.ID, Repository: "loomcli", RootBase: "main",
		},
		Nodes: []sourcecontrol.DesiredStackNode{{TaskID: "TASK-1"}, {TaskID: "TASK-2"}},
	})
	if err != nil {
		t.Fatalf("reconcile changed topology: %v", err)
	}
	if len(result.Created) != 0 || len(result.Reparented) != 1 || result.Reparented[0] != "TASK-2" {
		t.Fatalf("unexpected reconciliation result: %+v", result)
	}
	if result.Lineage["TASK-2"].BaseRef != "main" || result.Lineage["TASK-2"].OutputBranch != tip.OutputBranch {
		t.Fatalf("reparent changed branch or lineage projection: before=%+v after=%+v", tip, result.Lineage["TASK-2"])
	}

	again, err := service.ReconcileStack(ctx, sourcecontrol.ReconcileStackCommand{
		Stack: sourcecontrol.EnsureStackCommand{
			WorkspaceKey: "WS", StackID: stack.ID, Repository: "loomcli", RootBase: "main",
		},
		Nodes: []sourcecontrol.DesiredStackNode{{TaskID: "TASK-1"}, {TaskID: "TASK-2"}},
	})
	if err != nil {
		t.Fatalf("idempotent reconcile: %v", err)
	}
	if len(again.Created) != 0 || len(again.Reparented) != 0 {
		t.Fatalf("idempotent reconcile mutated stack: %+v", again)
	}
}

func TestStackLifecycleRejectsInvalidTopologyBeforePersistence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service := newStackLifecycle(t)

	_, err := service.ReconcileStack(ctx, sourcecontrol.ReconcileStackCommand{
		Stack: sourcecontrol.EnsureStackCommand{
			WorkspaceKey: "WS", StackID: "manual:cycle", Repository: "loomcli", RootBase: "main",
		},
		Nodes: []sourcecontrol.DesiredStackNode{
			{TaskID: "TASK-1", BaseTaskID: "TASK-2"},
			{TaskID: "TASK-2", BaseTaskID: "TASK-1"},
		},
	})
	if !errors.Is(err, sourcecontrol.ErrInvalidMaterialization) {
		t.Fatalf("cycle error = %v, want invalid materialization", err)
	}
	stacks, listErr := service.ListStacks(ctx, "WS")
	if listErr != nil {
		t.Fatalf("list after reject: %v", listErr)
	}
	if len(stacks) != 0 {
		t.Fatalf("invalid topology created a partial stack: %+v", stacks)
	}
}

func newStackLifecycle(t *testing.T) sourcecontrol.StackLifecycle {
	t.Helper()
	adapter, err := stackstoreadapter.New(stackstore.New(t.TempDir()))
	if err != nil {
		t.Fatalf("compose adapter: %v", err)
	}
	service, err := sourcecontrol.NewStackLifecycle(adapter)
	if err != nil {
		t.Fatalf("compose lifecycle: %v", err)
	}
	return service
}
