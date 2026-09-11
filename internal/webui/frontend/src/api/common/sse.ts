/**
 * SSE (Server-Sent Events) client for real-time mutation events from the workspace event server.
 * Provides injectable token exchange, typed event callbacks, and library-managed reconnect
 * with configurable exponential backoff.
 */

import {
  EventStreamContentType,
  fetchEventSource,
  type EventSourceMessage,
} from "@microsoft/fetch-event-source";

import { get, ApiError, wsUrl, getApiOrigin } from "./client";

// SSE token exchange: fetch opaque token to avoid exposing JWT in URL
export type SseTokenResult =
  | { kind: "token"; token: string }
  | { kind: "disabled" }
  | { kind: "error"; message: string; status?: number };

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
    return {
      kind: "error",
      message,
      ...(err instanceof ApiError ? { status: err.status } : {}),
    };
  }
}

// Connection states for real-time event streaming
export type ConnectionState =
  | "disconnected"
  | "connecting"
  | "connected"
  | "reconnecting";

/** Server-declared reason that a contiguous mutation stream cannot be delivered. */
export type ResyncReason = "cap" | "error" | "expired" | "overflow";

/** Cursor transition carried by a resync frame. */
export interface ResyncEvent {
  from: string | undefined;
  to: string;
  reason: ResyncReason;
}

// Mutation types: definitions live in src/types/workspace/mutation.ts (the canonical
// source per the Phase 7 frontend layer DAG). Re-exported here so existing
// code that imports them from @/api/sse continues to compile.
import type { MutationPayload } from "@/types/workspace";
export type {
  MutationEntityType,
  MutationType,
  MutationPayload,
} from "@/types/workspace";

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
  /** Called when the server advances the cursor across a range requiring snapshot refetch. */
  onResync?: (event: ResyncEvent) => void;
  /** Injectable token provider. Default: fetchSseToken(workspaceId) */
  fetchToken?: () => Promise<SseTokenResult>;
  /** Starting backoff delay in ms for reconnect (default 1000) */
  initialReconnectDelay?: number;
  /** Maximum backoff delay in ms for reconnect (default 30000) */
  maxReconnectDelay?: number;
}

class FatalSSEError extends Error {}

const FATAL_TOKEN_STATUSES = new Set([401, 403]);

/**
 * SSE client for workspace mutation events.
 * Uses fetch-event-source retry handling with configurable exponential backoff.
 */
export class WorkspaceSSEClient {
  private state: ConnectionState = "disconnected";
  private reconnectAttempts = 0;
  private lastEventId: string | undefined;
  private resumeHeaders: HeadersInit | undefined;
  private manualDisconnect = false;
  private currentSourceRepos?: string[] | undefined;
  private workspaceId: string;
  private destroyed = false;
  private connectAbortController: AbortController | null = null;
  private connectionGeneration = 0;
  private connectedFrameSeenForOpen = false;

