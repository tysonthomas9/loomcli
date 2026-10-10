/**
 * @vitest-environment jsdom
 */

import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { useState } from "react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import type { Agent, AgentEvent, AgentStreamOptions } from "@/api/agentsv1";
import { KeyboardShortcutProvider } from "@/hooks/ui";

const api = vi.hoisted(() => ({
  agents: [] as Agent[],
  listAgents: vi.fn(),
  archiveAgent: vi.fn(),
  streams: [] as { opts: AgentStreamOptions }[],
}));

vi.mock("@/api/agentsv1", () => ({
  listAgents: api.listAgents,
  getAgent: vi.fn(),
  archiveAgent: api.archiveAgent,
  deleteAgent: vi.fn(),
  newRequestId: () => "req-1",
  AgentEventStream: class {
    constructor(
      _ws: string,
      public opts: AgentStreamOptions,
    ) {
      api.streams.push(this);
    }
    connect() {
      return Promise.resolve();
    }
    close() {}
  },
}));

import { useRoster } from "@/hooks";
import { AgentList } from "../AgentList";
import { AgentRosterOwner } from "../AgentRosterOwner";
import { ApiAgentRailItems } from "../ApiAgentRailItems";

const agent = (id: string, over: Partial<Agent> = {}): Agent =>
  ({
    agent_id: id,
    name: id,
    harness: "opencode",
    preset: "lead",
    role_kind: "interactive",
    state: "idle",
    parent_agent_id: null,
    created_at: "2026-10-10T00:00:00Z",
    ...over,
  }) as Agent;

// Every roster size the shared roster published, as the home count reads it.
let sizes: number[] = [];
function Probe(): null {
  sizes.push(useRoster().size);
  return null;
}

// The tree's two views, swapped as collapsing does, under one owner.
function Tree(): JSX.Element {
  const [collapsed, setCollapsed] = useState(false);
  return (
    <KeyboardShortcutProvider>
      <MemoryRouter initialEntries={["/ws/ws1/agents"]}>
        <AgentRosterOwner workspaceId="ws1">
          <Probe />
          <button type="button" onClick={() => setCollapsed(!collapsed)}>
            toggle
          </button>
          {collapsed ? (
            <ApiAgentRailItems workspaceId="ws1" />
          ) : (
            <AgentList workspaceId="ws1" />
          )}
        </AgentRosterOwner>
      </MemoryRouter>
    </KeyboardShortcutProvider>
  );
}

const toggle = () =>
  fireEvent.click(screen.getByRole("button", { name: "toggle" }));
const railIds = () =>
  screen
    .queryAllByRole("link")
    .map((l) => l.getAttribute("data-agent-id"))
    .filter(Boolean);

beforeEach(() => {
  sizes = [];
  api.streams = [];
  localStorage.clear();
  api.agents = [
    agent("lead"),
    agent("kid", { parent_agent_id: "lead", state: "active", preset: "t" }),
  ];
  api.listAgents.mockReset();
  api.listAgents.mockImplementation(() =>
    Promise.resolve({ agents: api.agents, next: "" }),
  );
  api.archiveAgent.mockReset();
  api.archiveAgent.mockResolvedValue(undefined);
});

describe("AgentRosterOwner", () => {
  it("keeps a busy agent archived in the expanded list out of the collapsed rail until the stream reports it", async () => {
    render(<Tree />);
    await waitFor(() => expect(screen.getAllByRole("link")).toHaveLength(2));
    // kid is still at work, so the stream keeps it active for now.
    fireEvent.click(screen.getByRole("button", { name: "Archive kid" }));
    await waitFor(() => expect(screen.getAllByRole("link")).toHaveLength(1));

    toggle();
    await waitFor(() => expect(railIds()).toEqual(["lead"]));

    act(() =>
      api.streams.at(-1)!.opts.onEvents?.([
        {
          agent_id: "kid",
          seq: 2,
          event_id: "e2",
          kind: "agent.state_changed",
          turn_id: "",
          payload: { from: "stopping", to: "archived" },
          created_at: "",
        } as AgentEvent,
      ]),
    );
    expect(railIds()).toEqual(["lead"]);
  });

  it("collapsing and expanding keeps the one roster: no re-list and the shared roster never empties", async () => {
    render(<Tree />);
    await waitFor(() => expect(screen.getAllByRole("link")).toHaveLength(2));
    const lists = api.listAgents.mock.calls.length;
    const streams = api.streams.length;
    sizes = [];

    toggle();
    await waitFor(() => expect(railIds()).toEqual(["lead", "kid"]));
    toggle();
    await waitFor(() => expect(screen.getAllByRole("link")).toHaveLength(2));

    expect(api.listAgents.mock.calls.length).toBe(lists);
    expect(api.streams.length).toBe(streams);
    expect(sizes).not.toContain(0);
  });
});
