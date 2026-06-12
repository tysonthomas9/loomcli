package fleetdb

import "github.com/tysonthomas9/loomcli/internal/store"

// The fleet-db connector client lands in the CV3 chunk (after Router v2).
// Until then the client satisfies the Store aggregate with the fail-closed
// placeholders so callers get errors.ErrUnsupported instead of a
// nil-interface panic.

func (c *Client) Connectors() store.ConnectorStore {
	return store.UnimplementedConnectorStore{Backend: "fleetdb"}
}

func (c *Client) ConnectorGrants() store.ConnectorGrantStore {
	return store.UnimplementedConnectorGrantStore{Backend: "fleetdb"}
}

func (c *Client) ConnectorCalls() store.ConnectorAuditStore {
	return store.UnimplementedConnectorAuditStore{Backend: "fleetdb"}
}
