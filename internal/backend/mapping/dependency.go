package mapping

import (
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/entity"
)

// DependencyFromData converts backend.DependencyData to *entity.Dependency.
// Inline display fields (Title, Status, Priority, IssueType) are dropped —
// entity.Dependency is a pure edge type.
// Metadata and ThreadID are zero-valued (empty string) — DependencyData does
// not carry these fields, following the fleetdb_backend.go:convertDependency pattern.
func DependencyFromData(d backend.DependencyData) *entity.Dependency {
	return &entity.Dependency{
		IssueID:     d.IssueID,
		DependsOnID: d.DependsOnID,
		Type:        entity.DependencyType(d.Type),
		CreatedAt:   d.CreatedAt,
		CreatedBy:   d.CreatedBy,
	}
}

// DependenciesFromData converts a slice of backend.DependencyData to []*entity.Dependency.
// Returns a non-nil empty slice for nil or empty input.
func DependenciesFromData(ds []backend.DependencyData) []*entity.Dependency {
	out := make([]*entity.Dependency, 0, len(ds))
	for i := range ds {
		out = append(out, DependencyFromData(ds[i]))
	}
	return out
}

// DependencyToData converts *entity.Dependency to backend.DependencyData.
// Inline display fields (Title, Status, Priority, IssueType) are left empty.
// Metadata and ThreadID from entity.Dependency are silently dropped.
// Returns zero-value backend.DependencyData if d is nil (no panic).
func DependencyToData(d *entity.Dependency) backend.DependencyData {
	if d == nil {
		return backend.DependencyData{}
	}
	return backend.DependencyData{
		IssueID:     d.IssueID,
		DependsOnID: d.DependsOnID,
		Type:        string(d.Type),
		CreatedAt:   d.CreatedAt,
		CreatedBy:   d.CreatedBy,
	}
}

// DependenciesToData converts a slice of *entity.Dependency to []backend.DependencyData.
// Returns a non-nil empty slice for nil or empty input. Nil entries in the input
// slice are converted to zero-value DependencyData (not skipped).
func DependenciesToData(ds []*entity.Dependency) []backend.DependencyData {
	out := make([]backend.DependencyData, 0, len(ds))
	for i := range ds {
		out = append(out, DependencyToData(ds[i]))
	}
	return out
}
