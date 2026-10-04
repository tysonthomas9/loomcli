/**
 * @vitest-environment jsdom
 */

import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { MemoryRouter, useNavigate } from "react-router-dom";
import type { NavigateFunction } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import type {
  Agent,
  AgentEvent,
  AgentStreamOptions,
  ListAgentsQuery,
} from "@/api/agentsv1";

const api = vi.hoisted(() => ({
  agents: [] as Agent[],
  listAgents: vi.fn(),
  streams: [] as { opts: AgentStreamOptions; closed: boolean }[],
}));

vi.mock("@/api/agentsv1", () => ({
  listAgents: api.listAgents,
  AgentEventStream: class {
    closed = false;
    constructor(
      _ws: string,
      public opts: AgentStreamOptions,
    ) {
      api.streams.push(this);
    }
    connect() {
      return Promise.resolve();
    }
    close() {
      this.closed = true;
    }
  },
}));

import { useRosterAgent } from "@/hooks";
import { AgentList } from "../AgentList";

let created = 0;
function agent(id: string, over: Partial<Agent> = {}): Agent {
  return {
    agent_id: id,
    name: id,
    harness: "opencode",
    state: "idle",
    parent_agent_id: null,
    created_at: `2026-10-02T00:00:0${created++}Z`,
    ...over,
  } as Agent;
}

function ev(agentId: string, kind: string, payload: object = {}): AgentEvent {
  return {
    agent_id: agentId,
    seq: 1,
    event_id: `${kind}:${agentId}`,
    kind,
    turn_id: "",
    payload,
    created_at: "",
  };
}

// Two pages, so the roster must follow next.
function serve(q: ListAgentsQuery) {
  const all = api.agents.filter(
    (a) => !q.parent || a.parent_agent_id === q.parent,
  );
  const i = q.after ? Number(q.after) : 0;
  return Promise.resolve({
    agents: all.slice(i, i + 2),
    next: i + 2 < all.length ? String(i + 2) : "",
  });
}

const stream = () => api.streams.at(-1)!;
const names = () =>
  screen.queryAllByTestId("agent-list-name").map((n) => n.textContent ?? "");
const row = (name: string) =>
  screen
    .queryAllByRole("link")
    .find(
      (l) =>
        l.querySelector("[data-testid=agent-list-name]")?.textContent === name,
    ) ?? null;

let navigate: NavigateFunction = () => {};
function Nav() {
  navigate = useNavigate();
  return null;
}

function renderList(at = "/ws/ws1/chat/lead") {
  render(
    <MemoryRouter initialEntries={[at]}>
      <Nav />
      <AgentList workspaceId="ws1" />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  api.streams = [];
  api.agents = [
    agent("lead"),
    agent("other", { harness: "claude" }),
    agent("kid", { parent_agent_id: "lead", state: "active" }),
  ];
  api.listAgents.mockReset();
  api.listAgents.mockImplementation((_ws: string, q: ListAgentsQuery) =>
    serve(q),
  );
});

