// Package log owns the on-disk layout of agent and task logs under
// ~/.loom/logs/<workspace>/: path resolution, containment and symlink-safe
// opening, tail reads, and SSE streaming of newly appended bytes. Readers are
// internal/webui/svcimpl and `loom daemon logs`; internal/cli/agent and the
// daemon supervisor resolve the same paths to write them.
package log
