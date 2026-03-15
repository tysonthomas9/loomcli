/**
 * SSE (Server-Sent Events) client for real-time mutation events from the beads server.
 * Provides a simpler push model compared to WebSocket with built-in browser reconnection.
 */

import { getAuthToken, getAuthState, initAuth } from "./client";

// Connection states for real-time event streaming
export type ConnectionState =
  | "disconnected"
  | "connecting"
  | "connected"
  | "reconnecting";

// Mutation types from the backend
export type MutationType =
  | "create"
  | "update"
  | "delete"
  | "comment"
  | "status"
  | "bonded"
  | "squashed"
  | "burned"
  | "refresh"
  | "terminal_metadata"
  | "terminal_session_change"
  | "issue_tabs";

// Server → Client: Mutation payload
export interface MutationPayload {
  type: MutationType;
  issue_id: string;
  title?: string;
  assignee?: string;
  owner?: string;
  actor?: string;
  timestamp: string;
  old_status?: string;
  new_status?: string;
  parent_id?: string;
  step_count?: number;
  source_repo?: string;
}

/**
 * Options for the BeadsSSEClient.
 */
export interface SSEClientOptions {
  /** Called when a mutation event is received */
  onMutation?: (mutation: MutationPayload) => void;
  /** Called when an error occurs */
  onError?: (error: string) => void;
  /** Called when the connection state changes */
  onStateChange?: (state: ConnectionState) => void;
  /** Called on reconnection attempts (for tracking consecutive errors) */
  onReconnect?: (attempt: number) => void;
}

/**
 * SSE client for beads mutation events.
 * Uses the EventSource API which provides automatic reconnection.
 */
export class BeadsSSEClient {
  private eventSource: EventSource | null = null;
  private state: ConnectionState = "disconnected";
  private reconnectAttempts = 0;
  private lastEventId: number | undefined;
  private manualDisconnect = false;
  private currentSourceRepos?: string[] | undefined;

  private onMutation: ((mutation: MutationPayload) => void) | undefined;
  private onError: ((error: string) => void) | undefined;
  private onStateChange: ((state: ConnectionState) => void) | undefined;
  private onReconnect: ((attempt: number) => void) | undefined;

  constructor(options: SSEClientOptions = {}) {
    this.onMutation = options.onMutation;
    this.onError = options.onError;
    this.onStateChange = options.onStateChange;
    this.onReconnect = options.onReconnect;
  }

  /**
   * Connect to the SSE endpoint.
   * If auth state is 'failed', attempts token re-acquisition before connecting.
   * @param since Optional timestamp (ms) to receive events since that time
   * @param sourceRepos Optional repo filter for server-side event filtering
   */
  async connect(since?: number, sourceRepos?: string[]): Promise<void> {
    // Always update stored sourceRepos even if we bail early,
    // so retryNow() uses the latest filter
    if (sourceRepos !== undefined) {
      this.currentSourceRepos = sourceRepos;
    }

    if (this.state === "connected" || this.state === "connecting") {
      return;
    }

    this.manualDisconnect = false;
    this.setState("connecting");

    // If auth previously failed, try re-acquiring token before connecting
    if (getAuthToken() === null && getAuthState() === "failed") {
      await initAuth({ maxRetries: 0 }).catch(() => {
        // Best-effort; proceed even if re-auth fails (auth may be disabled)
      });
    }

    // Use provided since value or fall back to last received event ID
    const sinceParam = since ?? this.lastEventId;
    const url = getSSEUrl(sinceParam, sourceRepos);

    try {
      this.eventSource = new EventSource(url);
      this.eventSource.onopen = () => this.handleOpen();
      this.eventSource.onerror = () => this.handleError();
      this.eventSource.addEventListener("mutation", (e) =>
        this.handleMutation(e as MessageEvent),
      );
      this.eventSource.addEventListener("connected", () => {
        // Server sends 'connected' event on successful connection
        // State already set by onopen, so just log for debugging
        if (process.env.NODE_ENV === "development") {
          console.debug("[SSE] Received connected event");
        }
      });
    } catch (err) {
      console.error("[SSE] Failed to create EventSource:", err);
      this.handleError();
    }
  }

  /**
   * Disconnect from the SSE endpoint.
   */
  disconnect(): void {
    this.manualDisconnect = true;

    if (this.eventSource) {
      this.eventSource.close();
      this.eventSource = null;
    }

    this.setState("disconnected");
  }

