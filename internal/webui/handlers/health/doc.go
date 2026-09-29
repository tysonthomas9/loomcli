// Package health serves loom's liveness, readiness, and aggregate stats
// endpoints.
//
// The variants exist because a loom server does not always have a daemon
// behind it. HandleAPIHealth reports on a daemon pool, HandleAPIHealthNoDaemon
// answers for deployments that have none, and HandleWorkspaceRuntimeReady
// takes an explicit daemonExpected flag — the distinction between "the daemon
// is missing" and "no daemon was ever expected" is the difference between an
// outage and a healthy single-process install.
//
// HandleMetrics reports server-side counters, including realtime hub state and
// fleet timeouts, for operators watching a running server.
package health
