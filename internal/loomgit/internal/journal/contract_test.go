package journal_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/loomgittest"
)

func TestStoreContract(t *testing.T) {
	factories := map[string]func(*testing.T) loomgit.Store{
		"memory": func(t *testing.T) loomgit.Store { return loomgittest.NewStore() },
		"sqlite": func(t *testing.T) loomgit.Store {
			s, err := journal.OpenSQLite(filepath.Join(t.TempDir(), "journal.db"))
			if err != nil {
				t.Fatal(err)
			}
			return s
		},
	}
	for name, factory := range factories {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			s := factory(t)
			t.Cleanup(func() { s.Close() })
			var effects int
			for i := 0; i < 2; i++ {
				result, err := journal.Execute(ctx, s, "request-1", "create", func(context.Context) ([]byte, error) { effects++; return []byte("saved-result"), nil })
				if err != nil || string(result) != "saved-result" {
					t.Fatalf("execute: %q, %v", result, err)
				}
			}
			if effects != 1 {
				t.Fatalf("effect ran %d times", effects)
			}
			lease, err := s.ClaimLease(ctx, "workspace-1", "serve", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.ClaimLease(ctx, "workspace-1", "daemon", time.Minute); !errors.Is(err, journal.ErrLeaseHeld) {
				t.Fatalf("competing lease: %v", err)
			}
			lease, err = s.RenewLease(ctx, lease, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.ReleaseLease(ctx, lease); err != nil {
				t.Fatal(err)
			}
			next, err := s.ClaimLease(ctx, "workspace-1", "daemon", time.Minute)
			if err != nil || next.Fence != lease.Fence+1 {
				t.Fatalf("new lease: %+v, %v", next, err)
			}
			if _, err := s.RenewLease(ctx, lease, time.Minute); !errors.Is(err, journal.ErrStale) {
				t.Fatalf("stale lease: %v", err)
			}
			started, created, err := s.Begin(ctx, "request-2", "publish")
			if err != nil {
				t.Fatal(err)
			}
			if !created {
				t.Fatal("new request was not created")
			}
			if _, err := journal.Execute(ctx, s, "request-2", "publish", func(context.Context) ([]byte, error) { t.Error("unfinished effect ran"); return nil, nil }); !errors.Is(err, journal.ErrNeedsReplay) {
				t.Fatalf("unfinished replay: %v", err)
			}
			open, err := s.OpenEntries(ctx)
			if err != nil || len(open) != 1 || open[0].ID != started.ID || open[0].Phase != "started" {
				t.Fatalf("open entries: %+v, %v", open, err)
			}
			fenced, err := s.Takeover(ctx, started)
			if err != nil || fenced.Fence != started.Fence+1 {
				t.Fatalf("takeover: %+v, %v", fenced, err)
			}
			if _, err = s.Advance(ctx, started, "effect_applied", nil, nil); !errors.Is(err, journal.ErrStale) {
				t.Fatalf("old fence: %v", err)
			}
			var wins atomic.Int32
			var wg sync.WaitGroup
			for i := 0; i < 2; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, err := s.Advance(ctx, fenced, "effect_applied", nil, []loomgit.OutboxEvent{{Kind: "git.status_changed", Payload: []byte(`{"ok":true}`)}})
					if err == nil {
						wins.Add(1)
					} else if !errors.Is(err, journal.ErrStale) {
						t.Errorf("advance: %v", err)
					}
				}()
			}
			wg.Wait()
			if wins.Load() != 1 {
				t.Fatalf("winners: %d", wins.Load())
			}
			events, err := s.PendingEvents(ctx)
			if err != nil || len(events) != 1 || events[0].EntryID != started.ID {
				t.Fatalf("outbox: %+v, %v", events, err)
			}
			if err := s.MarkDelivered(ctx, events[0].ID); err != nil {
				t.Fatal(err)
			}
			events, err = s.PendingEvents(ctx)
			if err != nil || len(events) != 0 {
				t.Fatalf("delivered outbox: %+v, %v", events, err)
			}
		})
	}
}

