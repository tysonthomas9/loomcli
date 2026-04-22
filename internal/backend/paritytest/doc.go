//go:build parity

// Package paritytest implements a loomcli-side beads-vs-fleet-db parity
// harness that exercises the [backend.IssueBackend] surface methods that
// the upstream fleet-db parity harness in ~/codebase/fleet-db/test/parity/
// does not cover.
//
// fleet-db's harness validates the documented JSON-RPC operations defined
// in its contract.json. Loomcli uses additional methods on the IssueBackend
// interface that aren't part of that contract:
//
//   - SearchIssues(query, limit)         — full-text relevance search
//   - GetMutations(sinceMs)              — SSE/polling integration
//   - WaitForMutations(sinceMs, timeout) — long-poll variant
//   - Batch(ops []BatchOp)               — mixed-operation atomic batch
//   - ListEvents(id, limit)              — per-issue audit trail
//   - GetChildren(id)                    — epic children
//   - DeferIssue(id, until) / UndeferIssue(id)
//   - Count(opts)                        — aggregate count
//   - Stats() (full)                     — including ReadyIssues, EpicsEligibleForClosure, AverageLeadTime
//
// Several of these have known unimplemented stubs in
// internal/backend/fleet/fleet.go (Batch, GetMutations, WaitForMutations,
// Count) — running this harness will surface those as fail diffs and
// produce a machine-readable report identifying which methods need
// adapter implementation vs which are real fleet-db gaps.
//
// The report format mirrors fleet-db's so consolidated reports can read
// both repos' outputs identically. Types are copied (not imported) because
// fleet-db's test/parity/ package is internal-equivalent.
//
// Build tag: this package compiles only under -tags parity. It is excluded
// from default builds and the standard `go test ./...` run.
package paritytest
