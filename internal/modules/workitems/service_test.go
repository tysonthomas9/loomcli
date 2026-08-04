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
	searchQuery    SearchQuery
	getQuery       GetQuery
	claimCommand   ClaimCommand
	reopenCommand  ReopenCommand
	deleteCommand  DeleteCommand
	eventsQuery    ListEventsQuery
	comment        *Comment
	comments       []*Comment
	dependencies   []Dependency
	issues         []IssueSummary
	claimed        *IssueDetail
	deleteResult   DeleteResult
	events         []*Event
	err            error
}

func (f *fakeStore) Search(_ context.Context, query SearchQuery) ([]IssueSummary, error) {
	f.searchQuery = query
	return f.issues, f.err
}
func (f *fakeStore) Get(_ context.Context, query GetQuery) (*IssueDetail, error) {
	f.getQuery = query
	return f.claimed, f.err
}
func (f *fakeStore) Claim(_ context.Context, command ClaimCommand) (*IssueDetail, error) {
	f.claimCommand = command
	return f.claimed, f.err
}
func (f *fakeStore) Reopen(_ context.Context, command ReopenCommand) error {
	f.reopenCommand = command
	return f.err
}
func (f *fakeStore) Delete(_ context.Context, command DeleteCommand) (DeleteResult, error) {
	f.deleteCommand = command
	return f.deleteResult, f.err
}
func (f *fakeStore) ListEvents(_ context.Context, query ListEventsQuery) ([]*Event, error) {
	f.eventsQuery = query
	return f.events, f.err
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

func TestServiceSearchOwnsValidationAndCopiesResults(t *testing.T) {
	store := &fakeStore{issues: []IssueSummary{{ID: "TASK-1", Labels: []string{"proof"}}}}
	service, _ := New(store)
	values, err := service.Search(context.Background(), SearchQuery{Query: " proof ", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if store.searchQuery.Query != "proof" || store.searchQuery.Limit != 2 {
		t.Fatalf("unexpected search query: %#v", store.searchQuery)
	}
	values[0].Labels[0] = "mutated"
	if store.issues[0].Labels[0] != "proof" {
		t.Fatal("search leaked a durable labels slice")
	}
}

func TestServiceClaimRequiresCanonicalInProgressResult(t *testing.T) {
	store := &fakeStore{claimed: &IssueDetail{ID: "TASK-1", Status: "in_progress", Labels: []string{}, Dependencies: []Dependency{}, Dependents: []Dependency{}, Comments: []*Comment{}}}
	service, _ := New(store)
	value, err := service.Claim(context.Background(), ClaimCommand{IssueID: " TASK-1 "})
	if err != nil {
		t.Fatal(err)
	}
	if store.claimCommand.IssueID != "TASK-1" || value.ID != "TASK-1" {
		t.Fatalf("unexpected claim command=%#v result=%#v", store.claimCommand, value)
	}
	store.claimed.Status = "open"
	if _, err := service.Claim(context.Background(), ClaimCommand{IssueID: "TASK-1"}); !errors.Is(err, ErrInvalidPersistedState) {
		t.Fatalf("expected invalid persisted state, got %v", err)
	}
}

func TestServiceGetValidatesIdentity(t *testing.T) {
	store := &fakeStore{claimed: &IssueDetail{ID: "TASK-1", Status: "open", Labels: []string{}, Dependencies: []Dependency{}, Dependents: []Dependency{}, Comments: []*Comment{}}}
	service, _ := New(store)
	if _, err := service.Get(context.Background(), GetQuery{IssueID: " TASK-1 "}); err != nil {
		t.Fatal(err)
	}
	if store.getQuery.IssueID != "TASK-1" {
		t.Fatalf("unexpected get query: %#v", store.getQuery)
	}
}

func TestServiceLifecycleCommandsOwnValidation(t *testing.T) {
	store := &fakeStore{deleteResult: DeleteResult{DeletedCount: 1, DeletedIDs: []string{"TASK-1"}}}
	service, _ := New(store)
	if err := service.Reopen(context.Background(), ReopenCommand{IssueID: " TASK-1 ", Reason: "retry"}); err != nil {
		t.Fatal(err)
	}
	if store.reopenCommand.IssueID != "TASK-1" {
		t.Fatalf("unexpected reopen command: %#v", store.reopenCommand)
	}
	result, err := service.Delete(context.Background(), DeleteCommand{IssueID: " TASK-1 "})
	if err != nil {
		t.Fatal(err)
	}
	if result.DeletedCount != 1 || store.deleteCommand.IssueID != "TASK-1" {
		t.Fatalf("unexpected delete result=%#v command=%#v", result, store.deleteCommand)
	}
	if _, err := service.Search(context.Background(), SearchQuery{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected invalid empty search, got %v", err)
	}
}

func TestServiceListEventsRejectsNilPersistedEvent(t *testing.T) {
	service, _ := New(&fakeStore{events: []*Event{nil}})
	_, err := service.ListEvents(context.Background(), ListEventsQuery{IssueID: "TASK-1", Limit: 100})
	if !errors.Is(err, ErrInvalidPersistedState) {
		t.Fatalf("expected invalid persisted state, got %v", err)
	}
}
