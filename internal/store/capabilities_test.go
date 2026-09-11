package store_test

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type taskCapabilityStore struct {
	store.Store
}

func (s *taskCapabilityStore) PutTaskBranch(context.Context, domain.TaskBranch) (*domain.TaskBranch, error) {
	return nil, nil
}

func (s *taskCapabilityStore) GetTaskBranch(context.Context, string, string, string) (*domain.TaskBranch, error) {
	return nil, nil
}

func (s *taskCapabilityStore) CreateTaskChangeSet(context.Context, domain.TaskChangeSet) (*domain.TaskChangeSet, error) {
	return nil, nil
}

func (s *taskCapabilityStore) GetTaskChangeSet(context.Context, string, string, int) (*domain.TaskChangeSet, error) {
	return nil, nil
}

func (s *taskCapabilityStore) UpdateTaskRunExecutionContext(context.Context, string, string, store.TaskRunExecutionContextUpdate) (*domain.TaskRun, error) {
	return nil, nil
}

type unwrappingStore struct {
	store.Store
	inner store.Store
}

func (s *unwrappingStore) UnwrapStore() store.Store { return s.inner }

func TestResolveTaskCapabilitiesThroughStoreDecorator(t *testing.T) {
	capabilities := &taskCapabilityStore{}
	wrapper := &unwrappingStore{inner: capabilities}

	handoff, ok := store.ResolveTaskChangeHandoffStore(wrapper)
	if !ok || handoff != capabilities {
		t.Fatalf("ResolveTaskChangeHandoffStore = %T, %v; want inner capability store", handoff, ok)
	}
	lifecycle, ok := store.ResolveTaskRunExecutionContextStore(wrapper)
	if !ok || lifecycle != capabilities {
		t.Fatalf("ResolveTaskRunExecutionContextStore = %T, %v; want inner capability store", lifecycle, ok)
	}
}

func TestResolveTaskCapabilitiesRejectsUnwrapCycle(t *testing.T) {
	cycle := &unwrappingStore{}
	cycle.inner = cycle
	if _, ok := store.ResolveTaskChangeHandoffStore(cycle); ok {
		t.Fatal("ResolveTaskChangeHandoffStore accepted an unwrap cycle")
	}
	if _, ok := store.ResolveTaskRunExecutionContextStore(cycle); ok {
		t.Fatal("ResolveTaskRunExecutionContextStore accepted an unwrap cycle")
	}
}
