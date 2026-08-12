// Package entity defines the canonical domain types for loomcli's V2 architecture.
//
// This is a leaf package: its only internal import is internal/types, a peer in the
// same SDK layer (see the depguard sdk-leaf rule), and otherwise it relies on the Go
// standard library alone. That single edge exists so the issue status vocabulary is
// declared once — IssueStatus is defined in terms of types.Status rather than
// respelling the nine values a second time. All downstream packages (IssueBackend,
// DTO layer, frontend state) depend on these types as the single source of truth for
// the domain model.
//
// The existing internal/types package remains untouched during the migration period.
// Migration of consumers from internal/types to internal/entity is handled by
// downstream epics (IssueBackend, DTO Layer).
package entity
