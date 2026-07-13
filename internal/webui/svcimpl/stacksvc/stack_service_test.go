package stacksvc

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/stacklineage"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
)

type fakeStackStore struct {
	stacks []stacklineage.Stack
	nodes  map[stacklineage.StackID][]stacklineage.Node
}

var _ stackstore.Store = (*fakeStackStore)(nil)

func (s *fakeStackStore) EnsureStack(context.Context, stacklineage.Stack) error {
	return nil
}

func (s *fakeStackStore) GetStack(context.Context, string, stacklineage.StackID) (*stacklineage.Stack, error) {
	return nil, stackstore.ErrStackNotFound
}

func (s *fakeStackStore) ListStacks(context.Context, string) ([]stacklineage.Stack, error) {
	return s.stacks, nil
}

func (s *fakeStackStore) DeleteStack(context.Context, string, stacklineage.StackID) error {
	return nil
}

func (s *fakeStackStore) ListNodes(_ context.Context, _ string, id stacklineage.StackID) ([]stacklineage.Node, error) {
	return s.nodes[id], nil
}

func (s *fakeStackStore) AddNode(context.Context, string, stacklineage.StackID, string, string, stacklineage.CommitMode) (stacklineage.Node, error) {
	return stacklineage.Node{}, nil
}

func (s *fakeStackStore) SetBase(context.Context, string, stacklineage.StackID, string, string) error {
	return nil
}

func (s *fakeStackStore) RemoveNode(context.Context, string, stacklineage.StackID, string) error {
	return nil
}

func (s *fakeStackStore) UpdateNode(context.Context, string, stacklineage.StackID, string, func(*stacklineage.Node) error) error {
	return nil
}

func TestService_NilStoreReturnsEmptyStacks(t *testing.T) {
	svc := newWithProvider(func() stackstore.Store { return nil })

	got, err := svc.ListStacks(context.Background(), "ws")
	if err != nil {
		t.Fatalf("ListStacks: %v", err)
	}
	if len(got.Stacks) != 0 {
		t.Fatalf("stacks len = %d, want 0", len(got.Stacks))
	}
}

func TestService_ListStacksHappyPath(t *testing.T) {
	stack := stacklineage.Stack{ID: "epic:E", RepoName: "repo-a", RootBase: "main"}
	store := &fakeStackStore{
		stacks: []stacklineage.Stack{stack},
		nodes: map[stacklineage.StackID][]stacklineage.Node{
			stack.ID: {
				{StackID: stack.ID, TaskID: "T1", OutputBranch: "task/T1", State: stacklineage.NodeStatePublished},
				{StackID: stack.ID, TaskID: "T2", BaseTaskID: "T1", OutputBranch: "task/T2", State: stacklineage.NodeStatePublished},
			},
		},
	}
	svc := newWithProvider(func() stackstore.Store { return store })

	got, err := svc.ListStacks(context.Background(), "ws")
	if err != nil {
		t.Fatalf("ListStacks: %v", err)
	}
	if len(got.Stacks) != 1 {
		t.Fatalf("stacks len = %d, want 1", len(got.Stacks))
	}
	gotStack := got.Stacks[0]
	if gotStack.ID != "epic:E" || gotStack.Repo != "repo-a" || gotStack.RootBase != "main" {
		t.Fatalf("stack = %+v", gotStack)
	}
	if len(gotStack.Nodes) != 2 {
		t.Fatalf("nodes len = %d, want 2", len(gotStack.Nodes))
	}
	if gotStack.Nodes[0].TaskID != "T1" || gotStack.Nodes[0].BaseRef != "main" || gotStack.Nodes[0].Position != 0 {
		t.Fatalf("root node = %+v", gotStack.Nodes[0])
	}
	if gotStack.Nodes[1].TaskID != "T2" || gotStack.Nodes[1].BaseRef != "task/T1" || gotStack.Nodes[1].Position != 1 {
		t.Fatalf("child node = %+v", gotStack.Nodes[1])
	}
}

func TestService_BaseBranchSlidingErrorOmitsBaseRef(t *testing.T) {
	stack := stacklineage.Stack{ID: "epic:E", RepoName: "repo-a", RootBase: "main"}
	store := &fakeStackStore{
		stacks: []stacklineage.Stack{stack},
		nodes: map[stacklineage.StackID][]stacklineage.Node{
			stack.ID: {
				{StackID: stack.ID, TaskID: "T2", BaseTaskID: "missing", OutputBranch: "task/T2", State: stacklineage.NodeStatePublished},
			},
		},
	}
	svc := newWithProvider(func() stackstore.Store { return store })

	got, err := svc.ListStacks(context.Background(), "ws")
	if err != nil {
		t.Fatalf("ListStacks: %v", err)
	}
	if got.Stacks[0].Nodes[0].BaseRef != "" {
		t.Fatalf("base_ref = %q, want empty", got.Stacks[0].Nodes[0].BaseRef)
	}
}
