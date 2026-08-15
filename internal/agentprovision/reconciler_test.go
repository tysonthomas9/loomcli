package agentprovision

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func TestEnsureCreateConflictRegetsAndPatches(t *testing.T) {
	type entity struct {
		archived bool
		value    string
	}
	current := &entity{value: "old"}
	createCalls := 0
	beforeCalls := 0
	got, err := Ensure(t.Context(), "new", Reconciler[entity, string, string]{
		Create: func(context.Context, string) (*entity, error) {
			createCalls++
			return nil, domain.ErrAlreadyExists
		},
		Get:      func(context.Context) (*entity, error) { return current, nil },
		Archived: func(item *entity) bool { return item.archived },
		Diff:     func(item *entity) (string, bool) { return "new", item.value != "new" },
		BeforePatch: func(context.Context, *entity, string) error {
			beforeCalls++
			return nil
		},
		Patch: func(_ context.Context, item *entity, patch string) (*entity, error) {
			item.value = patch
			return item, nil
		},
	})
	if err != nil || got.value != "new" || createCalls != 1 || beforeCalls != 1 {
		t.Fatalf("Ensure = %+v err=%v create=%d before=%d", got, err, createCalls, beforeCalls)
	}
}

func TestEnsureRetriesCreateAfterCreateGetDeleteRace(t *testing.T) {
	type entity struct{ value string }
	createCalls := 0
	getCalls := 0
	got, err := Ensure(t.Context(), "new", Reconciler[entity, string, struct{}]{
		Create: func(context.Context, string) (*entity, error) {
			createCalls++
			if createCalls == 1 {
				return nil, domain.ErrAlreadyExists
			}
			return &entity{value: "new"}, nil
		},
		Get: func(context.Context) (*entity, error) {
			getCalls++
			return nil, domain.ErrNotFound
		},
	})
	if err != nil || got.value != "new" || createCalls != 2 || getCalls != 1 {
		t.Fatalf("Ensure race = %+v err=%v create=%d get=%d", got, err, createCalls, getCalls)
	}
}

func TestEnsureRejectsArchivedRecordBeforeDiff(t *testing.T) {
	type entity struct{ archived bool }
	diffCalled := false
	_, err := Ensure(t.Context(), struct{}{}, Reconciler[entity, struct{}, struct{}]{
		Create:   func(context.Context, struct{}) (*entity, error) { return nil, domain.ErrAlreadyExists },
		Get:      func(context.Context) (*entity, error) { return &entity{archived: true}, nil },
		Archived: func(item *entity) bool { return item.archived },
		Diff: func(*entity) (struct{}, bool) {
			diffCalled = true
			return struct{}{}, false
		},
	})
	if !errors.Is(err, domain.ErrInvalidTransition) || diffCalled {
		t.Fatalf("Ensure archived err=%v diffCalled=%v", err, diffCalled)
	}
}
