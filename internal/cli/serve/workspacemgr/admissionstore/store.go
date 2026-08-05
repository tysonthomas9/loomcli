// Package admissionstore defines Workspace repository-admission's exact
// legacy persistence projection. Keeping the named interface in a
// function-free owner-private package lets architecture policy classify its
// five read-only accessors without treating workspacemgr orchestration itself
// as a persistence implementation.
package admissionstore

import "github.com/tysonthomas9/loomcli/internal/store"

// Store exposes only the legacy aggregate facets required to materialize and
// publish one repository-admission batch.
type Store interface {
	Workspaces() store.WorkspaceStore
	Repos() store.RepoStore
	Roles() store.RoleStore
	AgentServices() store.AgentServiceStore
	Agents() store.AgentStore
}
