package loomstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func openAt(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func ptr(s string) *string { return &s }

func agent(id, mode string) Agent {
	return Agent{AgentID: id, WorkspaceID: "ws", Name: id, Preset: "lead", PresetVersion: "1",
		Mode: "persistent", InteractionMode: mode, RoleKind: "interactive", SpecJSON: "{}",
		SpecVersion: 1, OwnerKind: "user", OwnerID: "u", CreatedByKind: "user", CreatedByID: "u",
		CreateRequestID: "req-" + id, Repo: "/repo", Harness: "opencode", State: "idle"}
}

func TestSchemaPragmasAndMigrationRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "loom.db")
	s := openAt(t, path)
	var mode string
	var busy, version int
	s.db.QueryRow("PRAGMA journal_mode").Scan(&mode)
	s.db.QueryRow("PRAGMA busy_timeout").Scan(&busy)
	s.db.QueryRow("PRAGMA user_version").Scan(&version)
	if mode != "wal" || busy == 0 || version != len(migrations) {
		t.Fatalf("journal_mode=%q busy_timeout=%d user_version=%d", mode, busy, version)
	}
	if err := s.InsertAgent(ctx, agent("a1", "interactive")); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s = openAt(t, path) // reopening must not re-run migrations or lose rows
	s.db.QueryRow("PRAGMA user_version").Scan(&version)
	if version != len(migrations) {
		t.Fatalf("user_version after reopen = %d", version)
	}
	if a, err := s.GetAgent(ctx, "a1"); err != nil || a.Host != "local" || a.InteractionMode != "interactive" {
		t.Fatalf("GetAgent after reopen = %+v, %v", a, err)
	}
}

