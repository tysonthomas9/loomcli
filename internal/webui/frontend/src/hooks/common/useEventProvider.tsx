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
  useLayoutEffect,
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
  type ResyncEvent,
} from "@/api/common";
import {
  getAuthCredentialGeneration,
  getAuthToken,
  onAuthCredentialChange,
} from "@/api/common/client";
import {
  IssueRecoveryAttemptController,
  type IssueRecoveryAttemptStatus,
} from "./issueRecoveryAttempt";
import { useWorkspaceContext } from "@/hooks/workspace";
import {
  QueryRecoveryCoordinator,
  QueryRecoveryContext,
} from "./queryRecovery";
import {
  InvalidatedQueryRegistry,
  InvalidatedQueryRegistryContext,
} from "./invalidatedQueryRegistry";

import {
  IssueRecoverySelectionRegistry,
  IssueRecoverySelectionContext,
} from "./issueRecoverySelection";

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
  /** Native recovery preparation only; never a reset acknowledgment. */
  recoveryStatus: IssueRecoveryAttemptStatus;
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
  /** Register a transport-resync listener. Returns an unsubscribe function. */
  onResync: (callback: (event: ResyncEvent) => void) => () => void;
  /** Immediately retry connection (only works in 'reconnecting' state) */
  retryNow: () => void;
  /** Disconnect from the SSE endpoint */
  disconnect: () => void;
}

