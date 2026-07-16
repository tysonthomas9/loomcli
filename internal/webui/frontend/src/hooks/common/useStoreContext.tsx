/**
 * useStoreContext — React context wiring EventProvider to Zustand stores
 * (issueStore, agentStore). Creates store instances once, mounts EventProvider
 * for SSE, and uses an internal StoreWiring child to connect stores ↔ EventProvider.
 *
 * Follows useWorkspaceContext pattern.
 */

import {
  createContext,
  useContext,
  useEffect,
  useRef,
  useState,
  useMemo,
  type ReactNode,
} from "react";

import type { StoreApi } from "zustand/vanilla";

import { createIssueStore, type IssueStore } from "@/stores/issueStore";
import { createAgentStore, type AgentStore } from "@/stores/agentStore";
import { useWorkspaceContext } from "@/hooks/workspace";
import { useToast } from "@/hooks/ui";
import type { MutationPayload } from "@/types/workspace";
import { EventProvider, useEventContext } from "./useEventProvider";

// ---------------------------------------------------------------------------
// Context types
// ---------------------------------------------------------------------------

export interface StoreContextValue {
  issueStore: StoreApi<IssueStore>;
  agentStore: StoreApi<AgentStore>;
}

export interface StoreProviderProps {
  children: ReactNode;
}

// ---------------------------------------------------------------------------
// Safe defaults (NO_STORE_CONTEXT)
// ---------------------------------------------------------------------------

const NO_ISSUE_STORE = createIssueStore();
const NO_AGENT_STORE = createAgentStore();

const NO_STORE_CONTEXT: StoreContextValue = {
  issueStore: NO_ISSUE_STORE,
  agentStore: NO_AGENT_STORE,
};

export { NO_STORE_CONTEXT };

const MONITOR_REFRESH_TYPES = new Set<MutationPayload["type"]>([
  "create",
  "update",
  "delete",
  "comment",
  "status",
  "bonded",
  "squashed",
  "burned",
  "refresh",
]);

const MONITOR_REFRESH_ENTITY_TYPES = new Set([
  "issue",
  "dependency",
  "dep",
  "comment",
  "label",
  "agent",
  "workspace",
  "session",
]);

const MONITOR_REFRESH_DEBOUNCE_MS = 250;
const AGENT_REFRESH_INTERVAL_MS = 30_000;

// ---------------------------------------------------------------------------
// Context
// ---------------------------------------------------------------------------

export const StoreContext = createContext<StoreContextValue | undefined>(
  undefined,
);

// ---------------------------------------------------------------------------
// StoreWiring (internal — connects stores to EventProvider)
// ---------------------------------------------------------------------------

interface StoreWiringProps {
  issueStore: StoreApi<IssueStore>;
  agentStore: StoreApi<AgentStore>;
  retryNowRef: React.MutableRefObject<(() => void) | null>;
  children: ReactNode;
}

