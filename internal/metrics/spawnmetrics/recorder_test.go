package spawnmetrics

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func tempSnapshotPath(t *testing.T) string {
	t.Helper()
	return SnapshotPath(t.TempDir())
}

func findRow(t *testing.T, snap *Snapshot, role, status string, class Class) SpawnRow {
	t.Helper()
	for _, row := range snap.Spawns {
		if row.Role == role && row.Status == status && row.ErrorClass == class {
			return row
		}
	}
	t.Fatalf("no row for role=%q status=%q class=%q in %+v", role, status, class, snap.Spawns)
	return SpawnRow{}
}

func TestRecordFailureRoundTripsEveryLiveClass(t *testing.T) {
	classes := []Class{
		ClassBackendUnavailable,
		ClassMaterializeSkills,
		ClassBuildCommand,
		ClassStart,
		ClassUnknown,
	}

	for _, c := range classes {
		t.Run(string(c), func(t *testing.T) {
			path := tempSnapshotPath(t)
			r := NewRecorder(path)
			r.RecordFailure("worker", c)
			if err := r.Flush(); err != nil {
				t.Fatalf("Flush: %v", err)
			}

			snap, err := Load(path)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			row := findRow(t, snap, "worker", "failure", c)
			if row.Count != 1 {
				t.Errorf("count = %d, want 1", row.Count)
			}
			if snap.SchemaVersion != snapshotSchemaVersion {
				t.Errorf("schema version = %d, want %d", snap.SchemaVersion, snapshotSchemaVersion)
			}
		})
	}
}

func TestSpanReasonMatchesCallSiteLiterals(t *testing.T) {
	if got := ClassBackendUnavailable.SpanReason(); got != "spawn.backend_unavailable" {
		t.Errorf("SpanReason = %q", got)
	}
	if got := ClassMaterializeSkills.SpanReason(); got != "spawn.materialize_skills" {
		t.Errorf("SpanReason = %q", got)
	}
}

func TestNormalizeRejectsAnythingOutsideTheAllowlist(t *testing.T) {
	cases := []string{
		"spawn.something_new",
		"connection refused: dial tcp 127.0.0.1:3011",
		"Start",
		"unknown-ish",
	}
	for _, in := range cases {
		if got := Normalize(in); got != ClassUnknown {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, ClassUnknown)
		}
	}

	if got := Normalize("start"); got != ClassStart {
		t.Errorf("Normalize(\"start\") = %q, want %q", got, ClassStart)
	}
	if got := Normalize(""); got != ClassNone {
		t.Errorf("Normalize(\"\") = %q, want the success class", got)
	}
}

func TestSuccessStampsTimestampAndFailureLeavesItAlone(t *testing.T) {
	path := tempSnapshotPath(t)
	r := NewRecorder(path)

	r.RecordFailure("worker", ClassStart)
	if err := r.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	snap, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if snap.LastSuccessfulSpawnUnix != 0 {
		t.Fatalf("failure moved the timestamp to %d", snap.LastSuccessfulSpawnUnix)
	}

	before := time.Now().Unix()
	r.RecordSuccess("worker")
	if err := r.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	snap, err = Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if snap.LastSuccessfulSpawnUnix < before {
		t.Errorf("timestamp = %d, want >= %d", snap.LastSuccessfulSpawnUnix, before)
	}
	stamped := snap.LastSuccessfulSpawnUnix

	r.RecordFailure("worker", ClassBuildCommand)
	if err := r.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	snap, err = Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if snap.LastSuccessfulSpawnUnix != stamped {
		t.Errorf("timestamp = %d after a failure, want %d", snap.LastSuccessfulSpawnUnix, stamped)
	}
	if row := findRow(t, snap, "worker", "success", ClassNone); row.Count != 1 {
		t.Errorf("success count = %d, want 1", row.Count)
	}
}

func TestReloadKeepsCountersAndTimestampMonotonic(t *testing.T) {
	path := tempSnapshotPath(t)

	first := NewRecorder(path)
	first.RecordFailure("tester", ClassBackendUnavailable)
	first.RecordFailure("tester", ClassBackendUnavailable)
	first.RecordSuccess("tester")
	if err := first.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	before, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	second := NewRecorder(path)
	second.RecordFailure("tester", ClassBackendUnavailable)
	if err := second.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	after, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := findRow(t, after, "tester", "failure", ClassBackendUnavailable).Count; got != 3 {
		t.Errorf("failure count after restart = %d, want 3", got)
	}
	if got := findRow(t, after, "tester", "success", ClassNone).Count; got != 1 {
		t.Errorf("success count after restart = %d, want 1", got)
	}
	if after.LastSuccessfulSpawnUnix < before.LastSuccessfulSpawnUnix {
		t.Errorf("timestamp regressed across restart: %d < %d",
			after.LastSuccessfulSpawnUnix, before.LastSuccessfulSpawnUnix)
	}
}

func TestConcurrentRecording(t *testing.T) {
	path := tempSnapshotPath(t)
	r := NewRecorder(path)

	var wg sync.WaitGroup
	const goroutines = 8
	const perGoroutine = 50
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				r.RecordFailure("worker", ClassStart)
				r.RecordSuccess("worker")
			}
		}()
	}
	wg.Wait()

	if err := r.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	snap, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := uint64(goroutines * perGoroutine)
	if got := findRow(t, snap, "worker", "failure", ClassStart).Count; got != want {
		t.Errorf("failure count = %d, want %d", got, want)
	}
	if got := findRow(t, snap, "worker", "success", ClassNone).Count; got != want {
		t.Errorf("success count = %d, want %d", got, want)
	}
}

func TestNilRecorderIsInert(t *testing.T) {
	var r *Recorder
	r.RecordSuccess("x")
	r.RecordFailure("x", ClassStart)
	if err := r.Flush(); err != nil {
		t.Errorf("Flush on nil recorder: %v", err)
	}
}

func TestLoadReportsMissingFileUnwrapped(t *testing.T) {
	path := tempSnapshotPath(t)

	snap, err := Load(path)
	if snap != nil {
		t.Errorf("snapshot = %+v, want nil", snap)
	}
	if err != os.ErrNotExist { //nolint:errorlint // the unwrapped sentinel is the contract
		t.Fatalf("err = %v, want the bare os.ErrNotExist", err)
	}

	if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := Load(path); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Errorf("corrupt file err = %v, want a non-ErrNotExist error", err)
	}
}

func TestSnapshotPathIsTheSingleJoinSite(t *testing.T) {
	dir := t.TempDir()
	if got, want := SnapshotPath(dir), filepath.Join(dir, SnapshotFileName); got != want {
		t.Errorf("SnapshotPath = %q, want %q", got, want)
	}
}