func TestRegistryUniquenessAndImmutableInteractionMode(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, filepath.Join(t.TempDir(), "loom.db"))
	a := agent("a1", "interactive")
	a.ExternalKey = ptr("issue:1")
	if err := s.InsertAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*Agent){
		"agent_id":     func(b *Agent) { b.Name, b.CreateRequestID, b.ExternalKey = "n2", "r2", nil },
		"name":         func(b *Agent) { b.AgentID, b.CreateRequestID, b.ExternalKey = "a2", "r2", nil },
		"external_key": func(b *Agent) { b.AgentID, b.Name, b.CreateRequestID = "a2", "n2", "r2" },
		"create_req":   func(b *Agent) { b.AgentID, b.Name, b.ExternalKey = "a2", "n2", nil },
		"bad_mode": func(b *Agent) {
			b.AgentID, b.Name, b.CreateRequestID, b.ExternalKey, b.InteractionMode = "a2", "n2", "r2", nil, "other"
		},
	} {
		b := a
		mut(&b)
		if err := s.InsertAgent(ctx, b); err == nil {
			t.Errorf("%s: duplicate or invalid insert succeeded", name)
		}
	}
	// A tombstoned agent frees its name and ExternalKey.
	s.db.Exec(`UPDATE agents SET deleted_at = ? WHERE agent_id = 'a1'`, Stamp(time.Now()))
	b := agent("a2", "background")
	b.Name, b.ExternalKey = "a1", ptr("issue:1")
	if err := s.InsertAgent(ctx, b); err != nil {
		t.Fatalf("reuse after tombstone: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE agents SET interaction_mode = 'background' WHERE agent_id = 'a1'`); err == nil ||
		!strings.Contains(err.Error(), "immutable") {
		t.Fatalf("interaction_mode update err = %v", err)
	}
	if _, err := s.db.Exec(`UPDATE agents SET state = 'active' WHERE agent_id = 'a1'`); err != nil {
		t.Fatalf("other updates must still work: %v", err)
	}
}

func TestRegistryNativeSessionOwnership(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "loom.db")
	s := openAt(t, path)
	a := agent("a1", "interactive")
	a.HarnessSessionID = ptr("ses_1")
	if err := s.InsertAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertAgent(ctx, agent("a2", "interactive")); err != nil {
		t.Fatal(err)
	}
	for _, r := range [][3]string{{"codex", "thr_1", "switch"}, {"codex", "thr_2", "move"}, {"codex", "thr_2", "resume"}} {
		if err := s.RecordNativeSession(ctx, "a1", r[0], r[1], r[2]); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecordNativeSession(ctx, "a2", "codex", "thr_1", "switch"); !errors.Is(err, ErrSessionOwned) {
		t.Fatalf("stealing a session err = %v", err)
	}
	if err := s.RecordNativeSession(ctx, "a1", "codex", "thr_3", "guess"); err == nil {
		t.Fatal("unknown origin accepted")
	}
	if _, err := s.db.Exec(`DELETE FROM agent_native_sessions`); err == nil {
		t.Fatal("ownership rows deleted")
	}
	if _, err := s.db.Exec(`UPDATE agent_native_sessions SET agent_id = 'a2'`); err == nil {
		t.Fatal("ownership rows updated")
	}
	s.Close()
	s = openAt(t, path)
	got, err := s.NativeSessions(ctx, "a1")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, n := range got {
		ids = append(ids, n.Harness+"/"+n.SessionID+"/"+n.Origin)
	}
	if want := "opencode/ses_1/create codex/thr_1/switch codex/thr_2/move"; strings.Join(ids, " ") != want {
		t.Fatalf("sessions = %v, want %s", ids, want)
	}
	if owner, err := s.NativeSessionOwner(ctx, "codex", "thr_2"); err != nil || owner != "a1" {
		t.Fatalf("owner = %q, %v", owner, err)
	}
}

func TestRegistryRetentionSelectsOnlyEligible(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, filepath.Join(t.TempDir(), "loom.db"))
	now := time.Now()
	old, recent := Stamp(now.Add(-HistoryRetention-time.Hour)), Stamp(now.Add(-time.Hour))
	rows := []struct {
		id, mode string
		archived string
		finished string
		deleted  bool
		purged   bool
	}{
		{"i_old_archive", "interactive", old, "", false, false},       // due
		{"i_recent_archive", "interactive", recent, "", false, false}, // not yet
		{"i_finished_only", "interactive", "", old, false, false},     // interactive ignores finish
		{"b_old_finish", "background", "", old, false, false},         // due
		{"b_recent_finish", "background", "", recent, false, false},   // not yet
		{"b_archived_only", "background", old, "", false, false},      // background ignores archive
		{"i_deleted", "interactive", "", "", true, false},             // due: Delete purges now
		{"i_purged", "interactive", old, "", false, true},             // already purged
	}
	for _, r := range rows {
		a := agent(r.id, r.mode)
		if r.archived != "" {
			a.ArchivedAt = ptr(r.archived)
		}
		if r.finished != "" {
			a.FinishedAt = ptr(r.finished)
		}
		if r.deleted {
			a.DeletedAt = ptr(recent)
		}
		if r.purged {
			a.HistoryPurgedAt = ptr(recent)
		}
		if err := s.InsertAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	due, err := s.RetentionDue(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(due, ","); got != "b_old_finish,i_deleted,i_old_archive" {
		t.Fatalf("due = %s", got)
	}
	// Unarchive racing the sweep: the purge re-checks and refuses.
	if _, err := s.AppendEvent(ctx, Event{AgentID: "i_old_archive", EventID: "e1", Kind: "message", Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	s.db.Exec(`UPDATE agents SET archived_at = NULL WHERE agent_id = 'i_old_archive'`)
	if err := s.MarkHistoryPurged(ctx, "i_old_archive", now); !errors.Is(err, ErrNotDue) {
		t.Fatalf("purge after unarchive err = %v", err)
	}
	if p, _ := s.ListEvents(ctx, EventQuery{AgentID: "i_old_archive"}); len(p.Events) != 1 {
		t.Fatalf("events lost after refused purge: %+v", p)
	}
	if err := s.MarkHistoryPurged(ctx, "b_old_finish", now); err != nil {
		t.Fatal(err)
	}
	due, _ = s.RetentionDue(ctx, now)
	if got := strings.Join(due, ","); got != "i_deleted" {
		t.Fatalf("due after purge = %s", got)
	}
}

func TestAgentEventsConcurrentWritersAndRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "loom.db")
	s := openAt(t, path)
	for _, id := range []string{"a1", "a2"} {
		if err := s.InsertAgent(ctx, agent(id, "interactive")); err != nil {
			t.Fatal(err)
		}
	}
	// A second handle on the same file stands in for a concurrent writer.
	s2 := openAt(t, path)
	const writers, each = 8, 24
	var wg sync.WaitGroup
	errs := make(chan error, writers*each)
	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st := s
			if w%2 == 1 {
				st = s2
			}
			for i := range each {
				agentID := []string{"a1", "a2"}[i%2]
				_, err := st.AppendEvent(ctx, Event{AgentID: agentID, EventID: fmt.Sprintf("w%d-%d", w, i),
					Kind: "message", Payload: json.RawMessage(`{"text":"hi"}`)})
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	s2.Close()

	s = openAt(t, path)
	for _, id := range []string{"a1", "a2"} {
		p, err := s.ListEvents(ctx, EventQuery{AgentID: id, Limit: 1000})
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Events) != writers*each/2 || p.SnapshotSeq != int64(len(p.Events)) {
			t.Fatalf("%s: %d events, snapshot %d", id, len(p.Events), p.SnapshotSeq)
		}
		for i, e := range p.Events {
			if e.Seq != int64(i+1) {
				t.Fatalf("%s: seq gap at %d: %d", id, i, e.Seq)
			}
		}
	}
	// Re-appending a known EventID returns the stored row, no new seq.
	e, err := s.AppendEvent(ctx, Event{AgentID: "a1", EventID: "w0-0", Kind: "other", Payload: json.RawMessage(`{}`)})
	if p, _ := s.ListEvents(ctx, EventQuery{AgentID: "a1"}); err != nil || e.Kind != "message" || e.Seq < 1 || p.SnapshotSeq != writers*each/2 {
		t.Fatalf("duplicate EventID = %+v, %v (snapshot %d)", e, err, p.SnapshotSeq)
	}
	if _, err := s.db.Exec(`UPDATE agent_events SET kind = 'x'`); err == nil {
		t.Fatal("agent_events updated")
	}
}

func TestAgentEventsSnapshotPagingAndKinds(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, filepath.Join(t.TempDir(), "loom.db"))
	if err := s.InsertAgent(ctx, agent("a1", "interactive")); err != nil {
		t.Fatal(err)
	}
	add := func(id, kind string) {
		if _, err := s.AppendEvent(ctx, Event{AgentID: "a1", EventID: id, Kind: kind, TurnID: "t1", Payload: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 5 {
		add(fmt.Sprintf("e%d", i), []string{"message", "tool"}[i%2])
	}
	p1, err := s.ListEvents(ctx, EventQuery{AgentID: "a1", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if p1.SnapshotSeq != 5 || len(p1.Events) != 2 || !p1.More || p1.Next != 2 || p1.Events[0].TurnID != "t1" {
		t.Fatalf("page1 = %+v", p1)
	}
	add("late", "message") // appended after the snapshot: must not appear in this read
	var seqs []int64
	for p := p1; ; {
		for _, e := range p.Events {
			seqs = append(seqs, e.Seq)
		}
		if !p.More {
			break
		}
		if p, err = s.ListEvents(ctx, EventQuery{AgentID: "a1", After: p.Next, Snapshot: p.SnapshotSeq, Limit: 2}); err != nil {
			t.Fatal(err)
		}
	}
	if fmt.Sprint(seqs) != "[1 2 3 4 5]" {
		t.Fatalf("snapshot read = %v", seqs)
	}
	tools, err := s.ListEvents(ctx, EventQuery{AgentID: "a1", Kinds: []string{"tool"}})
	if err != nil || len(tools.Events) != 2 || tools.SnapshotSeq != 6 {
		t.Fatalf("kind filter = %+v, %v", tools, err)
	}
}

func TestRedactionAtWrite(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, filepath.Join(t.TempDir(), "loom.db"))
	if err := s.InsertAgent(ctx, agent("a1", "interactive")); err != nil {
		t.Fatal(err)
	}
	pattern := "ghp_" + strings.Repeat("aB3dE5fG7h", 3) + "123456" // gitleaks rule
	entropy := "Zq8vN2xLp4Rt7Wm9Ks1Yd6Hf3Jc5Bg0Ua"                 // no rule, high entropy
	payload := fmt.Sprintf(`{"text":"token %s here","nested":[{"out":"%s"}],"n":1}`, pattern, entropy)
	if _, err := s.AppendEvent(ctx, Event{AgentID: "a1", EventID: "e1", Kind: "tool", Payload: json.RawMessage(payload)}); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := s.db.QueryRow(`SELECT redacted_payload FROM agent_events`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, pattern) || strings.Contains(stored, entropy) || !strings.Contains(stored, "REDACTED") || !json.Valid([]byte(stored)) {
		t.Fatalf("stored payload not redacted: %s", stored)
	}
	if _, err := s.AppendEvent(ctx, Event{AgentID: "a1", EventID: "e2", Kind: "tool", Payload: json.RawMessage(`not json`)}); err == nil {
		t.Fatal("invalid JSON payload accepted")
	}
}
