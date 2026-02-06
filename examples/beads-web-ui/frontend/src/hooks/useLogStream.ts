/**
 * React hook for SSE log streaming.
 * Manages EventSource lifecycle with React component lifecycle (connect on mount, cleanup on unmount).
 * Specialized for line-based log streaming with buffer management.
 */

import { useState, useEffect, useRef, useCallback } from 'react';

/**
 * A single log line.
 */
export interface LogLine {
  line: string;
  lineNumber: number;
  timestamp: string;
}

/**
 * Connection states for log streaming.
 */
export type LogStreamState = 'disconnected' | 'connecting' | 'connected' | 'reconnecting';

/**
 * Options for the useLogStream hook.
 */
export interface UseLogStreamOptions {
  /** Log endpoint URL (e.g., "/api/agents/spark/logs/stream") */
  url: string;
  /** Auto-connect on mount. Default: true */
  autoConnect?: boolean;
  /** Maximum lines to keep in buffer. Default: 5000 */
  maxLines?: number;
}

/**
 * Return type for the useLogStream hook.
 */
export interface UseLogStreamReturn {
  /** Array of log lines */
  lines: LogLine[];
  /** Connection state */
  state: LogStreamState;
  /** Whether currently connected */
  isConnected: boolean;
  /** Current reconnection attempt count */
  reconnectAttempts: number;
  /** Last error message */
  lastError: string | null;
  /** Clear all lines from buffer */
  clearLines: () => void;
  /** Manually connect */
  connect: () => void;
  /** Manually disconnect */
  disconnect: () => void;
}

/**
 * React hook for managing SSE log stream connections.
 *
 * @example
 * ```tsx
 * function LogPanel() {
 *   const { lines, state, isConnected } = useLogStream({
 *     url: '/api/agents/spark/logs/stream',
 *     autoConnect: true,
 *   });
 *
 *   return (
 *     <div>
 *       <p>Status: {state}</p>
 *       {lines.map((line) => (
 *         <div key={line.lineNumber}>{line.line}</div>
 *       ))}
 *     </div>
 *   );
 * }
 * ```
 */
export function useLogStream(options: UseLogStreamOptions): UseLogStreamReturn {
  const { url, autoConnect = true, maxLines = 5000 } = options;

  // Reactive state
  const [lines, setLines] = useState<LogLine[]>([]);
  const [state, setState] = useState<LogStreamState>('disconnected');
  const [lastError, setLastError] = useState<string | null>(null);
  const [reconnectAttempts, setReconnectAttempts] = useState(0);

  // Refs for stable references across renders
  const eventSourceRef = useRef<EventSource | null>(null);
  const mountedRef = useRef(true);
  const manualDisconnectRef = useRef(false);
  const lastLineNumberRef = useRef(0);
  const urlRef = useRef(url);
  const maxLinesRef = useRef(maxLines);

  // Update refs when values change
  useEffect(() => {
    urlRef.current = url;
  }, [url]);

  useEffect(() => {
    maxLinesRef.current = maxLines;
  }, [maxLines]);

  // Handle incoming log line event
  const handleLogLine = useCallback((event: MessageEvent) => {
    if (!mountedRef.current) return;

    let data: { line: string; timestamp?: string };
    try {
      data = JSON.parse(event.data as string);
    } catch {
      // Plain text fallback
      data = { line: event.data as string };
    }

    const lineNumber = ++lastLineNumberRef.current;
    const newLine: LogLine = {
      line: data.line,
      lineNumber,
      timestamp: data.timestamp || new Date().toISOString(),
    };

    setLines((prev) => {
      const updated = [...prev, newLine];
      // Enforce maxLines limit (circular buffer - drop oldest)
      if (updated.length > maxLinesRef.current) {
        return updated.slice(updated.length - maxLinesRef.current);
      }
      return updated;
    });
  }, []);

  // Connect to SSE endpoint
  const connect = useCallback(() => {
    if (eventSourceRef.current) {
      return; // Already connected or connecting
    }

    manualDisconnectRef.current = false;
    setState('connecting');
    setLastError(null);

    // Build URL with since parameter for reconnection catch-up
    let connectUrl = urlRef.current;
    if (lastLineNumberRef.current > 0) {
      const separator = connectUrl.includes('?') ? '&' : '?';
      connectUrl = `${connectUrl}${separator}since=${lastLineNumberRef.current}`;
    }

    try {
      const es = new EventSource(connectUrl);
      eventSourceRef.current = es;

      es.onopen = () => {
        if (!mountedRef.current) return;
        setState('connected');
        setLastError(null);
        setReconnectAttempts(0);
      };

      es.onerror = () => {
        if (!mountedRef.current || manualDisconnectRef.current) return;

        if (es.readyState === EventSource.CONNECTING) {
          // Browser is reconnecting
          setState('reconnecting');
          setReconnectAttempts((prev) => prev + 1);
        } else if (es.readyState === EventSource.CLOSED) {
          // Connection permanently closed
          setState('reconnecting');
          setReconnectAttempts((prev) => prev + 1);
          setLastError('Connection closed');
        }
      };

      // Listen for log-line events (matching backend design)
      es.addEventListener('log-line', handleLogLine);

      // Also handle generic message events for backward compatibility
      es.onmessage = handleLogLine;
    } catch (err) {
      console.error('[useLogStream] Failed to create EventSource:', err);
      setState('disconnected');
      setLastError('Failed to connect');
    }
  }, [handleLogLine]);

  // Disconnect from SSE endpoint
  const disconnect = useCallback(() => {
    manualDisconnectRef.current = true;

    if (eventSourceRef.current) {
      eventSourceRef.current.close();
      eventSourceRef.current = null;
    }

    setState('disconnected');
  }, []);

  // Clear lines buffer
  const clearLines = useCallback(() => {
    setLines([]);
    lastLineNumberRef.current = 0;
  }, []);

  // Auto-connect and cleanup
  useEffect(() => {
    // Guard against SSR
    if (typeof window === 'undefined') return;

    mountedRef.current = true;

    if (autoConnect) {
      connect();
    }

    return () => {
      mountedRef.current = false;
      if (eventSourceRef.current) {
        eventSourceRef.current.close();
        eventSourceRef.current = null;
      }
    };
  }, [autoConnect, connect]);

  // Reconnect when URL changes (only if already connected)
  // Track previous URL to detect changes
  const prevUrlRef = useRef(url);
  useEffect(() => {
    // Only act if URL actually changed (not on initial mount)
    if (prevUrlRef.current !== url) {
      prevUrlRef.current = url;

      // Only reconnect if we have an active connection
      if (eventSourceRef.current) {
        // Disconnect from old URL
        eventSourceRef.current.close();
        eventSourceRef.current = null;
        // Clear lines when switching streams
        setLines([]);
        lastLineNumberRef.current = 0;
        // Connect to new URL
        connect();
      }
    }
    // connect is stable (uses refs internally) - safe to exclude
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [url]);

  const isConnected = state === 'connected';

  return {
    lines,
    state,
    isConnected,
    reconnectAttempts,
    lastError,
    clearLines,
    connect,
    disconnect,
  };
}
