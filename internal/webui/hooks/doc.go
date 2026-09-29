// Package hooks adapts loom's web server to fleet-backed deployments.
//
// Each hook replaces a local assumption with a fleet-aware one:
// FleetBackendHook routes issue operations to a fleet API with an actor and
// API key, FleetStoreHook resolves per-workspace stores from a registry,
// FleetSubscriberHook attaches realtime subscriptions across workspaces, and
// PTYHook binds terminal sessions to the multi-PTY manager.
//
// They exist as installable hooks rather than as branches inside the server so
// that single-machine loom carries no fleet code path, and fleet mode is a
// composition choice made once at startup.
package hooks
