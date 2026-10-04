/**
 * @vitest-environment jsdom
 */

import { fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";
import "@testing-library/jest-dom";

import type { Agent, WaitingMessage } from "@/api/agentsv1";
import { trayRows, trayWaves, type ChatItem } from "@/hooks";
import { AgentTray } from "../AgentTray";

const kid = (id: string, over: Partial<Agent> = {}): Agent =>
  ({
    agent_id: id,
    name: id,
    harness: "claude",
    model: "sonnet",
    branch: `loom/${id}`,
    parent_agent_id: "L",
    state: "active",
    attempt: 0,
    created_at: "2026-10-04T02:00:00Z",
    deleted_at: null,
    ...over,
  }) as Agent;

const started = (n: number, ...ids: string[]): ChatItem => ({
  key: `s${n}`,
  kind: "started",
  children: ids.map((child) => ({ child, name: child })),
  at: "",
});

function Harness({
  roster,
  items,
  waiting = [],
  narrow = false,
}: {
  roster: Agent[];
  items: ChatItem[];
  waiting?: WaitingMessage[];
  narrow?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const rows = trayRows("L", roster, items, waiting);
  return (
    <MemoryRouter>
      <AgentTray
        workspaceId="w1"
        rows={rows}
        waves={trayWaves(rows, items)}
        open={open}
        onOpenChange={setOpen}
        narrow={narrow}
        tucked
      />
    </MemoryRouter>
  );
}

describe("AgentTray", () => {
  it("is hidden when no child is working or waiting", () => {
    const { container } = render(
      <Harness roster={[kid("a", { state: "finished" })]} items={[]} />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("collapses to one line and expands to rows with the unread dot and attempt chip", () => {
    const items: ChatItem[] = [
      started(0, "api", "db"),
      {
        key: "task_completed:api:0",
        kind: "completion",
        name: "",
        record: {
          child: "api",
          attempt: 0,
          outcome: "completed",
          head: "a1b2c3d4e5",
          summary: "Implemented REST API\nmore",
        },
        at: "",
      },
      {
        key: "task_completed:db:0",
        kind: "completion",
        name: "",
        record: { child: "db", attempt: 0, outcome: "failed" },
        at: "",
      },
    ];
    render(
      <Harness
        roster={[kid("api", { state: "finished" }), kid("db")]}
        items={items}
        waiting={[
          {
            sender: "agent:api",
            text: "…",
            since: "",
            message: "",
            completions: [{ child: "api", attempt: 0 }],
          },
        ]}
      />,
    );
    const header = screen.getByRole("button", { expanded: false });
    expect(header).toHaveTextContent("2 agents · 1 running · 1 done");
    expect(screen.queryByText("Open")).toBeNull();
    fireEvent.click(header);
    expect(header).toHaveAttribute("aria-expanded", "true");
    const api = document.querySelector('[data-tray-row="api"]')!;
    expect(api).toHaveTextContent("done · waiting for Lead");
    expect(api).toHaveTextContent("sonnet · loom/api@a1b2c3d");
    expect(api).toHaveTextContent("Implemented REST API");
    expect(
      api.querySelector('[aria-label="unread by the Lead"]'),
    ).not.toBeNull();
    const db = document.querySelector('[data-tray-row="db"]')!;
    expect(db).toHaveTextContent("attempt 2");
    expect(db.querySelector('[aria-label="unread by the Lead"]')).toBeNull();
    expect(db.querySelector("a")).toHaveAttribute("href", "/ws/w1/chat/db");
    // One wave: no dividers.
    expect(screen.queryByTestId("tray-divider")).toBeNull();
  });

  it("marks a later wave +N new until opened, with dividers per wave", () => {
    const roster = [kid("api"), kid("docs")];
    const { rerender } = render(
      <Harness roster={roster} items={[started(0, "api")]} />,
    );
    expect(screen.queryByTestId("tray-new")).toBeNull();
    const items = [started(0, "api"), started(1, "docs")];
    rerender(<Harness roster={roster} items={items} />);
    expect(screen.getByTestId("tray-new")).toHaveTextContent("+1 new");
    fireEvent.click(screen.getByRole("button", { expanded: false }));
    expect(screen.queryByTestId("tray-new")).toBeNull();
    const dividers = screen.getAllByTestId("tray-divider");
    expect(dividers).toHaveLength(2);
    dividers.forEach((d) =>
      expect(d.textContent).toMatch(/^Started \S+( ago)? · 1 agent$/),
    );
    const order = [...document.querySelectorAll("[data-tray-row]")].map((r) =>
      r.getAttribute("data-tray-row"),
    );
    expect(order).toEqual(["docs", "api"]);
  });

  it("uses short labels when narrow", () => {
    render(
      <Harness
        roster={[kid("a"), kid("b")]}
        items={[started(0, "a", "b")]}
        narrow
      />,
    );
    expect(screen.getByRole("button")).toHaveTextContent("2 agents · 2 run");
  });
});