/** Default no-op value returned when useEventContext is called outside an EventProvider. */
export const NO_EVENT_CONTEXT: EventContextValue = {
  state: "disconnected",
  recoveryStatus: "idle",
  reconnectAttempts: 0,
  lastError: null,
  isConnected: false,
  connectionEpoch: 0,
  subscribe: () => () => {},
  onResync: () => () => {},
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
  const [recoveryStatus, setRecoveryStatus] =
    useState<IssueRecoveryAttemptStatus>("idle");
  const [credentialGeneration, setCredentialGeneration] = useState(
    getAuthCredentialGeneration,
  );
  const recoveryAttempts = useMemo(
    () => new IssueRecoveryAttemptController(),
    [],
  );

  const recoverySelections = useMemo(
    () => new IssueRecoverySelectionRegistry(),
    [],
  );

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
  const resyncSubscribersRef = useRef<
    Map<number, (event: ResyncEvent) => void>
  >(new Map());

  // SSE client ref
  const clientRef = useRef<WorkspaceSSEClient | null>(null);
  const ownerRevisionRef = useRef(0);
  const handshakeResyncPendingRef = useRef(false);

  // Track sourceRepos for reconnect detection
  const sourceReposRef = useRef(sourceRepos);
  const sourceReposKey = (sourceRepos ?? []).slice().sort().join(",");
  const prevSourceReposKeyRef = useRef(sourceReposKey);
  const queryRecovery = useMemo(
    () =>
      new QueryRecoveryCoordinator(
        JSON.stringify([workspaceId, sourceReposKey]),
      ),
    [workspaceId, sourceReposKey],
  );
  const queryRecoveryRef = useRef(queryRecovery);
  useLayoutEffect(() => {
    queryRecoveryRef.current = queryRecovery;
    const unregister = queryRecovery.register(
      "invalidated queries",
      (signal) => invalidatedQueryRegistry.refreshForRecovery(signal),
      () => invalidatedQueryRegistry.getRecoveryRevision(),
    );
    return () => {
      unregister();
      queryRecovery.cancel();
    };
  }, [invalidatedQueryRegistry, queryRecovery]);

  useLayoutEffect(() => {
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

  const onResync = useCallback(
    (callback: (event: ResyncEvent) => void): (() => void) => {
      const id = ++subscriberIdRef.current;
      resyncSubscribersRef.current.set(id, callback);
      return () => {
        resyncSubscribersRef.current.delete(id);
      };
    },
    [],
  );

  const dispatchMutation = useCallback(
    (mutation: MutationPayload, owns: () => boolean): void => {
      for (const entry of subscribersRef.current.values()) {
        if (!owns()) return;
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
          (mutation.action == null || !entry.actions.includes(mutation.action))
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
    [],
  );

  useLayoutEffect(
    () =>
      onAuthCredentialChange((generation) => {
        ownerRevisionRef.current++;
        recoveryAttempts.cancel();
        clientRef.current?.destroy();
        queryRecoveryRef.current.cancel();
        setCredentialGeneration(generation);
      }),
    [recoveryAttempts],
  );

  // Install ownership at commit, before stale asynchronous callbacks can run.
  useLayoutEffect(() => {
    if (typeof window === "undefined") return;

    const owner = ownerRevisionRef;
    owner.current++;

    // Reset reactive state for the new client (prevents stale values
    // from a previous workspaceId's client bleeding into the new one)
    setState("disconnected");
    setReconnectAttempts(0);
    setLastError(null);
    setConnectionEpoch(0);
    recoveryAttempts.cancel();
    setRecoveryStatus("idle");
    handshakeResyncPendingRef.current = false;

    let active = true;
    const client = new WorkspaceSSEClient(workspaceId, {
      onMutation: (mutation: MutationPayload) => {
        if (!active || clientRef.current !== client) return;
        const revision = ownerRevisionRef.current;
        dispatchMutation(
          mutation,
          () =>
            active &&
            clientRef.current === client &&
            revision === ownerRevisionRef.current,
        );
      },
      onStateChange: (newState: ConnectionState) => {
        if (!active || clientRef.current !== client) return;
        if (newState === "connecting" || newState === "reconnecting") {
          handshakeResyncPendingRef.current = false;
        }
        setState(newState);
        if (newState === "connected") {
          setLastError(null);
        }
      },
      onError: (error: string) => {
        if (!active || clientRef.current !== client) return;
        setLastError(error);
      },
      onReconnect: (attempt: number) => {
        if (!active || clientRef.current !== client) return;
        setReconnectAttempts(attempt);
      },
      onConnected: () => {
        if (!active || clientRef.current !== client) return;
        if (handshakeResyncPendingRef.current) {
          handshakeResyncPendingRef.current = false;
          return;
        }
        setConnectionEpoch((epoch) => epoch + 1);
      },
      onResync: (event: ResyncEvent) => {
        const revision = ownerRevisionRef.current;
        const owns = () =>
          active &&
          clientRef.current === client &&
          revision === ownerRevisionRef.current;
        if (!owns()) return;
        if (event.reason === "expired" && event.recovery) {
          const lease = client.suspendForRecovery();
          try {
            const selection = recoverySelections.capture(workspaceId);
            if (!owns() || !lease.isCurrent()) {
              selection.release?.();
              return;
            }
            recoveryAttempts.start(
              event.recovery,
              lease,
              (status) => {
                if (active && clientRef.current === client)
                  setRecoveryStatus(status);
              },
              selection,
            );
          } catch {
            if (owns()) {
              recoveryAttempts.cancel();
              if (owns()) setRecoveryStatus("failed");
            }
          }
          if (!active || clientRef.current !== client || !lease.isCurrent())
            return;
        }
        handshakeResyncPendingRef.current = event.reason !== "overflow";
        setConnectionEpoch((epoch) => epoch + 1);
        dispatchMutation(
          {
            type: "refresh",
            timestamp: new Date().toISOString(),
            workspace_id: workspaceId,
          },
          owns,
        );
        if (!owns()) return;
        // This covers registered surfaces, not a committed source snapshot.
        // Successful query refresh never resets the SSE checkpoint here.
        void queryRecoveryRef.current.refresh().catch((error: unknown) => {
          if (error instanceof Error && error.name === "AbortError") return;
          console.error("[EventProvider] Query recovery failed:", error);
        });
        for (const callback of resyncSubscribersRef.current.values()) {
          if (!owns()) return;
          try {
            callback(event);
          } catch (err) {
            console.error("[EventProvider] Resync subscriber threw:", err);
          }
        }
      },
    });
    clientRef.current = client;

    if (
      autoConnect &&
      (credentialGeneration === 0 || getAuthToken() !== null)
    ) {
      client.connect(undefined, sourceReposRef.current);
    }

    const handleSignOut = () => {
      ownerRevisionRef.current++;
      recoveryAttempts.cancel();
      queryRecoveryRef.current.cancel();
      client.destroy();
    };
    window.addEventListener("auth-sign-out", handleSignOut);

    return () => {
      active = false;
      recoveryAttempts.cancel();
      owner.current++;
      window.removeEventListener("auth-sign-out", handleSignOut);
      client.destroy();
      clientRef.current = null;
    };
  }, [
    autoConnect,
    credentialGeneration,
    dispatchMutation,
    recoveryAttempts,
    recoverySelections,
    workspaceId,
  ]);

  // Revoke recovery and rebind synchronously when committed scope changes.
  useLayoutEffect(() => {
    const prevKey = prevSourceReposKeyRef.current;
    prevSourceReposKeyRef.current = sourceReposKey;
    if (prevKey === sourceReposKey) return;

    const client = clientRef.current;
    if (client) {
      ownerRevisionRef.current++;
      recoveryAttempts.cancel();
      client.updateSourceRepos(sourceRepos);
    }
  }, [recoveryAttempts, sourceRepos, sourceReposKey]);

  // Stable control methods
  const retryNow = useCallback(() => {
    ownerRevisionRef.current++;
    clientRef.current?.retryNow();
  }, []);

  const disconnect = useCallback(() => {
    ownerRevisionRef.current++;
    clientRef.current?.disconnect();
  }, []);

  const isConnected = state === "connected";

  const value = useMemo<EventContextValue>(
    () => ({
      state,
      recoveryStatus,
      reconnectAttempts,
      lastError,
      isConnected,
      connectionEpoch,
      subscribe,
      onResync,
      retryNow,
      disconnect,
    }),
    [
      state,
      recoveryStatus,
      reconnectAttempts,
      lastError,
      isConnected,
      connectionEpoch,
      subscribe,
      onResync,
      retryNow,
      disconnect,
    ],
  );

  return (
    <InvalidatedQueryRegistryContext.Provider value={invalidatedQueryRegistry}>
      <QueryRecoveryContext.Provider value={queryRecovery}>
        <IssueRecoverySelectionContext.Provider value={recoverySelections}>
          <EventContext.Provider value={value}>
            {children}
          </EventContext.Provider>
        </IssueRecoverySelectionContext.Provider>
      </QueryRecoveryContext.Provider>
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

/** Subscribe to transport resync cursor transitions without receiving mutations. */
export function useResyncSubscription(
  callback: (event: ResyncEvent) => void,
): void {
  const { onResync } = useEventContext();
  const callbackRef = useRef(callback);

  useEffect(() => {
    callbackRef.current = callback;
  }, [callback]);

  useEffect(() => onResync((event) => callbackRef.current(event)), [onResync]);
}
