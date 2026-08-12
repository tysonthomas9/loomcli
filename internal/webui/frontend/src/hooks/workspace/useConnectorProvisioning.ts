import { useCallback, useMemo } from "react";

import {
  createConnector,
  createConnectorGrant,
  replaceConnectorBindingGrants,
  type Connector,
  type ConnectorGrant,
  type CreateConnectorGrantRequest,
  type CreateConnectorRequest,
  type ReplaceConnectorBindingGrantsRequest,
  type ReplaceConnectorBindingGrantsResponse,
} from "@/api/connectors";
import {
  preflightRuntimeCredential,
  type RuntimeCredentialProvider,
  type RuntimeCredentialReadiness,
} from "@/api/common";

// Re-exported for components (the layer DAG bars them from importing the api
// module directly), so the canonical id has a single definition.
export { GITHUB_CONNECTOR_ID } from "@/api/connectors";

export interface UseConnectorProvisioningReturn {
  /** Verify a Settings credential before provisioning workflow authority. */
  preflightCredential: (
    provider: RuntimeCredentialProvider,
  ) => Promise<RuntimeCredentialReadiness>;
  /** Create (or reuse) a workspace connector. */
  ensureConnector: (req: CreateConnectorRequest) => Promise<Connector>;
  /** Attach a deny-by-default grant to a connector. */
  addGrant: (
    connectorId: string,
    req: CreateConnectorGrantRequest,
  ) => Promise<ConnectorGrant>;
  /** Authoritatively replace one disabled binding's connector grant set. */
  replaceGrants: (
    connectorId: string,
    bindingId: string,
    req: ReplaceConnectorBindingGrantsRequest,
  ) => Promise<ReplaceConnectorBindingGrantsResponse>;
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
  const preflightCredential = useCallback(
    (
      provider: RuntimeCredentialProvider,
    ): Promise<RuntimeCredentialReadiness> =>
      preflightRuntimeCredential(provider),
    [],
  );

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

  const replaceGrants = useCallback(
    (
      connectorId: string,
      bindingId: string,
      req: ReplaceConnectorBindingGrantsRequest,
    ): Promise<ReplaceConnectorBindingGrantsResponse> =>
      replaceConnectorBindingGrants(workspaceId, connectorId, bindingId, req),
    [workspaceId],
  );

  return useMemo(
    () => ({
      preflightCredential,
      ensureConnector,
      addGrant,
      replaceGrants,
    }),
    [preflightCredential, ensureConnector, addGrant, replaceGrants],
  );
}
