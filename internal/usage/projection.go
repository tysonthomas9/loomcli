package usage

import "time"

// Projection is the owner-level API for Loom's local immutable usage
// projection. Callers depend on projection operations rather than the
// persistence Store implementation.
type Projection struct {
	store *Store
}

// NewProjection opens the usage projection rooted at runtimeDir.
func NewProjection(runtimeDir string) (*Projection, error) {
	store, err := NewStore(runtimeDir)
	if err != nil {
		return nil, err
	}
	return &Projection{store: store}, nil
}

// Append records one completed session in the usage projection.
func (p *Projection) Append(record SessionUsage) error {
	return p.store.Append(record)
}

// Read returns usage records matching filter.
func (p *Projection) Read(filter Filter) ([]SessionUsage, error) {
	return p.store.Read(filter)
}

// PurgeOlderThan removes usage records older than age.
func (p *Projection) PurgeOlderThan(age time.Duration) (int, error) {
	return p.store.PurgeOlderThan(age)
}
