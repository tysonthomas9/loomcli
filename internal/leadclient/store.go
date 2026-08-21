package leadclient

import "github.com/tysonthomas9/loomcli/internal/store"

func (c *Client) Workspaces() store.WorkspaceStore                     { return nil }
func (c *Client) Repos() store.RepoStore                               { return nil }
func (c *Client) Agents() store.AgentStore                             { return c.agents }
func (c *Client) Nodes() store.NodeStore                               { return nil }
func (c *Client) AgentSessions() store.AgentSessionStore               { return c.agentSessions }
func (c *Client) TerminalSessions() store.TerminalSessionStore         { return nil }
func (c *Client) Artifacts() store.ArtifactStore                       { return nil }
func (c *Client) AgentLeases() store.AgentLeaseStore                   { return nil }
func (c *Client) AgentOwnershipLeases() store.AgentOwnershipLeaseStore { return nil }
func (c *Client) AgentCommands() store.AgentCommandStore               { return nil }
func (c *Client) AgentInboxMessages() store.AgentInboxMessageStore     { return c.inboxMessages }
func (c *Client) Drivers() store.DriverStore                           { return nil }
func (c *Client) DriverVersions() store.DriverVersionStore             { return nil }
func (c *Client) WorkerProfiles() store.WorkerProfileStore             { return nil }
func (c *Client) AgentServices() store.AgentServiceStore               { return nil }
func (c *Client) TriggerBindings() store.TriggerBindingStore           { return nil }
func (c *Client) TriggerEvents() store.TriggerEventStore               { return nil }
func (c *Client) TriggerDeliveries() store.TriggerDeliveryStore        { return nil }
func (c *Client) TriggerRoutes() store.TriggerRouteDispatcher          { return nil }
func (c *Client) DriverRuns() store.DriverRunStore                     { return nil }
func (c *Client) DriverSteps() store.DriverStepStore                   { return nil }
func (c *Client) TaskRuns() store.TaskRunStore                         { return nil }
func (c *Client) TaskRunEvents() store.TaskRunEventStore               { return nil }
func (c *Client) Outbox() store.OutboxStore                            { return nil }
func (c *Client) Awaits() store.AwaitStore                             { return nil }
func (c *Client) Connectors() store.ConnectorStore                     { return nil }
func (c *Client) ConnectorGrants() store.ConnectorGrantStore           { return nil }
func (c *Client) ConnectorCalls() store.ConnectorAuditStore            { return nil }
func (c *Client) Workers() store.WorkerStore                           { return nil }
func (c *Client) Roles() store.RoleStore                               { return nil }
func (c *Client) Daemon() store.DaemonProfileStore                     { return nil }
func (c *Client) Close() error                                         { return nil }
