// Package webui is the root of loom's HTTP server: the shared types, the
// handlers that do not belong to a resource subpackage, and the process-level
// concerns of serving.
//
// The daemon-facing handlers here — agent queue, daemon config, daemon
// supervisor — take functions rather than a daemon, so the server can be built
// and tested without one behind it (internal/cli/serve/daemonwire supplies the
// real implementations).
//
// FindAvailablePort picks a listener starting from DefaultPort, which is what
// lets several loom instances coexist on one machine without an operator
// assigning ports by hand. InitSentry and FlushSentry install and drain error
// reporting around the server's lifetime.
//
// The resource handlers live in internal/webui/handlers, the service layer in
// internal/webui/service, and the transport concerns in
// internal/webui/server.
package webui
