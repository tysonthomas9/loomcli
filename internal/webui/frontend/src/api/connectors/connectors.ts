/**
 * API client for workspace connector endpoints (egress credentials + grants).
 *
 * A connector is a workspace-scoped credential (e.g. GitHub) that scheduled
 * workflows egress through. The create-agent gallery's review-loop template
 * provisions one that reuses the Settings runtime GitHub token, then attaches
 * deny-by-default grants scoping which actions/resources its binding may reach.
 */

import { post, put, wsUrl } from "@/api/common";

/** The canonical connector id used for the GitHub egress credential. */
export const GITHUB_CONNECTOR_ID = "github";

export interface Connector {
  workspace_key?: string;
  connector_id: string;
  source_kind?: string;
  display_name?: string;
  status?: string;
  created_at?: string;
  updated_at?: string;
}

export interface CreateConnectorRequest {
  /** Connector source (e.g. "github"). */
  source: string;
  /** Stable connector id (e.g. "github"). */
  connector_id: string;
  /**
   * Reuse the Settings runtime credential for this source instead of sealing a
   * new secret — the review loop opts in so no new token field is needed.
   */
  reuse_runtime_credential?: boolean;
  display_name?: string;
}

/**
 * Create (or reuse) a workspace connector. Idempotent per connector_id so
 * re-activating the same template is safe.
 */
export async function createConnector(
  workspaceId: string,
  req: CreateConnectorRequest,
): Promise<Connector> {
  return post<Connector>(wsUrl(workspaceId, `/connectors`), req);
}

export interface ConnectorGrant {
  workspace_key?: string;
  grant_id?: string;
  connector_id?: string;
  binding_id: string;
  action: string;
  resource_pattern: string;
  created_at?: string;
}

export interface CreateConnectorGrantRequest {
  /** Trigger binding the grant authorizes. */
  binding_id: string;
  /** Connector action (e.g. "github.pull_request.read"). */
  action: string;
  /** Resource pattern (e.g. "repo:octocat/hello"). */
  resource_pattern: string;
}

/**
 * Attach a deny-by-default grant to a connector, authorizing one action on one
 * resource pattern for the given trigger binding.
 */
export async function createConnectorGrant(
  workspaceId: string,
  connectorId: string,
  req: CreateConnectorGrantRequest,
): Promise<ConnectorGrant> {
  return post<ConnectorGrant>(
    wsUrl(workspaceId, `/connectors/${encodeURIComponent(connectorId)}/grants`),
    req,
  );
}

export interface ReplaceConnectorGrant {
  /** Connector action (e.g. "github.pull_request.read"). */
  action: string;
  /** Resource pattern (e.g. "repo:octocat/hello"). */
  resource_pattern: string;
}

export interface ReplaceConnectorBindingGrantsRequest {
  /** Exact binding generation observed after disabling and updating it. */
  expected_binding_created_at: string;
  /** Exact disabled binding revision whose run input these grants authorize. */
  expected_binding_updated_at: string;
  /** Complete desired authority set for this connector and binding. */
  grants: ReplaceConnectorGrant[];
}

export interface ReplaceConnectorBindingGrantsResponse {
  binding_id: string;
  binding_created_at: string;
  binding_updated_at: string;
  grants: ConnectorGrant[];
  grants_revoked: number;
}

/**
 * Replace a connector's complete grant set for one exact disabled binding
 * revision. The server owns stale-grant revocation and generation fencing, so
 * singleton workflow retargeting never retains the prior repository scope.
 */
export async function replaceConnectorBindingGrants(
  workspaceId: string,
  connectorId: string,
  bindingId: string,
  req: ReplaceConnectorBindingGrantsRequest,
): Promise<ReplaceConnectorBindingGrantsResponse> {
  return put<ReplaceConnectorBindingGrantsResponse>(
    wsUrl(
      workspaceId,
      `/connectors/${encodeURIComponent(connectorId)}/bindings/${encodeURIComponent(bindingId)}/grants`,
    ),
    req,
  );
}
