package workitems

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeStore struct {
	commentCommand AddCommentCommand
	dependency     AddDependencyCommand
	comment        *Comment
	comments       []*Comment
	dependencies   []Dependency
	err            error
}

func (f *fakeStore) AddComment(_ context.Context, command AddCommentCommand) (*Comment, error) {
	f.commentCommand = command
	return f.comment, f.err
}
func (f *fakeStore) ListComments(context.Context, ListCommentsQuery) ([]*Comment, error) {
	return f.comments, f.err
}
func (f *fakeStore) AddDependency(_ context.Context, command AddDependencyCommand) error {
	f.dependency = command
	return f.err
}
func (f *fakeStore) RemoveDependency(context.Context, RemoveDependencyCommand) error { return f.err }
func (f *fakeStore) ListDependencies(context.Context, ListDependenciesQuery) ([]Dependency, error) {
	return f.dependencies, f.err
}

func TestServiceAddCommentOwnsNormalization(t *testing.T) {
	store := &fakeStore{comment: &Comment{IssueID: "TASK-1", Text: "hello", Author: "web-ui"}}
	service, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	comment, err := service.AddComment(context.Background(), AddCommentCommand{IssueID: " TASK-1 ", Text: "  hello  "})
	if err != nil {
		t.Fatal(err)
	}
	if store.commentCommand.IssueID != "TASK-1" || store.commentCommand.Text != "hello" || store.commentCommand.Author != "web-ui" {
		t.Fatalf("unexpected normalized command: %#v", store.commentCommand)
	}
	comment.Text = "mutated"
	if store.comment.Text != "hello" {
		t.Fatal("service leaked the durable comment pointer")
	}
}

func TestServiceRejectsInvalidCommentBeforeStore(t *testing.T) {
	service, _ := New(&fakeStore{})
	_, err := service.AddComment(context.Background(), AddCommentCommand{IssueID: "TASK-1", Text: strings.Repeat("x", maxCommentBytes+1)})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid, got %v", err)
	}
}

func TestServiceAddDependencyOwnsDefaultsAndSelfCheck(t *testing.T) {
	store := &fakeStore{}
	service, _ := New(store)
	if err := service.AddDependency(context.Background(), AddDependencyCommand{IssueID: "TASK-1", DependsOnID: "TASK-2"}); err != nil {
		t.Fatal(err)
	}
	if store.dependency.Type != "blocks" {
		t.Fatalf("expected blocks default, got %q", store.dependency.Type)
	}
	if err := service.AddDependency(context.Background(), AddDependencyCommand{IssueID: "TASK-1", DependsOnID: "TASK-1"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected self dependency rejection, got %v", err)
	}
}

func TestServiceFailsClosedOnInvalidPersistedComment(t *testing.T) {
	service, _ := New(&fakeStore{comment: &Comment{IssueID: "OTHER", Text: "hello"}})
	_, err := service.AddComment(context.Background(), AddCommentCommand{IssueID: "TASK-1", Text: "hello"})
	if !errors.Is(err, ErrInvalidPersistedState) {
		t.Fatalf("expected ErrInvalidPersistedState, got %v", err)
	}
}
