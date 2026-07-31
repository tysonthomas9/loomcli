package connectors

import "time"

// ConnectorGrant is the Connectors-owned, transport-neutral projection of
// one active or historical binding-scoped egress grant. The immutable tuple
// (GrantID, ConnectorID, BindingID, Action, ResourcePattern) is the durable
// idempotency identity used by EnsureGrant.
type ConnectorGrant struct {
	WorkspaceKey    string     `json:"workspace_key"`
	GrantID         string     `json:"grant_id"`
	ConnectorID     string     `json:"connector_id"`
	BindingID       string     `json:"binding_id"`
	Action          string     `json:"action"`
	ResourcePattern string     `json:"resource_pattern"`
	CreatedAt       time.Time  `json:"created_at"`
	RevokedAt       *time.Time `json:"revoked_at,omitempty"`
}

// EnsureGrantCommand requests one exact binding-scoped grant. GrantID is the
// durable replay key; no caller-chosen authority or audit actor is accepted.
type EnsureGrantCommand struct {
	WorkspaceKey    string `json:"workspace_key"`
	GrantID         string `json:"grant_id"`
	ConnectorID     string `json:"connector_id"`
	BindingID       string `json:"binding_id"`
	Action          string `json:"action"`
	ResourcePattern string `json:"resource_pattern"`
}

func cloneConnectorGrant(in *ConnectorGrant) *ConnectorGrant {
	if in == nil {
		return nil
	}
	out := *in
	if in.RevokedAt != nil {
		value := *in.RevokedAt
		out.RevokedAt = &value
	}
	return &out
}

// GitReadOperation is the closed set of credential-brokered Git reads.
// Clone materializes an admitted repository. Fetch-ref updates one exact
// Source-Control-owned local ref from one exact remote ref; it cannot fetch an
// arbitrary refspec or write outside refs/loom.
type GitReadOperation string

const (
	GitReadClone    GitReadOperation = "clone"
	GitReadFetchRef GitReadOperation = "fetch-ref"
)

// GitReadCommand authorizes no standing credential. It identifies one exact
// repository, operation, workspace root, and checkout target. RemoteURL must
// be token-free; URL userinfo, query strings, and fragments are rejected for
// every scheme. Canonical SCP-style remotes may carry a username, not a
// password or other embedded authority.
type GitReadCommand struct {
	WorkspaceKey   string           `json:"workspace_key"`
	OperationID    string           `json:"operation_id"`
	RepositoryRef  string           `json:"repository_ref"`
	Operation      GitReadOperation `json:"operation"`
	RemoteURL      string           `json:"remote_url"`
	WorkspacePath  string           `json:"workspace_path"`
	TargetPath     string           `json:"target_path"`
	RemoteName     string           `json:"remote_name,omitempty"`
	SourceRef      string           `json:"source_ref,omitempty"`
	DestinationRef string           `json:"destination_ref,omitempty"`
}

// GitReadReceipt is deliberately credential-free. It lets Source Control
// validate that the broker executed the same bounded request without learning
// how the provider authenticated it.
type GitReadReceipt struct {
	WorkspaceKey   string           `json:"workspace_key"`
	OperationID    string           `json:"operation_id"`
	RepositoryRef  string           `json:"repository_ref"`
	Operation      GitReadOperation `json:"operation"`
	TargetPath     string           `json:"target_path"`
	RemoteName     string           `json:"remote_name,omitempty"`
	SourceRef      string           `json:"source_ref,omitempty"`
	DestinationRef string           `json:"destination_ref,omitempty"`
}
