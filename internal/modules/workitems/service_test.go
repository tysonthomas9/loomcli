package workitems

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeStore struct {
	createdCommand  CreateCommand
	created         *IssueSummary
	admissionChecks int
	admissionCalls  int
	admissionResult *RepositoryAdmissionResult
	commentCommand  AddCommentCommand
	dependency      AddDependencyCommand
	searchQuery     SearchQuery
	getQuery        GetQuery
	claimCommand    ClaimCommand
	reopenCommand   ReopenCommand
	deleteCommand   DeleteCommand
	eventsQuery     ListEventsQuery
	comment         *Comment
	comments        []*Comment
	dependencies    []Dependency
	issues          []IssueSummary
	blockedIssues   []IssueSummary
	readyIssues     []IssueSummary
	readyQuery      AvailabilityQuery
	deferredIssues  []IssueSummary
	listFilter      ListFilter
	claimed         *IssueDetail
	deleteResult    DeleteResult
	events          []*Event
	patched         PatchCommand
	closed          CloseCommand
	closeResult     *CloseResult
	assigned        AssignRepositoryCommand
	assignedIssue   *IssueSummary
	err             error
}

func (f *fakeStore) RequireRepositoryAdmission(context.Context) error {
	f.admissionChecks++
	return f.err
}
func (f *fakeStore) Create(_ context.Context, command CreateCommand) (*IssueSummary, error) {
	f.createdCommand = command
	return f.created, f.err
}
func (f *fakeStore) BlockRepositoryRequired(_ context.Context, _ string) (*RepositoryAdmissionResult, error) {
	f.admissionCalls++
	return f.admissionResult, f.err
}
func (f *fakeStore) List(_ context.Context, filter ListFilter) ([]IssueSummary, error) {
	f.listFilter = filter
	return f.issues, f.err
}
func (f *fakeStore) Blocked(context.Context, AvailabilityQuery) ([]IssueSummary, error) {
	return f.blockedIssues, f.err
}
func (f *fakeStore) Ready(_ context.Context, query AvailabilityQuery) ([]IssueSummary, error) {
	f.readyQuery = query
	return f.readyIssues, f.err
}
func (f *fakeStore) Deferred(context.Context, AvailabilityQuery) ([]IssueSummary, error) {
	return f.deferredIssues, f.err
}

