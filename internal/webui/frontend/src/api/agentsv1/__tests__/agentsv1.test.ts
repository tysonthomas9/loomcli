/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import {
  AgentEventStream,
  AgentHistory,
  archiveAgent,
  createAgent,
  respondToAsk,
  sendMessage,
  withdrawMessage,
  type AgentEvent,
} from "..";

// A fake loom serve: committed events per agent, ListEvents paged two at a
// time with the snapshot pin, the one-time token route, and a 410 for purged
// agents. Every request is recorded.
const committed: Record<string, AgentEvent[]> = {};
const purged = new Set<string>();
let requests: { method: string; url: URL; headers: Headers; body: unknown }[];
let tokens = 0;
// While set, ListEvents reads wait for it: a slow page.
let hold: Promise<void> | null = null;

function ev(agent: string, seq: number, kind = "item.completed"): AgentEvent {
  return {
    agent_id: agent,
    seq,
    event_id: `${agent}-e${seq}`,
    kind,
    turn_id: "t1",
    payload: {},
    created_at: "2026-10-01T00:00:00Z",
  };
}

function commit(agent: string, ...seqs: number[]) {
  (committed[agent] ??= []).push(...seqs.map((s) => ev(agent, s)));
}

function holdPages(): () => void {
  let release = () => {};
  hold = new Promise((r) => (release = r));
  return () => {
    hold = null;
    release();
  };
}

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

async function fakeFetch(input: string, init: RequestInit = {}) {
  const url = new URL(input, "http://localhost");
  const body = init.body ? JSON.parse(init.body as string) : undefined;
  requests.push({
    method: init.method ?? "GET",
    url,
    headers: new Headers(init.headers),
    body,
  });
  if (url.pathname.endsWith("/events/token")) {
    return json(200, { token: `tok${++tokens}` });
  }
  const m = url.pathname.match(/\/v1\/agents\/([^/]+)\/events$/);
  if (m) {
    const id = m[1]!;
    if (purged.has(id)) return json(410, { error: id, code: "cursor_expired" });
    const all = committed[id] ?? [];
    const after = Number(url.searchParams.get("after") ?? 0);
    const snap =
      Number(url.searchParams.get("snapshot") ?? 0) || (all.at(-1)?.seq ?? 0);
    const rest = all.filter((e) => e.seq > after && e.seq <= snap);
    const events = rest.slice(0, 2);
    if (hold) await hold; // the snapshot is pinned when the request arrives
    return json(200, {
      events,
      snapshot_seq: snap,
      next: events.at(-1)?.seq ?? after,
      more: rest.length > 2,
    });
  }
  return json(200, {});
}

class MockEventSource {
  static instances: MockEventSource[] = [];
  url: URL;
  closed = false;
  onopen: (() => void) | null = null;
  listeners = new Map<string, ((e: Event) => void)[]>();
  constructor(url: string) {
    this.url = new URL(url);
    MockEventSource.instances.push(this);
  }
  addEventListener(type: string, fn: (e: Event) => void) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), fn]);
  }
  close() {
    this.closed = true;
  }
  // A frame named `name` whose data is `data`, as the server writes it.
  send(name: string, data: unknown) {
    const e = new MessageEvent(name, { data: JSON.stringify(data) });
    this.listeners.get(name)?.forEach((fn) => fn(e));
  }
  // Saved events and notices all come as "event" frames.
  saved(e: AgentEvent) {
    this.send("event", e);
  }
  gap() {
    this.saved({ ...ev("", 0, "feed.gap"), event_id: "" });
  }
  drop() {
    this.listeners.get("error")?.forEach((fn) => fn(new Event("error")));
  }
}

const lastES = () => MockEventSource.instances.at(-1)!;
const seqs = (h: AgentHistory, a: string) => h.events(a).map((e) => e.seq);
const flush = () => vi.advanceTimersByTimeAsync(0);
const eventReads = () =>
  requests.filter(
    (r) =>
      r.url.pathname.endsWith("/events") && r.url.pathname.includes("/agents/"),
  );

