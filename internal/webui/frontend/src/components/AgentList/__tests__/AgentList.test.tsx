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
const names = () => screen.getAllByRole("link").map((l) => l.textContent ?? "");

function renderList() {
  render(
    <MemoryRouter initialEntries={["/ws/ws1/chat/lead"]}>
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
  it("groups children under their lead and shows the harness as a label", async () => {
    renderList();
    await waitFor(() => expect(names()).toHaveLength(3));
    const lead = screen.getByRole("link", { name: /^lead/ });
    expect(lead).toHaveAttribute("href", "/ws/ws1/chat/lead");
    expect(lead).toHaveAttribute("aria-current", "page");
    const leadItem = lead.closest("li")!;
    expect(leadItem.querySelector("ul")).toHaveTextContent("kidopencodeactive");
    expect(screen.getByRole("link", { name: /^other/ })).toHaveTextContent(
      "otherclaudeidle",
    );
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
    expect(screen.getByRole("link", { name: /^kid/ })).toHaveTextContent(
      "finished",
    );
    expect(screen.queryByRole("link", { name: /^other/ })).toBeNull();
  });

  it("lists a lead's children on child.created and subscribes the new child", async () => {
    renderList();
    await waitFor(() => expect(names()).toHaveLength(3));
    const first = stream();
    api.agents.push(agent("kid2", { parent_agent_id: "lead" }));
    act(() => first.opts.onEvents!([ev("lead", "child.created")]));
    await waitFor(() => expect(names()).toContain("kid2opencodeidle"));
    expect(api.listAgents).toHaveBeenLastCalledWith("ws1", {
      parent: "lead",
      after: "",
    });
    expect(first.closed).toBe(true);
    expect(stream().opts.agents).toEqual(["kid", "kid2", "lead", "other"]);
  });

  it("shows a new lead without a reload when its chat opens, starting empty", async () => {
    api.agents = [];
    let navigate: NavigateFunction = () => {};
    function Nav() {
      navigate = useNavigate();
      return null;
    }
    render(
      <MemoryRouter initialEntries={["/ws/ws1/agents"]}>
        <Nav />
        <AgentList workspaceId="ws1" />
      </MemoryRouter>,
    );
    await waitFor(() => expect(api.listAgents).toHaveBeenCalledTimes(1));
    expect(screen.queryAllByRole("link")).toHaveLength(0);
    expect(api.streams).toHaveLength(0);
    // New Agent creates the lead, then opens its chat.
    for (const id of ["lead", "lead2"]) {
      api.agents.push(agent(id));
      act(() => navigate(`/ws/ws1/chat/${id}`));
      await waitFor(() =>
        expect(
          screen.getByRole("link", { name: new RegExp(`^${id}`) }),
        ).toHaveAttribute("aria-current", "page"),
      );
    }
    expect(names()).toEqual(["leadopencodeidle", "lead2opencodeidle"]);
    expect(stream().opts.agents).toEqual(["lead", "lead2"]);
    expect(api.streams.filter((s) => !s.closed)).toHaveLength(1);
  });

  it("re-lists on resync (feed.gap or reconnect) without duplicates", async () => {
    renderList();
    await waitFor(() => expect(names()).toHaveLength(3));
    api.agents = api.agents.filter((a) => a.agent_id !== "other");
    api.agents.push(agent("kid3", { parent_agent_id: "lead" }));
    act(() => stream().opts.onResync!());
    await waitFor(() => expect(names()).toContain("kid3opencodeidle"));
    expect(names()).toEqual([
      "leadopencodeidle",
      "kidopencodeactive",
      "kid3opencodeidle",
    ]);
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
      }),
    ];
    renderList();
    await waitFor(() => expect(names()).toHaveLength(4));
    // Lead first, its worker child nested under it, then the rest, then Background.
    expect(names()).toEqual([
      "leadopencodeidle",
      "kidopencodeidle",
      "reviewopencodeidle",
      "workeropencodeidle",
    ]);
    const lead = screen.getByRole("link", { name: /^lead/ }).closest("li")!;
    expect(within(lead).getByRole("link", { name: /^kid/ })).toBeVisible();
    const bg = screen.getByTestId("agent-list-background");
    const bgNames = within(bg)
      .getAllByRole("link")
      .map((l) => l.textContent);
    expect(bgNames).toEqual(["workeropencodeidle"]);

    const toggle = within(bg).getByRole("button", { name: "Background" });
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("link", { name: /^worker/ })).toBeNull();
    expect(names()).toHaveLength(3);
    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByRole("link", { name: /^worker/ })).toBeVisible();
  });

  it("shows no Background group without an independent worker", async () => {
    renderList();
    await waitFor(() => expect(names()).toHaveLength(3));
    expect(screen.queryByTestId("agent-list-background")).toBeNull();
  });
});