func TestSQLiteSurvivesProcessExitAndFencesProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.db")
	cmd := exec.Command(os.Args[0], "-test.run=TestJournalChildProcess")
	cmd.Env = append(os.Environ(), "LOOMGIT_JOURNAL_CHILD=begin", "LOOMGIT_JOURNAL_PATH="+path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child begin: %v: %s", err, out)
	}
	s, err := journal.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	open, err := s.OpenEntries(context.Background())
	if err != nil || len(open) != 1 || open[0].Phase != "started" {
		t.Fatalf("post-exit open entries: %+v, %v", open, err)
	}
	var wg sync.WaitGroup
	results := make(chan string, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := exec.Command(os.Args[0], "-test.run=TestJournalChildProcess")
			c.Env = append(os.Environ(), "LOOMGIT_JOURNAL_CHILD=advance", "LOOMGIT_JOURNAL_PATH="+path)
			out, err := c.CombinedOutput()
			if err != nil {
				results <- "error:" + err.Error() + ":" + string(out)
			} else {
				results <- strings.TrimSpace(string(out))
			}
		}()
	}
	wg.Wait()
	close(results)
	var won, stale int
	for r := range results {
		if strings.Contains(r, "WIN") {
			won++
		} else if strings.Contains(r, "STALE") {
			stale++
		} else {
			t.Errorf("child result: %s", r)
		}
	}
	if won != 1 || stale != 1 {
		t.Fatalf("process winners=%d stale=%d", won, stale)
	}
}

func TestJournalChildProcess(t *testing.T) {
	mode := os.Getenv("LOOMGIT_JOURNAL_CHILD")
	if mode == "" {
		return
	}
	s, err := journal.OpenSQLite(os.Getenv("LOOMGIT_JOURNAL_PATH"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if mode == "begin" {
		if _, _, err := s.Begin(ctx, "crashed", "create"); err != nil {
			t.Fatal(err)
		}
		return
	}
	if mode == "advance" {
		e, err := s.Get(ctx, "crashed")
		if err != nil {
			t.Fatal(err)
		}
		e.Version = 1
		e.Fence = 1
		_, err = s.Advance(ctx, e, "done", []byte("ok"), nil)
		if err == nil {
			println("WIN")
		} else if errors.Is(err, journal.ErrStale) {
			println("STALE")
		} else {
			t.Fatal(err)
		}
		return
	}
	t.Fatalf("unknown child mode %q", mode)
}

func TestUnwritableSQLiteStopsBeforeEffect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.db")
	s, err := journal.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if err := os.Chmod(path, 0400); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(path, 0600)
	locked, err := journal.OpenSQLite(path)
	if err == nil {
		defer locked.Close()
		_, err = journal.Execute(context.Background(), locked, "blocked", "create", func(context.Context) ([]byte, error) {
			t.Error("effect ran after store write failure")
			return nil, nil
		})
	}
	if err == nil {
		t.Fatal("unwritable store accepted a write")
	}
}

func TestSQLiteResultAndOutboxSurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.db")
	ctx := context.Background()
	s, err := journal.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	e, created, err := s.Begin(ctx, "durable", "publish")
	if err != nil || !created {
		t.Fatalf("begin: %+v %v", e, err)
	}
	_, err = s.Advance(ctx, e, "done", []byte("published"), []loomgit.OutboxEvent{{Kind: "git.published", Payload: []byte(`{"sha":"abc"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = journal.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	called := false
	got, err := journal.Execute(ctx, s, "durable", "publish", func(context.Context) ([]byte, error) { called = true; return nil, nil })
	if err != nil || string(got) != "published" || called {
		t.Fatalf("replay: %q called=%v err=%v", got, called, err)
	}
	events, err := s.PendingEvents(ctx)
	if err != nil || len(events) != 1 || events[0].Kind != "git.published" {
		t.Fatalf("reopened outbox: %+v %v", events, err)
	}
}
