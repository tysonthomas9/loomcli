/**
 * SSE (Server-Sent Events) client for real-time mutation events from the workspace event server.
 * Provides injectable token exchange, typed event callbacks, and unified manual reconnect
 * with configurable exponential backoff.
 */

import { get, ApiError, wsUrl, getApiOrigin } from "./client";

// SSE token exchange: fetch opaque token to avoid exposing JWT in URL
export type SseTokenResult =
  | { kind: "token"; token: string }
  | { kind: "disabled" }
  | { kind: "error"; message: string };

export async function fetchSseToken(
  workspaceId: string,
): Promise<SseTokenResult> {
  try {
    const resp = await get<{ token?: string; disabled?: boolean }>(
      wsUrl(workspaceId, "/events/token"),
    );
    if (resp.token) {
      return { kind: "token", token: resp.token };
    }
    return { kind: "disabled" };
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) {
      return { kind: "disabled" };
    }
    const message = err instanceof Error ? err.message : "Unknown error";
    return { kind: "error", message };
  }
}

// Connection states for real-time event streaming
export type ConnectionState =
  | "disconnected"
  | "connecting"
  | "connected"
  | "reconnecting";

// Mutation types: definitions live in src/types/workspace/mutation.ts (the canonical
// source per the Phase 7 frontend layer DAG). Re-exported here so existing
// code that imports them from @/api/sse continues to compile.
import type { MutationPayload } from "@/types/workspace";
export type { MutationType, MutationPayload } from "@/types/workspace";

/**
 * Options for the WorkspaceSSEClient.
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
  /** Called when the server sends the "connected" SSE event (after catch-up events are flushed) */
  onConnected?: () => void;
  /** Injectable token provider. Default: fetchSseToken(workspaceId) */
  fetchToken?: () => Promise<SseTokenResult>;
  /** Starting backoff delay in ms for reconnect (default 1000) */
  initialReconnectDelay?: number;
  /** Maximum backoff delay in ms for reconnect (default 30000) */
  maxReconnectDelay?: number;
}

/**
 * SSE client for workspace mutation events.
 * Uses unified manual reconnect with configurable exponential backoff.
 */
export class WorkspaceSSEClient {
  private eventSource: EventSource | null = null;
  private state: ConnectionState = "disconnected";
  private reconnectAttempts = 0;
  private lastEventId: string | undefined;
  private manualDisconnect = false;
  private currentSourceRepos?: string[] | undefined;
  private workspaceId: string;
  private destroyed = false;
  private connectAbortController: AbortController | null = null;
  private retryTimerId: ReturnType<typeof setTimeout> | null = null;

  private onMutation: ((mutation: MutationPayload) => void) | undefined;
  private onError: ((error: string) => void) | undefined;
  private onStateChange: ((state: ConnectionState) => void) | undefined;
  private onReconnect: ((attempt: number) => void) | undefined;
  private onConnected: (() => void) | undefined;
  private fetchTokenFn: () => Promise<SseTokenResult>;
  private initialReconnectDelay: number;
  private maxReconnectDelay: number;

  constructor(workspaceId: string, options: SSEClientOptions = {}) {
    this.workspaceId = workspaceId;
    this.onMutation = options.onMutation;
    this.onError = options.onError;
    this.onStateChange = options.onStateChange;
    this.onReconnect = options.onReconnect;
    this.onConnected = options.onConnected;
    this.fetchTokenFn =
      options.fetchToken ?? (() => fetchSseToken(this.workspaceId));
    this.initialReconnectDelay = options.initialReconnectDelay ?? 1000;
    this.maxReconnectDelay = options.maxReconnectDelay ?? 30000;
  }

  /**
   * Connect to the SSE endpoint.
   * @param since Optional cursor to receive events after
   * @param sourceRepos Optional repo filter for server-side event filtering
   */
  async connect(
    since?: string | number,
    sourceRepos?: string[],
  ): Promise<void> {
    if (this.destroyed) return;

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

    // Create AbortController for this connection attempt
    const abortController = new AbortController();
    this.connectAbortController = abortController;

    // Fetch opaque SSE token (injectable or default)
    let tokenResult: SseTokenResult;
    try {
      tokenResult = await this.fetchTokenFn();
    } catch (err) {
      // Custom fetchToken threw — treat as error
      if (abortController.signal.aborted || this.destroyed) return;
      const message = err instanceof Error ? err.message : "Unknown error";
      this.onError?.(`SSE auth failed: ${message}`);
      this.setState("disconnected");
      return;
    }

    // Bail out if aborted or destroyed while awaiting token
    if (
      abortController.signal.aborted ||
      this.destroyed ||
      this.manualDisconnect ||
      this.state === "disconnected"
    ) {
      return;
    }

    if (tokenResult.kind === "error") {
      this.onError?.(`SSE auth failed: ${tokenResult.message}`);
      this.setState("disconnected");
      return;
    }

    const opaqueToken =
      tokenResult.kind === "token" ? tokenResult.token : undefined;

    // Use provided since value or fall back to last received event ID
    const sinceParam = since ?? this.lastEventId;
    const url = getSSEUrl(
      this.workspaceId,
      sinceParam,
      sourceRepos,
      opaqueToken,
    );

    try {
      this.eventSource = new EventSource(url);
      this.eventSource.onopen = () => this.handleOpen();
      this.eventSource.onerror = () => this.handleError();
      this.eventSource.addEventListener("mutation", (e) =>
        this.handleMutation(e as MessageEvent),
      );
      this.eventSource.addEventListener("connected", () =>
        this.handleConnected(),
      );
    } catch (err) {
      console.error("[SSE] Failed to create EventSource:", err);
      this.scheduleReconnect();
    }
  }

