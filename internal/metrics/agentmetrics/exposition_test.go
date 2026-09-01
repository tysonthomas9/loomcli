package agentmetrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/tysonthomas9/loomcli/internal/metrics/spawnmetrics"
)

// scrape serves the collector through the same handler shape webui.PromHandler
// uses and returns the response body, so these tests read what a real scraper
// would read off the wire.
func scrape(t *testing.T, c prometheus.Collector) (int, string) {
	t.Helper()
	reg := prometheus.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("register: %v", err)
	}
	rec := httptest.NewRecorder()
	promhttp.HandlerFor(reg, promhttp.HandlerOpts{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return rec.Code, string(body)
}

func TestSeededRuntimeDirExposesAllFourFamilies(t *testing.T) {
	dir := t.TempDir()
	writeSnapshot(t, dir, spawnmetrics.Snapshot{
		SchemaVersion:           1,
		LastSuccessfulSpawnUnix: 1756000000,
		Spawns: []spawnmetrics.SpawnRow{
			{Role: "worker", Status: "success", ErrorClass: spawnmetrics.ClassNone, Count: 4},
			{Role: "worker", Status: "failure", ErrorClass: spawnmetrics.ClassStart, Count: 1},
		},
	})
	appendIndex(t, dir,
		running("s1", "worker", "implementation"),
		finished("s1", "worker", "implementation", "completed", 30),
	)

	// Built from the runtime dir, so the test exercises production's own path
	// derivation rather than paths the test invented.
	code, body := scrape(t, New(dir))
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}

	for _, want := range []string{
		"# TYPE loom_agent_spawns_total counter",
		"# TYPE loom_agent_sessions_total counter",
		"# TYPE loom_agent_session_duration_seconds histogram",
		"# TYPE loom_last_successful_spawn_timestamp_seconds gauge",
		`loom_agent_spawns_total{error_class="",role="worker",status="success"} 4`,
		`loom_agent_spawns_total{error_class="start",role="worker",status="failure"} 1`,
		`loom_agent_sessions_total{phase="implementation",role="worker",status="completed"} 1`,
		`loom_agent_session_duration_seconds_sum{phase="implementation",role="worker"} 30`,
		"loom_last_successful_spawn_timestamp_seconds 1.756e+09",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape body missing %q\n---\n%s", want, body)
		}
	}

	// The running row must not have produced a second session series.
	if n := strings.Count(body, "loom_agent_sessions_total{"); n != 1 {
		t.Errorf("session samples on the wire = %d, want 1\n---\n%s", n, body)
	}
}

// A fresh workspace: no daemon has ever run here. A Collect that sends no
// samples contributes no HELP/TYPE lines however many descriptors Describe
// advertises — the families are simply absent, which is what the alert's
// absent() arm is written for.
func TestBareRuntimeDirExposesNothingAndStillScrapes(t *testing.T) {
	code, body := scrape(t, New(t.TempDir()))
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	for _, name := range []string{
		"loom_agent_spawns_total",
		"loom_agent_sessions_total",
		"loom_agent_session_duration_seconds",
		"loom_last_successful_spawn_timestamp_seconds",
	} {
		if strings.Contains(body, name) {
			t.Errorf("family %q present on a bare runtime dir\n---\n%s", name, body)
		}
	}
}
