// Package eventstore is loom's durable, authoritative record of a run's
// transcript: ONE events.jsonl per logical run (the dir the caller supplies),
// holding ALL of the run's EventEnvelopes — the parent conversation AND its
// subagents, distinguished within the file by (HarnessSessionID,
// ParentSessionID). It is the sink for harness.Run's OnEvent stream.
//
// Contract (plan review #16):
//   - identity/upsert = (RunID, HarnessSessionID, Event.ID()). Storage is
//     APPEND-ONLY, so "upsert" means APPEND a replacement row; last-writer-wins
//     is resolved on READ (dedup, keep the latest by file offset) and at
//     COMPACTION (rewrite collapsing superseded rows). This absorbs the OnEvent
//     contract's at-least-once delivery (hooks re-parse the whole file each
//     event; --resume replays earlier turns).
//   - AppendEnvelope is LOCAL + BOUNDED: a flock + local append ONLY — no
//     network / UI / fleet-db calls — so the synchronous OnEvent back-pressure
//     that can abort the harness is never blocked behind unrelated I/O.
//   - the durable row persists Source/NativeID/SchemaVersion (the public Event
//     JSON omits them); the authority filter + dedup depend on them.
package eventstore

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/olesho/harness-wrapper/pkg/transcript"

	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

// EventsFile is the per-run events file name (sibling to the native archive).
const EventsFile = "events.jsonl"

// Store is one run's events.jsonl, scoped to a directory the caller supplies
// (loom maps RunID → that dir). Concurrency across writers/readers/compaction is
// guarded by an exclusive flock on the file itself.
type Store struct {
	path string
}

// Open returns a Store for the events.jsonl under dir. It does not create the
// file; AppendEnvelope creates it on first write.
func Open(dir string) *Store {
	return &Store{path: filepath.Join(dir, EventsFile)}
}

// Path is the events.jsonl path (for diagnostics / siblings).
func (s *Store) Path() string { return s.path }

// storedRow is the DURABLE wire form of an EventEnvelope: every field explicit
// (incl. the internal Source/NativeID/SchemaVersion the public Event JSON omits).
type storedRow struct {
	RunID            string          `json:"run_id,omitempty"`
	Harness          string          `json:"harness,omitempty"`
	HarnessSessionID string          `json:"harness_session_id,omitempty"`
	ParentSessionID  string          `json:"parent_session_id,omitempty"`
	Seq              int             `json:"seq"`
	Timestamp        time.Time       `json:"timestamp"`
	Role             string          `json:"role,omitempty"`
	Type             string          `json:"type,omitempty"`
	Text             string          `json:"text,omitempty"`
	ToolName         string          `json:"tool_name,omitempty"`
	ToolUseID        string          `json:"tool_use_id,omitempty"`
	ToolInput        json.RawMessage `json:"tool_input,omitempty"`
	Output           string          `json:"output,omitempty"`
	UUID             string          `json:"uuid,omitempty"`
	Source           string          `json:"source,omitempty"`
	NativeID         string          `json:"native_id,omitempty"`
	SchemaVersion    int             `json:"schema_version,omitempty"`
}

func toRow(env transcript.EventEnvelope) storedRow {
	e := env.Event
	return storedRow{
		RunID: env.RunID, Harness: env.Harness,
		HarnessSessionID: env.HarnessSessionID, ParentSessionID: env.ParentSessionID,
		Seq: e.Seq, Timestamp: e.Timestamp, Role: e.Role, Type: e.Type, Text: e.Text,
		ToolName: e.ToolName, ToolUseID: e.ToolUseID, ToolInput: e.ToolInput,
		Output: e.Output, UUID: e.UUID,
		Source: e.Source, NativeID: e.NativeID, SchemaVersion: e.SchemaVersion,
	}
}

func (r storedRow) envelope() transcript.EventEnvelope {
	return transcript.EventEnvelope{
		RunID: r.RunID, Harness: r.Harness,
		HarnessSessionID: r.HarnessSessionID, ParentSessionID: r.ParentSessionID,
		Event: transcript.Event{
			Seq: r.Seq, Timestamp: r.Timestamp, Role: r.Role, Type: r.Type, Text: r.Text,
			ToolName: r.ToolName, ToolUseID: r.ToolUseID, ToolInput: r.ToolInput,
			Output: r.Output, UUID: r.UUID,
			Source: r.Source, NativeID: r.NativeID, SchemaVersion: r.SchemaVersion,
		},
	}
}

// dedupKey is the identity an append-only "upsert" collapses on.
func (r storedRow) dedupKey() string {
	return r.RunID + "\x00" + r.HarnessSessionID + "\x00" + r.envelope().Event.ID()
}

