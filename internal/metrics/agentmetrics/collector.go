// Package agentmetrics exposes the daemon's spawn outcomes and the fleet's
// session history to Prometheus from inside the `loom serve` process.
//
// The two halves of that sentence are the whole design. Spawns are counted in
// the `loom daemon` process, so an in-process counter there is invisible to a
// scrape of serve; the daemon persists a snapshot file (internal/metrics/
// spawnmetrics) and this collector reads it at scrape time. Sessions are
// finalized in several processes at once, so no single snapshot can own them;
// they are derived by incrementally tailing sessions/index.jsonl.
//
// Nothing here registers itself: serve registers the collector explicitly, so
// a test can build one against a temp directory without fighting the default
// registry. The package must stay out of the supervisor's import graph — that
// one-way edge is what keeps daemon-side recording free of the prometheus
// client — and must not import internal/cli.
package agentmetrics

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/tysonthomas9/loomcli/internal/metrics/spawnmetrics"
)

// logThrottle bounds how often a repeatedly unreadable snapshot may log. A
// scrape runs every few seconds; an unthrottled warning would be a log flood.
const logThrottle = time.Minute

var (
	spawnsDesc = prometheus.NewDesc(
		"loom_agent_spawns_total",
		"Total agent spawn attempts by role, outcome and failure class.",
		[]string{"role", "status", "error_class"}, nil,
	)
	sessionsDesc = prometheus.NewDesc(
		"loom_agent_sessions_total",
		"Total finished agent sessions by role, phase and terminal status.",
		[]string{"role", "phase", "status"}, nil,
	)
	sessionDurationDesc = prometheus.NewDesc(
		"loom_agent_session_duration_seconds",
		"Duration of finished agent sessions by role and phase.",
		[]string{"role", "phase"}, nil,
	)
	lastSuccessDesc = prometheus.NewDesc(
		"loom_last_successful_spawn_timestamp_seconds",
		"Unix timestamp of the most recent successful agent spawn.",
		nil, nil,
	)
)

// Collector reports the spawn snapshot and the session index as Prometheus
// metrics. It implements prometheus.Collector.
type Collector struct {
	snapshotPath string
	tail         *sessionTailer
	now          func() time.Time

	mu      sync.Mutex
	lastLog time.Time
}

// New builds a Collector for a workspace runtime directory. It takes the
// runtime dir — not the individual files — so the two paths it reads are
// derived exactly once, here, from the same value the daemon resolves.
func New(runtimeDir string) *Collector {
	return &Collector{
		snapshotPath: spawnmetrics.SnapshotPath(runtimeDir),
		tail:         newSessionTailer(filepath.Join(runtimeDir, "sessions", "index.jsonl")),
		now:          time.Now,
	}
}

// SnapshotPath returns the spawn snapshot file this collector reads. Exposed so
// a test can assert it is byte-identical to the path the daemon writes.
func (c *Collector) SnapshotPath() string { return c.snapshotPath }

// SessionIndexPath returns the session index file this collector tails.
func (c *Collector) SessionIndexPath() string { return c.tail.path }

// Describe implements prometheus.Collector.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- spawnsDesc
	ch <- sessionsDesc
	ch <- sessionDurationDesc
	ch <- lastSuccessDesc
}

// Collect implements prometheus.Collector. It never panics: a panic here would
// take down the scrape handler for every other metric family too, so a missing
// or corrupt input is reported as the absence of those series. Absence is the
// honest answer, and the alerting rules cover it with absent().
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.collectSpawns(ch)
	c.tail.advance(c.logf)
	c.tail.emit(ch)
}

func (c *Collector) collectSpawns(ch chan<- prometheus.Metric) {
	snap, err := spawnmetrics.Load(c.snapshotPath)
	if err != nil {
		// A missing snapshot is a supported state: no daemon has spawned
		// anything yet, or none is running. Only the corrupt case is worth a
		// word, and only occasionally.
		if !errors.Is(err, os.ErrNotExist) {
			c.logf("agentmetrics: cannot read spawn snapshot", "path", c.snapshotPath, "error", err)
		}
		return
	}
	if snap == nil {
		return
	}

	for _, row := range snap.Spawns {
		ch <- prometheus.MustNewConstMetric(
			spawnsDesc, prometheus.CounterValue, float64(row.Count),
			row.Role, row.Status, string(row.ErrorClass),
		)
	}

	// Emit the gauge only for a real success. A zero would render as 1970 and
	// permanently fire the outage alert on a fresh install; the alert's
	// absent() arm is what covers "no successful spawn yet".
	if snap.LastSuccessfulSpawnUnix > 0 {
		ch <- prometheus.MustNewConstMetric(
			lastSuccessDesc, prometheus.GaugeValue, float64(snap.LastSuccessfulSpawnUnix),
		)
	}
}

// logf logs at most once per logThrottle. Called with c.mu held.
func (c *Collector) logf(msg string, args ...any) {
	now := c.now()
	if !c.lastLog.IsZero() && now.Sub(c.lastLog) < logThrottle {
		return
	}
	c.lastLog = now
	slog.Warn(msg, args...)
}
