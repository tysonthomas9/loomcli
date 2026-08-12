import { useCallback, useMemo } from "react";

import {
  createConnector,
  createConnectorGrant,
  type Connector,
  type ConnectorGrant,
  type CreateConnectorGrantRequest,
  type CreateConnectorRequest,
} from "@/api/connectors";

// Re-exported for components (the layer DAG bars them from importing the api
// module directly), so the canonical id has a single definition.
export { GITHUB_CONNECTOR_ID } from "@/api/connectors";

export interface UseConnectorProvisioningReturn {
  /** Create (or reuse) a workspace connector. */
  ensureConnector: (req: CreateConnectorRequest) => Promise<Connector>;
  /** Attach a deny-by-default grant to a connector. */
  addGrant: (
    connectorId: string,
    req: CreateConnectorGrantRequest,
  ) => Promise<ConnectorGrant>;
}

/**
 * Connector provisioning bound to a workspace. The create-agent gallery's
 * review-loop template calls these to stand up a github connector (reusing the
 * Settings runtime credential) and its scoped grants, mirroring the idempotent
 * `useEnsureWorkspaceRole` provisioning pattern.
 */
export function useConnectorProvisioning(
  workspaceId: string,
): UseConnectorProvisioningReturn {
  const ensureConnector = useCallback(
    (req: CreateConnectorRequest): Promise<Connector> =>
      createConnector(workspaceId, req),
    [workspaceId],
  );

  const addGrant = useCallback(
    (
      connectorId: string,
      req: CreateConnectorGrantRequest,
    ): Promise<ConnectorGrant> =>
      createConnectorGrant(workspaceId, connectorId, req),
    [workspaceId],
  );

  return useMemo(
    () => ({ ensureConnector, addGrant }),
    [ensureConnector, addGrant],
  );
}
