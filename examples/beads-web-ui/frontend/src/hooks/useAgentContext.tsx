/**
 * useAgentContext - React context for sharing agent data across components.
 * Wraps useAgents() so a single polling loop serves all consumers.
 */

import { createContext, useContext, useCallback, type ReactNode } from 'react';

import type { LoomAgentStatus } from '@/types';

import { useAgents } from './useAgents';
import type { UseAgentsResult } from './useAgents';

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
 * AgentProvider wraps the app and provides agent data to all children.
 * Internally manages a single useAgents() polling loop (5s interval).
 */
export function AgentProvider({ children }: AgentProviderProps): JSX.Element {
  const agentsResult = useAgents({ pollInterval: 5000 });

  const getAgentByName = useCallback(
    (name: string): LoomAgentStatus | undefined => {
      return agentsResult.agents.find((a) => a.name === name);
    },
    [agentsResult.agents]
  );

  const value: AgentContextValue = {
    ...agentsResult,
    getAgentByName,
  };

  return <AgentContext.Provider value={value}>{children}</AgentContext.Provider>;
}

/** Default no-op value returned when useAgentContext is called outside a provider. */
const NO_AGENT_CONTEXT: AgentContextValue = {
  agents: [],
  tasks: { needs_planning: 0, ready_to_implement: 0, in_progress: 0, need_review: 0, blocked: 0 },
  taskLists: {
    needsPlanning: [],
    readyToImplement: [],
    needsReview: [],
    inProgress: [],
    blocked: [],
  },
  agentTasks: {},
  sync: { db_synced: true, db_last_sync: '', git_needs_push: 0, git_needs_pull: 0 },
  stats: { open: 0, closed: 0, total: 0, completion: 0 },
  isLoading: false,
  isConnected: false,
  connectionState: 'never_connected',
  wasEverConnected: false,
  retryCountdown: 0,
  error: null,
  lastUpdated: null,
  refetch: async () => {},
  retryNow: () => {},
  getAgentByName: () => undefined,
};

/**
 * Hook to access agent context.
 * Returns safe defaults when used outside an AgentProvider (e.g., in tests).
 */
export function useAgentContext(): AgentContextValue {
  const context = useContext(AgentContext);
  return context ?? NO_AGENT_CONTEXT;
}
