package loomgittest

import (
	"context"
	"errors"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

// Store is a concurrency-safe in-memory implementation of the journal port.
type Store struct {
	mu             sync.Mutex
	entries        map[string]loomgit.JournalEntry
	byRequest      map[string]string
	leases         map[string]loomgit.Lease
	events         []loomgit.OutboxEvent
	workspaceRepos map[string][]loomgit.WorkspaceRepo
	nextID         int64
}

func NewStore() *Store {
	return &Store{entries: make(map[string]loomgit.JournalEntry), byRequest: make(map[string]string), leases: make(map[string]loomgit.Lease), workspaceRepos: make(map[string][]loomgit.WorkspaceRepo)}
}
func (s *Store) Begin(_ context.Context, requestID, operation string) (loomgit.JournalEntry, bool, error) {
	if requestID == "" || operation == "" {
		return loomgit.JournalEntry{}, false, errors.New("request ID and operation are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.byRequest[requestID]; ok {
		return clone(s.entries[id]), false, nil
	}
	s.nextID++
	e := loomgit.JournalEntry{ID: requestID, RequestID: requestID, Operation: operation, Phase: "started", Version: 1, Fence: 1}
	s.entries[e.ID] = e
	s.byRequest[requestID] = e.ID
	return clone(e), true, nil
}
func (s *Store) Get(_ context.Context, id string) (loomgit.JournalEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[id]
	if !ok {
		return e, journal.ErrNotFound
	}
	return clone(e), nil
}
func (s *Store) OpenEntries(_ context.Context) ([]loomgit.JournalEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []loomgit.JournalEntry
	for _, e := range s.entries {
		if e.Phase != "done" {
			out = append(out, clone(e))
		}
	}
	return out, nil
}
func (s *Store) Advance(_ context.Context, prior loomgit.JournalEntry, phase string, result []byte, events []loomgit.OutboxEvent) (loomgit.JournalEntry, error) {
	if phase == "" {
		return loomgit.JournalEntry{}, errors.New("phase is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[prior.ID]
	if !ok {
		return e, journal.ErrNotFound
	}
	if e.Version != prior.Version || e.Fence != prior.Fence || e.Phase == "done" {
		return e, journal.ErrStale
	}
	e.Phase = phase
	e.Version++
	if phase == "done" {
		e.Result = append([]byte(nil), result...)
	}
	s.entries[e.ID] = e
	for _, ev := range events {
		s.nextID++
		ev.ID = s.nextID
		ev.EntryID = e.ID
		ev.Payload = append([]byte(nil), ev.Payload...)
		s.events = append(s.events, ev)
	}
	return clone(e), nil
}
func (s *Store) Takeover(_ context.Context, prior loomgit.JournalEntry) (loomgit.JournalEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[prior.ID]
	if !ok {
		return e, journal.ErrNotFound
	}
	if e.Version != prior.Version || e.Fence != prior.Fence || e.Phase == "done" {
		return e, journal.ErrStale
	}
	e.Fence++
	e.Version++
	s.entries[e.ID] = e
	return clone(e), nil
}
func (s *Store) PendingEvents(_ context.Context) ([]loomgit.OutboxEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []loomgit.OutboxEvent
	for _, e := range s.events {
		if !e.Delivered {
			e.Payload = append([]byte(nil), e.Payload...)
			out = append(out, e)
		}
	}
	return out, nil
}
func (s *Store) MarkDelivered(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.events {
		if s.events[i].ID == id {
			s.events[i].Delivered = true
			return nil
		}
	}
	return journal.ErrNotFound
}
func (s *Store) Close() error { return nil }
func clone(e loomgit.JournalEntry) loomgit.JournalEntry {
	e.Result = append([]byte(nil), e.Result...)
	return e
}

func (s *Store) CommitWorkspace(_ context.Context, prior loomgit.JournalEntry, repos []loomgit.WorkspaceRepo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[prior.ID]
	if !ok || e.Version != prior.Version || e.Fence != prior.Fence || (e.Phase != "rows_written" && !(e.Operation == "attach_workspace_repos" && e.Phase == "started")) {
		return journal.ErrStale
	}
	seen := make(map[string]bool, len(repos))
	for _, repo := range repos {
		key := repo.Workspace + "\x00" + repo.Repo
		if seen[key] {
			return errors.New("workspace repo already exists")
		}
		seen[key] = true
		for _, existing := range s.workspaceRepos[repo.Workspace] {
			if existing.Repo == repo.Repo {
				return errors.New("workspace repo already exists")
			}
		}
	}
	for _, repo := range repos {
		s.workspaceRepos[repo.Workspace] = append(s.workspaceRepos[repo.Workspace], repo)
	}
	e.Phase = "done"
	e.Version++
	s.entries[e.ID] = e
	return nil
}

func (s *Store) AbortWorkspace(_ context.Context, prior loomgit.JournalEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[prior.ID]
	if !ok || e.Version != prior.Version || e.Fence != prior.Fence || e.Phase == "done" {
		return journal.ErrStale
	}
	delete(s.entries, prior.ID)
	delete(s.byRequest, prior.RequestID)
	return nil
}

func (s *Store) WorkspaceRepos(_ context.Context, workspace string) ([]loomgit.WorkspaceRepo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]loomgit.WorkspaceRepo(nil), s.workspaceRepos[workspace]...), nil
}

var _ loomgit.Store = (*Store)(nil)
var _ loomgit.WorkspaceStore = (*Store)(nil)