beforeEach(() => {
  vi.useFakeTimers();
  for (const k of Object.keys(committed)) delete committed[k];
  purged.clear();
  requests = [];
  tokens = 0;
  hold = null;
  MockEventSource.instances = [];
  vi.stubGlobal("fetch", vi.fn(fakeFetch));
  vi.stubGlobal("EventSource", MockEventSource);
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("AgentHistory", () => {
  it("merges by EventID in seq order and drops repeats", () => {
    const h = new AgentHistory();
    expect(h.merge([ev("a", 1), ev("a", 3)])).toHaveLength(2);
    expect(
      h.merge([ev("a", 3), ev("a", 2), ev("b", 1)]).map((e) => e.event_id),
    ).toEqual(["a-e2", "b-e1"]);
    expect(seqs(h, "a")).toEqual([1, 2, 3]);
    expect(h.lastSeq("a")).toBe(3);
    expect(h.lastSeq("none")).toBe(0);
  });
});

describe("AgentEventStream", () => {
  function open(agents = ["a1", "a2"], extra = {}) {
    const onEvents = vi.fn();
    const onError = vi.fn();
    const onResync = vi.fn();
    const s = new AgentEventStream("ws1", {
      agents,
      deltas: true,
      onEvents,
      onError,
      onResync,
      ...extra,
    });
    return { s, onEvents, onError, onResync };
  }
  const delivered = (fn: ReturnType<typeof vi.fn>) =>
    fn.mock.calls.flatMap((c) => (c[0] as AgentEvent[]).map((e) => e.event_id));

  it("opens the stream with a token, then pages committed history into the cache", async () => {
    commit("a1", 1, 2, 3, 4, 5);
    const { s, onResync } = open();
    await s.connect();
    expect(seqs(s.history, "a1")).toEqual([1, 2, 3, 4, 5]);
    // Three pages for a1, all pinned at the first page's snapshot.
    const a1Reads = eventReads().filter((r) => r.url.pathname.includes("/a1/"));
    expect(a1Reads.map((r) => r.url.searchParams.get("snapshot"))).toEqual([
      null,
      "5",
      "5",
    ]);
    const u = lastES().url;
    expect(u.pathname).toBe("/api/workspaces/ws1/v1/events");
    expect(u.searchParams.get("agents")).toBe("a1,a2");
    expect(u.searchParams.get("after")).toBe("a1:0,a2:0");
    expect(u.searchParams.get("deltas")).toBe("true");
    expect(u.searchParams.get("token")).toBe("tok1");
    expect(onResync).toHaveBeenCalledTimes(1);
    s.close();
  });

  it("live mode opens at the cache's cursors, never pages, and resyncs on connect and feed.gap", async () => {
    commit("a1", 1, 2);
    const { s, onEvents, onResync } = open(["a1", "a2", "gone"], {
      live: true,
      expired: ["gone"],
    });
    s.history.merge([ev("a1", 2)]);
    await s.connect();
    const es = lastES();
    // The server replays after each cursor; a purged agent has none.
    expect(es.url.searchParams.get("after")).toBe("a1:2,a2:0");
    es.saved(ev("a1", 3));
    es.gap();
    await flush();
    expect(eventReads()).toHaveLength(0);
    expect(delivered(onEvents)).toEqual(["a1-e3"]);
    expect(onResync).toHaveBeenCalledTimes(2);
    s.close();
  });

  it("after feed.gap pages on the open stream and merges without gaps or duplicates", async () => {
    commit("a1", 1, 2);
    const { s, onEvents, onResync } = open();
    await s.connect();
    const es = lastES();
    commit("a1", 3, 4, 5, 6);
    es.saved(ev("a1", 3));
    es.saved(ev("a1", 3)); // a repeat on the wire
    // 4..6 committed but missed live; the gap pages while the stream stays open.
    es.gap();
    await flush();
    expect(es.closed).toBe(false);
    expect(MockEventSource.instances).toHaveLength(1);
    es.saved(ev("a1", 5)); // late live copy of a paged event
    commit("a1", 7);
    es.saved(ev("a1", 7));
    expect(seqs(s.history, "a1")).toEqual([1, 2, 3, 4, 5, 6, 7]);
    expect(delivered(onEvents)).toEqual(
      [1, 2, 3, 4, 5, 6, 7].map((n) => `a1-e${n}`),
    );
    const gapRead = eventReads()
      .filter((r) => r.url.pathname.includes("/a1/"))
      .at(-2)!;
    expect(gapRead.url.searchParams.get("after")).toBe("3");
    expect(onResync).toHaveBeenCalledTimes(2);
    s.close();
  });

  it("reconnects after a drop with a fresh token from the merged cursor", async () => {
    commit("a1", 1);
    const { s, onEvents } = open(["a1"]);
    await s.connect();
    const first = lastES();
    commit("a1", 2, 3, 4);
    first.saved(ev("a1", 2));
    first.drop();
    expect(first.closed).toBe(true);
    await vi.advanceTimersByTimeAsync(1000);
    const second = lastES();
    expect(second).not.toBe(first);
    expect(second.url.searchParams.get("token")).toBe("tok2");
    expect(second.url.searchParams.get("after")).toBe("a1:2");
    second.saved(ev("a1", 3)); // server replay overlapping the page
    second.saved(ev("a1", 4));
    expect(seqs(s.history, "a1")).toEqual([1, 2, 3, 4]);
    expect(delivered(onEvents)).toHaveLength(4);
    s.close();
  });

  it("treats the error frame (subscriber_lagged) as a reconnect, but a saved error event as an event", async () => {
    const { s, onError } = open(["a1"]);
    await s.connect();
    const es = lastES();
    es.saved(ev("a1", 1, "error"));
    expect(seqs(s.history, "a1")).toEqual([1]);
    expect(onError).not.toHaveBeenCalled();
    es.send("error", { error: "slow", code: "subscriber_lagged" });
    expect(onError).toHaveBeenCalledWith("subscriber_lagged");
    expect(es.closed).toBe(true);
    await vi.advanceTimersByTimeAsync(1000);
    expect(lastES().url.searchParams.get("after")).toBe("a1:1");
    s.close();
  });

  it("follows an agent with purged history live-only", async () => {
    purged.add("a2");
    commit("a1", 1);
    const { s } = open();
    await s.connect();
    // The server refuses a cursor into purged history; the retry leaves it out.
    lastES().drop();
    await vi.advanceTimersByTimeAsync(1000);
    expect(lastES().url.searchParams.get("after")).toBe("a1:1");
    s.close();
  });

  it("delivers any saved kind, live, after a reconnect and after feed.gap", async () => {
    const commitKind = (seq: number, kind: string) =>
      committed["a1"]!.push(ev("a1", seq, kind));
    commit("a1", 1);
    const { s, onEvents } = open(["a1"]);
    await s.connect();
    let es = lastES();
    // Live: a kind no client list knows, then a known one.
    commitKind(2, "custom.kind");
    commit("a1", 3);
    es.saved(ev("a1", 2, "custom.kind"));
    es.saved(ev("a1", 3));
    // Missed while the stream is down: recovered by the reconnect's paging.
    es.drop();
    commitKind(4, "custom.kind");
    commit("a1", 5);
    await vi.advanceTimersByTimeAsync(1000);
    es = lastES();
    expect(es.url.searchParams.get("after")).toBe("a1:3");
    // Missed live, then a feed.gap: recovered by the gap's paging.
    commitKind(6, "custom.kind");
    commit("a1", 7);
    es.gap();
    await flush();
    expect(seqs(s.history, "a1")).toEqual([1, 2, 3, 4, 5, 6, 7]);
    expect(s.history.events("a1")[1]!.kind).toBe("custom.kind");
    expect(delivered(onEvents)).toEqual(
      [1, 2, 3, 4, 5, 6, 7].map((n) => `a1-e${n}`),
    );
    s.close();
  });

  it("treats types: [] as all kinds", async () => {
    const { s } = open(["a1"], { types: [] });
    await s.connect();
    const es = lastES();
    expect(es.url.searchParams.has("types")).toBe(false);
    es.saved(ev("a1", 1, "custom.kind"));
    expect(seqs(s.history, "a1")).toEqual([1]);
    s.close();
  });

  it("buffers the open stream while a feed.gap page is in flight, keeping seq order", async () => {
    commit("a1", 1);
    const { s, onEvents, onResync } = open(["a1"]);
    await s.connect();
    commit("a1", 2, 3);
    const release = holdPages();
    const es = lastES();
    es.gap(); // its page is pinned at seq 3
    await flush();
    commit("a1", 4); // missed live
    es.gap(); // a second gap while paging: one more page, one resync
    commit("a1", 5);
    es.saved(ev("a1", 5)); // newer, arrives before the pages
    expect(seqs(s.history, "a1")).toEqual([1]);
    release();
    await flush();
    expect(delivered(onEvents)).toEqual([1, 2, 3, 4, 5].map((n) => `a1-e${n}`));
    expect(onResync).toHaveBeenCalledTimes(2);
    s.close();
  });

  it("pages no more after a close from onEvents while another gap waits", async () => {
    commit("a1", 1);
    const onResync = vi.fn();
    const onEvents = vi.fn((events: AgentEvent[]) => {
      if (events.some((e) => e.seq === 2)) s.close();
    });
    const s = new AgentEventStream("ws1", {
      agents: ["a1"],
      onEvents,
      onResync,
    });
    await s.connect();
    commit("a1", 2);
    const release = holdPages();
    const es = lastES();
    es.gap();
    await flush();
    commit("a1", 3); // missed live
    es.gap();
    const reads = eventReads().length;
    release();
    await flush();
    expect(eventReads()).toHaveLength(reads);
    expect(seqs(s.history, "a1")).toEqual([1, 2]);
    expect(onResync).toHaveBeenCalledTimes(1);
  });

  it("makes no callbacks after a close during a pending page", async () => {
    commit("a1", 1, 2);
    const { s, onEvents, onResync } = open(["a1"]);
    const release = holdPages();
    const done = s.connect();
    await flush();
    s.close();
    expect(lastES().closed).toBe(true);
    lastES().saved(ev("a1", 3));
    release();
    await done;
    expect(onEvents).not.toHaveBeenCalled();
    expect(onResync).not.toHaveBeenCalled();
    expect(seqs(s.history, "a1")).toEqual([]);
  });

  it.each([
    ["a paged event", 2, 2],
    ["a buffered event", 3, 3],
  ])(
    "makes no callbacks after a close from inside onEvents on %s",
    async (_, closeAt, lastCall) => {
      commit("a1", 1);
      const onResync = vi.fn();
      const onEvents = vi.fn((events: AgentEvent[]) => {
        if (events.some((e) => e.seq === closeAt)) s.close();
      });
      const s = new AgentEventStream("ws1", {
        agents: ["a1"],
        onEvents,
        onResync,
      });
      await s.connect();
      commit("a1", 2);
      const release = holdPages();
      lastES().gap();
      await flush();
      lastES().saved(ev("a1", 3)); // buffered behind the page
      release();
      await flush();
      expect(onResync).toHaveBeenCalledTimes(1); // the first connect only
      const last = onEvents.mock.calls.at(-1)![0] as AgentEvent[];
      expect(last.at(-1)!.seq).toBe(lastCall);
      expect(seqs(s.history, "a1").at(-1)).toBe(lastCall);
    },
  );

  it("stops reconnecting after close", async () => {
    const { s } = open(["a1"]);
    await s.connect();
    lastES().drop();
    s.close();
    await vi.advanceTimersByTimeAsync(60000);
    expect(MockEventSource.instances).toHaveLength(1);
  });
});

describe("REST writes", () => {
  it("send the RequestID as Idempotency-Key and never an actor", async () => {
    await createAgent("ws1", { preset: "lead", first_message: "hi" }, "r1");
    await sendMessage("ws1", "a1", "hello", "r2");
    await withdrawMessage("ws1", "a1", "r3");
    await respondToAsk("ws1", "a1", "k1", { decision: "allow_once" }, "r4");
    await archiveAgent("ws1", "a1", "r5");
    expect(
      requests.map((r) => [
        r.method,
        r.url.pathname,
        r.headers.get("Idempotency-Key"),
      ]),
    ).toEqual([
      ["POST", "/api/workspaces/ws1/v1/agents", "r1"],
      ["POST", "/api/workspaces/ws1/v1/agents/a1/messages", "r2"],
      ["DELETE", "/api/workspaces/ws1/v1/agents/a1/messages/waiting", "r3"],
      ["POST", "/api/workspaces/ws1/v1/agents/a1/asks/k1", "r4"],
      ["POST", "/api/workspaces/ws1/v1/agents/a1/archive", "r5"],
    ]);
    for (const r of requests) {
      expect(JSON.stringify(r.body ?? {})).not.toMatch(
        /actor|created_by|owner/,
      );
    }
  });
});
