// Package usageprojection is the owner adapter for Loom's local immutable
// usage projection. CLI entrypoints use these commands instead of writing the
// projection store directly.
package usageprojection

import (
	"time"

	"github.com/tysonthomas9/loomcli/internal/usage"
)

func New(runtimeDir string) (*usage.Store, error) {
	return usage.NewStore(runtimeDir)
}

func Append(store *usage.Store, record usage.SessionUsage) error {
	return store.Append(record)
}

func PurgeOlderThan(store *usage.Store, age time.Duration) (int, error) {
	return store.PurgeOlderThan(age)
}