func (f *fakeStore) Search(_ context.Context, query SearchQuery) ([]IssueSummary, error) {
	f.searchQuery = query
	return f.issues, f.err
}
func (f *fakeStore) Get(_ context.Context, query GetQuery) (*IssueDetail, error) {
	f.getQuery = query
	return f.claimed, f.err
}
func (f *fakeStore) Patch(_ context.Context, command PatchCommand) error {
	f.patched = command
	return f.err
}
func (f *fakeStore) Close(_ context.Context, command CloseCommand) (*CloseResult, error) {
	f.closed = command
	return f.closeResult, f.err
}
func (f *fakeStore) AssignRepository(_ context.Context, command AssignRepositoryCommand) (*IssueSummary, error) {
	f.assigned = command
	return f.assignedIssue, f.err
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

func TestServiceCreateOwnsRepositoryAdmissionAndCanonicalMerge(t *testing.T) {
	store := &fakeStore{
		created: &IssueSummary{ID: "TASK-1", Title: "Proof", Status: "open"},
		admissionResult: &RepositoryAdmissionResult{Issue: &IssueSummary{
			ID: "TASK-1", Title: "Proof", Status: "blocked", Labels: []string{"loom.repository-required"},
		}},
		claimed: &IssueDetail{ID: "TASK-1", Title: "Proof", Status: "open", Labels: []string{}, Dependencies: []Dependency{}, Dependents: []Dependency{}, Comments: []*Comment{}},
	}
	service, _ := New(store)
	result, err := service.Create(context.Background(), CreateCommand{Title: "Proof", IssueType: "task", Priority: 2})
	if err != nil {
		t.Fatal(err)
	}
	if store.admissionChecks != 1 || store.admissionCalls != 1 || result.Detail == nil || result.Detail.Status != "blocked" || len(result.Detail.Labels) != 1 {
		t.Fatalf("repository admission not preserved: checks=%d calls=%d result=%#v", store.admissionChecks, store.admissionCalls, result)
	}
}

func TestServiceCreateWithExplicitRepositorySkipsAdmission(t *testing.T) {
	store := &fakeStore{
		created: &IssueSummary{ID: "TASK-1", Title: "Proof", Status: "open", SourceRepo: "loomcli", Repo: "loomcli"},
		claimed: &IssueDetail{ID: "TASK-1", Title: "Proof", Status: "open", SourceRepo: "loomcli", Repo: "loomcli", Labels: []string{}, Dependencies: []Dependency{}, Dependents: []Dependency{}, Comments: []*Comment{}},
	}
	service, _ := New(store)
	result, err := service.Create(context.Background(), CreateCommand{Title: "Proof", IssueType: "task", Priority: 2, SourceRepo: "loomcli"})
	if err != nil {
		t.Fatal(err)
	}
	if store.admissionChecks != 0 || store.admissionCalls != 0 || result.Detail.SourceRepo != "loomcli" {
		t.Fatalf("explicit repository unexpectedly entered admission: %#v", result)
	}
}

func TestServiceCreateRejectsInvalidInputBeforePersistence(t *testing.T) {
	store := &fakeStore{}
	service, _ := New(store)
	if _, err := service.Create(context.Background(), CreateCommand{IssueType: "task", Priority: 2}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected invalid create, got %v", err)
	}
	if store.createdCommand.IssueType != "" || store.admissionChecks != 0 {
		t.Fatalf("invalid create reached persistence: %#v", store.createdCommand)
	}
}

func TestServiceListBuildsCanonicalKanbanProjection(t *testing.T) {
	store := &fakeStore{
		issues:         []IssueSummary{{ID: "TASK-1", Status: "open"}},
		blockedIssues:  []IssueSummary{{ID: "TASK-2", Status: "open", BlockedBy: []string{"TASK-9"}}},
		readyIssues:    []IssueSummary{{ID: "TASK-1", Status: "open"}},
		deferredIssues: []IssueSummary{{ID: "TASK-3", Status: "deferred"}},
	}
	service, _ := New(store)
	result, err := service.List(context.Background(), ListQuery{IncludeBlocked: true, Filter: ListFilter{Labels: []string{"proof"}}})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]KanbanItem{}
	for _, item := range result.KanbanIssues {
		byID[item.ID] = item
	}
	if !byID["TASK-1"].IsReady || !byID["TASK-2"].IsBlocked || byID["TASK-2"].BlockedByCount != 1 || !byID["TASK-3"].IsDeferred {
		t.Fatalf("unexpected kanban projection: %#v", byID)
	}
	if store.listFilter.Labels[0] != "proof" {
		t.Fatalf("list filter was not preserved: %#v", store.listFilter)
	}
}

func TestServiceListRejectsNegativeLimitBeforePersistence(t *testing.T) {
	store := &fakeStore{}
	service, _ := New(store)
	_, err := service.List(context.Background(), ListQuery{Filter: ListFilter{Limit: -1}})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid, got %v", err)
	}
	if store.listFilter.Limit != 0 {
		t.Fatalf("invalid list reached persistence: %#v", store.listFilter)
	}
}

func TestServiceReadyOwnsQueryAndProjection(t *testing.T) {
	labels := []string{"ready"}
	labelsAny := []string{"backend"}
	sourceRepos := []string{"loomcli"}
	store := &fakeStore{readyIssues: []IssueSummary{{ID: "TASK-1", Status: "open", Labels: []string{"persisted"}}}}
	service, _ := New(store)
	issues, err := service.Ready(context.Background(), AvailabilityQuery{
		Assignee: "agent-1", Labels: labels, LabelsAny: labelsAny, SourceRepos: sourceRepos,
		Limit: 10, SortPolicy: "priority", MolType: "work",
	})
	if err != nil {
		t.Fatal(err)
	}
	labels[0] = "mutated"
	labelsAny[0] = "mutated"
	sourceRepos[0] = "mutated"
	issues[0].Labels[0] = "mutated"
	if store.readyQuery.Labels[0] != "ready" || store.readyQuery.LabelsAny[0] != "backend" || store.readyQuery.SourceRepos[0] != "loomcli" {
		t.Fatalf("ready query leaked caller slices: %#v", store.readyQuery)
	}
	if store.readyIssues[0].Labels[0] != "persisted" {
		t.Fatalf("ready projection leaked durable slices: %#v", store.readyIssues[0])
	}
}

