// Package usagecmd serves the cost and token-usage endpoint.
//
// InitStore opens the usage record store for a directory and HandleUsage
// serves it, aggregating raw session records into the Response the UI renders:
// per-agent and per-backend summaries and a DailyCost series.
//
// Aggregation happens at read time rather than on write, because the stored
// records are append-only observations and the groupings callers want change
// more often than the records do.
package usagecmd
