// Package serve implements `loom serve`, the process that hosts loom's HTTP
// API and web UI.
//
// Beyond serving requests, this process is where loom's periodic work runs.
// serve_loops.go starts the background loops that keep the system moving when
// nothing is calling in: a stale task sweeper, the driver outbox dispatcher,
// the trigger cron scheduler and its delivery sweeper, the await timeout
// sweeper, and the issue journal bridge. Each loop's interval is read from the
// environment and clamped to a minimum, so intervals are tunable but a loop
// cannot be switched off by zeroing one. Only the issue journal bridge has an
// actual disable switch (LOOM_ISSUE_BRIDGE_DISABLED).
//
// serve_auth.go validates the --auth-url flag, and serve_cache.go the caching
// in front of the API. The authentication mode itself is decided in serve.go
// by whether an auth URL was supplied at all. Fleet mode is toggled by flag or
// by its own environment variable, deliberately separate from the variable
// that routes issue reads to a fleet backend — the two decisions are made at
// different layers and conflating them would couple transport to storage.
//
// The subpackages hold the pieces this process wires together: daemonwire,
// install, logroutercmd, metricscmd, observability, opsimpl, serveadapter,
// usagecmd, worker, and workspacemgr.
package serve
