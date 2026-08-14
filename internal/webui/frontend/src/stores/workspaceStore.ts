/**
 * Zustand vanilla store for workspace data with polling.
 * Factory-created (workspace-scoped) — not a singleton.
 * Replaces useWorkspace hook's React state + polling logic.
 */

import { createStore, type StoreApi } from "zustand/vanilla";

import { fetchWorkspaceApi } from "../api/workspace";
import type { WorkspaceData } from "../api/workspace";

type WorkspaceAgent = WorkspaceData["agents"][number];

function normalizeWorkspaceAgent(agent: WorkspaceAgent): WorkspaceAgent {
  return {
    ...agent,
    repos: agent.repos ?? [],
    repo_groups: agent.repo_groups ?? [],
  };
}

function normalizeWorkspaceData(data: WorkspaceData): WorkspaceData {
  return {
    ...data,
    agents: (data.agents ?? []).map(normalizeWorkspaceAgent),
  };
}

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface WorkspaceStoreState {
  workspace: WorkspaceData | null;
  isLoading: boolean;
  error: string | null;
}

export interface WorkspaceStoreActions {
  fetchWorkspace: (workspaceId?: string) => Promise<void>;
  startPolling: (options: WorkspacePollingOptions) => void;
  stopPolling: () => void;
  refetch: () => void;
  upsertAgent: (agent: WorkspaceAgent) => void;
  reset: () => void;
}

export type WorkspaceStore = WorkspaceStoreState & WorkspaceStoreActions;

export interface WorkspacePollingOptions {
  workspaceId?: string;
  pollInterval?: number;
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

export const DEFAULT_POLL_INTERVAL = 60_000;

export const INITIAL_WORKSPACE_STATE: WorkspaceStoreState = {
  workspace: null,
  isLoading: false,
  error: null,
};

// ---------------------------------------------------------------------------
// Factory
// ---------------------------------------------------------------------------

export function createWorkspaceStore(): StoreApi<WorkspaceStore> {
  // Closure state — not in Zustand, doesn't trigger re-renders
  let inflightPromise: Promise<WorkspaceData> | null = null;
  let generation = 0;
  let pollIntervalId: ReturnType<typeof setInterval> | null = null;
  let visibilityHandler: (() => void) | null = null;
  let activeWorkspaceId: string | undefined;
  let activePollInterval = DEFAULT_POLL_INTERVAL;
  const pendingAgents = new Map<string, WorkspaceAgent>();

  const mergePendingAgents = (rawData: WorkspaceData): WorkspaceData => {
    const data = normalizeWorkspaceData(rawData);
    if (pendingAgents.size === 0) return data;

    const agents = data.agents ?? [];
    let changed = false;
    const nextAgents = agents.map((agent) => {
      const pending = pendingAgents.get(agent.name);
      if (!pending) return agent;
      pendingAgents.delete(agent.name);
      return agent;
    });

    for (const pending of pendingAgents.values()) {
      nextAgents.push(pending);
      changed = true;
    }

    if (!changed && nextAgents.length === agents.length) return data;
    return {
      ...data,
      agents: nextAgents,
    };
  };

  return createStore<WorkspaceStore>((set, get) => {
    const fetchWorkspace = async (workspaceId?: string): Promise<void> => {
      // Dedup: reuse in-flight promise if same generation
      if (inflightPromise !== null) {
        try {
          await inflightPromise;
        } catch {
          // Handled below via generation check
        }
        return;
      }

      // Only show loading spinner on initial load (no workspace data yet)
      if (get().workspace === null) {
        set({ isLoading: true });
      }

      const gen = generation;
      const promise = fetchWorkspaceApi(workspaceId);
      inflightPromise = promise;

      try {
        const rawData = await promise;
        if (gen === generation) {
          const data = mergePendingAgents(rawData);
          // Preserve workspace reference when payload is unchanged so downstream
          // useMemo chains (sourceReposFilter, activeRepos, etc.) don't churn on
          // every poll tick. The cherry-pick of v2's polling-bug fix removed the
          // join/split string-key dedup workaround on the assumption that this
          // upstream equality guard exists.
          const prev = get().workspace;
          const sameRef =
            prev !== null && JSON.stringify(prev) === JSON.stringify(data);
          if (sameRef) {
            set({ isLoading: false, error: null });
          } else {
            set({ workspace: data, isLoading: false, error: null });
          }
        }
      } catch (err) {
        // AbortError — silently ignore (but still clear isLoading)
        if (err instanceof DOMException && err.name === "AbortError") {
          if (gen === generation) {
            set({ isLoading: false });
          }
          return;
        }
        if (gen === generation) {
          const message =
            err instanceof Error
              ? err.message
              : "Failed to load workspace data";
          // Keep stale data on error
          set({ error: message, isLoading: false });
        }
      } finally {
        if (inflightPromise === promise) {
          inflightPromise = null;
        }
      }
    };

    const stopPolling = (): void => {
      if (pollIntervalId !== null) {
        clearInterval(pollIntervalId);
        pollIntervalId = null;
      }
      if (visibilityHandler !== null) {
        document.removeEventListener("visibilitychange", visibilityHandler);
        visibilityHandler = null;
      }
    };

    const startPolling = (options: WorkspacePollingOptions): void => {
      // Clean up any existing polling
      stopPolling();

      if (options.workspaceId !== activeWorkspaceId) {
        pendingAgents.clear();
      }
      activeWorkspaceId = options.workspaceId;
      activePollInterval = options.pollInterval ?? DEFAULT_POLL_INTERVAL;

      // Reset state for new workspace
      generation++;
      inflightPromise = null;

      // Initial fetch
      void fetchWorkspace(activeWorkspaceId);

      // Start interval
      if (activePollInterval > 0) {
        pollIntervalId = setInterval(() => {
          inflightPromise = null;
          void fetchWorkspace(activeWorkspaceId);
        }, activePollInterval);
      }

      // Visibility change listener — refetch when tab becomes visible
      visibilityHandler = () => {
        if (document.visibilityState === "visible") {
          inflightPromise = null;
          void fetchWorkspace(activeWorkspaceId);
        }
      };
      document.addEventListener("visibilitychange", visibilityHandler);
    };

    const refetch = (): void => {
      inflightPromise = null;
      generation++;
      void fetchWorkspace(activeWorkspaceId);
    };

    const upsertAgent = (agent: WorkspaceAgent): void => {
      const normalizedAgent = normalizeWorkspaceAgent(agent);
      if (normalizedAgent.name) {
        pendingAgents.set(normalizedAgent.name, normalizedAgent);
      }

      const current = get().workspace;
      if (!current || !normalizedAgent.name) return;

      const agents = (current.agents ?? []).map(normalizeWorkspaceAgent);
      const existingIndex = agents.findIndex(
        (item) => item.name === normalizedAgent.name,
      );
      const nextAgents =
        existingIndex === -1
          ? [...agents, normalizedAgent]
          : agents.map((item, index) =>
              index === existingIndex
                ? normalizeWorkspaceAgent({ ...item, ...normalizedAgent })
                : item,
            );

      set({
        workspace: {
          ...current,
          agents: nextAgents,
        },
      });
    };

    const reset = (): void => {
      stopPolling();
      generation++;
      inflightPromise = null;
      activeWorkspaceId = undefined;
      pendingAgents.clear();
      set(INITIAL_WORKSPACE_STATE);
    };

    return {
      ...INITIAL_WORKSPACE_STATE,
      fetchWorkspace,
      startPolling,
      stopPolling,
      refetch,
      upsertAgent,
      reset,
    };
  });
}