  /**
   * Get the current connection state.
   */
  getState(): ConnectionState {
    return this.state;
  }

  /**
   * Get the current number of consecutive reconnection errors.
   */
  getReconnectAttempts(): number {
    return this.reconnectAttempts;
  }

  /**
   * Get the last event ID received from the server.
   * This is the timestamp (in ms) used for catch-up on reconnection.
   * Returns undefined if no events have been received yet.
   */
  getLastEventId(): number | undefined {
    return this.lastEventId;
  }

  /**
   * Immediately retry connection.
   * Only works when in 'reconnecting' state.
   * Resets the reconnect counter on manual retry.
   */
  retryNow(): void {
    if (this.state !== "reconnecting") {
      return;
    }

    // Close existing EventSource if any
    if (this.eventSource) {
      this.eventSource.close();
      this.eventSource = null;
    }

    this.reconnectAttempts = 0;
    this.onReconnect?.(0);
    this.connect(undefined, this.currentSourceRepos);
  }

  /**
   * Disconnect and clean up all resources.
   * After calling destroy(), this instance should not be reused.
   */
  destroy(): void {
    // Clear callbacks first to prevent any callbacks during cleanup
    this.onMutation = undefined;
    this.onError = undefined;
    this.onStateChange = undefined;
    this.onReconnect = undefined;
    this.disconnect();
  }

  private setState(state: ConnectionState): void {
    if (this.state !== state) {
      this.state = state;
      this.onStateChange?.(state);
    }
  }

  private handleOpen(): void {
    const wasReconnecting = this.reconnectAttempts > 0;
    this.setState("connected");
    this.reconnectAttempts = 0;
    // Only notify about reconnect counter reset if we were actually reconnecting
    if (wasReconnecting) {
      this.onReconnect?.(0);
    }
  }

  private handleError(): void {
    // If manually disconnected, don't process error
    if (this.manualDisconnect) {
      return;
    }

    // EventSource has three readyStates: CONNECTING(0), OPEN(1), CLOSED(2)
    // Browser automatically retries on error, so we track attempts
    if (
      this.eventSource &&
      this.eventSource.readyState === EventSource.CONNECTING
    ) {
      // Browser is reconnecting
      this.reconnectAttempts++;
      this.setState("reconnecting");
      this.onReconnect?.(this.reconnectAttempts);
    } else if (
      this.eventSource &&
      this.eventSource.readyState === EventSource.CLOSED
    ) {
      // Connection permanently closed
      this.reconnectAttempts++;
      this.setState("reconnecting");
      this.onReconnect?.(this.reconnectAttempts);
      this.onError?.("Connection closed");
    }

    // Log warning after multiple failures
    if (this.reconnectAttempts === 5) {
      console.warn(
        "[SSE] Multiple connection failures, will continue retrying",
      );
    }
  }

  private handleMutation(event: MessageEvent): void {
    let mutation: MutationPayload;
    try {
      mutation = JSON.parse(event.data as string);
    } catch {
      // Invalid JSON - log and skip
      console.warn("[SSE] Received malformed mutation event");
      return;
    }

    // Track last event ID for reconnection catch-up
    // The server sends `id: <unix_ms>` which is exposed via event.lastEventId
    if (event.lastEventId) {
      const eventId = parseInt(event.lastEventId, 10);
      if (!isNaN(eventId) && eventId > 0) {
        if (this.lastEventId === undefined || eventId > this.lastEventId) {
          this.lastEventId = eventId;
        }
      }
    }

    this.onMutation?.(mutation);
  }
}

/**
 * Get the SSE URL for the events endpoint.
 * @param since Optional timestamp (ms) for catch-up events
 */
export function getSSEUrl(since?: number, sourceRepos?: string[]): string {
  const base = `${window.location.origin}/api/events`;
  const params = new URLSearchParams();
  if (since !== undefined) {
    params.set("since", String(since));
  }
  if (sourceRepos && sourceRepos.length > 0) {
    params.set("source_repos", sourceRepos.join(","));
  }
  const token = getAuthToken();
  if (token) {
    params.set("token", token);
  }
  const qs = params.toString();
  return qs ? `${base}?${qs}` : base;
}
