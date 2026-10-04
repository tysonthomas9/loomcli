// The Agent API event stream (design v2 §9.1, §9.5). Every connect, and every
// reconnect after a drop or an error frame (subscriber_lagged), fetches a
// fresh one-time token and reopens the SSE stream with after=<agent>:<seq>
// from the cache's cursors, then syncs. A feed.gap syncs on the open stream.
// A sync buffers the stream's saved events, pages each agent's committed
// history after its cursor, merges the pages and then the buffer by EventID
// in seq order, and calls onResync so the UI refreshes List and Get.
// Nothing is missed or shown twice, and onEvents stays in seq order.

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
  /**
   * No paging: the stream opens with each agent's cursor (its last held seq,
   * 0 for none), so the server replays whatever came after it, and a
   * reconnect or feed.gap just calls onResync, whose List is the catch-up
   * (the agent roster). The cursors cover a change made after that List but
   * before the server registered the stream (RR1).
   */
  live?: boolean;
  /** Agents whose history is purged: followed with no cursor, which the server refuses. */
  expired?: readonly string[];
  /** The cache to merge into; a new one when omitted. */
  history?: AgentHistory;
  /** Newly merged saved events, from paging or the stream. */
  onEvents?: (events: AgentEvent[]) => void;
  /** Live-only notices: delta, tool.started and feed.gap (seq 0). */
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
  // Bumped by every connect, reconnect and close: work started under an
  // older gen, including after a user callback that closed, does nothing.
  private gen = 0;
  private timer: ReturnType<typeof setTimeout> | null = null;
  private expired = new Set<string>();
  private syncing = false; // paging; the stream's saved events are buffered
  private again = false; // a feed.gap came while paging: page once more
  private buffer: AgentEvent[] = [];

  constructor(
    private ws: string,
    private opts: AgentStreamOptions,
  ) {
    this.history = opts.history ?? new AgentHistory();
    for (const a of opts.expired ?? []) this.expired.add(a);
  }

  /** Opens the stream and catches up; reconnects until close(). */
  async connect(): Promise<void> {
    const gen = ++this.gen;
    const live = () => gen === this.gen;
    this.closeSource();
    this.setState(this.attempts > 0 ? "reconnecting" : "connecting");
    if (!live()) return;
    try {
      const fetchToken = this.opts.fetchToken ?? (() => fetchSseToken(this.ws));
      const token = await fetchToken();
      if (!live()) return;
      if (token.kind === "error") throw new Error(token.message);
      this.open(token);
      await this.sync(gen);
    } catch (err) {
      if (!live()) return;
      this.opts.onError?.(err instanceof Error ? err.message : String(err));
      if (live()) this.reconnect();
    }
  }

  /** Closes the stream for good. */
  close(): void {
    this.gen++;
    this.closeSource();
    this.setState("disconnected");
  }

  private open(token: SseTokenResult): void {
    const es = new EventSource(this.url(token));
    this.es = es;
    es.onopen = () => {
      if (es !== this.es) return;
      this.attempts = 0;
      this.setState("connected");
    };
    // Every saved event and notice is one "event" frame with its kind in the
    // data; "error" is the server's error frame or a native connection error.
    es.addEventListener("event", (e) => this.frame(es, e));
    es.addEventListener("error", (e) => this.frame(es, e));
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

  // One sync at a time; a feed.gap during one makes it page once more. It
  // stops, with no further callbacks, once gen is stale (closed, reconnected
  // or closed from inside a callback).
  private async sync(gen: number): Promise<void> {
    const live = () => gen === this.gen;
    if (this.syncing) {
      this.again = true;
      return;
    }
    this.syncing = true;
    do {
      this.again = false;
      const pages = await this.page();
      if (!live()) return;
      this.emit(pages.flat());
      if (!live()) return;
    } while (this.again);
    this.syncing = false;
    const buffered = this.buffer.sort((a, b) => a.seq - b.seq);
    this.buffer = [];
    this.emit(buffered);
    if (live()) this.opts.onResync?.();
  }

  // Pages every agent's committed events after its cursor. An agent whose
  // history is purged (410) is followed live-only from then on; a stream
  // opened with its cursor fails, and the reconnect leaves the cursor out.
  private page(): Promise<AgentEvent[][]> {
    if (this.opts.live) return Promise.resolve([]);
    return Promise.all(
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
      if (es === this.es) this.reconnect();
      return;
    }
    if (body.seq > 0) {
      if (this.syncing) this.buffer.push(body);
      else this.emit([body]);
      return;
    }
    this.opts.onNotice?.(body);
    if (body.kind !== "feed.gap" || es !== this.es) return;
    const gen = this.gen;
    this.sync(gen).catch(() => gen === this.gen && this.reconnect());
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
    this.syncing = false;
    this.buffer = [];
  }

  private setState(state: ConnectionState): void {
    if (this.state === state) return;
    this.state = state;
    this.opts.onStateChange?.(state);
  }
}
