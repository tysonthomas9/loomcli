package sourcecontrol

import (
	"context"
	"errors"
	"testing"
	"time"
)

type taskOutcomeStoreStub struct {
	stacks    []TaskStack
	nodes     map[string][]TaskStackNode
	updated   TaskStackOutcomeMutation
	stackID   string
	taskID    string
	updateErr error
}

func (store *taskOutcomeStoreStub) ListTaskStacks(context.Context, string) ([]TaskStack, error) {
	return append([]TaskStack(nil), store.stacks...), nil
}

func (store *taskOutcomeStoreStub) ListTaskStackNodes(_ context.Context, _ string, stackID string) ([]TaskStackNode, error) {
	return append([]TaskStackNode(nil), store.nodes[stackID]...), nil
}

func (store *taskOutcomeStoreStub) UpdateTaskStackOutcome(
	_ context.Context, _ string, stackID, taskID string, mutation TaskStackOutcomeMutation,
) error {
	store.stackID, store.taskID, store.updated = stackID, taskID, mutation
	return store.updateErr
}

func TestTaskOutcomeServiceOwnsPublishedAndEmptyTransitions(t *testing.T) {
	now := time.Date(2026, 8, 4, 13, 0, 0, 0, time.UTC)
	store := &taskOutcomeStoreStub{
		stacks: []TaskStack{{StackID: "epic:E", WorkspaceKey: "WS", Repository: "acme/widgets"}},
		nodes:  map[string][]TaskStackNode{"epic:E": {{TaskID: "A"}, {TaskID: "B"}}},
	}
	service, err := NewTaskOutcomes(store, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	recorded, err := service.RecordTaskOutcome(t.Context(), TaskOutcomeCommand{
		WorkspaceKey: "WS", Repository: "acme/widgets", TaskID: "A",
		Metadata: map[string]string{"delivery": "pull_request", "github_head_sha": "deadbeef"},
	})
	if err != nil || !recorded || store.stackID != "epic:E" || store.taskID != "A" ||
		store.updated.State != TaskOutcomePublished || store.updated.OutputSHA != "deadbeef" ||
		store.updated.PublishedAt == nil || !store.updated.PublishedAt.Equal(now) {
		t.Fatalf("published outcome = recorded %v err %v mutation %#v", recorded, err, store.updated)
	}
	recorded, err = service.RecordTaskOutcome(t.Context(), TaskOutcomeCommand{
		WorkspaceKey: "WS", Repository: "acme/widgets", TaskID: "A",
		Metadata: map[string]string{"delivery": "pull_request_skipped_no_changes"},
	})
	if err != nil || !recorded || store.updated.State != TaskOutcomeEmpty || store.updated.PublishedAt != nil {
		t.Fatalf("empty outcome = recorded %v err %v mutation %#v", recorded, err, store.updated)
	}
}

func TestTaskOutcomeServiceRejectsAmbiguousAndEscapedLineage(t *testing.T) {
	service, err := NewTaskOutcomes(&taskOutcomeStoreStub{
		stacks: []TaskStack{
			{StackID: "one", WorkspaceKey: "WS", Repository: "repo"},
			{StackID: "two", WorkspaceKey: "WS", Repository: "repo"},
		},
		nodes: map[string][]TaskStackNode{"one": {{TaskID: "A"}}, "two": {{TaskID: "A"}}},
	}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	recorded, err := service.RecordTaskOutcome(t.Context(), TaskOutcomeCommand{
		WorkspaceKey: "WS", Repository: "repo", TaskID: "A", Metadata: map[string]string{"files_changed": "0"},
	})
	if err != nil || recorded {
		t.Fatalf("ambiguous outcome = %v, %v", recorded, err)
	}

	escaped, err := NewTaskOutcomes(&taskOutcomeStoreStub{
		stacks: []TaskStack{{StackID: "one", WorkspaceKey: "OTHER", Repository: "repo"}},
	}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = escaped.RecordTaskOutcome(t.Context(), TaskOutcomeCommand{
		WorkspaceKey: "WS", Repository: "repo", TaskID: "A", Metadata: map[string]string{"files_changed": "0"},
	})
	if !errors.Is(err, ErrInvalidMaterialization) {
		t.Fatalf("escaped lineage error = %v, want invalid materialization", err)
	}
}
