package memstore

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/fleetdbcap"
)

// capabilityStore answers the boot preflight for an in-memory run.
//
// There is no mux to probe: memstore satisfies the whole Store interface in
// process, so every capability this loom build declares is available. The
// document is built from the manifest rather than hard-coded so a requirement
// added to fleetdbcap cannot make in-memory runs start failing preflight.
type capabilityStore struct {
	doc domain.FleetDBCapabilities
}

func newCapabilityStore() *capabilityStore {
	names := make([]string, 0, len(fleetdbcap.Requirements()))
	for _, r := range fleetdbcap.Requirements() {
		names = append(names, r.Capability)
	}
	return &capabilityStore{doc: domain.FleetDBCapabilities{
		APIVersion:   memstoreCapabilityAPIVersion,
		Commit:       "memstore",
		Capabilities: names,
	}}
}

// memstoreCapabilityAPIVersion mirrors fleet-db's current document version.
// Nothing compares it; it exists so a boot message reads the same shape here.
const memstoreCapabilityAPIVersion = 1

func (s *capabilityStore) Get(context.Context) (domain.FleetDBCapabilities, error) {
	return s.doc, nil
}
