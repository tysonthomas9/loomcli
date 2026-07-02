package store

import (
	"sort"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// SortDriverRunsNewestFirst orders driver runs by StartedAt descending, with
// not-yet-started (zero StartedAt) runs last, breaking ties by CreatedAt
// descending so the order is a deterministic total order. It is the shared
// recency order for run history (the workflows runs handler) and per-binding
// failure surfacing (the trigger-bindings list decorator), so both read a
// binding's runs the same way.
func SortDriverRunsNewestFirst(runs []*domain.DriverRun) {
	sort.Slice(runs, func(i, j int) bool {
		a, b := runs[i], runs[j]
		if a.StartedAt.IsZero() != b.StartedAt.IsZero() {
			return !a.StartedAt.IsZero() // a started run precedes an unstarted one
		}
		if !a.StartedAt.Equal(b.StartedAt) {
			return a.StartedAt.After(b.StartedAt)
		}
		return a.CreatedAt.After(b.CreatedAt)
	})
}