describe("AgentList", () => {
  it("groups children under their lead and shows the harness as a logo", async () => {
    renderList();
    await waitFor(() => expect(names()).toHaveLength(3));
    const lead = row("lead")!;
    expect(lead).toHaveAttribute("href", "/ws/ws1/chat/lead");
    expect(lead).toHaveAttribute("aria-current", "page");
    const leadItem = lead.closest("li")!;
    expect(within(leadItem.querySelector("ul")!).getByRole("link")).toBe(
      row("kid"),
    );
    // The harness is a logo named for it; no harness or state words show.
    expect(
      within(row("other")!).getByRole("img", { name: "claude" }),
    ).toHaveAttribute("title", "claude");
    for (const name of ["lead", "kid", "other"]) {
      const text = row(name)!.textContent ?? "";
      expect(text).not.toMatch(/opencode|claude|idle|active/);
    }
    expect(stream().opts).toMatchObject({
      agents: ["kid", "lead", "other"],
      live: true,
    });
  });

  it("shares the roster with chat child cards over the one stream", async () => {
    function Card() {
      return <p data-testid="card">{useRosterAgent("kid")?.state}</p>;
    }
    render(
      <MemoryRouter initialEntries={["/ws/ws1/chat/lead"]}>
        <AgentList workspaceId="ws1" />
        <Card />
      </MemoryRouter>,
    );
    await waitFor(() =>
      expect(screen.getByTestId("card")).toHaveTextContent("active"),
    );
    act(() =>
      stream().opts.onEvents!([
        ev("kid", "agent.state_changed", { from: "active", to: "finished" }),
      ]),
    );
    expect(screen.getByTestId("card")).toHaveTextContent("finished");
    expect(api.streams).toHaveLength(1);
  });

  it("projects state changes and deletes from the stream", async () => {
    renderList();
    await waitFor(() => expect(names()).toHaveLength(3));
    act(() =>
      stream().opts.onEvents!([
        ev("kid", "agent.state_changed", { from: "active", to: "finished" }),
        ev("other", "agent.deleted"),
      ]),
    );
    // A finished child leaves the sidebar; the Lead stays.
    expect(row("kid")).toBeNull();
    expect(row("lead")).not.toBeNull();
    expect(row("other")).toBeNull();
    // A new Send makes it active again, and it comes back.
    act(() =>
      stream().opts.onEvents!([
        ev("kid", "agent.state_changed", { from: "finished", to: "active" }),
      ]),
    );
    expect(row("lead")!.closest("li")!.querySelector("ul")).toContainElement(
      row("kid"),
    );
  });

  it("lists a lead's children on child.created and subscribes the new child", async () => {
    renderList();
    await waitFor(() => expect(names()).toHaveLength(3));
    const first = stream();
    api.agents.push(
      agent("kid2", { parent_agent_id: "lead", state: "creating" }),
    );
    act(() => first.opts.onEvents!([ev("lead", "child.created")]));
    await waitFor(() => expect(names()).toContain("kid2"));
    expect(api.listAgents).toHaveBeenLastCalledWith("ws1", {
      parent: "lead",
      after: "",
    });
    expect(first.closed).toBe(true);
    expect(stream().opts.agents).toEqual(["kid", "kid2", "lead", "other"]);
  });

  it("shows a new lead without a reload when its chat opens, starting empty", async () => {
    api.agents = [];
    renderList("/ws/ws1/agents");
    await waitFor(() => expect(api.listAgents).toHaveBeenCalledTimes(1));
    expect(screen.queryAllByRole("link")).toHaveLength(0);
    expect(api.streams).toHaveLength(0);
    // New Agent creates the lead, then opens its chat.
    for (const id of ["lead", "lead2"]) {
      api.agents.push(agent(id));
      act(() => navigate(`/ws/ws1/chat/${id}`));
      await waitFor(() =>
        expect(row(id)).toHaveAttribute("aria-current", "page"),
      );
    }
    expect(names()).toEqual(["lead", "lead2"]);
    expect(stream().opts.agents).toEqual(["lead", "lead2"]);
    expect(api.streams.filter((s) => !s.closed)).toHaveLength(1);
  });

  it("re-lists on resync (feed.gap or reconnect) without duplicates", async () => {
    renderList();
    await waitFor(() => expect(names()).toHaveLength(3));
    api.agents = api.agents.filter((a) => a.agent_id !== "other");
    api.agents.push(
      agent("kid3", { parent_agent_id: "lead", state: "waiting" }),
    );
    act(() => stream().opts.onResync!());
    await waitFor(() => expect(names()).toContain("kid3"));
    expect(names()).toEqual(["lead", "kid", "kid3"]);
  });

  it("lists leads first and independent workers under a collapsible Background group", async () => {
    api.agents = [
      agent("worker", { preset: "daemon-worker", role_kind: "worker" }),
      agent("review", {
        preset: "pr-review-interactive",
        role_kind: "interactive",
      }),
      agent("lead", { preset: "lead", role_kind: "interactive" }),
      agent("kid", {
        parent_agent_id: "lead",
        preset: "task",
        role_kind: "worker",
        state: "active",
      }),
    ];
    renderList();
    await waitFor(() => expect(names()).toHaveLength(4));
    // Lead first, its worker child nested under it, then the rest, then Background.
    expect(names()).toEqual(["lead", "kid", "review", "worker"]);
    const lead = row("lead")!.closest("li")!;
    expect(lead).toContainElement(row("kid"));
    const bg = screen.getByTestId("agent-list-background");
    const bgNames = within(bg)
      .getAllByTestId("agent-list-name")
      .map((l) => l.textContent);
    expect(bgNames).toEqual(["worker"]);

    const toggle = within(bg).getByRole("button", { name: "Background" });
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(row("worker")).toBeNull();
    expect(names()).toHaveLength(3);
    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    expect(row("worker")).toBeVisible();
  });

  it("shows no Background group without an independent worker", async () => {
    renderList();
    await waitFor(() => expect(names()).toHaveLength(3));
    expect(screen.queryByTestId("agent-list-background")).toBeNull();
  });

  it("shows a child only while it is at work; Leads and Background are not hidden", async () => {
    api.agents = [
      agent("lead", { preset: "lead", state: "finished" }),
      agent("bg", {
        role_kind: "worker",
        state: "finished",
        outcome: "failed",
      }),
      ...["creating", "active", "waiting", "stopping"].map((state) =>
        agent(`k-${state}`, { parent_agent_id: "lead", state }),
      ),
      agent("k-idle", { parent_agent_id: "lead", state: "idle" }),
      agent("k-done", {
        parent_agent_id: "lead",
        state: "finished",
        outcome: "completed",
      }),
      agent("k-failed", {
        parent_agent_id: "lead",
        state: "finished",
        outcome: "failed",
      }),
      agent("k-stopped", {
        parent_agent_id: "lead",
        state: "archived",
        outcome: "cancelled",
      }),
      agent("k-deleted", {
        parent_agent_id: "lead",
        state: "active",
        deleted_at: "2026-10-04T00:00:00Z",
      }),
    ];
    renderList("/ws/ws1/agents");
    await waitFor(() => expect(names()).toContain("bg"));
    expect(names()).toEqual([
      "lead",
      "k-creating",
      "k-active",
      "k-waiting",
      "k-stopping",
      "bg",
    ]);
  });

  it("keeps a finished child while its chat is open, until you navigate away", async () => {
    api.agents = [
      agent("lead", { preset: "lead" }),
      agent("done", { parent_agent_id: "lead", state: "finished" }),
    ];
    renderList("/ws/ws1/chat/done");
    await waitFor(() => expect(row("done")).not.toBeNull());
    expect(row("done")).toHaveAttribute("aria-current", "page");
    expect(row("lead")!.closest("li")).toContainElement(row("done"));
    act(() => navigate("/ws/ws1/chat/lead"));
    await waitFor(() => expect(row("done")).toBeNull());
    expect(row("lead")).toHaveAttribute("aria-current", "page");
  });

  it("keeps a finished child that has a child at work, so the working one stays nested", async () => {
    api.agents = [
      agent("lead", { preset: "lead" }),
      agent("mid", { parent_agent_id: "lead", state: "finished" }),
      agent("deep", { parent_agent_id: "mid", state: "active" }),
    ];
    renderList("/ws/ws1/agents");
    await waitFor(() => expect(names()).toHaveLength(3));
    expect(row("mid")!.closest("li")).toContainElement(row("deep"));
  });

  it("renders the AgentCard look: role line, status dot and the harness logo per harness", async () => {
    api.agents = [
      agent("lead", { preset: "lead", harness: "opencode" }),
      agent("cx", { role_kind: "worker", harness: "codex", state: "active" }),
      agent("cl", { preset: "pr-review-interactive", harness: "claude" }),
      agent("zz", { harness: "other-harness", state: "waiting" }),
    ];
    renderList("/ws/ws1/agents");
    await waitFor(() => expect(names()).toHaveLength(4));
    const glyph = (name: string) =>
      row(name)!.querySelector("[data-provider-icon]")!;
    expect(glyph("lead")).toHaveAttribute("data-provider-icon", "opencode");
    expect(glyph("cx")).toHaveAttribute("data-provider-icon", "codex");
    expect(glyph("cl")).toHaveAttribute("data-provider-icon", "claude");
    for (const name of ["lead", "cx", "cl"])
      expect(glyph(name).querySelector("svg")).not.toBeNull();
    // An unknown harness falls back to initials, still named for the harness.
    expect(glyph("zz").querySelector("svg")).toBeNull();
    expect(
      within(row("zz")!).getByRole("img", { name: "other-harness" }),
    ).toBeInTheDocument();

    expect(row("lead")).toHaveTextContent("Lead");
    expect(row("cx")).toHaveTextContent("Worker");
    expect(row("cl")).toHaveTextContent("Reviewer");
    expect(row("zz")).toHaveTextContent("Agent");
    const dot = (name: string) =>
      row(name)!.querySelector("[data-dot]")!.getAttribute("data-dot");
    expect([dot("lead"), dot("cx"), dot("zz")]).toEqual([
      "idle",
      "working",
      "waiting",
    ]);
    // The avatar shows initials and is hidden from the link's name.
    expect(screen.getByRole("link", { name: "cx Worker codex" })).toBe(
      row("cx"),
    );
  });

  it("shows a 20-character name in full, wrapping instead of cutting it off", async () => {
    const long = "db-worker-migrations";
    expect(long).toHaveLength(20);
    api.agents = [
      agent("lead", { preset: "lead" }),
      agent(long, { preset: "lead" }),
    ];
    renderList("/ws/ws1/agents");
    await waitFor(() => expect(names()).toContain(long));
    const name = within(row(long)!).getByTestId("agent-list-name");
    expect(name.textContent).toBe(long);
    // The name rule wraps rather than clipping with an ellipsis.
    const css = readFileSync(
      resolve(__dirname, "../AgentList.module.css"),
      "utf8",
    );
    const rule = /\.name \{([^}]*)\}/.exec(css)?.[1] ?? "";
    expect(rule).toMatch(/overflow-wrap: anywhere/);
    expect(rule).not.toMatch(/ellipsis|nowrap|overflow: hidden/);
  });
});