func TestServiceReadyRejectsInvalidInputAndPersistedState(t *testing.T) {
	store := &fakeStore{}
	service, _ := New(store)
	if _, err := service.Ready(context.Background(), AvailabilityQuery{Limit: -1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected invalid query, got %v", err)
	}
	if store.readyQuery.Limit != 0 {
		t.Fatalf("invalid ready query reached persistence: %#v", store.readyQuery)
	}
	store.readyIssues = []IssueSummary{{Status: "open"}}
	if _, err := service.Ready(context.Background(), AvailabilityQuery{}); !errors.Is(err, ErrInvalidPersistedState) {
		t.Fatalf("expected invalid persisted state, got %v", err)
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

func TestServicePatchSerializesLabelsAndReturnsCanonicalIssue(t *testing.T) {
	store := &fakeStore{claimed: &IssueDetail{ID: "TASK-1", Status: "open", Labels: []string{"proof"}, Dependencies: []Dependency{}, Dependents: []Dependency{}, Comments: []*Comment{}}}
	service, _ := New(store)
	title := "Updated"
	value, err := service.Patch(context.Background(), PatchCommand{IssueID: " TASK-1 ", Title: &title, AddLabels: []string{"proof"}})
	if err != nil {
		t.Fatal(err)
	}
	if store.patched.IssueID != "TASK-1" || store.getQuery.IssueID != "TASK-1" || value.ID != "TASK-1" {
		t.Fatalf("unexpected patch command=%#v get=%#v result=%#v", store.patched, store.getQuery, value)
	}
	store.patched.AddLabels[0] = "mutated"
	if value.Labels[0] != "proof" {
		t.Fatal("patch leaked durable labels into the returned issue")
	}
}

func TestServiceCloseOwnsIdempotencyAndValidatesCanonicalResult(t *testing.T) {
	store := &fakeStore{closeResult: &CloseResult{Closed: &IssueSummary{ID: "TASK-1", Status: "closed"}, Unblocked: []IssueSummary{{ID: "TASK-2", Labels: []string{"ready"}}}}}
	service, _ := New(store)
	result, err := service.Close(context.Background(), CloseCommand{IssueID: " TASK-1 ", Reason: "done"})
	if err != nil {
		t.Fatal(err)
	}
	if store.closed.IssueID != "TASK-1" || result.Closed.ID != "TASK-1" || len(result.Unblocked) != 1 {
		t.Fatalf("unexpected close command=%#v result=%#v", store.closed, result)
	}
	store.err = ErrAlreadyClosed
	result, err = service.Close(context.Background(), CloseCommand{IssueID: "TASK-1"})
	if err != nil || result.Closed.Status != "closed" {
		t.Fatalf("idempotent close failed: result=%#v err=%v", result, err)
	}
}

func TestServiceAssignRepositoryRequiresCanonicalOwnerResult(t *testing.T) {
	store := &fakeStore{assignedIssue: &IssueSummary{ID: "TASK-1", SourceRepo: "loomcli", Repo: "loomcli"}}
	service, _ := New(store)
	value, err := service.AssignRepository(context.Background(), AssignRepositoryCommand{IssueID: " TASK-1 ", Repository: " loomcli "})
	if err != nil {
		t.Fatal(err)
	}
	if store.assigned.IssueID != "TASK-1" || store.assigned.Repository != "loomcli" || value.SourceRepo != "loomcli" {
		t.Fatalf("unexpected assignment=%#v result=%#v", store.assigned, value)
	}
	store.assignedIssue.SourceRepo = "other"
	if _, err := service.AssignRepository(context.Background(), AssignRepositoryCommand{IssueID: "TASK-1", Repository: "loomcli"}); !errors.Is(err, ErrInvalidPersistedState) {
		t.Fatalf("expected invalid canonical assignment, got %v", err)
	}
}
