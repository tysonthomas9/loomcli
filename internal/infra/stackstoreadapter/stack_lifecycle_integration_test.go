package stackstoreadapter_test

import (
	"context"
	"errors"
	"testing"
	"time"

	stackstore "github.com/tysonthomas9/loomcli/internal/infra/sourcecontrolstackstore"
	stackstoreadapter "github.com/tysonthomas9/loomcli/internal/infra/stackstoreadapter"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
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

func TestStackLifecycleOwnsPublicationStateAndPreservesEvidence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clock := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	store := stackstore.New(t.TempDir())
	adapter, err := stackstoreadapter.New(store)
	if err != nil {
		t.Fatalf("compose adapter: %v", err)
	}
	service, err := sourcecontrol.NewStackLifecycle(adapter, func() time.Time { return clock })
	if err != nil {
		t.Fatalf("compose lifecycle: %v", err)
	}
	stack, err := service.EnsureStack(ctx, sourcecontrol.EnsureStackCommand{
		WorkspaceKey: "WS", StackID: "manual:publish", Repository: "loomcli", RootBase: "main",
	})
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if _, err := service.AddStackNode(ctx, sourcecontrol.AddStackNodeCommand{
		WorkspaceKey: "WS", StackID: stack.ID, TaskID: "TASK-1",
	}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := service.RecordStackNodePublication(ctx, sourcecontrol.RecordStackNodePublicationCommand{
		WorkspaceKey: "WS", StackID: stack.ID, TaskID: "TASK-1",
		State: sourcecontrol.StackPublicationPublished, PRNumber: 42,
		PRURL: "https://example.test/pull/42", OutputSHA: "abc123",
	}); err != nil {
		t.Fatalf("record published: %v", err)
	}
	nodes, err := service.ListStackNodes(ctx, "WS", stack.ID)
	if err != nil || len(nodes) != 1 {
		t.Fatalf("list published node: nodes=%+v err=%v", nodes, err)
	}
	published := nodes[0]
	if published.State != "published" || published.PRNumber != 42 || published.OutputSHA != "abc123" ||
		published.LastPublishedAt == nil || !published.LastPublishedAt.Equal(clock) {
		t.Fatalf("published evidence mismatch: %+v", published)
	}

	err = service.RecordStackNodePublication(ctx, sourcecontrol.RecordStackNodePublicationCommand{
		WorkspaceKey: "WS", StackID: stack.ID, TaskID: "TASK-1",
		State: sourcecontrol.StackPublicationMerged, PRNumber: 99,
	})
	if !errors.Is(err, sourcecontrol.ErrInvalid) {
		t.Fatalf("terminal evidence error = %v, want invalid", err)
	}
	if err := service.RecordStackNodePublication(ctx, sourcecontrol.RecordStackNodePublicationCommand{
		WorkspaceKey: "WS", StackID: stack.ID, TaskID: "TASK-1", State: sourcecontrol.StackPublicationMerged,
	}); err != nil {
		t.Fatalf("record merged: %v", err)
	}
	nodes, err = service.ListStackNodes(ctx, "WS", stack.ID)
	if err != nil || len(nodes) != 1 {
		t.Fatalf("list merged node: nodes=%+v err=%v", nodes, err)
	}
	merged := nodes[0]
	if merged.State != "merged" || merged.PRNumber != published.PRNumber || merged.PRURL != published.PRURL ||
		merged.OutputSHA != published.OutputSHA || merged.LastPublishedAt == nil || !merged.LastPublishedAt.Equal(clock) {
		t.Fatalf("merged transition discarded publication evidence: before=%+v after=%+v", published, merged)
	}
}

func newStackLifecycle(t *testing.T) sourcecontrol.StackLifecycle {
	t.Helper()
	adapter, err := stackstoreadapter.New(stackstore.New(t.TempDir()))
	if err != nil {
		t.Fatalf("compose adapter: %v", err)
	}
	service, err := sourcecontrol.NewStackLifecycle(adapter, time.Now)
	if err != nil {
		t.Fatalf("compose lifecycle: %v", err)
	}
	return service
}
