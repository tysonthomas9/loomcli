/**
 * useEventProvider - React context for a shared SSE connection with typed event subscription.
 * Consolidates SSE into a single WorkspaceSSEClient instance per workspace,
 * fanning out mutation events to subscribers via a ref-based registry.
 *
 * Follows useWorkspaceContext pattern.
 */

import {
  createContext,
  useContext,
  useEffect,
  useRef,
  useState,
  useCallback,
  useMemo,
  type ReactNode,
} from "react";

import {
  WorkspaceSSEClient,
  type ConnectionState,
  type MutationEntityType,
  type MutationPayload,
  type MutationType,
} from "@/api/common";
import { useWorkspaceContext } from "@/hooks/workspace";
import {
  InvalidatedQueryRegistry,
  InvalidatedQueryRegistryContext,
} from "./invalidatedQueryRegistry";

export type { ConnectionState } from "@/api/common";

/**
 * Options for filtering which mutation events a subscriber receives.
 */
export interface SubscriptionOptions {
  /** If provided, only mutations with matching types are delivered. Empty array = all events. */
  types?: MutationType[];
  /** If provided, only mutations with matching entity types are delivered. Empty array = all events. */
  entityTypes?: MutationEntityType[];
  /** If provided, only mutations with matching source actions are delivered. Empty array = all events. */
  actions?: string[];
}

/**
 * Context value exposed by EventProvider.
 * Contains connection metadata (reactive) and a stable subscribe function.
 */
export interface EventContextValue {
  /** Current SSE connection state */
  state: ConnectionState;
  /** Number of consecutive reconnection attempts */
  reconnectAttempts: number;
  /** Last error message, if any */
  lastError: string | null;
  /** Convenience boolean — true when state === 'connected' */
  isConnected: boolean;
  /** Number of completed application-level SSE handshakes. */
  connectionEpoch: number;
  /** Register a mutation listener. Returns an unsubscribe function. */
  subscribe: (
    callback: (mutation: MutationPayload) => void,
    options?: SubscriptionOptions,
  ) => () => void;
  /** Immediately retry connection (only works in 'reconnecting' state) */
  retryNow: () => void;
  /** Disconnect from the SSE endpoint */
  disconnect: () => void;
}

/** Default no-op value returned when useEventContext is called outside an EventProvider. */
export const NO_EVENT_CONTEXT: EventContextValue = {
  state: "disconnected",
  reconnectAttempts: 0,
  lastError: null,
  isConnected: false,
  connectionEpoch: 0,
  subscribe: () => () => {},
  retryNow: () => {},
  disconnect: () => {},
};

export const EventContext = createContext<EventContextValue | undefined>(
  undefined,
);

/**
 * Props for EventProvider.
 */
export interface EventProviderProps {
  /** Source repo filter for server-side SSE event filtering */
  sourceRepos?: string[] | undefined;
  /** Auto-connect on mount. Default: true */
  autoConnect?: boolean;
  children: ReactNode;
}

interface SubscriberEntry {
  callback: (mutation: MutationPayload) => void;
  types: MutationType[] | undefined;
  entityTypes: MutationEntityType[] | undefined;
  actions: string[] | undefined;
}

/**
 * EventProvider owns a single WorkspaceSSEClient and fans out mutation events
 * to subscribers registered via subscribe(). Connection metadata is exposed
 * via context; mutations are dispatched via direct callback invocation
 * (no React re-render path).
 */