// AppendEnvelope appends one envelope as a durable row under an exclusive flock.
// It performs NO network/UI I/O. A duplicate (same dedupKey) is absorbed on read,
// so the caller may deliver the same logical event more than once.
func (s *Store) AppendEnvelope(env transcript.EventEnvelope) error {
	data, err := json.Marshal(toRow(env))
	if err != nil {
		return fmt.Errorf("eventstore: marshal row: %w", err)
	}
	data = append(data, '\n')

	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // run dir is loom-owned
	if err != nil {
		return fmt.Errorf("eventstore: open %s: %w", s.path, err)
	}
	defer func() { _ = f.Close() }()
	if err := lockfile.FlockExclusiveBlocking(f); err != nil {
		return fmt.Errorf("eventstore: flock: %w", err)
	}
	defer func() { _ = lockfile.FlockUnlock(f) }()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("eventstore: append: %w", err)
	}
	return nil
}

// Read returns the run's envelopes, deduped (last writer by file offset wins per
// identity) and ordered by (Timestamp, Seq, Event.ID()). Seq breaks ties for
// live events that share a zero native timestamp (it is the orchestrator's
// monotonic arrival order within this run). A missing file yields nil, nil.
func (s *Store) Read() ([]transcript.EventEnvelope, error) {
	rows, err := s.scan()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil // missing/empty store ⇒ no events
	}
	order, latest := dedupRows(rows)
	out := make([]transcript.EventEnvelope, 0, len(order))
	for _, k := range order {
		out = append(out, latest[k].envelope())
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Event, out[j].Event
		if !a.Timestamp.Equal(b.Timestamp) {
			return a.Timestamp.Before(b.Timestamp)
		}
		if a.Seq != b.Seq {
			return a.Seq < b.Seq
		}
		return a.ID() < b.ID()
	})
	return out, nil
}

// HasTranscript reports whether the store holds at least one event.
func (s *Store) HasTranscript() bool {
	rows, err := s.scan()
	return err == nil && len(rows) > 0
}

// Compact rewrites the file collapsing superseded duplicate rows to one row per
// identity (the latest), preserving file order of first appearance. Atomic
// (temp + rename) under the same flock. A missing file is a no-op.
func (s *Store) Compact() error {
	f, err := os.OpenFile(s.path, os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // run dir is loom-owned
	if err != nil {
		return fmt.Errorf("eventstore: open for compact: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := lockfile.FlockExclusiveBlocking(f); err != nil {
		return fmt.Errorf("eventstore: flock for compact: %w", err)
	}
	defer func() { _ = lockfile.FlockUnlock(f) }()

	rows, err := scanReader(f)
	if err != nil {
		return err
	}
	order, latest := dedupRows(rows)
	return writeRowsAtomic(s.path, order, latest)
}

// dedupRows collapses rows by identity, returning the unique keys in first-
// appearance order and the latest (highest-offset) row per key.
func dedupRows(rows []storedRow) (order []string, latest map[string]storedRow) {
	latest = make(map[string]storedRow, len(rows))
	for _, r := range rows {
		k := r.dedupKey()
		if _, seen := latest[k]; !seen {
			order = append(order, k)
		}
		latest[k] = r
	}
	return order, latest
}

// writeRowsAtomic writes the deduped rows (in order) to path via a temp file +
// rename, so a concurrent reader never sees a half-rewritten file.
func writeRowsAtomic(path string, order []string, latest map[string]storedRow) error {
	tmp := fmt.Sprintf("%s.compact-%d", path, os.Getpid())
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // sibling of the events file
	if err != nil {
		return fmt.Errorf("eventstore: open compact temp: %w", err)
	}
	w := bufio.NewWriter(out)
	for _, k := range order {
		data, mErr := json.Marshal(latest[k])
		if mErr != nil {
			_ = out.Close()
			_ = os.Remove(tmp)
			return fmt.Errorf("eventstore: marshal during compact: %w", mErr)
		}
		_, _ = w.Write(append(data, '\n'))
	}
	if err := w.Flush(); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("eventstore: flush compact: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("eventstore: close compact temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("eventstore: commit compact: %w", err)
	}
	return nil
}

// scan reads all rows under a shared-intent flock (we take exclusive for
// simplicity; reads are short). A missing file yields nil.
func (s *Store) scan() ([]storedRow, error) {
	f, err := os.Open(s.path) //nolint:gosec // run dir is loom-owned
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("eventstore: open %s: %w", s.path, err)
	}
	defer func() { _ = f.Close() }()
	if err := lockfile.FlockExclusiveBlocking(f); err != nil {
		return nil, fmt.Errorf("eventstore: flock for read: %w", err)
	}
	defer func() { _ = lockfile.FlockUnlock(f) }()
	return scanReader(f)
}

func scanReader(f *os.File) ([]storedRow, error) {
	if _, err := f.Seek(0, 0); err != nil {
		return nil, fmt.Errorf("eventstore: seek: %w", err)
	}
	var rows []storedRow
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var r storedRow
		if err := json.Unmarshal(line, &r); err != nil {
			// Skip a torn/garbled line rather than failing the whole read; a
			// concurrent append is atomic per line, so this is rare.
			continue
		}
		rows = append(rows, r)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("eventstore: scan: %w", err)
	}
	return rows, nil
}