function StoreWiring({
  issueStore,
  agentStore,
  retryNowRef,
  children,
}: StoreWiringProps): JSX.Element {
  const { workspaceId } = useWorkspaceContext();
  const {
    retryNow,
    subscribe,
    state: connectionState,
    reconnectAttempts,
  } = useEventContext();

  // 1. Wire retryNow ref
  retryNowRef.current = retryNow;

  // 2. Connect issueStore to EventProvider SSE events
  useEffect(() => {
    const unsubscribe = issueStore.getState().connectToEvents(subscribe);
    return unsubscribe;
  }, [issueStore, subscribe]);

  // 3. Refresh monitor status from the existing workspace SSE stream.
  // SSE gives low-latency updates; periodic polling covers agent-only changes
  // and reconnect races that are not represented in the issue mutation stream.
  useEffect(() => {
    let refreshTimer: ReturnType<typeof setTimeout> | null = null;

    const scheduleRefresh = (): void => {
      if (refreshTimer) clearTimeout(refreshTimer);
      refreshTimer = setTimeout(() => {
        refreshTimer = null;
        void agentStore.getState().fetchData();
      }, MONITOR_REFRESH_DEBOUNCE_MS);
    };

    const unsubscribe = subscribe((mutation) => {
      if (mutation.workspace_id && mutation.workspace_id !== workspaceId)
        return;
      if (mutation.entity_type) {
        if (!MONITOR_REFRESH_ENTITY_TYPES.has(mutation.entity_type)) return;
      } else if (!MONITOR_REFRESH_TYPES.has(mutation.type)) {
        return;
      }
      scheduleRefresh();
    });

    return () => {
      if (refreshTimer) clearTimeout(refreshTimer);
      unsubscribe();
    };
  }, [agentStore, subscribe, workspaceId]);

  // 4. Mirror EventProvider connection state → issueStore
  useEffect(() => {
    issueStore.getState().setConnectionState(connectionState);
  }, [issueStore, connectionState]);

  useEffect(() => {
    issueStore.getState().setReconnectAttempts(reconnectAttempts);
  }, [issueStore, reconnectAttempts]);

  // 5. Reset stores on workspace *change* + fetch initial agent status.
  // Issue fetching is driven by App.tsx (mode-aware), not here.
  // Skip reset on the initial mount: App.tsx's sibling useEffect fires its
  // fetchIssues(...) call before this parent effect runs (children-first
  // ordering), and reset() would abort that in-flight fetch via its
  // activeController.abort(). The aborted fetch hits the AbortError branch,
  // clears isLoading, and doesn't schedule a retry — leaving the kanban
  // empty until the user switches tabs. Only reset on actual transitions.
  const prevWorkspaceIdRef = useRef<string | null>(null);
  useEffect(() => {
    if (
      prevWorkspaceIdRef.current !== null &&
      prevWorkspaceIdRef.current !== workspaceId
    ) {
      issueStore.getState().reset();
      agentStore.getState().reset();
    }
    prevWorkspaceIdRef.current = workspaceId;

    agentStore.getState().startPolling({
      workspaceId,
      pollInterval: AGENT_REFRESH_INTERVAL_MS,
    });

    return () => {
      agentStore.getState().stopPolling();
    };
  }, [workspaceId]); // eslint-disable-line react-hooks/exhaustive-deps

  // 6. Cleanup on unmount
  useEffect(() => {
    return () => {
      issueStore.getState().reset();
      agentStore.getState().reset();
    };
  }, [issueStore, agentStore]);

  return <>{children}</>;
}

// ---------------------------------------------------------------------------
// StoreProvider
// ---------------------------------------------------------------------------

export function StoreProvider({ children }: StoreProviderProps): JSX.Element {
  const { sourceReposFilter } = useWorkspaceContext();
  const { showToast } = useToast();

  // Bridge ref for retryConnection → EventProvider.retryNow
  const retryNowRef = useRef<(() => void) | null>(null);

  // Create stores once — stable across workspace switches
  const [issueStore] = useState(() =>
    createIssueStore({
      onToast: (msg, opts) =>
        showToast(msg, opts as Parameters<typeof showToast>[1]),
      retryConnectionFn: () => retryNowRef.current?.(),
    }),
  );
  const [agentStore] = useState(() =>
    createAgentStore({ onToast: (msg) => showToast(msg) }),
  );

  // Stable context value (store references never change)
  const contextValue = useMemo(
    () => ({ issueStore, agentStore }),
    [issueStore, agentStore],
  );

  return (
    <StoreContext.Provider value={contextValue}>
      <EventProvider sourceRepos={sourceReposFilter}>
        <StoreWiring
          issueStore={issueStore}
          agentStore={agentStore}
          retryNowRef={retryNowRef}
        >
          {children}
        </StoreWiring>
      </EventProvider>
    </StoreContext.Provider>
  );
}

// ---------------------------------------------------------------------------
// Consumer hooks
// ---------------------------------------------------------------------------

export function useIssueStoreInstance(): StoreApi<IssueStore> {
  const context = useContext(StoreContext);
  return (context ?? NO_STORE_CONTEXT).issueStore;
}

export function useAgentStoreInstance(): StoreApi<AgentStore> {
  const context = useContext(StoreContext);
  return (context ?? NO_STORE_CONTEXT).agentStore;
}