  private onMutation: ((mutation: MutationPayload) => void) | undefined;
  private onError: ((error: string) => void) | undefined;
  private onStateChange: ((state: ConnectionState) => void) | undefined;
  private onReconnect: ((attempt: number) => void) | undefined;
  private onConnected: (() => void) | undefined;
  private onResync: ((event: ResyncEvent) => void) | undefined;
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
    this.onResync = options.onResync;
    this.fetchTokenFn =
      options.fetchToken ?? (() => fetchSseToken(this.workspaceId));
    this.initialReconnectDelay = options.initialReconnectDelay ?? 1000;
    this.maxReconnectDelay = options.maxReconnectDelay ?? 30000;
  }

  /**
   * Connect to the SSE endpoint.
   * Starts the retry loop and resolves once that loop has been started; it does
   * not wait for the token exchange or an open connection.
   * @param since Optional cursor to receive events after
   * @param sourceRepos Optional repo filter for server-side event filtering.
   * When omitted, the previously configured filter is retained.
   */
  async connect(
    since?: string | number,
    sourceRepos?: string[],
  ): Promise<void> {
    if (this.destroyed) return;

    if (sourceRepos !== undefined) {
      this.currentSourceRepos = sourceRepos;
    }

    if (this.state === "connected" || this.state === "connecting") {
      return;
    }

    this.lastEventId =
      since !== undefined ? String(since) : this.getLastEventId();
    this.resumeHeaders = undefined;

    this.manualDisconnect = false;
    this.setState("connecting");

    // A direct connect while reconnecting replaces the library-owned retry loop.
    this.connectAbortController?.abort();
    const abortController = new AbortController();
    this.connectAbortController = abortController;
    const generation = ++this.connectionGeneration;
    const sinceParam = since ?? this.lastEventId;
    const url = getSSEUrl(
      this.workspaceId,
      sinceParam,
      this.currentSourceRepos,
    );
    let streamAttempt = 0;

    void fetchEventSource(url, {
      signal: abortController.signal,
      // The library owns resume state from the first attempt, including token
      // and HTTP failures before any wire ID, and explicit empty-ID resets.
      headers:
        sinceParam === undefined ? {} : { "last-event-id": String(sinceParam) },
      openWhenHidden: true,
      fetch: (input, init) =>
        this.fetchStream(
          input,
          init,
          abortController,
          generation,
          streamAttempt++ > 0,
        ),
      onopen: (response): Promise<void> => {
        if (!this.isActive(abortController, generation)) {
          return Promise.resolve();
        }
        const contentType = response.headers.get("content-type");
        if (!response.ok) {
          throw new Error(`SSE stream request failed: ${response.status}`);
        }
        if (!contentType?.startsWith(EventStreamContentType)) {
          throw new Error(
            `Expected content-type to be ${EventStreamContentType}, Actual: ${contentType}`,
          );
        }
        this.handleOpen(abortController, generation);
        return Promise.resolve();
      },
      onmessage: (event) => {
        if (this.isActive(abortController, generation)) {
          this.handleMessage(event);
        }
      },
      onclose: () => {
        if (this.isActive(abortController, generation)) {
          throw new Error("SSE stream closed");
        }
      },
      // Intentionally return our exponential delay from every error. This
      // overrides the server's `retry:` directive so token, HTTP, and stream
      // failures all use one capped reconnect policy.
      onerror: (error) => this.handleError(error, abortController, generation),
    }).catch((error: unknown) => {
      // Fatal errors are reported in handleError. Abort resolves normally, and
      // this final guard prevents a stale connection from changing new state.
      if (
        this.isActive(abortController, generation) &&
        !(error instanceof FatalSSEError)
      ) {
        const message =
          error instanceof Error ? error.message : "Unknown error";
        this.callSafely(
          "onError",
          this.onError,
          `SSE connection failed: ${message}`,
        );
        this.setState("disconnected");
      }
    });
  }

  /**
   * Replace the saved source-repository filter and reconnect from the cursor
   * already held by this client. Unlike `connect(undefined, undefined)`, which
   * retains the saved filter, `updateSourceRepos(undefined)` explicitly clears
   * it and reconnects without source-repository scoping.
   */
  updateSourceRepos(sourceRepos: string[] | undefined): void {
    if (this.destroyed) return;

    this.currentSourceRepos = sourceRepos;
    this.disconnect();
    void this.connect();
  }

  /**
   * Disconnect from the SSE endpoint.
   */
  disconnect(): void {
    if (this.destroyed) return;

    this.lastEventId = this.getLastEventId();
    this.resumeHeaders = undefined;
    this.manualDisconnect = true;

    this.connectionGeneration++;
    this.connectAbortController?.abort();
    this.connectAbortController = null;

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
   * Get the observed transport checkpoint, including ID-only frames and resets.
   * This is resume state, not an acknowledgment that query state was applied.
   * Returns undefined if no checkpoint is held or the server explicitly reset it.
   */
  getLastEventId(): string | undefined {
    return this.resumeHeaders === undefined
      ? this.lastEventId
      : new Headers(this.resumeHeaders).get("last-event-id") || undefined;
  }

  /**
   * Immediately retry connection.
   * Only works when in 'reconnecting' state.
   * Resets the reconnect counter on manual retry.
   */
  retryNow(): void {
    if (this.destroyed) return;
    if (this.state !== "reconnecting") return;

    this.connectAbortController?.abort();
    this.connectAbortController = null;
    this.reconnectAttempts = 0;
    this.callSafely("onReconnect", this.onReconnect, 0);
    void this.connect(undefined, this.currentSourceRepos);
  }

  /**
   * Disconnect and clean up all resources.
   * After calling destroy(), all public methods become no-ops.
   */
  destroy(): void {
    if (this.destroyed) return;
    this.destroyed = true;

    this.manualDisconnect = true;
    this.connectionGeneration++;
    this.connectAbortController?.abort();
    this.connectAbortController = null;

    // Clear callbacks first to prevent any callbacks during cleanup
    this.onMutation = undefined;
    this.onError = undefined;
    this.onStateChange = undefined;
    this.onReconnect = undefined;
    this.onConnected = undefined;
    this.onResync = undefined;
  }

  private setState(state: ConnectionState): void {
    if (this.state !== state) {
      this.state = state;
      this.callSafely("onStateChange", this.onStateChange, state);
    }
  }

  private handleOpen(
    abortController: AbortController,
    generation: number,
  ): void {
    const wasReconnecting = this.reconnectAttempts > 0;
    this.connectedFrameSeenForOpen = false;
    this.setState("connected");
    this.throwIfInactive(abortController, generation);
    this.reconnectAttempts = 0;
    // Only notify about reconnect counter reset if we were actually reconnecting
    if (wasReconnecting) {
      this.callSafely("onReconnect", this.onReconnect, 0);
    }
  }

  private handleError(
    error: unknown,
    abortController: AbortController,
    generation: number,
  ): number {
    if (!this.isActive(abortController, generation)) {
      throw new FatalSSEError("SSE connection superseded");
    }
    if (error instanceof FatalSSEError) {
      this.callSafely("onError", this.onError, error.message);
      this.setState("disconnected");
      throw error;
    }

    this.reconnectAttempts++;
    this.setState("reconnecting");
    this.throwIfInactive(abortController, generation);
    this.callSafely("onReconnect", this.onReconnect, this.reconnectAttempts);
    // A callback may synchronously call retryNow(), which aborts this loop and
    // starts a new generation. Throwing here prevents fetch-event-source from
    // scheduling the superseded loop's timer.
    this.throwIfInactive(abortController, generation);

    // Log warning after multiple failures
    if (this.reconnectAttempts === 5) {
      console.warn(
        "[SSE] Multiple connection failures, will continue retrying",
      );
    }

    // Exponential backoff: min(initialDelay * 2^(attempts-1), maxDelay)
    return Math.min(
      this.initialReconnectDelay * Math.pow(2, this.reconnectAttempts - 1),
      this.maxReconnectDelay,
    );
  }

  private handleMessage(event: EventSourceMessage): void {
    const previousEventId = this.lastEventId;
    this.lastEventId = this.getLastEventId();
    if (event.event === "resync") {
      let reason: ResyncReason = "error";
      try {
        const parsed = JSON.parse(event.data) as { reason?: unknown };
        if (
          parsed.reason !== "cap" &&
          parsed.reason !== "error" &&
          parsed.reason !== "expired" &&
          parsed.reason !== "overflow"
        ) {
          throw new Error("invalid resync reason");
        }
        reason = parsed.reason;
      } catch {
        console.warn("[SSE] Received malformed resync event");
      }

      this.callSafely("onResync", this.onResync, {
        from: previousEventId,
        to: event.id,
        reason,
      });
      return;
    }
    if (event.event === "connected") {
      if (this.connectedFrameSeenForOpen) return;
      this.connectedFrameSeenForOpen = true;
      this.callSafely("onConnected", this.onConnected);
      return;
    }
    if (event.event !== "mutation") return;

    let mutation: MutationPayload;
    try {
      mutation = JSON.parse(event.data) as MutationPayload;
    } catch {
      // The parser has already advanced its transport header. Stop this loop
      // and retain the prior checkpoint so reconnect cannot silently skip it.
      this.lastEventId = previousEventId;
      this.resumeHeaders = undefined;
      console.warn("[SSE] Received malformed mutation event");
      throw new FatalSSEError("Malformed SSE mutation payload");
    }

    this.callSafely("onMutation", this.onMutation, mutation);
  }

  private async fetchStream(
    input: RequestInfo | URL,
    init: RequestInit | undefined,
    abortController: AbortController,
    generation: number,
    isRetry: boolean,
  ): Promise<Response> {
    this.throwIfInactive(abortController, generation);
    // fetch-event-source 2.x passes its live headers object to custom fetch.
    // Its parser updates/deletes last-event-id on explicit ID fields, while
    // onmessage.id cannot distinguish an absent ID from an empty reset. Keep
    // this reference (not a Headers copy), including during token failures.
    this.resumeHeaders = init?.headers;
    let tokenResult: SseTokenResult;
    try {
      tokenResult = await this.fetchTokenFn();
    } catch (error) {
      const message = error instanceof Error ? error.message : "Unknown error";
      tokenResult = {
        kind: "error",
        message,
        ...(error instanceof ApiError ? { status: error.status } : {}),
      };
    }

    if (!this.isActive(abortController, generation)) {
      throw new FatalSSEError("SSE connection superseded");
    }
    if (tokenResult.kind === "error" && tokenResult.status !== 404) {
      const message = `SSE auth failed: ${tokenResult.message}`;
      if (
        tokenResult.status !== undefined &&
        FATAL_TOKEN_STATUSES.has(tokenResult.status)
      ) {
        throw new FatalSSEError(message);
      }
      throw new Error(message);
    }

    const requestUrl = new URL(
      input instanceof Request ? input.url : String(input),
      window.location.href,
    );
    requestUrl.searchParams.delete("token");

    // The input URL is reused by fetch-event-source. `since` belongs only to
    // the first request; retries use the library's seeded Last-Event-ID header,
    // which may also have been explicitly cleared by an empty `id:` line.
    if (isRetry) {
      requestUrl.searchParams.delete("since");
    }
    if (tokenResult.kind === "token") {
      requestUrl.searchParams.set("token", tokenResult.token);
    }

    return window.fetch(requestUrl, init);
  }

  private isActive(
    abortController: AbortController,
    generation: number,
  ): boolean {
    return (
      !abortController.signal.aborted &&
      !this.destroyed &&
      !this.manualDisconnect &&
      generation === this.connectionGeneration
    );
  }

  private throwIfInactive(
    abortController: AbortController,
    generation: number,
  ): void {
    if (!this.isActive(abortController, generation)) {
      throw new FatalSSEError("SSE connection superseded");
    }
  }

  private callSafely<Args extends unknown[]>(
    name: string,
    callback: ((...args: Args) => void) | undefined,
    ...args: Args
  ): void {
    if (!callback) return;
    try {
      callback(...args);
    } catch (error) {
      console.error(`[SSE] ${name} callback threw:`, error);
    }
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
