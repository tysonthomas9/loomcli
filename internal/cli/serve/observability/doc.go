// Package observability serves loom's event and metrics endpoints from the
// event log on disk.
//
// ResolveEventsDir locates the log; ReadJSONLFile and ReadEventsFromJSONL read
// it, and ReplayAllEvents folds the whole history into a MetricsStore to
// rebuild a snapshot from scratch.
//
// Because that replay is expensive and the answer changes slowly, metrics are
// served through CachedValue — a generic TTL cache that recomputes on demand —
// with NewMetricsCache supplying the metrics-specific instance. HandleEvents
// streams raw events for inspection while HandleMetrics serves the cached
// aggregate.
package observability
