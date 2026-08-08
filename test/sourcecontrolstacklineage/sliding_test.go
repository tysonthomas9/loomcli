package sourcecontrolstacklineage_test

import (
	"errors"
	"testing"

	. "github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
)

func TestBaseBranchSliding(t *testing.T) {
	stack := Stack{ID: "epic:E", RootBase: "main"}

	mk := func(task, base, branch string, state NodeState) StackNode {
		return StackNode{TaskID: task, BaseTaskID: base, OutputBranch: branch, State: state}
	}

	t.Run("root returns RootBase", func(t *testing.T) {
		n := mk("A", "", "", NodeStatePending)
		got, err := BaseBranchSliding(stack, n, ByTask([]StackNode{n}))
		if err != nil || got != "main" {
			t.Fatalf("got (%q, %v), want (main, nil)", got, err)
		}
	})

	t.Run("published predecessor returns its branch", func(t *testing.T) {
		a := mk("A", "", "loom/stack/epic:E/A", NodeStatePublished)
		b := mk("B", "A", "", NodeStatePending)
		got, err := BaseBranchSliding(stack, b, ByTask([]StackNode{a, b}))
		if err != nil || got != "loom/stack/epic:E/A" {
			t.Fatalf("got (%q, %v), want (A branch, nil)", got, err)
		}
	})

	t.Run("slides past an empty predecessor to the nearest real branch", func(t *testing.T) {
		a := mk("A", "", "loom/stack/epic:E/A", NodeStatePublished)
		b := mk("B", "A", "", NodeStateEmpty) // empty diff → no branch
		c := mk("C", "B", "", NodeStatePending)
		got, err := BaseBranchSliding(stack, c, ByTask([]StackNode{a, b, c}))
		if err != nil || got != "loom/stack/epic:E/A" {
			t.Fatalf("got (%q, %v), want slide to A branch", got, err)
		}
	})

	t.Run("slides past a closed predecessor", func(t *testing.T) {
		a := mk("A", "", "loom/stack/epic:E/A", NodeStateClosed) // dropped
		b := mk("B", "A", "", NodeStatePending)
		got, err := BaseBranchSliding(stack, b, ByTask([]StackNode{a, b}))
		if err != nil || got != "main" {
			t.Fatalf("got (%q, %v), want slide to RootBase", got, err)
		}
	})

	t.Run("slides to RootBase when all ancestors are empty", func(t *testing.T) {
		a := mk("A", "", "", NodeStateEmpty)
		b := mk("B", "A", "", NodeStateEmpty)
		c := mk("C", "B", "", NodeStatePending)
		got, err := BaseBranchSliding(stack, c, ByTask([]StackNode{a, b, c}))
		if err != nil || got != "main" {
			t.Fatalf("got (%q, %v), want (main, nil)", got, err)
		}
	})

	t.Run("missing predecessor fails closed", func(t *testing.T) {
		b := mk("B", "ghost", "", NodeStatePending)
		_, err := BaseBranchSliding(stack, b, ByTask([]StackNode{b}))
		if !errors.Is(err, ErrMissingPredecessor) {
			t.Fatalf("err = %v, want ErrMissingPredecessor", err)
		}
	})

	t.Run("cycle fails closed rather than spinning", func(t *testing.T) {
		// A↔B cycle: each names the other as base, none is a root.
		a := mk("A", "B", "", NodeStatePending)
		b := mk("B", "A", "", NodeStatePending)
		_, err := BaseBranchSliding(stack, b, ByTask([]StackNode{a, b}))
		if !errors.Is(err, ErrCycle) {
			t.Fatalf("err = %v, want ErrCycle", err)
		}
	})
}
