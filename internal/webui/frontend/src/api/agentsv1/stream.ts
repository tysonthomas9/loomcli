// The Agent API event stream (design v2 §9.1, §9.5). Each connect fetches a
// fresh one-time token, pages committed history after the cache's cursor,
// then opens the SSE stream with after=<agent>:<seq> so the server replays
// whatever was committed in between. A dropped stream, an error frame
// (subscriber_lagged) or a feed.gap repeats the paging; everything is merged
// by EventID, so nothing is missed or shown twice.

import { ApiError, getApiOrigin, wsUrl } from "../common/client";
import { fetchSseToken } from "../common/sse";
import type { ConnectionState, SseTokenResult } from "../common/sse";
import { AgentHistory } from "./history";
import { listEventsAfter } from "./rest";
import type { AgentApiErrorBody, AgentEvent } from "./types";

/** Saved event kinds the stream listens for when no types are given. */
export const AGENT_EVENT_KINDS = [
  "agent.created",
  "agent.updated",
  "agent.state_changed",
  "agent.idle",
  "agent.settled",
  "agent.turn_completed",
  "agent.archived",
  "agent.deleted",
  "attention.raised",
  "attention.cleared",
  "harness.changed",
  "harness.subagent.started",
  "message.waiting",
  "message.withdrawn",
  "message.delivered",
  "turn.started",
  "turn.resumed",
  "turn.completed",
  "item.started",
  "item.completed",
  "usage",
  "ask.opened",
  "ask.resolved",
  "ask.lost",
  "error",
];

export interface AgentStreamOptions {
  agents: string[];
  /** Saved kinds to receive; all known kinds when omitted. */
  types?: string[];
  deltas?: boolean;
  /** The cache to merge into; a new one when omitted. */
  history?: AgentHistory;
  /** Newly merged saved events, from paging or the stream. */
  onEvents?: (events: AgentEvent[]) => void;
  /** Live-only notices: delta and feed.gap (seq 0). */
  onNotice?: (notice: AgentEvent) => void;
  /** After each catch-up; refresh the agent list and open agents (§9.5 step 4). */
  onResync?: () => void;
  onStateChange?: (state: ConnectionState) => void;
  onError?: (message: string) => void;
  fetchToken?: () => Promise<SseTokenResult>;
  initialReconnectDelay?: number;
  maxReconnectDelay?: number;
}

export class AgentEventStream {
  readonly history: AgentHistory;
  private es: EventSource | null = null;
  private state: ConnectionState = "disconnected";
  private attempts = 0;
  private gen = 0;
  private timer: ReturnType<typeof setTimeout> | null = null;
  private expired = new Set<string>();

  constructor(
    private ws: string,
    private opts: AgentStreamOptions,
  ) {
    this.history = opts.history ?? new AgentHistory();
  }

  /** Pages history, then opens the stream; reconnects until close(). */
  async connect(): Promise<void> {
    const gen = ++this.gen;
    this.closeSource();
    this.setState(this.attempts > 0 ? "reconnecting" : "connecting");
    let token: SseTokenResult;
    try {
      await this.catchUp();
      token = await (this.opts.fetchToken ?? (() => fetchSseToken(this.ws)))();
      if (token.kind === "error") throw new Error(token.message);
    } catch (err) {
      if (gen !== this.gen) return;
      this.opts.onError?.(err instanceof Error ? err.message : String(err));
      return this.reconnect();
    }
    if (gen !== this.gen) return;
    const es = new EventSource(this.url(token));
    this.es = es;
    es.onopen = () => {
      this.attempts = 0;
      this.setState("connected");
    };
    const kinds = this.opts.types ?? AGENT_EVENT_KINDS;
    for (const kind of new Set([...kinds, "error", "delta", "feed.gap"])) {
      es.addEventListener(kind, (e) => this.frame(es, e));
    }
  }

  /** Closes the stream for good. */
  close(): void {
    this.gen++;
    this.closeSource();
    this.setState("disconnected");
  }

  private url(token: SseTokenResult): string {
    const { agents, types, deltas } = this.opts;
    const p = new URLSearchParams({ agents: agents.join(",") });
    const after = agents
      .filter((a) => !this.expired.has(a))
      .map((a) => `${a}:${this.history.lastSeq(a)}`);
    if (after.length) p.set("after", after.join(","));
    if (types) p.set("types", types.join(","));
    if (deltas) p.set("deltas", "true");
    if (token.kind === "token") p.set("token", token.token);
    return `${getApiOrigin()}${wsUrl(this.ws, "/v1/events")}?${p}`;
  }

  // Pages every agent's committed events after its cursor into the cache. An
  // agent whose history is purged (410) is followed live-only from then on.
  private async catchUp(): Promise<void> {
    await Promise.all(
      this.opts.agents
        .filter((a) => !this.expired.has(a))
        .map(async (a) => {
          try {
            const events = await listEventsAfter(
              this.ws,
              a,
              this.history.lastSeq(a),
              this.opts.types,
            );
            this.emit(events);
          } catch (err) {
            if (!(err instanceof ApiError && err.status === 410)) throw err;
            this.expired.add(a);
          }
        }),
    );
    this.opts.onResync?.();
  }

  // One SSE frame. A native connection error has no data; a saved event has
  // seq > 0; a notice has seq 0; anything else is the server's error frame.
  // The id field is not used: EventSource keeps the last id on frames without one.
  private frame(es: EventSource, e: Event): void {
    if (es !== this.es) return;
    const data = (e as MessageEvent).data;
    if (typeof data !== "string") return this.reconnect();
    let body: AgentEvent | AgentApiErrorBody;
    try {
      body = JSON.parse(data);
    } catch {
      return;
    }
    if (!("seq" in body)) {
      this.opts.onError?.(body.code || body.error);
      return this.reconnect();
    }
    if (body.seq > 0) return this.emit([body]);
    this.opts.onNotice?.(body);
    if (body.kind === "feed.gap") {
      this.catchUp().catch(() => es === this.es && this.reconnect());
    }
  }

  private emit(events: AgentEvent[]): void {
    const added = this.history.merge(events);
    if (added.length) this.opts.onEvents?.(added);
  }

  private reconnect(): void {
    const gen = ++this.gen;
    this.closeSource();
    this.attempts++;
    this.setState("reconnecting");
    const delay = Math.min(
      (this.opts.initialReconnectDelay ?? 1000) * 2 ** (this.attempts - 1),
      this.opts.maxReconnectDelay ?? 30000,
    );
    this.timer = setTimeout(() => {
      this.timer = null;
      if (gen === this.gen) void this.connect();
    }, delay);
  }

  private closeSource(): void {
    if (this.timer) clearTimeout(this.timer);
    this.timer = null;
    this.es?.close();
    this.es = null;
  }

  private setState(state: ConnectionState): void {
    if (this.state === state) return;
    this.state = state;
    this.opts.onStateChange?.(state);
  }
}
