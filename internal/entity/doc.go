// Package entity defines the canonical domain types for loomcli's V2 architecture.
//
// This is a leaf package: it has zero imports of other internal packages, relying
// only on the Go standard library. All downstream packages (IssueBackend, DTO layer,
// frontend state) depend on these types as the single source of truth for the domain model.
//
// The existing internal/types package remains untouched during the migration period.
// Migration of consumers from internal/types to internal/entity is handled by
// downstream epics (IssueBackend, DTO Layer).
package entity
