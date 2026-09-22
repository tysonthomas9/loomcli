// Package rpc defines the wire protocol and client for talking to a running
// loom daemon over a per-workspace Unix socket.
//
// The protocol is operation-oriented: an Op constant names the request
// (OpPing, OpCreate, OpBatch, OpCompact, and the rest), and each operation has
// a matching Args and Result type in protocol_args.go and protocol_results.go.
// OpBatch carries several BatchOperation values in one round trip so a caller
// can apply a set of mutations without paying per-call latency.
//
// TryConnect and TryConnectWithTimeout return a Client only if a daemon is
// already listening; ErrDaemonStarting distinguishes a daemon mid-startup from
// one that is absent, so callers can wait rather than starting a second.
//
// Socket paths are constrained by the platform: MaxUnixSocketPath is 103 bytes
// to stay inside both the macOS and Linux limits. When a workspace path would
// exceed that, ShortSocketPath hashes the canonicalized workspace path into a
// short /tmp/loom-{hash}/ location, and NeedsShortPath reports when that
// substitution is required. EnsureSocketDir and CleanupSocketDir manage the
// lifetime of that directory. Windows has its own transport, selected by build
// tag.
package rpc
