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
import { MemoryRouter, useLocation, useNavigate } from "react-router-dom";
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
import type { DragEndEvent } from "@dnd-kit/core";
import { agentColorIndex } from "@/hooks/agents/agentColor";
import { KeyboardShortcutProvider } from "@/hooks/ui";
import { ApiError } from "@/types/common";

const api = vi.hoisted(() => ({
  agents: [] as Agent[],
  listAgents: vi.fn(),
  getAgent: vi.fn(),
  archiveAgent: vi.fn(),
  deleteAgent: vi.fn(),
  streams: [] as { opts: AgentStreamOptions; closed: boolean }[],
  // Each sortable list's onDragEnd, in render order (last render wins).
  dragEnds: [] as ((e: DragEndEvent) => void)[],
}));

// jsdom has no layout for a pointer drag, so tests call each list's
// onDragEnd as dnd-kit would on a drop.
vi.mock("@dnd-kit/core", async () => {
  const actual =
    await vi.importActual<typeof import("@dnd-kit/core")>("@dnd-kit/core");
  return {
    ...actual,
    DndContext: (props: Parameters<typeof actual.DndContext>[0]) => {
      if (props.onDragEnd) api.dragEnds.push(props.onDragEnd);
      return <actual.DndContext {...props} />;
    },
  };
});

