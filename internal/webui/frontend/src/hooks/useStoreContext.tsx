/**
 * useStoreContext — React context wiring EventProvider to Zustand stores
 * (issueStore, agentStore). Creates store instances once, mounts EventProvider
 * for SSE, and uses an internal StoreWiring child to connect stores ↔ EventProvider.
 *
 * Follows useWorkspaceContext / useAgentContext pattern.
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

import { createIssueStore, type IssueStore } from "../stores/issueStore";
import { createAgentStore, type AgentStore } from "../stores/agentStore";
import { useWorkspaceContext } from "./useWorkspaceContext";
import { useToast } from "./useToast";
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
  const eventContext = useEventContext();

  // 1. Wire retryNow ref
  retryNowRef.current = eventContext.retryNow;

  // 2. Connect issueStore to EventProvider SSE events
  useEffect(() => {
    const unsubscribe = issueStore
      .getState()
      .connectToEvents(eventContext.subscribe);
    return unsubscribe;
  }, [issueStore, eventContext.subscribe]);

  // 3. Mirror EventProvider connection state → issueStore
  useEffect(() => {
    issueStore.getState().setConnectionState(eventContext.state);
  }, [issueStore, eventContext.state]);

  useEffect(() => {
    issueStore.getState().setReconnectAttempts(eventContext.reconnectAttempts);
  }, [issueStore, eventContext.reconnectAttempts]);

  // 4. Reset stores on workspace change + start agent polling.
  // Issue fetching is driven by App.tsx (mode-aware), not here.
  useEffect(() => {
    issueStore.getState().reset();

    agentStore.getState().reset();
    agentStore.getState().startPolling({ pollInterval: 5000 });

    return () => {
      agentStore.getState().stopPolling();
    };
  }, [workspaceId]); // eslint-disable-line react-hooks/exhaustive-deps

  // 5. Cleanup on unmount
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
