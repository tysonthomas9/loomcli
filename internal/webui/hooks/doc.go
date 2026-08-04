// Package hooks implements the coordinator.LifecycleHook set the web UI runs
// when a workspace is registered or deregistered: the per-workspace fleet-db
// issue backend and fleet store, the mutation subscriber, the web-terminal PTY
// manager, and tmux session reaping. Constructed and ordered by
// internal/webui/appinfra — FleetBackendHook must register before the subscriber.
package hooks
