package localredis

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Registered on prometheus.DefaultRegisterer (promauto), which is what
// loom's /metrics endpoint gathers (internal/webui/prom_metrics.go), so
// these are scraped alongside the rest of the loom_* family.
//
// snapshotLastSuccess is refreshed on every healthy sweep — including
// idle sweeps whose hash-match short-circuits the disk write — so
// freshness tracks "the on-disk snapshot is verified current", not
// "the file was rewritten". A stale value is the signal that durability
// is degrading without having to grep serve.log.
//
// snapshotFailuresTotal counts every sweep that did NOT leave a
// verified-healthy snapshot on disk: partial-read aborts, scan
// failures, and marshal/file-I/O errors alike.
var (
	snapshotLastSuccess = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "loom_localredis_last_snapshot_success_timestamp_seconds",
		Help: "Unix timestamp of the most recent successful localredis snapshot sweep (including verified-unchanged sweeps that skip the write).",
	})
	snapshotFailuresTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "loom_localredis_snapshot_failures_total",
		Help: "Total localredis snapshot sweeps that failed to leave a verified-healthy snapshot on disk (partial reads, scan failures, write errors).",
	})
	// writeThroughTotal separates debounce-triggered sweeps from the 30s
	// tick, which is how we tell in production whether the hook fires at
	// all and whether writeThroughGap is tuned sanely. Outcomes are
	// already covered by the two metrics above — write-through dumps go
	// through the same dump path.
	writeThroughTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "loom_localredis_write_through_snapshots_total",
		Help: "Total localredis snapshot sweeps triggered by a terminal-state mutation rather than the periodic tick.",
	})
)