vi.mock("@/api/agentsv1", () => ({
  listAgents: api.listAgents,
  getAgent: api.getAgent,
  archiveAgent: api.archiveAgent,
  deleteAgent: api.deleteAgent,
  newRequestId: () => "req-1",
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
  return <p data-testid="where">{useLocation().pathname}</p>;
}

function renderList(at = "/ws/ws1/chat/lead") {
  render(
    // ConfirmDialog registers an Escape layer, as in the app.
    <KeyboardShortcutProvider>
      <MemoryRouter initialEntries={[at]}>
        <Nav />
        <AgentList workspaceId="ws1" />
      </MemoryRouter>
    </KeyboardShortcutProvider>,
  );
}

beforeEach(() => {
  api.streams = [];
  api.dragEnds = [];
  localStorage.clear();
  api.archiveAgent.mockReset();
  api.archiveAgent.mockResolvedValue(undefined);
  api.deleteAgent.mockReset();
  api.deleteAgent.mockResolvedValue(undefined);
  api.agents = [
    agent("lead"),
    agent("other", { harness: "claude" }),
    agent("kid", { parent_agent_id: "lead", state: "active" }),
  ];
  api.listAgents.mockReset();
  api.listAgents.mockImplementation((_ws: string, q: ListAgentsQuery) =>
    serve(q),
  );
  api.getAgent.mockReset();
  api.getAgent.mockImplementation((_ws: string, id: string) => {
    const a = api.agents.find((x) => x.agent_id === id);
    return a
      ? Promise.resolve(a)
      : Promise.reject(new ApiError(404, "Not Found"));
  });
});

describe("AgentList", () => {
  it("groups children under their lead and shows the harness as a logo", async () => {
    renderList();
    await waitFor(() => expect(names()).toHaveLength(3));
    const lead = row("lead")!;
    expect(lead).toHaveAttribute("href", "/ws/ws1/chat/lead");
    expect(lead).toHaveAttribute("aria-current", "page");
    const leadKids = screen.getByRole("group", { name: "lead children" });
    expect(within(leadKids).getByRole("link")).toBe(row("kid"));
    // The harness is a logo named for it; no harness or state words show.
    expect(
      within(row("other")!).getByRole("img", { name: "claude" }),
    ).toHaveAttribute("title", "claude");
    for (const name of ["lead", "kid", "other"]) {
      const text = row(name)!.textContent ?? "";
      expect(text).not.toMatch(/opencode|claude|idle|active/);
    }
    // The roster opens its stream in an effect after the rows render.
    await waitFor(() =>
      expect(stream().opts).toMatchObject({
        agents: ["kid", "lead", "other"],
        live: true,
      }),
    );
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
    expect(
      screen.getByRole("group", { name: "lead children" }),
    ).toContainElement(row("kid"));
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
    expect(
      screen.getByRole("group", { name: "lead children" }),
    ).toContainElement(row("kid"));
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
    expect(
      screen.getByRole("group", { name: "lead children" }),
    ).toContainElement(row("done"));
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
    expect(
      screen.getByRole("group", { name: "mid children" }),
    ).toContainElement(row("deep"));
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

  it("colours each row's avatar with the agent's own colour, as the Lead chat does", async () => {
    renderList();
    await waitFor(() => expect(row("kid")).not.toBeNull());
    for (const id of ["lead", "kid"])
      expect(row(id)!.querySelector("[data-agent-color]")).toHaveAttribute(
        "data-agent-color",
        String(agentColorIndex(id)),
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

  it("does not underline a row on hover, over the global a:hover rule", () => {
    // base.css a:hover (0,1,1) beats .row (0,1,0), so .row:hover must reset it.
    const css = readFileSync(
      resolve(__dirname, "../AgentList.module.css"),
      "utf8",
    );
    const rule = /\.row:hover \{([^}]*)\}/.exec(css)?.[1] ?? "";
    expect(rule).toMatch(/text-decoration: none/);
  });

  it("reorders Leads by drag, keeps each child under its Lead, and keeps the order after a reload", async () => {
    api.agents = [
      agent("lead", { preset: "lead" }),
      agent("lead2", { preset: "lead" }),
      agent("kid", { parent_agent_id: "lead", state: "active" }),
    ];
    const { unmount } = render(
      <MemoryRouter initialEntries={["/ws/ws1/agents"]}>
        <AgentList workspaceId="ws1" />
      </MemoryRouter>,
    );
    await waitFor(() => expect(names()).toEqual(["lead", "kid", "lead2"]));
    // Leads have a drag handle; a child does not drag on its own.
    expect(screen.getByLabelText("Drag to reorder lead")).toBeInTheDocument();
    expect(screen.getByLabelText("Drag to reorder lead2")).toBeInTheDocument();
    expect(screen.queryByLabelText("Drag to reorder kid")).toBeNull();

    // Drop lead2 on lead; its child moves with lead.
    act(() =>
      api.dragEnds.at(-1)!({
        active: { id: "lead2" },
        over: { id: "lead" },
      } as unknown as DragEndEvent),
    );
    expect(names()).toEqual(["lead2", "lead", "kid"]);
    expect(
      within(screen.getByRole("group", { name: "lead children" })).getByRole(
        "link",
      ),
    ).toBe(row("kid"));

    // A reload reads the saved order, as the fleet rows do.
    unmount();
    renderList("/ws/ws1/agents");
    await waitFor(() => expect(names()).toEqual(["lead2", "lead", "kid"]));
  });

  it("archives an agent from the hover action, and the row leaves the list", async () => {
    renderList("/ws/ws1/agents");
    await waitFor(() => expect(names()).toHaveLength(3));
    fireEvent.click(screen.getByRole("button", { name: "Archive other" }));
    // The chat header's Archive call.
    await waitFor(() =>
      expect(api.archiveAgent).toHaveBeenCalledWith("ws1", "other", "req-1"),
    );
    await waitFor(() => expect(row("other")).toBeNull());
    expect(row("lead")).not.toBeNull();
  });

  it("archives a child from the right-click menu, leaving its Lead", async () => {
    renderList("/ws/ws1/agents");
    await waitFor(() => expect(names()).toHaveLength(3));
    fireEvent.contextMenu(row("kid")!, { clientX: 10, clientY: 20 });
    fireEvent.click(screen.getByRole("menuitem", { name: "Archive" }));
    await waitFor(() =>
      expect(api.archiveAgent).toHaveBeenCalledWith("ws1", "kid", "req-1"),
    );
    await waitFor(() => expect(row("kid")).toBeNull());
    expect(names()).toEqual(["lead", "other"]);
  });

  it("keeps the row when the archive fails", async () => {
    api.archiveAgent.mockRejectedValueOnce(new Error("agent_busy"));
    renderList("/ws/ws1/agents");
    await waitFor(() => expect(names()).toHaveLength(3));
    fireEvent.click(screen.getByRole("button", { name: "Archive other" }));
    await waitFor(() => expect(api.archiveAgent).toHaveBeenCalled());
    expect(row("other")).not.toBeNull();
  });

  it("drops an archived agent from the list when the stream says so", async () => {
    renderList("/ws/ws1/agents");
    await waitFor(() => expect(names()).toHaveLength(3));
    act(() =>
      stream().opts.onEvents?.([
        ev("other", "agent.state_changed", { from: "idle", to: "archived" }),
      ]),
    );
    await waitFor(() => expect(row("other")).toBeNull());
  });

  // Delete moved from the chat header to the sidebar menu (SB4); DA1's
  // refusal and Delete anyway follow it.
  const menuDelete = (name: string) => {
    fireEvent.contextMenu(row(name)!, { clientX: 10, clientY: 20 });
    fireEvent.click(screen.getByRole("menuitem", { name: "Delete" }));
  };
  const unsavedWork = (paths: string[]) =>
    new ApiError(409, "Conflict", {
      error: "uncommitted changes in /wt/other",
      code: "unsaved_work",
      paths,
      fingerprint: "f1",
    });

  it("deletes from the right-click menu only after a confirm, and the row leaves", async () => {
    renderList("/ws/ws1/agents");
    await waitFor(() => expect(names()).toHaveLength(3));
    menuDelete("other");
    expect(api.deleteAgent).not.toHaveBeenCalled();
    const confirm = screen.getByRole("alertdialog", { name: "Delete agent" });
    expect(confirm).toHaveTextContent("other");
    fireEvent.click(within(confirm).getByRole("button", { name: "Cancel" }));
    expect(api.deleteAgent).not.toHaveBeenCalled();
    expect(row("other")).not.toBeNull();

    menuDelete("other");
    fireEvent.click(screen.getByTestId("confirm-dialog-confirm"));
    await waitFor(() =>
      expect(api.deleteAgent).toHaveBeenCalledWith(
        "ws1",
        "other",
        "req-1",
        undefined,
      ),
    );
    await waitFor(() => expect(row("other")).toBeNull());
    expect(screen.getByTestId("where")).toHaveTextContent("/ws/ws1/agents");
  });

  it("leaves the open chat once its agent's delete succeeds", async () => {
    renderList("/ws/ws1/chat/other");
    await waitFor(() => expect(names()).toHaveLength(3));
    menuDelete("other");
    fireEvent.click(screen.getByTestId("confirm-dialog-confirm"));
    await waitFor(() =>
      expect(screen.getByTestId("where")).toHaveTextContent("/ws/ws1/home"),
    );
  });

  it("shows an unsaved-work refusal with its files and Delete anyway, which sends the fingerprint with no second confirm (DA1)", async () => {
    renderList("/ws/ws1/agents");
    await waitFor(() => expect(names()).toHaveLength(3));
    api.deleteAgent.mockRejectedValueOnce(unsavedWork(["main.go", "notes.md"]));
    menuDelete("other");
    fireEvent.click(screen.getByTestId("confirm-dialog-confirm"));
    const refusal = await screen.findByRole("alertdialog", {
      name: "Not deleted",
    });
    // The title says Not deleted; the body does not repeat it.
    expect(
      within(refusal).getByText(
        "Uncommitted changes in /wt/other: main.go, notes.md. Delete anyway loses these changes.",
      ),
    ).toBeInTheDocument();
    expect(refusal).not.toHaveTextContent("Not deleted:");
    expect(row("other")).not.toBeNull();

    fireEvent.click(screen.getByTestId("agent-delete-anyway"));
    await waitFor(() => expect(row("other")).toBeNull());
    expect(api.deleteAgent).toHaveBeenCalledTimes(2);
    expect(api.deleteAgent).toHaveBeenLastCalledWith("ws1", "other", "req-1", {
      fingerprint: "f1",
    });
    expect(screen.queryByRole("alertdialog")).toBeNull();
  });

  it("keeps the agent when the refusal is dismissed, and offers no Delete anyway for other errors", async () => {
    renderList("/ws/ws1/agents");
    await waitFor(() => expect(names()).toHaveLength(3));
    api.deleteAgent.mockRejectedValueOnce(unsavedWork(["README.md"]));
    menuDelete("other");
    fireEvent.click(screen.getByTestId("confirm-dialog-confirm"));
    const refusal = await screen.findByRole("alertdialog", {
      name: "Not deleted",
    });
    fireEvent.click(
      within(refusal).getByRole("button", { name: "Keep agent" }),
    );
    expect(screen.queryByRole("alertdialog")).toBeNull();
    expect(api.deleteAgent).toHaveBeenCalledTimes(1);

    api.deleteAgent.mockRejectedValueOnce(
      new ApiError(500, "Server Error", { error: "boom" }),
    );
    menuDelete("other");
    fireEvent.click(screen.getByTestId("confirm-dialog-confirm"));
    await waitFor(() => expect(api.deleteAgent).toHaveBeenCalledTimes(2));
    await act(() => Promise.resolve());
    expect(screen.queryByTestId("agent-delete-anyway")).toBeNull();
    expect(row("other")).not.toBeNull();
  });

  // Codex review of SB4.
  it("drags a row with its children as one unit", async () => {
    renderList();
    await waitFor(() => expect(names()).toHaveLength(3));
    const item = (name: string) =>
      row(name)!.closest("[data-testid=sortable-agent-item]");
    expect(item("lead")).toContainElement(row("kid"));
    expect(item("lead")).not.toContainElement(row("other"));
  });

  it("shows a row again once its archived agent is unarchived", async () => {
    renderList("/ws/ws1/agents");
    await waitFor(() => expect(names()).toHaveLength(3));
    fireEvent.click(screen.getByRole("button", { name: "Archive other" }));
    await waitFor(() => expect(row("other")).toBeNull());
    act(() =>
      stream().opts.onEvents!([
        ev("other", "agent.state_changed", { from: "idle", to: "archived" }),
      ]),
    );
    act(() =>
      stream().opts.onEvents!([
        ev("other", "agent.state_changed", { from: "archived", to: "idle" }),
      ]),
    );
    await waitFor(() => expect(row("other")).not.toBeNull());
  });

  it("shows an archived agent again once it is unarchived from its chat after a reload", async () => {
    // List leaves archived agents out, so the open chat's agent is fetched.
    api.agents = [agent("lead"), agent("old", { state: "archived" })];
    api.listAgents.mockImplementation((_ws: string, q: ListAgentsQuery) =>
      serve(q).then((p) => ({
        ...p,
        agents: p.agents.filter((a) => a.state !== "archived"),
      })),
    );
    renderList("/ws/ws1/chat/old");
    await waitFor(() => expect(stream().opts.agents).toContain("old"));
    expect(row("old")).toBeNull();
    act(() =>
      stream().opts.onEvents!([
        ev("old", "agent.state_changed", { from: "archived", to: "idle" }),
      ]),
    );
    await waitFor(() => expect(row("old")).not.toBeNull());
  });

  it("leaves a deleted open agent out of the list", async () => {
    api.agents = [
      agent("lead"),
      agent("gone", { deleted_at: "2026-10-02T00:00:09Z" }),
    ];
    api.listAgents.mockImplementation((_ws: string, q: ListAgentsQuery) =>
      serve(q).then((p) => ({
        ...p,
        agents: p.agents.filter((a) => !a.deleted_at),
      })),
    );
    renderList("/ws/ws1/chat/gone");
    await waitFor(() => expect(stream().opts.agents).toEqual(["lead"]));
    expect(row("gone")).toBeNull();
  });

  it("shows the error when the open agent's Get fails", async () => {
    api.getAgent.mockRejectedValue(new ApiError(503, "Service Unavailable"));
    renderList("/ws/ws1/chat/old");
    expect(await screen.findByRole("alert")).toBeInTheDocument();
  });

  it("ignores a failed Get from a List a newer one replaced", async () => {
    let fail: (err: unknown) => void = () => {};
    api.getAgent.mockReturnValueOnce(new Promise((_, rej) => (fail = rej)));
    renderList("/ws/ws1/chat/old");
    await waitFor(() => expect(api.getAgent).toHaveBeenCalled());
    act(() => navigate("/ws/ws1/chat/lead"));
    await waitFor(() => expect(names()).toHaveLength(3));
    await act(async () => fail(new ApiError(503, "Service Unavailable")));
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("keeps an archived Lead's working child in the list", async () => {
    api.agents = [
      agent("lead", { preset: "lead" }),
      agent("kid", { parent_agent_id: "lead", state: "active" }),
      agent("done", { parent_agent_id: "lead", state: "finished" }),
    ];
    renderList("/ws/ws1/agents");
    await waitFor(() => expect(names()).toEqual(["lead", "kid"]));
    act(() =>
      stream().opts.onEvents!([
        ev("lead", "agent.state_changed", { from: "idle", to: "archived" }),
      ]),
    );
    await waitFor(() => expect(names()).toEqual(["kid"]));
  });

  it("closes the row menu when the workspace changes", async () => {
    const tree = (ws: string) => (
      <KeyboardShortcutProvider>
        <MemoryRouter initialEntries={["/ws/ws1/agents"]}>
          <AgentList workspaceId={ws} />
        </MemoryRouter>
      </KeyboardShortcutProvider>
    );
    const { rerender } = render(tree("ws1"));
    await waitFor(() => expect(names()).toHaveLength(3));
    fireEvent.contextMenu(row("other")!, { clientX: 10, clientY: 20 });
    expect(screen.getByRole("menu")).toBeInTheDocument();
    rerender(tree("ws2"));
    expect(screen.queryByRole("menu")).toBeNull();
  });
});
