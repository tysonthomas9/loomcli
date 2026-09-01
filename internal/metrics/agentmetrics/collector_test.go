package agentmetrics

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/tysonthomas9/loomcli/internal/metrics/spawnmetrics"
)

func TestCollectorRendersSpawnSnapshot(t *testing.T) {
	dir := t.TempDir()
	writeSnapshot(t, dir, spawnmetrics.Snapshot{
		SchemaVersion:           1,
		LastSuccessfulSpawnUnix: 1756000000,
		Spawns: []spawnmetrics.SpawnRow{
			{Role: "tester", Status: "failure", ErrorClass: spawnmetrics.ClassStart, Count: 2},
			{Role: "worker", Status: "failure", ErrorClass: spawnmetrics.ClassMaterializeSkills, Count: 3},
			{Role: "worker", Status: "success", ErrorClass: spawnmetrics.ClassNone, Count: 7},
		},
	})

	want := `
# HELP loom_agent_spawns_total Total agent spawn attempts by role, outcome and failure class.
# TYPE loom_agent_spawns_total counter
loom_agent_spawns_total{error_class="",role="worker",status="success"} 7
loom_agent_spawns_total{error_class="materialize_skills",role="worker",status="failure"} 3
loom_agent_spawns_total{error_class="start",role="tester",status="failure"} 2
# HELP loom_last_successful_spawn_timestamp_seconds Unix timestamp of the most recent successful agent spawn.
# TYPE loom_last_successful_spawn_timestamp_seconds gauge
loom_last_successful_spawn_timestamp_seconds 1.756e+09
`
	if err := testutil.CollectAndCompare(New(dir), strings.NewReader(want),
		"loom_agent_spawns_total", "loom_last_successful_spawn_timestamp_seconds"); err != nil {
		t.Fatal(err)
	}
}

func TestMissingSnapshotEmitsNothingAndDoesNotPanic(t *testing.T) {
	c := New(t.TempDir())
	for _, name := range []string{"loom_agent_spawns_total", "loom_last_successful_spawn_timestamp_seconds"} {
		if got := testutil.CollectAndCount(c, name); got != 0 {
			t.Errorf("%s: collected %d samples from a missing snapshot, want 0", name, got)
		}
	}
}

func TestCorruptSnapshotEmitsNothingAndDoesNotPanic(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(spawnmetrics.SnapshotPath(dir), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	c := New(dir)
	for _, name := range []string{"loom_agent_spawns_total", "loom_last_successful_spawn_timestamp_seconds"} {
		if got := testutil.CollectAndCount(c, name); got != 0 {
			t.Errorf("%s: collected %d samples from a corrupt snapshot, want 0", name, got)
		}
	}
}

// A zero timestamp means "no successful spawn yet". Emitting it would render as
// 1970 and fire the outage alert forever on a fresh install.
func TestZeroLastSuccessOmitsTheGauge(t *testing.T) {
	dir := t.TempDir()
	writeSnapshot(t, dir, spawnmetrics.Snapshot{
		SchemaVersion:           1,
		LastSuccessfulSpawnUnix: 0,
		Spawns: []spawnmetrics.SpawnRow{
			{Role: "worker", Status: "failure", ErrorClass: spawnmetrics.ClassStart, Count: 1},
		},
	})

	c := New(dir)
	if got := testutil.CollectAndCount(c, "loom_last_successful_spawn_timestamp_seconds"); got != 0 {
		t.Errorf("gauge emitted for a zero timestamp (%d samples)", got)
	}
	if got := testutil.CollectAndCount(c, "loom_agent_spawns_total"); got != 1 {
		t.Errorf("spawn samples = %d, want 1", got)
	}
}

// The shape the ticket names: a wedged fleet retrying one failure class, with
// no success for hours. This is the state LoomSpawnOutage must fire on.
func TestSpawnOutageShape(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 8, 30, 18, 0, 0, 0, time.UTC)
	lastSuccess := now.Add(-8 * time.Hour)

	writeSnapshot(t, dir, spawnmetrics.Snapshot{
		SchemaVersion:           1,
		LastSuccessfulSpawnUnix: lastSuccess.Unix(),
		Spawns: []spawnmetrics.SpawnRow{
			{Role: "worker", Status: "failure", ErrorClass: spawnmetrics.ClassMaterializeSkills, Count: 774},
		},
	})

	c := New(dir)
	c.now = func() time.Time { return now }

	families := gather(t, c)

	got, ok := counterValue(families["loom_agent_spawns_total"], map[string]string{
		"role": "worker", "status": "failure", "error_class": "materialize_skills",
	})
	if !ok {
		t.Fatal("no failure series")
	}
	if got != 774 {
		t.Errorf("failure count = %v, want 774", got)
	}

	for _, m := range families["loom_agent_spawns_total"].GetMetric() {
		for _, l := range m.GetLabel() {
			if l.GetName() == "status" && l.GetValue() == "success" {
				t.Error("a success series exists during a total spawn outage")
			}
		}
	}

	stamp, ok := counterValue(families["loom_last_successful_spawn_timestamp_seconds"], map[string]string{})
	if !ok {
		t.Fatal("no last-success gauge")
	}
	// This is the alert's own arithmetic: time() - gauge > 3600.
	if age := float64(now.Unix()) - stamp; age <= 3600 {
		t.Errorf("time() - last_successful_spawn = %v, want > 3600", age)
	}
}
