/**
 * @vitest-environment jsdom
 */

// The live roster against a fake Agent API server whose SSE subscriptions
// register only when the test says so, as a real server's do some time after
// the browser opens the stream (RR1).

import { act, renderHook, waitFor } from "@testing-library/react";
import { flushSync } from "react-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Agent, AgentEvent } from "@/api/agentsv1";
import { useAgentRoster } from "../useAgentRoster";

const agents = new Map<string, Agent>();
const log = new Map<string, AgentEvent[]>();
let lists: URL[] = [];

function agent(id: string, over: Partial<Agent> = {}): Agent {
  return {
    agent_id: id,
    name: id,
    harness: "opencode",
    state: "idle",
    parent_agent_id: null,
    history_purged_at: null,
    created_at: `2026-10-04T00:00:0${agents.size}Z`,
    ...over,
  } as Agent;
}

class FakeEventSource {
  static all: FakeEventSource[] = [];
  url: URL;
  closed = false;
  registered = false;
  onopen: (() => void) | null = null;
  listeners = new Map<string, ((e: Event) => void)[]>();
  constructor(url: string) {
    this.url = new URL(url);
    FakeEventSource.all.push(this);
  }
  addEventListener(type: string, fn: (e: Event) => void) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), fn]);
  }
  close() {
    this.closed = true;
  }
  get agents(): string[] {
    return this.url.searchParams.get("agents")?.split(",") ?? [];
  }
  wants(e: AgentEvent): boolean {
    const types = this.url.searchParams.get("types")?.split(",");
    return (
      this.agents.includes(e.agent_id) && (!types || types.includes(e.kind))
    );
  }
  deliver(e: AgentEvent) {
    const m = new MessageEvent("event", { data: JSON.stringify(e) });
    this.listeners.get("event")?.forEach((fn) => fn(m));
  }
  // The server subscribes: it replays each cursor's later events, then the
  // stream is live. An agent without a cursor gets new events only.
  register() {
    this.registered = true;
    const after = new Map(
      (this.url.searchParams.get("after")?.split(",") ?? []).map((c) => {
        const i = c.lastIndexOf(":");
        return [c.slice(0, i), Number(c.slice(i + 1))] as const;
      }),
    );
    for (const [id, cursor] of after)
      for (const e of log.get(id) ?? [])
        if (e.seq > cursor && this.wants(e)) this.deliver(e);
  }
}

// Saves an event and sends it to every registered, open subscriber.
function commit(id: string, kind: string, payload: object = {}) {
  const events = log.get(id) ?? [];
  const e: AgentEvent = {
    agent_id: id,
    seq: events.length + 1,
    event_id: `${id}:${events.length + 1}`,
    kind,
    turn_id: "",
    payload,
    created_at: "",
  };
  log.set(id, [...events, e]);
  const p = payload as { to?: string };
  const a = agents.get(id);
  if (kind === "agent.state_changed" && a && p.to)
    agents.set(id, { ...a, state: p.to });
  for (const es of FakeEventSource.all)
    if (es.registered && !es.closed && es.wants(e)) act(() => es.deliver(e));
}

const json = (body: unknown) =>
  new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });

// While set, a full List reads the agents when it arrives but answers only
// once released: a slow response that is stale when it lands.
let holdFull: Promise<void> | null = null;
function holdFullLists(): () => void {
  let release = () => {};
  holdFull = new Promise((r) => (release = r));
  return () => {
    holdFull = null;
    release();
  };
}

// A workspace's own List answer, in place of the shared agents (RS1).
const otherWs = new Map<string, () => Promise<Response>>();

