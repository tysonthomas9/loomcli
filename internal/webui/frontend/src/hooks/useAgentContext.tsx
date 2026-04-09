/**
 * useAgentContext - React context for sharing agent data across components.
 * Wraps useAgents() so a single polling loop serves all consumers.
 */

import {
  createContext,
  useContext,
  useCallback,
  useMemo,
  type ReactNode,
} from "react";

import type { LoomAgentStatus } from "@/types";

import { useAgents } from "./useAgents";
import type { UseAgentsResult } from "./useAgents";
import { useWorkspaceContext } from "./useWorkspaceContext";

/**
 * Context value exposed by AgentProvider.
 * Extends UseAgentsResult with a helper for looking up agents by name.
 */
export interface AgentContextValue extends UseAgentsResult {
  /** Look up an agent by name. Returns undefined if not found. */
  getAgentByName: (name: string) => LoomAgentStatus | undefined;
}

const AgentContext = createContext<AgentContextValue | undefined>(undefined);

/**
 * Props for AgentProvider.
 */
export interface AgentProviderProps {
  children: ReactNode;
}

/**
 * AgentProvider wraps the app and provides workspace-scoped agent data to all children.
 * Internally manages a single useAgents() polling loop (5s interval).
 */
export function AgentProvider({ children }: AgentProviderProps): JSX.Element {
  const { workspaceId } = useWorkspaceContext();
  const agentsResult = useAgents({ pollInterval: 5000, workspaceId });

  const getAgentByName = useCallback(
    (name: string): LoomAgentStatus | undefined => {
      return agentsResult.agents.find((a) => a.name === name);
    },
    [agentsResult.agents],
  );

  const value: AgentContextValue = useMemo(
    () => ({
      ...agentsResult,
      getAgentByName,
    }),
    [agentsResult, getAgentByName],
  );

  return (
    <AgentContext.Provider value={value}>{children}</AgentContext.Provider>
  );
}

/** Default no-op value returned when useAgentContext is called outside a provider. */
const NO_AGENT_CONTEXT: AgentContextValue = {
  agents: [],
  isLoading: false,
  isConnected: false,
  connectionState: "never_connected",
  wasEverConnected: false,
  retryCountdown: 0,
  error: null,
  lastUpdated: null,
  refetch: async () => {},
  retryNow: () => {},
  showStaleBanner: false,
  connectionLost: false,
  disconnectedSince: null,
  getAgentByName: () => undefined,
};

/**
 * useAgentContext returns the shared agent context.
 * Safe to call outside AgentProvider — returns no-op defaults.
 */
export function useAgentContext(): AgentContextValue {
  return useContext(AgentContext) ?? NO_AGENT_CONTEXT;
}
