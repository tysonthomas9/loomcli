// Package metricscmd serves the agent, stats, and metrics HTTP endpoints.
//
// Each handler is exposed in several forms — taking a collect function, a
// MonitorDataSource, or an additional issue-backend or store source — because
// the same view is assembled from different places depending on how the server
// was started. The data sources are parameters rather than package state so a
// handler can be exercised without a running daemon behind it.
package metricscmd
