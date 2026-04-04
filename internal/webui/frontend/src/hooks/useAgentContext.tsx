/**
 * useAgentContext - React context for sharing agent data across components.
 * Wraps useAgents() so a single polling loop serves all consumers.
 */

import {
  createContext,
  useContext,
  useCallback,
  useState,
  useEffect,
  useRef,
  useMemo,
  type ReactNode,
} from "react";

import type { LoomAgentStatus } from "@/types";
import { fetchWorkspaceAgents } from "@/api/agents";

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
 * AgentProvider wraps the app and provides agent data to all children.
 * Internally manages a single useAgents() polling loop (5s interval).
 */
export function AgentProvider({ children }: AgentProviderProps): JSX.Element {
  const agentsResult = useAgents({ pollInterval: 5000 });
  const { workspaceId } = useWorkspaceContext();
  const [wsAgents, setWsAgents] = useState<LoomAgentStatus[]>([]);
  const mountedRef = useRef(true);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  // Fetch workspace agents whenever global agents update (piggyback on same poll cycle).
  // No separate interval — avoids dual-polling that doubles request rate.
  const lastUpdated = agentsResult.lastUpdated;
  useEffect(() => {
    if (!workspaceId || !lastUpdated) return;
    let cancelled = false;
    fetchWorkspaceAgents(workspaceId)
      .then((agents) => {
        if (!cancelled && mountedRef.current) setWsAgents(agents);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [workspaceId, lastUpdated]);

  // Merge: workspace agents take priority (they have repo-specific data),
  // then append global agents not already present by name.
  const mergedAgents = useMemo(() => {
    const byName = new Map<string, LoomAgentStatus>();
    for (const a of wsAgents) byName.set(a.name, a);
    for (const a of agentsResult.agents) {
      if (!byName.has(a.name)) byName.set(a.name, a);
    }
    return Array.from(byName.values());
  }, [wsAgents, agentsResult.agents]);

  const getAgentByName = useCallback(
    (name: string): LoomAgentStatus | undefined => {
      return mergedAgents.find((a) => a.name === name);
    },
    [mergedAgents],
  );

  const value: AgentContextValue = {
    ...agentsResult,
    agents: mergedAgents,
    getAgentByName,
  };

  return (
    <AgentContext.Provider value={value}>{children}</AgentContext.Provider>
  );
}

/** Default no-op value returned when useAgentContext is called outside a provider. */
const NO_AGENT_CONTEXT: AgentContextValue = {
  agents: [],
  tasks: {
    needs_planning: 0,
    ready_to_implement: 0,
    in_progress: 0,
    need_review: 0,
    backlog: 0,
  },
  taskLists: {
    needsPlanning: [],
    readyToImplement: [],
    needsReview: [],
    inProgress: [],
    backlog: [],
    done: [],
  },
  agentTasks: {},
  sync: {
    db_synced: true,
    db_last_sync: "",
    git_needs_push: 0,
    git_needs_pull: 0,
  },
  stats: {
    open: 0,
    closed: 0,
    total: 0,
    completion: 0,
    remaining: 0,
    in_progress: 0,
    review: 0,
    blocked: 0,
  },
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
 * Hook to access agent context.
 * Returns safe defaults when used outside an AgentProvider (e.g., in tests).
 */
export function useAgentContext(): AgentContextValue {
  const context = useContext(AgentContext);
  return context ?? NO_AGENT_CONTEXT;
}