async function fakeFetch(input: string) {
  const url = new URL(input, "http://localhost");
  const ws = url.pathname.split("/")[3];
  if (url.pathname.endsWith("/v1/agents") && otherWs.has(ws))
    return otherWs.get(ws)!();
  if (url.pathname.endsWith("/events/token")) return json({ token: "tok" });
  if (url.pathname.endsWith("/v1/agents")) {
    lists.push(url);
    const parent = url.searchParams.get("parent");
    // Each row carries its last_seq, read with it, as the server's does.
    const body = {
      agents: [...agents.values()]
        .filter((a) => !parent || a.parent_agent_id === parent)
        .map((a) => ({ ...a, last_seq: log.get(a.agent_id)?.length ?? 0 })),
      next: "",
    };
    if (!parent && holdFull) await holdFull;
    return json(body);
  }
  return new Response("{}", { status: 404 });
}

const openStream = () => FakeEventSource.all.filter((s) => !s.closed).at(-1);

beforeEach(() => {
  agents.clear();
  log.clear();
  lists = [];
  holdFull = null;
  otherWs.clear();
  FakeEventSource.all = [];
  vi.stubGlobal("fetch", vi.fn(fakeFetch));
  vi.stubGlobal("EventSource", FakeEventSource);
});

afterEach(() => vi.unstubAllGlobals());