export function EventProvider({
  sourceRepos,
  autoConnect = true,
  children,
}: EventProviderProps): JSX.Element {
  const { workspaceId } = useWorkspaceContext();

  // Reactive connection state (exposed via context)
  const [state, setState] = useState<ConnectionState>("disconnected");
  const [reconnectAttempts, setReconnectAttempts] = useState(0);
  const [lastError, setLastError] = useState<string | null>(null);
  const [connectionEpoch, setConnectionEpoch] = useState(0);

  // One invalidated-query registry belongs to this workspace SSE owner.
  const invalidatedQueryRegistryRef = useRef<InvalidatedQueryRegistry | null>(
    null,
  );
  if (invalidatedQueryRegistryRef.current === null) {
    invalidatedQueryRegistryRef.current = new InvalidatedQueryRegistry();
  }
  const invalidatedQueryRegistry = invalidatedQueryRegistryRef.current;

  // Ref-based subscriber registry — changes don't trigger re-renders
  const subscriberIdRef = useRef(0);
  const subscribersRef = useRef<Map<number, SubscriberEntry>>(new Map());

  // SSE client ref
  const clientRef = useRef<WorkspaceSSEClient | null>(null);
  const mountedRef = useRef(true);

  // Track sourceRepos for reconnect detection
  const sourceReposRef = useRef(sourceRepos);
  const prevSourceReposRef = useRef<string[] | undefined>(undefined);

  useEffect(() => {
    sourceReposRef.current = sourceRepos;
  }, [sourceRepos]);

  // Stable subscribe function — lives in a ref so context value stays stable
  const subscribeRef = useRef(
    (
      callback: (mutation: MutationPayload) => void,
      options?: SubscriptionOptions,
    ): (() => void) => {
      const id = ++subscriberIdRef.current;
      const types =
        options?.types && options.types.length > 0 ? options.types : undefined;
      const entityTypes =
        options?.entityTypes && options.entityTypes.length > 0
          ? options.entityTypes
          : undefined;
      const actions =
        options?.actions && options.actions.length > 0
          ? options.actions
          : undefined;
      subscribersRef.current.set(id, {
        callback,
        types,
        entityTypes,
        actions,
      });
      return () => {
        subscribersRef.current.delete(id);
      };
    },
  );

  // Exposed as a stable callback (identity never changes)
  const subscribe = useCallback(
    (
      callback: (mutation: MutationPayload) => void,
      options?: SubscriptionOptions,
    ) => subscribeRef.current(callback, options),
    [],
  );

  // Create client on mount / workspaceId change
  useEffect(() => {
    if (typeof window === "undefined") return;

    mountedRef.current = true;

    // Reset reactive state for the new client (prevents stale values
    // from a previous workspaceId's client bleeding into the new one)
    setState("disconnected");
    setReconnectAttempts(0);
    setLastError(null);
    setConnectionEpoch(0);

    const client = new WorkspaceSSEClient(workspaceId, {
      onMutation: (mutation: MutationPayload) => {
        if (!mountedRef.current) return;
        // Fan out to all subscribers
        for (const entry of subscribersRef.current.values()) {
          if (entry.types && !entry.types.includes(mutation.type)) {
            continue;
          }
          if (
            entry.entityTypes &&
            (mutation.entity_type == null ||
              !entry.entityTypes.includes(mutation.entity_type))
          ) {
            continue;
          }
          if (
            entry.actions &&
            (mutation.action == null ||
              !entry.actions.includes(mutation.action))
          ) {
            continue;
          }
          try {
            entry.callback(mutation);
          } catch (err) {
            console.error("[EventProvider] Subscriber callback threw:", err);
          }
        }
      },
      onStateChange: (newState: ConnectionState) => {
        if (!mountedRef.current) return;
        setState(newState);
        if (newState === "connected") {
          setLastError(null);
        }
      },
      onError: (error: string) => {
        if (!mountedRef.current) return;
        setLastError(error);
      },
      onReconnect: (attempt: number) => {
        if (!mountedRef.current) return;
        setReconnectAttempts(attempt);
      },
      onConnected: () => {
        if (!mountedRef.current) return;
        setConnectionEpoch((epoch) => epoch + 1);
      },
    });
    clientRef.current = client;

    if (autoConnect) {
      client.connect(undefined, sourceReposRef.current);
    }

    const handleSignOut = () => {
      client.destroy();
    };
    window.addEventListener("auth-sign-out", handleSignOut);

    return () => {
      mountedRef.current = false;
      prevSourceReposRef.current = undefined;
      window.removeEventListener("auth-sign-out", handleSignOut);
      client.destroy();
      clientRef.current = null;
    };
  }, [autoConnect, workspaceId]);

  // Reconnect when sourceRepos changes
  useEffect(() => {
    const prev = prevSourceReposRef.current;
    prevSourceReposRef.current = sourceRepos;

    // Skip initial mount (autoConnect handles that)
    if (prev === undefined) return;

    // Compare arrays (sorted join to ignore reordering)
    const prevKey = (prev ?? []).slice().sort().join(",");
    const nextKey = (sourceRepos ?? []).slice().sort().join(",");
    if (prevKey === nextKey) return;

    const client = clientRef.current;
    if (client) {
      client.disconnect();
      client.connect(undefined, sourceRepos);
    }
  }, [sourceRepos]);

  // Stable control methods
  const retryNow = useCallback(() => {
    clientRef.current?.retryNow();
  }, []);

  const disconnect = useCallback(() => {
    clientRef.current?.disconnect();
  }, []);

  const isConnected = state === "connected";

  const value = useMemo<EventContextValue>(
    () => ({
      state,
      reconnectAttempts,
      lastError,
      isConnected,
      connectionEpoch,
      subscribe,
      retryNow,
      disconnect,
    }),
    [
      state,
      reconnectAttempts,
      lastError,
      isConnected,
      connectionEpoch,
      subscribe,
      retryNow,
      disconnect,
    ],
  );

  return (
    <InvalidatedQueryRegistryContext.Provider value={invalidatedQueryRegistry}>
      <EventContext.Provider value={value}>{children}</EventContext.Provider>
    </InvalidatedQueryRegistryContext.Provider>
  );
}

/**
 * Hook to access event context.
 * Returns safe defaults when used outside an EventProvider.
 */
export function useEventContext(): EventContextValue {
  const context = useContext(EventContext);
  return context ?? NO_EVENT_CONTEXT;
}

/**
 * Convenience hook that subscribes to mutation events and automatically
 * unsubscribes on unmount. The callback is stored in a ref so identity
 * changes don't cause re-subscription.
 */
export function useEventSubscription(
  callback: (mutation: MutationPayload) => void,
  options?: SubscriptionOptions,
): void {
  const { subscribe } = useEventContext();
  const callbackRef = useRef(callback);

  useEffect(() => {
    callbackRef.current = callback;
  }, [callback]);

  // Stable key for the types filter — re-subscribe only when the actual
  // filter set changes, not on every render due to a new array reference.
  const typesKey = options?.types?.slice().sort().join(",") ?? "";
  const entityTypesKey = options?.entityTypes?.slice().sort().join(",") ?? "";
  const actionsKey = options?.actions?.slice().sort().join(",") ?? "";

  useEffect(() => {
    const types = typesKey
      ? (typesKey.split(",") as MutationType[])
      : undefined;
    const entityTypes = entityTypesKey
      ? (entityTypesKey.split(",") as MutationEntityType[])
      : undefined;
    const actions = actionsKey ? actionsKey.split(",") : undefined;
    const subscriptionOptions: SubscriptionOptions = {};
    if (types) subscriptionOptions.types = types;
    if (entityTypes) subscriptionOptions.entityTypes = entityTypes;
    if (actions) subscriptionOptions.actions = actions;
    const unsubscribe = subscribe(
      (mutation: MutationPayload) => {
        callbackRef.current(mutation);
      },
      types || entityTypes || actions ? subscriptionOptions : undefined,
    );
    return unsubscribe;
  }, [subscribe, typesKey, entityTypesKey, actionsKey]);
}
