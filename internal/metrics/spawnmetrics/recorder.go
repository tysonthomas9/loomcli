package spawnmetrics

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// SnapshotFileName is the file the daemon writes its spawn counters to inside
// the runtime directory.
const SnapshotFileName = "spawn-metrics.json"

// snapshotSchemaVersion is bumped when the on-disk shape changes incompatibly.
const snapshotSchemaVersion = 1

// flushInterval debounces writes: a wedged agent retry-loops far faster than
// this, and the snapshot only needs to be roughly current.
const flushInterval = time.Second

// SnapshotPath is the only place in the tree where SnapshotFileName is joined
// to a directory. Both the daemon writer and the serve-side reader call it, so
// the two can never drift apart.
func SnapshotPath(runtimeDir string) string {
	return filepath.Join(runtimeDir, SnapshotFileName)
}

// SpawnRow is one counter: spawns of a role that ended in a given status, with
// the failure class for failures (empty for successes).
type SpawnRow struct {
	Role       string `json:"role"`
	Status     string `json:"status"`
	ErrorClass Class  `json:"error_class"`
	Count      uint64 `json:"count"`
}

// Snapshot is the on-disk form of the counters. Spawns is a slice rather than a
// nested map so the JSON stays stable and diffable across writes.
type Snapshot struct {
	SchemaVersion           int        `json:"schema_version"`
	UpdatedAt               time.Time  `json:"updated_at"`
	LastSuccessfulSpawnUnix int64      `json:"last_successful_spawn_unix"`
	Spawns                  []SpawnRow `json:"spawns"`
}

const (
	statusSuccess = "success"
	statusFailure = "failure"
)

type key struct {
	role   string
	status string
	class  Class
}

// Recorder accumulates spawn outcomes in memory and flushes them to a JSON
// snapshot. Every method is safe on a nil receiver, so call sites need no
// guards and tests that construct a bare supervisor keep working.
type Recorder struct {
	mu          sync.Mutex
	path        string
	counts      map[key]uint64
	lastSuccess int64
	nextFlush   time.Time
}

// NewRecorder returns a Recorder writing to path, seeded from any snapshot
// already there so counters stay monotonic across daemon restarts.
func NewRecorder(path string) *Recorder {
	r := &Recorder{
		path:   path,
		counts: make(map[key]uint64),
	}
	if snap, err := Load(path); err == nil && snap != nil {
		for _, row := range snap.Spawns {
			r.counts[key{role: row.Role, status: row.Status, class: Normalize(string(row.ErrorClass))}] += row.Count
		}
		if snap.LastSuccessfulSpawnUnix > r.lastSuccess {
			r.lastSuccess = snap.LastSuccessfulSpawnUnix
		}
	}
	return r
}

// RecordFailure counts one failed spawn of role, under the normalized class.
func (r *Recorder) RecordFailure(role string, c Class) {
	if r == nil {
		return
	}
	r.record(key{role: role, status: statusFailure, class: Normalize(string(c))}, false)
}

// RecordSuccess counts one successful spawn of role and stamps the wall-clock
// time of the last success. This is the only writer of that timestamp; it is
// wall clock because Prometheus compares it against time().
func (r *Recorder) RecordSuccess(role string) {
	if r == nil {
		return
	}
	r.record(key{role: role, status: statusSuccess, class: ClassNone}, true)
}

func (r *Recorder) record(k key, success bool) {
	r.mu.Lock()
	r.counts[k]++
	if success {
		r.lastSuccess = time.Now().Unix()
	}
	now := time.Now()
	due := now.After(r.nextFlush)
	if due {
		r.nextFlush = now.Add(flushInterval)
	}
	r.mu.Unlock()

	if due {
		// A snapshot write is best-effort: losing one costs at most the counts
		// since the previous flush, and a spawn must not fail over it.
		_ = r.Flush()
	}
}

// Flush writes the current counters to the snapshot path via a temp file and a
// rename, so a reader never observes a partial file. It is safe to call
// unconditionally, including on shutdown.
func (r *Recorder) Flush() error {
	if r == nil {
		return nil
	}

	r.mu.Lock()
	snap := Snapshot{
		SchemaVersion:           snapshotSchemaVersion,
		UpdatedAt:               time.Now().UTC(),
		LastSuccessfulSpawnUnix: r.lastSuccess,
		Spawns:                  make([]SpawnRow, 0, len(r.counts)),
	}
	for k, count := range r.counts {
		snap.Spawns = append(snap.Spawns, SpawnRow{Role: k.role, Status: k.status, ErrorClass: k.class, Count: count})
	}
	path := r.path
	r.mu.Unlock()

	sort.Slice(snap.Spawns, func(i, j int) bool {
		a, b := snap.Spawns[i], snap.Spawns[j]
		if a.Role != b.Role {
			return a.Role < b.Role
		}
		if a.Status != b.Status {
			return a.Status < b.Status
		}
		return a.ErrorClass < b.ErrorClass
	})

	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	// PID in the temp name so two daemons writing the same path cannot collide.
	tempFile := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(tempFile, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tempFile, path); err != nil {
		os.Remove(tempFile)
		return err
	}
	return nil
}

// Load reads a snapshot from path. A missing file comes back as an unwrapped
// os.ErrNotExist, so a reader can tell "never spawned" from "corrupt".
func Load(path string) (*Snapshot, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is the daemon runtime directory, resolved by the caller
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}

	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}