describe("useAgentRoster", () => {
  it("keeps a child's state change that lands after the re-list but before the new stream registers", async () => {
    agents.set("lead", agent("lead", { preset: "lead" }));
    commit("lead", "agent.state_changed", { from: "creating", to: "idle" });
    const { result } = renderHook(() => useAgentRoster("ws1"));
    await waitFor(() => expect(openStream()?.agents).toEqual(["lead"]));
    act(() => openStream()!.register());

    // The lead creates a child; the roster lists it (idle) and reopens the
    // stream to follow it too, then re-lists everything.
    const relists = () =>
      lists.filter((u) => !u.searchParams.get("parent")).length;
    const before = relists();
    agents.set("kid", agent("kid", { parent_agent_id: "lead" }));
    commit("kid", "agent.state_changed", { from: "creating", to: "idle" });
    commit("lead", "child.created", { child: "kid" });
    await waitFor(() => expect(openStream()?.agents).toEqual(["kid", "lead"]));
    const next = openStream()!;
    await waitFor(() => expect(relists()).toBeGreaterThan(before));
    await waitFor(() =>
      expect(result.current.roster.get("kid")?.state).toBe("idle"),
    );

    // The child starts work before the server has registered the stream.
    commit("kid", "agent.state_changed", { from: "idle", to: "active" });
    act(() => next.register());

    await waitFor(() =>
      expect(result.current.roster.get("kid")?.state).toBe("active"),
    );
  });

  it("does not let a List read before a replayed change undo it when it lands late", async () => {
    agents.set("lead", agent("lead", { preset: "lead" }));
    commit("lead", "agent.state_changed", { from: "creating", to: "idle" });
    const { result } = renderHook(() => useAgentRoster("ws1"));
    await waitFor(() => expect(openStream()?.agents).toEqual(["lead"]));
    act(() => openStream()!.register());
    await waitFor(() =>
      expect(result.current.roster.get("lead")).toBeDefined(),
    );

    // The child's reopened stream re-lists, and that List is slow: it reads
    // the child idle but answers only after the replay made it active.
    const release = holdFullLists();
    const relists = () =>
      lists.filter((u) => !u.searchParams.get("parent")).length;
    const before = relists();
    agents.set("kid", agent("kid", { parent_agent_id: "lead" }));
    commit("kid", "agent.state_changed", { from: "creating", to: "idle" });
    commit("lead", "child.created", { child: "kid" });
    await waitFor(() => expect(openStream()?.agents).toEqual(["kid", "lead"]));
    const next = openStream()!;
    await waitFor(() => expect(relists()).toBeGreaterThan(before));

    commit("kid", "agent.state_changed", { from: "idle", to: "active" });
    act(() => next.register());
    await waitFor(() =>
      expect(result.current.roster.get("kid")?.state).toBe("active"),
    );
    await act(async () => release());
    expect(result.current.roster.get("kid")?.state).toBe("active");
  });

  it("follows an agent with purged history without a cursor", async () => {
    agents.set("lead", agent("lead", { preset: "lead" }));
    agents.set(
      "old",
      agent("old", { history_purged_at: "2026-10-01T00:00:00Z" }),
    );
    commit("lead", "agent.state_changed", { from: "creating", to: "idle" });
    renderHook(() => useAgentRoster("ws1"));
    await waitFor(() => expect(openStream()?.agents).toEqual(["lead", "old"]));
    expect(openStream()!.url.searchParams.get("after")).toBe("lead:1");
  });

  it("opens each agent's stream at its listed last_seq, not at 0", async () => {
    agents.set("lead", agent("lead", { preset: "lead" }));
    for (let i = 0; i < 5; i++)
      commit("lead", "agent.state_changed", { from: "idle", to: "idle" });
    renderHook(() => useAgentRoster("ws1"));
    await waitFor(() => expect(openStream()?.agents).toEqual(["lead"]));
    expect(openStream()!.url.searchParams.get("after")).toBe("lead:5");
  });

  it("ignores a List for the previous workspace that lands after a switch", async () => {
    agents.set("a1", agent("a1"));
    const release = holdFullLists();
    let answerB = (_: Response) => {};
    otherWs.set("wsB", () => new Promise((r) => (answerB = r)));
    const { result, rerender } = renderHook(({ ws }) => useAgentRoster(ws), {
      initialProps: { ws: "wsA" },
    });
    await waitFor(() => expect(lists).toHaveLength(1));

    rerender({ ws: "wsB" });
    await act(async () => release());
    expect([...result.current.roster.keys()]).toEqual([]);

    await act(async () => answerB(json({ agents: [agent("b1")], next: "" })));
    expect([...result.current.roster.keys()]).toEqual(["b1"]);
  });

  it("never shows the previous workspace's agents when the new List fails", async () => {
    agents.set("a1", agent("a1"));
    otherWs.set("wsB", async () => new Response("{}", { status: 500 }));
    const { result, rerender } = renderHook(({ ws }) => useAgentRoster(ws), {
      initialProps: { ws: "wsA" },
    });
    await waitFor(() =>
      expect([...result.current.roster.keys()]).toEqual(["a1"]),
    );

    rerender({ ws: "wsB" });
    expect([...result.current.roster.keys()]).toEqual([]);
    await waitFor(() => expect(result.current.error).not.toBeNull());
    expect([...result.current.roster.keys()]).toEqual([]);
  });

  // A's List lands and queues its update, the switch to B commits first,
  // and B's List lands before React replays A's update.
  async function switchWhileAQueued(answerB: () => Promise<Response>) {
    agents.set("a1", agent("a1"));
    const release = holdFullLists();
    otherWs.set("wsB", answerB);
    const hook = renderHook(({ ws }) => useAgentRoster(ws), {
      initialProps: { ws: "wsA" },
    });
    await waitFor(() => expect(lists).toHaveLength(1));
    await act(async () => {
      release();
      await new Promise((r) => setTimeout(r, 0));
      flushSync(() => hook.rerender({ ws: "wsB" }));
      await new Promise((r) => setTimeout(r, 0));
    });
    return hook.result;
  }

  it("shows B's List, not A's queued one, after a synchronous switch", async () => {
    const result = await switchWhileAQueued(async () =>
      json({ agents: [agent("b1")], next: "" }),
    );
    await waitFor(() =>
      expect([...result.current.roster.keys()]).toEqual(["b1"]),
    );
  });

  it("shows B's List error, not A's queued roster, after a synchronous switch", async () => {
    const result = await switchWhileAQueued(
      async () => new Response("{}", { status: 500 }),
    );
    await waitFor(() => expect(result.current.error).not.toBeNull());
    expect([...result.current.roster.keys()]).toEqual([]);
  });
});