  /**
   * Disconnect from the SSE endpoint.
   */
  disconnect(): void {
    if (this.destroyed) return;

    this.manualDisconnect = true;

    // Abort any in-flight token fetch
    if (this.connectAbortController) {
      this.connectAbortController.abort();
      this.connectAbortController = null;
    }

    // Clear any pending retry timer
    if (this.retryTimerId !== null) {
      clearTimeout(this.retryTimerId);
      this.retryTimerId = null;
    }

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
   * This is the durable cursor used for catch-up on reconnection.
   * Returns undefined if no events have been received yet.
   */
  getLastEventId(): string | undefined {
    return this.lastEventId;
  }

  /**
   * Immediately retry connection.
   * Only works when in 'reconnecting' state.
   * Resets the reconnect counter on manual retry.
   */
  retryNow(): void {
    if (this.destroyed) return;
    if (this.state !== "reconnecting") return;

    // Clear pending retry timer
    if (this.retryTimerId !== null) {
      clearTimeout(this.retryTimerId);
      this.retryTimerId = null;
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
   * After calling destroy(), all public methods become no-ops.
   */
  destroy(): void {
    if (this.destroyed) return;
    this.destroyed = true;

    // Abort any in-flight token fetch
    if (this.connectAbortController) {
      this.connectAbortController.abort();
      this.connectAbortController = null;
    }

    // Clear any pending retry timer
    if (this.retryTimerId !== null) {
      clearTimeout(this.retryTimerId);
      this.retryTimerId = null;
    }

    // Clear callbacks first to prevent any callbacks during cleanup
    this.onMutation = undefined;
    this.onError = undefined;
    this.onStateChange = undefined;
    this.onReconnect = undefined;
    this.onConnected = undefined;

    if (this.eventSource) {
      this.eventSource.close();
      this.eventSource = null;
    }
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

  private handleConnected(): void {
    this.onConnected?.();
  }

  private handleError(): void {
    // If manually disconnected, don't process error
    if (this.manualDisconnect) return;
    if (this.destroyed) return;

    // Unified manual reconnect: always close EventSource and schedule retry
    if (this.eventSource) {
      this.eventSource.close();
      this.eventSource = null;
    }

    this.scheduleReconnect();
  }

  private scheduleReconnect(): void {
    this.reconnectAttempts++;
    this.setState("reconnecting");
    this.onReconnect?.(this.reconnectAttempts);

    // Log warning after multiple failures
    if (this.reconnectAttempts === 5) {
      console.warn(
        "[SSE] Multiple connection failures, will continue retrying",
      );
    }

    // Exponential backoff: min(initialDelay * 2^(attempts-1), maxDelay)
    const delay = Math.min(
      this.initialReconnectDelay * Math.pow(2, this.reconnectAttempts - 1),
      this.maxReconnectDelay,
    );

    this.retryTimerId = setTimeout(() => {
      this.retryTimerId = null;
      if (
        !this.manualDisconnect &&
        !this.destroyed &&
        this.state === "reconnecting"
      ) {
        this.connect(undefined, this.currentSourceRepos);
      }
    }, delay);
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

    // Track the server-provided event ID for reconnection catch-up. Fleet-db
    // emits opaque durable cursors here, so preserve the value as-is.
    if (event.lastEventId) {
      this.lastEventId = event.lastEventId;
    }

    this.onMutation?.(mutation);
  }
}

/**
 * Get the SSE URL for the events endpoint.
 * @param workspaceId Workspace UUID for path-based routing
 * @param since Optional cursor for catch-up events
 */
export function getSSEUrl(
  workspaceId: string,
  since?: string | number,
  sourceRepos?: string[],
  opaqueToken?: string,
): string {
  const base = `${getApiOrigin()}${wsUrl(workspaceId, "/events")}`;
  const params = new URLSearchParams();
  if (since !== undefined) {
    params.set("since", String(since));
  }
  if (sourceRepos && sourceRepos.length > 0) {
    params.set("source_repos", sourceRepos.join(","));
  }
  if (opaqueToken) {
    params.set("token", opaqueToken);
  }
  const qs = params.toString();
  return qs ? `${base}?${qs}` : base;
}
