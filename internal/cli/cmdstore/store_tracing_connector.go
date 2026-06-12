package cmdstore

import "github.com/tysonthomas9/loomcli/internal/store"

// Connector sub-stores pass through untraced for now, mirroring
// TriggerEvents/TriggerDeliveries; dedicated traced wrappers can follow once
// the memstore/fleet-db implementations land (CV2/CV3).

func (t *tracedStore) Connectors() store.ConnectorStore {
	return t.inner.Connectors()
}

func (t *tracedStore) ConnectorGrants() store.ConnectorGrantStore {
	return t.inner.ConnectorGrants()
}

func (t *tracedStore) ConnectorCalls() store.ConnectorAuditStore {
	return t.inner.ConnectorCalls()
}
