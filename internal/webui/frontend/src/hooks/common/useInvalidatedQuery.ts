/**
 * React facade for the shared invalidated-query registry. Equal keys share
 * one entry; modern entity events match before refresh/type fallbacks, and a
 * changed provider connection epoch triggers a debounced repair fetch.
 */

import {
  useCallback,
  useContext,
  useEffect,
  useRef,
  useSyncExternalStore,
} from "react";

import { useEventContext } from "./useEventProvider";
import {
  InvalidatedQueryRegistryContext,
  type InvalidatedQueryRegistration,
  type UseInvalidatedQueryOptions,
  type UseInvalidatedQuerySnapshot,
} from "./invalidatedQueryRegistry";

export type {
  UseInvalidatedQueryOptions,
  UseInvalidatedQuerySnapshot,
} from "./invalidatedQueryRegistry";

export interface UseInvalidatedQueryResult<T> {
  data: T | null;
  loading: boolean;
  error: Error | null;
  refetch: () => Promise<void>;
}

export function useInvalidatedQuery<T>(
  fetcher: (signal: AbortSignal) => Promise<T>,
  options: UseInvalidatedQueryOptions,
): UseInvalidatedQueryResult<T> {
  const eventContext = useEventContext();
  const { subscribe: eventSubscribe, connectionEpoch } = eventContext;
  const registry = useContext(InvalidatedQueryRegistryContext);
  const enabled = options.enabled ?? true;
  const resetOnKeyChange = options.resetOnKeyChange ?? false;
  const registrationRef = useRef<InvalidatedQueryRegistration<T> | null>(null);

  const previousRegistration = registrationRef.current;
  if (
    previousRegistration === null ||
    previousRegistration.key !== options.key
  ) {
    registrationRef.current = registry.register(
      options.key,
      fetcher,
      options,
      eventContext.connectionEpoch,
    );
  }
  const registration = registrationRef.current;
  if (registration === null) {
    throw new Error("useInvalidatedQuery failed to create a registration");
  }

  useEffect(() => {
    registration.revive(fetcher, connectionEpoch);
  }, [connectionEpoch, fetcher, registration]);

  useEffect(() => {
    registration.setEnabled(enabled);
  }, [enabled, registration]);

  useEffect(() => {
    if (!enabled) return;
    return eventSubscribe((mutation) => {
      registration.invalidate(mutation);
    });
  }, [enabled, eventSubscribe, registration]);

  useEffect(() => {
    registration.onEpoch(connectionEpoch);
  }, [connectionEpoch, registration]);

  useEffect(() => {
    return () => registration.dispose();
  }, [registration]);

  const subscribe = useCallback(
    (listener: () => void) => registration.subscribe(listener),
    [registration],
  );
  const getSnapshot = useCallback(
    () => registration.getSnapshot(),
    [registration],
  );
  const snapshot = useSyncExternalStore<UseInvalidatedQuerySnapshot<T>>(
    subscribe,
    getSnapshot,
    getSnapshot,
  );

  const previousDataRef = useRef<T | null>(null);
  const previousKeyRef = useRef(options.key);
  if (previousKeyRef.current !== options.key) {
    previousKeyRef.current = options.key;
    if (resetOnKeyChange) previousDataRef.current = null;
  }
  if (snapshot.data !== null) previousDataRef.current = snapshot.data;

  const data =
    snapshot.data !== null
      ? snapshot.data
      : resetOnKeyChange
        ? null
        : previousDataRef.current;

  const refetch = useCallback(() => registration.refetch(), [registration]);
  return {
    data,
    loading: snapshot.loading,
    error: snapshot.error,
    refetch,
  };
}
