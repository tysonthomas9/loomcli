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

export interface AgentStreamOptions {
  agents: string[];
  /** Saved kinds to receive; all kinds when omitted or empty. */
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
  private syncing = false; // a catch-up is paging; live saved events wait
  private again = false; // another catch-up was asked for meanwhile
  private buffer: AgentEvent[] = [];

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
    this.syncing = false;
    this.buffer = [];
    this.setState(this.attempts > 0 ? "reconnecting" : "connecting");
    let token: SseTokenResult;
    try {
      await this.catchUp(gen);
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
    // Every saved event and notice is one "event" frame with its kind in the
    // data; "error" is the server's error frame or a native connection error.
    es.addEventListener("event", (e) => this.frame(es, e));
    es.addEventListener("error", (e) => this.frame(es, e));
  }

  /** Closes the stream for good. */
  close(): void {
    this.gen++;
    this.closeSource();
    this.setState("disconnected");
  }

  private url(token: SseTokenResult): string {
    const { agents, deltas } = this.opts;
    const types = this.filter();
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

  /** The saved kinds asked for; undefined (all kinds) for none or []. */
  private filter(): string[] | undefined {
    return this.opts.types?.length ? this.opts.types : undefined;
  }

  // Pages every agent's committed events after its cursor into the cache,
  // one catch-up at a time; live saved events that arrive meanwhile are
  // buffered and merged after it, so onEvents stays in seq order. A stale
  // catch-up (closed or reconnected since) emits nothing. An agent whose
  // history is purged (410) is followed live-only from then on.
  private async catchUp(gen: number): Promise<void> {
    if (this.syncing) {
      this.again = true;
      return;
    }
    this.syncing = true;
    try {
      do {
        this.again = false;
        const pages = await Promise.all(
          this.opts.agents
            .filter((a) => !this.expired.has(a))
            .map((a) =>
              listEventsAfter(
                this.ws,
                a,
                this.history.lastSeq(a),
                this.filter(),
              ).catch((err) => {
                if (!(err instanceof ApiError && err.status === 410)) throw err;
                this.expired.add(a);
                return [];
              }),
            ),
        );
        if (gen !== this.gen) return;
        this.emit(pages.flat());
        this.opts.onResync?.();
      } while (this.again);
    } finally {
      if (gen === this.gen) this.syncing = false;
    }
    const buffered = this.buffer.sort((a, b) => a.seq - b.seq);
    this.buffer = [];
    buffered.forEach((e) => this.saved(e));
  }

  private resync(): void {
    const { gen, es } = this;
    this.catchUp(gen).catch(
      () => gen === this.gen && es === this.es && this.reconnect(),
    );
  }

  // A saved event from the stream; held while a catch-up is paging.
  private saved(e: AgentEvent): void {
    if (this.syncing) this.buffer.push(e);
    else this.emit([e]);
  }

  // One SSE frame. A native connection error has no data; a saved event has
  // seq > 0; a notice has seq 0; the server's error frame has no seq. The id
  // field is not used: EventSource keeps the last id on frames without one.
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
    if (body.seq > 0) return this.saved(body);
    this.opts.onNotice?.(body);
    if (body.kind === "feed.gap") this.resync();
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
