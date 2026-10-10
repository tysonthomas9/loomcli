/**
 * @vitest-environment jsdom
 */

import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import type { Agent } from "@/api/agentsv1";

const mockRoster = vi.hoisted(() => ({ current: new Map() }));
const mockError = vi.hoisted(() => ({ current: null as string | null }));

vi.mock("@/hooks", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/hooks")>();
  return {
    ...actual,
    useAgentRoster: () => ({
      roster: mockRoster.current,
      error: mockError.current,
    }),
  };
});

import { ApiAgentRailItems } from "../ApiAgentRailItems";

const api = (id: string, over: Partial<Agent> = {}): Agent =>
  ({
    agent_id: id,
    name: id,
    harness: "opencode",
    preset: "lead",
    role_kind: "interactive",
    state: "idle",
    parent_agent_id: null,
    created_at: `2026-10-04T00:00:0${id.length}Z`,
    ...over,
  }) as Agent;

const at = (path: string, empty?: JSX.Element) =>
  render(
    <MemoryRouter initialEntries={[path]}>
      <ApiAgentRailItems workspaceId="w1" empty={empty} />
    </MemoryRouter>,
  );

beforeEach(() => {
  mockRoster.current = new Map();
  mockError.current = null;
});

describe("ApiAgentRailItems", () => {
  it("lists Leads with their working children by the sidebar's rules, and marks the open chat", () => {
    mockRoster.current = new Map(
      [
        api("l1"),
        api("busy", { parent_agent_id: "l1", state: "active", preset: "t" }),
        api("done", { parent_agent_id: "l1", state: "finished", preset: "t" }),
        api("l2"),
        api("old", { state: "archived" }),
        // ORPH1: a finished child of an archived (unlisted) Lead leaves too.
        api("echo", {
          parent_agent_id: "unlisted",
          state: "finished",
          preset: "t",
          role_kind: "worker",
        }),
      ].map((a) => [a.agent_id, a]),
    );

    at("/ws/w1/chat/l2");

    const links = screen.getAllByRole("link");
    expect(links.map((l) => l.getAttribute("data-agent-id"))).toEqual([
      "l1",
      "busy",
      "l2",
    ]);
    expect(links[2]).toHaveAttribute("href", "/ws/w1/chat/l2");
    expect(links[2]).toHaveAttribute("aria-current", "page");
    expect(links[0]).not.toHaveAttribute("aria-current");
  });

  it("shows the empty hint once the roster has no agents to show", () => {
    mockRoster.current = new Map([["old", api("old", { state: "archived" })]]);

    at("/ws/w1/home", <span>No agents</span>);

    expect(screen.queryAllByRole("link")).toHaveLength(0);
    expect(screen.getByText("No agents")).toBeInTheDocument();
  });

  it("shows a failed Agent API list as an error, not as No agents", () => {
    mockError.current = "HTTP 502";

    at("/ws/w1/home", <span>No agents</span>);

    expect(screen.getByRole("alert")).toHaveAccessibleName(
      "Agent API agents unavailable: HTTP 502",
    );
    expect(screen.queryByText("No agents")).not.toBeInTheDocument();
  });
});
