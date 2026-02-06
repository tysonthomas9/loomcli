/**
 * React hook that wraps BeadsSSEClient for use in components.
 * Manages SSE lifecycle with React component lifecycle (connect on mount, cleanup on unmount).
 *
 * SSE provides a simpler push model compared to WebSocket:
 * - Connection equals subscription (no separate subscribe message)
 * - Built-in browser reconnection with Last-Event-ID header
 * - Automatic catch-up of missed events on reconnect
 */

import { useState, useEffect, useRef, useCallback } from 'react';

import {
  BeadsSSEClient,
  type SSEClientOptions,
  type ConnectionState,
  type MutationPayload,
} from '../api/sse';

/**
 * Options for the useSSE hook.
 */
export interface UseSSEOptions {
  /** Called when a mutation event is received */
  onMutation?: (mutation: MutationPayload) => void;
  /** Called when an error occurs */
  onError?: (error: string) => void;
  /** Called when the connection state changes */
  onStateChange?: (state: ConnectionState) => void;
  /** Auto-connect on mount. Default: true */
  autoConnect?: boolean;
  /** Initial timestamp (ms) for catch-up events when connecting */
  since?: number | undefined;
}

/**
 * Return type for the useSSE hook.
 * Designed to be compatible with UseWebSocketReturn for easier migration.
 */
export interface UseSSEReturn {
  /** Current connection state (reactive) */
  state: ConnectionState;
  /** Last error message, if any (reactive) */
  lastError: string | null;
  /** Convenience boolean - true when state === 'connected' */
  isConnected: boolean;
  /** Current number of reconnection attempts (reactive) */
  reconnectAttempts: number;
  /** Last event ID received from server (for debugging/catch-up tracking) */
  lastEventId: number | undefined;
  /** Connect to the SSE endpoint */
  connect: () => void;
  /** Disconnect from the SSE endpoint */
  disconnect: () => void;
  /** Immediately retry connection (only works in 'reconnecting' state) */
  retryNow: () => void;
}

/**
 * React hook for managing SSE connections to the beads server.
 *
 * @example
 * ```tsx
 * function MyComponent() {
 *   const { state, isConnected } = useSSE({
 *     autoConnect: true,
 *     since: Date.now(), // Start from current time
 *     onMutation: (mutation) => {
 *       console.log('Received mutation:', mutation)
 *     },
 *   })
 *
 *   return <div>Status: {state}</div>
 * }
 * ```
 */
export function useSSE(options: UseSSEOptions = {}): UseSSEReturn {
  const { autoConnect = true, since, onMutation, onError, onStateChange } = options;

  // Reactive state
  const [state, setState] = useState<ConnectionState>('disconnected');
  const [lastError, setLastError] = useState<string | null>(null);
  const [reconnectAttempts, setReconnectAttempts] = useState(0);
  const [lastEventId, setLastEventId] = useState<number | undefined>(undefined);

  // Refs for stable references across renders
  const clientRef = useRef<BeadsSSEClient | null>(null);
  const mountedRef = useRef(true);

  // Store callbacks in refs to avoid stale closures
  const onMutationRef = useRef(onMutation);
  const onErrorRef = useRef(onError);
  const onStateChangeRef = useRef(onStateChange);
  const sinceRef = useRef(since);

  // Update refs when callbacks/values change
  useEffect(() => {
    onMutationRef.current = onMutation;
  }, [onMutation]);

  useEffect(() => {
    onErrorRef.current = onError;
  }, [onError]);

  useEffect(() => {
    onStateChangeRef.current = onStateChange;
  }, [onStateChange]);

  useEffect(() => {
    sinceRef.current = since;
  }, [since]);

  // Create client on mount
  useEffect(() => {
    // Guard against SSR (no window)
    if (typeof window === 'undefined') return;

    mountedRef.current = true;

    const clientOptions: SSEClientOptions = {
      onMutation: (mutation: MutationPayload) => {
        if (!mountedRef.current) return;
        // Update lastEventId from the client after each mutation
        const eventId = clientRef.current?.getLastEventId();
        if (eventId !== undefined) {
          setLastEventId(eventId);
        }
        onMutationRef.current?.(mutation);
      },
      onError: (error: string) => {
        if (!mountedRef.current) return;
        setLastError(error);
        onErrorRef.current?.(error);
      },
      onStateChange: (newState: ConnectionState) => {
        if (!mountedRef.current) return;
        setState(newState);
        // Clear error on successful connection
        if (newState === 'connected') {
          setLastError(null);
        }
        onStateChangeRef.current?.(newState);
      },
      onReconnect: (attempt: number) => {
        if (!mountedRef.current) return;
        setReconnectAttempts(attempt);
      },
    };

    const client = new BeadsSSEClient(clientOptions);
    clientRef.current = client;

    // Auto-connect if enabled
    if (autoConnect) {
      client.connect(sinceRef.current);
    }

    // Cleanup on unmount
    return () => {
      mountedRef.current = false;
      client.destroy();
      clientRef.current = null;
    };
  }, [autoConnect]);

  // Stable method references
  const connect = useCallback(() => {
    clientRef.current?.connect(sinceRef.current);
  }, []);

  const disconnect = useCallback(() => {
    clientRef.current?.disconnect();
  }, []);

  const retryNow = useCallback(() => {
    clientRef.current?.retryNow();
  }, []);

  // Computed values
  const isConnected = state === 'connected';

  return {
    state,
    lastError,
    isConnected,
    reconnectAttempts,
    lastEventId,
    connect,
    disconnect,
    retryNow,
  };
}
