// Package daemonwire connects the HTTP server to the daemon running beside it.
//
// The web layer is defined against function types, not against the daemon.
// Each Build* constructor here returns one of those functions backed by a real
// daemon or by the store: agent control and input, the workspace claim hold,
// the agent queue, daemon config, and the supervisor view. This is the seam
// that lets handlers be tested with plain functions while production wires
// them to a live daemon.
//
// DecodeJWTKeyEnv reads a signing key from the environment for the endpoints
// that authenticate, and InitStaleDetectorHandler builds the Redis-backed
// handler that reports agents which have stopped reporting in.
package daemonwire
