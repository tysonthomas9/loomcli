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
import { MemoryRouter, useLocation } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import type { Agent, AgentEvent, AgentStreamOptions } from "@/api/agentsv1";
import { ApiError } from "@/types/common";

const api = vi.hoisted(() => ({
  getAgent: vi.fn(),
  sendMessage: vi.fn(),
  withdrawMessage: vi.fn(),
  respondToAsk: vi.fn(),
  archiveAgent: vi.fn(),
  unarchiveAgent: vi.fn(),
  getHarness: vi.fn(),
  streams: [] as { opts: AgentStreamOptions; events: AgentEvent[] }[],
  ids: 0,
  user: null as { id: string } | null,
}));

vi.mock("@/contexts/AuthContext", () => ({
  useAuth: () => ({ user: api.user }),
}));

vi.mock("@/api/agentsv1", () => ({
  getAgent: api.getAgent,
  sendMessage: api.sendMessage,
  withdrawMessage: api.withdrawMessage,
  respondToAsk: api.respondToAsk,
  archiveAgent: api.archiveAgent,
  unarchiveAgent: api.unarchiveAgent,
  updateAgent: () => Promise.resolve({}),
  getHarness: api.getHarness,
  listHarnessModels: (_ws: string, harness: string) =>
    Promise.resolve({ harness, providers: [] }),
  newRequestId: () => `req-${++api.ids}`,
  AgentEventStream: class {
    events: AgentEvent[] = [];
    history = { events: () => this.events };
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

import { AgentChat, daysLeftText } from "../AgentChat";

function agent(over: Partial<Agent> = {}): Agent {
  return {
    agent_id: "a1",
    name: "lead",
    harness: "opencode",
    state: "idle",
    running_turn_id: null,
    waiting_messages: [],
    open_asks: [],
    ...over,
  } as Agent;
}

let seq = 0;
function ev(kind: string, payload: object = {}): AgentEvent {
  seq++;
  return {
    agent_id: "a1",
    seq,
    event_id: `${kind}:${seq}`,
    kind,
    turn_id: "t1",
    payload,
    created_at: "",
  };
}

// Pushes saved events through the stream the chat opened.
function deliver(...events: AgentEvent[]) {
  const s = api.streams[api.streams.length - 1];
  s.events.push(...events);
  act(() => s.opts.onEvents?.(events));
}

// The router's path, to see where a card went.
function Where() {
  return <span data-testid="where">{useLocation().pathname}</span>;
}

async function mount(a: Agent) {
  api.getAgent.mockResolvedValue(a);
  const view = render(
    <MemoryRouter>
      <AgentChat workspaceId="w1" agentId="a1" />
      <Where />
    </MemoryRouter>,
  );
  await screen.findByText(a.name);
  return view;
}

const XSS = `<img src=x onerror="window.pwned=1"><script>window.pwned=1</script>`;

const fixture = () => [
  ev("message.delivered", { text: "hello" }),
  ev("item.completed", { itemKind: "reasoning", text: "thinking" }),
  ev("item.completed", { itemKind: "tool", text: "ls -la" }),
  ev("item.completed", { itemKind: "message", text: XSS }),
  ev("agent.turn_completed", { stopReason: "completed" }),
];

beforeEach(() => {
  vi.clearAllMocks();
  api.getHarness.mockResolvedValue({ health: { ok: true } });
  api.streams = [];
  api.user = null;
  seq = 0;
});

describe("AgentChat", () => {
  const done = (attempt: number, summary: string, outcome = "completed") => ({
    ...ev("task_completed", {
      child: "k1",
      attempt,
      outcome,
      branch: "loom/k1",
      head: "0123456789abcdef",
      summary,
    }),
    event_id: `task_completed:k1:${attempt}`,
  });

  it("shows children as started and result markers, never a raw task_completed bubble", async () => {
    const { container } = await mount(agent());
    deliver(
      ev("message.delivered", { text: "make a child", sender: "user:local" }),
      ev("child.created", { child: "k1", name: "kid", preset: "task" }),
      ev("child.created", { child: "k2", name: "kid2", preset: "task" }),
      done(0, "first try", "failed"),
      // Saved before DF1: no completions field, so hidden as before.
      ev("message.delivered", {
        text: "task_completed:k1:0 outcome=failed …",
        sender: "agent:k1",
      }),
      done(1, "second try"),
      // Only the record: nothing left to show as a message.
      ev("message.delivered", {
        text: "task_completed:k1:1 outcome=completed …",
        sender: "agent:k1",
        completions: [{ child: "k1", attempt: 1 }],
        message: "",
      }),
      // The child's own message, sent on purpose, merged with no record.
      ev("message.delivered", {
        text: "Heads up: use **cursor** paging",
        sender: "agent:k2",
        completions: [],
        message: "Heads up: use **cursor** paging",
      }),
      ev("message.delivered", {
        text: "from another agent",
        sender: "agent:x",
      }),
    );
    expect(container.textContent).not.toMatch(/task_completed:/);
    expect(screen.getByText("make a child")).toBeInTheDocument();
    const started = screen.getByTestId("started-marker");
    // Each child is its colour avatar (initials) and name, a link.
    expect(started).toHaveTextContent("↳StartedKIkidKIkid2");
    expect(
      [...started.querySelectorAll("a")].map((a) => a.getAttribute("href")),
    ).toEqual(["/ws/w1/chat/k1", "/ws/w1/chat/k2"]);
    const records = screen.getAllByTestId("completion-record");
    expect(records).toHaveLength(2);
    expect(records[0]).toHaveTextContent(/^KIkid✕ failedfirst try$/);
    expect(records[1]).toHaveTextContent(/^KIkid✓ done · attempt 2second try$/);
    // No "Lead read the result" tag and no "view" button: the card is the link.
    expect(records[1]).not.toHaveTextContent("Lead read the result");
    expect(records[1].querySelector("button")).toBeNull();
    expect(records[1]).toHaveAttribute("title", "loom/k1@0123456");
    // A message an agent sent on purpose is a named bubble with markdown.
    const from = screen.getAllByTestId("from-agent");
    expect(from.map((f) => f.textContent)).toEqual([
      "from kid2Heads up: use cursor paging",
      "from xfrom another agent",
    ]);
    expect(from[0].innerHTML).toContain("<strong>cursor</strong>");
  });

  it("shows a finished child as one E1 card that opens its chat", async () => {
    await mount(agent());
    deliver(
      ev("child.created", { child: "k1", name: "ui-test-agent-1" }),
      ev("child.created", { child: "k2", name: "ui-test-agent-2" }),
      {
        ...done(0, "node --test 3/3 passed · **smoke** OK\nsecond line"),
        created_at: "2026-10-04T10:40:00Z",
      },
    );
    const card = screen.getByTestId("completion-record");
    expect(card).toHaveAttribute("role", "link");
    expect(card).toHaveAttribute("tabindex", "0");
    expect(card).toHaveTextContent(
      /^U1ui-test-agent-1✓ donenode --test 3\/3 passed · smoke OK$/,
    );
    // The time and › show only on hover or focus.
    expect(screen.queryByTestId("card-time")).toBeNull();
    fireEvent.mouseEnter(card);
    expect(screen.getByTestId("card-time")).toHaveTextContent(/10:40|\d:\d\d/);
    expect(card).toHaveTextContent("›");
    fireEvent.mouseLeave(card);
    expect(screen.queryByTestId("card-time")).toBeNull();
    fireEvent.focus(card);
    expect(screen.getByTestId("card-time")).toBeInTheDocument();
    fireEvent.blur(card);
    expect(screen.queryByTestId("card-time")).toBeNull();
    // The card, the Started marker and the tray share the child's colour.
    const color = card.getAttribute("data-agent-color");
    expect(color).toMatch(/^[0-7]$/);
    const marker = screen.getByTestId("started-marker");
    expect(
      marker.querySelector('a[href="/ws/w1/chat/k1"] [data-agent-color]'),
    ).toHaveAttribute("data-agent-color", color);
    // Enter, Space and a click each open the child's chat.
    expect(screen.getByTestId("where")).toHaveTextContent("/");
    fireEvent.keyDown(card, { key: "Enter" });
    expect(screen.getByTestId("where")).toHaveTextContent("/ws/w1/chat/k1");
  });

  it("opens a card's chat on Space and on click", async () => {
    await mount(agent());
    deliver(ev("child.created", { child: "k1", name: "kid" }), done(0, "ok"));
    const card = screen.getByTestId("completion-record");
    fireEvent.keyDown(card, { key: " " });
    expect(screen.getByTestId("where")).toHaveTextContent("/ws/w1/chat/k1");
    fireEvent.keyDown(card, { key: "a" });
    fireEvent.click(card);
    expect(screen.getByTestId("where")).toHaveTextContent("/ws/w1/chat/k1");
  });

  it("shows a failed child as the same card with a red ✕ and its error", async () => {
    await mount(agent());
    deliver(
      ev("child.created", { child: "k1", name: "db-worker" }),
      done(0, 'Failed: relation "channels" does not exist', "failed"),
    );
    const card = screen.getByTestId("completion-record");
    expect(card).toHaveAttribute("data-outcome", "failed");
    expect(card).toHaveTextContent("✕ failed");
    expect(card).toHaveTextContent(
      'Failed: relation "channels" does not exist',
    );
    expect(card.querySelector("[data-failed=true]")).not.toBeNull();
  });

  it("folds the Lead's agent_create calls into the Started marker and shows agent_get as one line", async () => {
    const execute = (code: string) =>
      ev("item.completed", {
        itemKind: "tool",
        tool: { name: "execute", input: JSON.stringify({ code }) },
      });
    const { container } = await mount(agent());
    deliver(
      ev("item.completed", {
        itemKind: "message",
        text: "Starting two UI test agents.",
      }),
      ev("child.created", { child: "k1", name: "ui-test-agent-1" }),
      ev("child.created", { child: "k2", name: "ui-test-agent-2" }),
      execute("return await tools.loom.agent_create({brief:'test 1'})"),
      execute("return await tools.loom.agent_create({brief:'test 2'})"),
      done(0, "node --test 3/3 passed"),
      execute("return await tools.loom.agent_get({agent:'k1'})"),
    );
    // No "Used 2 tools" row and no raw Execute JSON.
    expect(screen.queryByTestId("tool-group")).toBeNull();
    expect(screen.queryByTestId("tool-call")).toBeNull();
    expect(container.textContent).not.toMatch(/tools\.loom|Execute/);
    const marker = screen.getByTestId("started-marker");
    const calls = within(marker).getByRole("button", { name: /2 tool calls/ });
    expect(calls).toHaveAttribute("aria-expanded", "false");
    expect(screen.getByTestId("bridge-call")).toHaveTextContent(
      "·Checked ui-test-agent-1",
    );
    // Expanding shows each call by its plain label.
    fireEvent.click(calls);
    expect(calls).toHaveAttribute("aria-expanded", "true");
    const rows = screen.getAllByTestId("tool-call");
    expect(rows).toHaveLength(2);
    for (const r of rows) expect(r).toHaveTextContent("Started an agent");
  });

  it("never shows a Lead's raw execute code in a row header, only when expanded (CL4)", async () => {
    const execute = (code: string) =>
      ev("item.completed", {
        itemKind: "tool",
        tool: { name: "execute", input: JSON.stringify({ code }) },
      });
    const spawn =
      "const s=search({namespace:'loom', query:'agent_create'}); " +
      "for (const name of ['ui-test-agent-1','ui-test-agent-2']) await s[0].call({name})";
    const { container } = await mount(agent());
    deliver(
      ev("item.completed", { itemKind: "reasoning", text: "Plan the tests" }),
      ev("child.created", { child: "k1", name: "ui-test-agent-1" }),
      ev("child.created", { child: "k2", name: "ui-test-agent-2" }),
      execute(spawn),
      ev("item.completed", { itemKind: "reasoning", text: "Count files" }),
      execute("return (await fs.readdir('.')).length"),
      // The agents-v1-lead bridge case.
      execute("return await tools.loom.agent_list({})"),
    );
    expect(container.textContent).not.toMatch(
      /Execute|"code"|search\(|readdir|tools\.loom/,
    );
    const marker = screen.getByTestId("started-marker");
    expect(marker).toHaveTextContent(
      "↳StartedU1ui-test-agent-1U2ui-test-agent-21 tool call ›",
    );
    expect(screen.getByTestId("tool-call")).toHaveTextContent(/^.?Ran code/);
    expect(screen.getByTestId("bridge-call")).toHaveTextContent(
      "·Listed agents",
    );
    // Expanded, each row still shows its code.
    fireEvent.click(within(marker).getByRole("button", { name: /tool call/ }));
    const rows = screen.getAllByTestId("tool-call");
    expect(rows[0]).toHaveTextContent("Ran code");
    for (const r of rows) fireEvent.click(within(r).getByRole("button"));
    expect(rows[0]).toHaveTextContent("search({namespace:'loom'");
    expect(rows[1]).toHaveTextContent("fs.readdir('.')");
  });

  it("folds a waiting result into its marker and shows only a child's own words", async () => {
    const { container } = await mount(
      agent({
        waiting_messages: [
          {
            sender: "agent:k1",
            text: "Heads up: **cursor** paging\ntask_completed:k1:0 outcome=completed …",
            since: "",
            message: "Heads up: **cursor** paging",
            completions: [{ child: "k1", attempt: 0 }],
          },
        ],
      }),
    );
    deliver(
      ev("child.created", { child: "k1", name: "kid", preset: "task" }),
      done(0, "all done"),
    );
    expect(container.textContent).not.toMatch(/task_completed:/);
    const record = screen.getByTestId("completion-record");
    expect(record).toHaveTextContent("waiting for Lead");
    const bubble = screen.getByTestId("from-agent");
    expect(bubble).toHaveAttribute("data-waiting", "true");
    expect(bubble).toHaveTextContent(
      "Waiting · from kidHeads up: cursor paging",
    );
    expect(bubble.innerHTML).toContain("<strong>cursor</strong>");
  });

  it("shows no bubble at all when only a child's record waits", async () => {
    const { container } = await mount(
      agent({
        waiting_messages: [
          {
            sender: "agent:k1",
            text: "task_completed:k1:0 outcome=completed …",
            since: "",
            message: "",
            completions: [{ child: "k1", attempt: 0 }],
          },
        ],
      }),
    );
    deliver(
      ev("child.created", { child: "k1", name: "kid", preset: "task" }),
      done(0, "all done"),
    );
    expect(container.textContent).not.toMatch(/task_completed:/);
    expect(container.querySelector('[data-kind="waiting"]')).toBeNull();
    expect(screen.getByTestId("completion-record")).toHaveTextContent(
      "waiting for Lead",
    );
  });

  it("renders untrusted text as text, never as markup", async () => {
    const { container } = await mount(agent());
    deliver(...fixture());
    expect(screen.getByText(XSS)).toBeInTheDocument();
    expect(container.querySelector("img, script")).toBeNull();
    expect((window as { pwned?: number }).pwned).toBeUndefined();
  });

  it("marks an incomplete history expiry until the purge succeeds", async () => {
    await mount(
      agent({
        state: "archived",
        history_purge_failed_at: "t",
        history_purged_at: null,
      }),
    );
    expect(screen.getByRole("status")).toHaveTextContent(
      "History expiry incomplete",
    );
  });

  it("shows no expiry marker once history is purged", async () => {
    await mount(
      agent({
        state: "archived",
        history_purge_failed_at: null,
        history_purged_at: "t",
      }),
    );
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("explains an empty saved reasoning event instead of offering an empty toggle", async () => {
    await mount(agent());
    deliver(ev("item.completed", { itemKind: "reasoning" }));
    const row = screen.getByTestId("reasoning");
    expect(row).toHaveTextContent("No reasoning text available");
    expect(within(row).queryByRole("button")).toBeNull();
  });

  it("expands reasoning text when the saved event supplies it", async () => {
    await mount(agent());
    deliver(
      ev("item.completed", {
        itemKind: "reasoning",
        text: "Plan the answer\nCheck the requested format.",
      }),
    );
    const row = screen.getByTestId("reasoning");
    fireEvent.click(within(row).getByRole("button"));
    expect(
      within(row).getByText(/Check the requested format/),
    ).toBeInTheDocument();
    expect(row).not.toHaveTextContent("No reasoning text available");
  });

  it("shows the full long reply without requiring expansion", async () => {
    await mount(agent());
    const long = "x".repeat(17000);
    deliver(
      ev("item.completed", {
        itemKind: "message",
        text: `${long}\n\n**End of reply**`,
      }),
    );
    expect(screen.getByText(long)).toBeInTheDocument();
    expect(screen.getByText("End of reply").tagName).toBe("STRONG");
    expect(screen.queryByRole("button", { name: /Show all/ })).toBeNull();
  });

  it("renders the same fixture for opencode, codex and claude except the label", async () => {
    const html: string[] = [];
    for (const harness of ["opencode", "codex", "claude"]) {
      seq = 0;
      const { container, unmount } = await mount(agent({ harness }));
      deliver(...fixture());
      expect(screen.getByTestId("harness-label")).toHaveTextContent(harness);
      html.push(container.innerHTML.replace(`>${harness}<`, "><"));
      unmount();
    }
    expect(html[1]).toBe(html[0]);
    expect(html[2]).toBe(html[0]);
  });

  it("shows one waiting bubble per sender; edit is a new Send and clear is Withdraw", async () => {
    api.sendMessage.mockResolvedValue({ state: "waiting" });
    api.withdrawMessage.mockResolvedValue({ result: "withdrawn" });
    await mount(
      agent({
        state: "active",
        waiting_messages: [
          { sender: "user:local", text: "first", since: "" },
          { sender: "agent:child", text: "done", since: "" },
        ],
      }),
    );
    expect(screen.getAllByText("Waiting", { exact: false })).toHaveLength(2);
    expect(screen.getAllByRole("button", { name: "Edit" })).toHaveLength(1);

    const input = screen.getByLabelText("Message");
    fireEvent.change(input, { target: { value: "one" } });
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    await vi.waitFor(() => expect(api.sendMessage).toHaveBeenCalledTimes(1));
    await vi.waitFor(() => expect(input).toHaveValue(""));

    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    expect(input).toHaveValue("first");
    fireEvent.change(input, { target: { value: "second" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await vi.waitFor(() => expect(api.sendMessage).toHaveBeenCalledTimes(2));
    const [first, second] = api.sendMessage.mock.calls;
    expect(second.slice(0, 3)).toEqual(["w1", "a1", "second"]);
    expect(second[3]).not.toBe(first[3]);

    fireEvent.click(screen.getByRole("button", { name: "Clear" }));
    await vi.waitFor(() =>
      expect(api.withdrawMessage).toHaveBeenCalledWith(
        "w1",
        "a1",
        expect.any(String),
      ),
    );
  });

  it("with two signed-in users, shows Edit and Clear only on the caller's own slot", async () => {
    api.user = { id: "u1" };
    api.sendMessage.mockResolvedValue({ state: "waiting" });
    api.withdrawMessage.mockResolvedValue({ result: "withdrawn" });
    await mount(
      agent({
        state: "active",
        waiting_messages: [
          { sender: "user:u2", text: "theirs", since: "" },
          { sender: "user:u1", text: "mine", since: "" },
        ],
      }),
    );
    expect(screen.getAllByRole("button", { name: "Edit" })).toHaveLength(1);
    expect(screen.getAllByRole("button", { name: "Clear" })).toHaveLength(1);
    expect(
      screen.getByText("from user:u2", { exact: false }),
    ).toBeInTheDocument();
    expect(screen.queryByText("from user:u1", { exact: false })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    expect(screen.getByLabelText("Message")).toHaveValue("mine");
  });

  it("answers an approval once and hides the card", async () => {
    api.respondToAsk.mockResolvedValue(undefined);
    await mount(
      agent({ open_asks: [{ id: "A1", type: "approval", about: "rm -rf" }] }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Approve" }));
    fireEvent.click(screen.getByRole("button", { name: "Decline" }));
    await vi.waitFor(() => expect(screen.queryByTestId("ask-card")).toBeNull());
    expect(api.respondToAsk).toHaveBeenCalledTimes(1);
    expect(api.respondToAsk).toHaveBeenCalledWith(
      "w1",
      "a1",
      "A1",
      { decision: "allow_once" },
      expect.any(String),
    );
  });

  it("hides a card another request already answered", async () => {
    api.respondToAsk.mockRejectedValue(
      new ApiError(409, "Conflict", { error: "x", code: "already_answered" }),
    );
    await mount(
      agent({ open_asks: [{ id: "Q1", type: "question", about: "Which?" }] }),
    );
    fireEvent.change(screen.getByLabelText("Write custom answer"), {
      target: { value: "B" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Submit answer" }));
    await vi.waitFor(() => expect(screen.queryByTestId("ask-card")).toBeNull());
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("answers a question, and hides a card answered elsewhere", async () => {
    api.respondToAsk.mockRejectedValue(
      new ApiError(404, "Not Found", { error: "x", code: "ask_not_found" }),
    );
    await mount(
      agent({ open_asks: [{ id: "Q1", type: "question", about: "Which?" }] }),
    );
    fireEvent.change(screen.getByLabelText("Write custom answer"), {
      target: { value: "B" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Submit answer" }));
    await vi.waitFor(() => expect(screen.queryByTestId("ask-card")).toBeNull());
    expect(api.respondToAsk.mock.calls[0][3]).toEqual({ answer: "B" });
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("shows what an approval asks about; Always allow this session is allow_always; Cancel declines and stops the turn", async () => {
    api.respondToAsk.mockResolvedValue(undefined);
    api.sendMessage.mockResolvedValue({ state: "handed", interrupted: true });
    const asks = [
      { id: "A1", type: "approval", about: "rm -rf build\nin /repo" },
      { id: "A2", type: "approval", about: "@@ -1 +1 @@\n-a\n+b" },
    ];
    const { unmount } = await mount(
      agent({ running_turn_id: "t1", open_asks: asks }),
    );
    expect(screen.getByLabelText("Approval request")).toHaveTextContent(
      "rm -rf build in /repo",
    );
    expect(screen.getByText("1/2")).toBeInTheDocument();
    fireEvent.click(
      screen.getByRole("button", { name: "Always allow this session" }),
    );
    await vi.waitFor(() =>
      expect(screen.getByLabelText("Approval request")).toHaveTextContent(
        "-a +b",
      ),
    );
    expect(api.respondToAsk.mock.calls[0]?.[3]).toEqual({
      decision: "allow_always",
    });
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await vi.waitFor(() => expect(api.sendMessage).toHaveBeenCalled());
    expect(api.respondToAsk.mock.calls[1]?.[3]).toEqual({ decision: "deny" });
    expect(api.sendMessage.mock.calls[0]?.slice(2, 5)).toEqual([
      "",
      expect.any(String),
      "interrupt",
    ]);
    unmount();
  });

  it("asks a question's questions one at a time with progress, options and a custom answer, then sends every answer", async () => {
    api.respondToAsk.mockResolvedValue(undefined);
    const questions = [
      {
        id: "q0",
        header: "Color",
        question: "Which color?",
        options: [{ label: "Red", description: "warm" }, { label: "Blue" }],
      },
      {
        id: "q1",
        header: "Sizes",
        question: "Which sizes?",
        options: [{ label: "S" }, { label: "M" }],
        multi_select: true,
      },
      { id: "q2", header: "Name", question: "Name it" },
    ];
    await mount(
      agent({
        open_asks: [
          { id: "Q1", type: "question", about: "Which color?", questions },
        ],
      }),
    );
    expect(screen.getByText("1/3")).toBeInTheDocument();
    expect(screen.getByText("warm")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /^Blue/ }));
    await screen.findByText("Which sizes?"); // a single choice moves on
    expect(screen.getByText("2/3")).toBeInTheDocument();
    expect(screen.getByText("Select one or more options.")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /^S\s/ }));
    fireEvent.click(screen.getByRole("button", { name: /^M\s/ }));
    fireEvent.click(screen.getByRole("button", { name: "Next question" }));
    expect(screen.getByText("Name it")).toBeInTheDocument();
    const submit = screen.getByRole("button", { name: "Submit answers" });
    expect(submit).toBeDisabled();
    fireEvent.change(screen.getByLabelText("Write custom answer"), {
      target: { value: "Loom" },
    });
    fireEvent.click(submit);
    await vi.waitFor(() => expect(screen.queryByTestId("ask-card")).toBeNull());
    expect(api.respondToAsk.mock.calls[0]?.[3]).toEqual({
      answers: { q0: ["Blue"], q1: ["S", "M"], q2: ["Loom"] },
    });
  });

  it("shows a failed turn's reason in a dismissable banner and on the turn's note", async () => {
    await mount(agent());
    deliver(
      ev("message.delivered", { text: "hi" }),
      ev("agent.turn_completed", {
        stopReason: "failed",
        error: "OpenAI Chat tool call delta is missing id or name",
      }),
    );
    const banner = await screen.findByTestId("turn-error");
    expect(banner).toHaveTextContent(
      "OpenAI Chat tool call delta is missing id or name",
    );
    expect(screen.getByTestId("turn-end-error")).toHaveTextContent(
      "OpenAI Chat tool call delta is missing id or name",
    );
    fireEvent.click(screen.getByRole("button", { name: "Dismiss error" }));
    expect(screen.queryByTestId("turn-error")).toBeNull();
    expect(screen.getByTestId("turn-end-error")).toBeInTheDocument();
  });

  it("drops the turn error banner once a later message starts another turn", async () => {
    await mount(agent());
    deliver(
      ev("agent.turn_completed", { stopReason: "failed", error: "quota" }),
    );
    await screen.findByTestId("turn-error");
    deliver(ev("message.delivered", { text: "again" }));
    await vi.waitFor(() =>
      expect(screen.queryByTestId("turn-error")).toBeNull(),
    );
  });

  it("removes the card on ask.lost", async () => {
    await mount(
      agent({ open_asks: [{ id: "A1", type: "approval", about: "run" }] }),
    );
    expect(screen.getByTestId("ask-card")).toBeInTheDocument();
    api.getAgent.mockResolvedValue(agent());
    deliver(ev("ask.lost", { askId: "A1" }));
    await vi.waitFor(() => expect(screen.queryByTestId("ask-card")).toBeNull());
  });

  it("shows Stop only while a turn runs; Stop is an interrupt Send with no message", async () => {
    api.sendMessage.mockResolvedValue({ state: "handed", interrupted: true });
    const { unmount } = await mount(agent());
    expect(screen.queryByRole("button", { name: "Stop" })).toBeNull();
    unmount();
    await mount(agent({ running_turn_id: "t1" }));
    fireEvent.click(screen.getByRole("button", { name: "Stop" }));
    await vi.waitFor(() =>
      expect(api.sendMessage).toHaveBeenCalledWith(
        "w1",
        "a1",
        "",
        expect.any(String),
        "interrupt",
      ),
    );
  });

  it("streams deltas, then shows the completed item once", async () => {
    await mount(agent());
    const s = api.streams[0];
    const delta = (text: string) => ({
      ...ev("delta", { itemId: "m1", itemKind: "message", text }),
      seq: 0,
    });
    act(() => s.opts.onNotice?.(delta("Hel")));
    act(() => s.opts.onNotice?.(delta("lo")));
    // The new word is revealed on the next animation frame.
    await waitFor(() =>
      expect(screen.getByTestId("chat-markdown")).toHaveTextContent(/^Hello$/),
    );
    deliver(
      ev("item.completed", {
        itemId: "m1",
        itemKind: "message",
        text: "Hello!",
      }),
    );
    expect(screen.queryByText("Hello")).toBeNull();
    expect(screen.getAllByText("Hello!")).toHaveLength(1);
  });

  it("smooths the first delta of a newly inserted live assistant row", async () => {
    const frames: FrameRequestCallback[] = [];
    vi.stubGlobal("matchMedia", () => ({ matches: false }));
    vi.stubGlobal("requestAnimationFrame", (frame: FrameRequestCallback) => {
      frames.push(frame);
      return frames.length;
    });
    vi.stubGlobal("cancelAnimationFrame", () => {});
    try {
      const { container } = await mount(agent());
      const stream = api.streams[0];
      await act(async () => stream.opts.onResync?.());
      const firstChunk = Array.from({ length: 15 }, (_, i) => `word${i}`).join(
        " ",
      );
      act(() =>
        stream.opts.onNotice?.({
          ...ev("delta", {
            itemId: "m1",
            itemKind: "message",
            text: firstChunk,
          }),
          seq: 0,
        }),
      );
      const row = container.querySelector('li[data-kind="agent"]');
      expect(row).toHaveAttribute("data-enter");
      const markdown = row?.querySelector('[data-testid="chat-markdown"]');
      expect(markdown).not.toBeNull();
      const words = () =>
        markdown?.textContent?.trim().split(/\s+/).filter(Boolean).length ?? 0;
      expect(words()).toBeLessThanOrEqual(2);
      let previous = words();
      for (let i = 1; i <= 24 && words() < 15; i++) {
        const scheduled = frames.splice(0);
        act(() => scheduled.forEach((frame) => frame((i * 1000) / 60)));
        expect(words() - previous).toBeLessThanOrEqual(2);
        previous = words();
      }
      expect(markdown).toHaveTextContent(firstChunk);
      deliver(
        ev("item.completed", {
          itemId: "m1",
          itemKind: "message",
          text: firstChunk,
        }),
      );
      expect(container.querySelectorAll('li[data-kind="agent"]')).toHaveLength(
        1,
      );
      expect(
        container.querySelector('li[data-kind="agent"]'),
      ).toHaveTextContent(firstChunk);
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("keeps the first painted frame of a synced 900-character delta to two words", async () => {
    const frames: FrameRequestCallback[] = [];
    vi.stubGlobal("matchMedia", () => ({ matches: false }));
    vi.stubGlobal("requestAnimationFrame", (frame: FrameRequestCallback) => {
      frames.push(frame);
      return frames.length;
    });
    vi.stubGlobal("cancelAnimationFrame", () => {});
    try {
      const { container } = await mount(agent());
      const stream = api.streams[0];
      await act(async () => stream.opts.onResync?.());
      const firstChunk = "word ".repeat(180);
      act(() =>
        stream.opts.onNotice?.({
          ...ev("delta", {
            itemId: "m1",
            itemKind: "message",
            text: firstChunk,
          }),
          seq: 0,
        }),
      );
      const row = container.querySelector('li[data-kind="agent"]');
      expect(row).toHaveAttribute("data-enter");
      const markdown = row?.querySelector('[data-testid="chat-markdown"]');
      expect(markdown).not.toBeNull();
      const words = () =>
        markdown?.textContent?.trim().split(/\s+/).filter(Boolean).length ?? 0;
      expect(words()).toBe(0);
      const scheduled = frames.splice(0);
      expect(scheduled.length).toBeGreaterThan(0);
      act(() => scheduled.forEach((frame) => frame(1000 / 60)));
      expect(words()).toBeGreaterThan(0);
      expect(words()).toBeLessThanOrEqual(2);
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("shows preexisting unsynced midstream text immediately", async () => {
    const { container } = await mount(agent());
    const firstChunk = Array.from({ length: 15 }, (_, i) => `word${i}`).join(
      " ",
    );
    act(() =>
      api.streams[0].opts.onNotice?.({
        ...ev("delta", { itemId: "m1", itemKind: "message", text: firstChunk }),
        seq: 0,
      }),
    );
    const row = container.querySelector('li[data-kind="agent"]');
    expect(row).not.toHaveAttribute("data-enter");
    expect(
      row?.querySelector('[data-testid="chat-markdown"]'),
    ).toHaveTextContent(firstChunk);
  });

  it.each([1, 2])(
    "fades in a message that arrives live, not a %i-row history load",
    async (n) => {
      const { container } = await mount(agent());
      const s = api.streams[0];
      deliver(
        ...Array.from({ length: n }, (_, i) =>
          ev("message.delivered", { sender: "user:u1", text: `old ${i}` }),
        ),
      );
      act(() => s.opts.onResync?.());
      expect(container.querySelectorAll("li[data-enter]")).toHaveLength(0);
      deliver(ev("message.delivered", { sender: "user:u1", text: "four" }));
      const entered = container.querySelectorAll("li[data-enter]");
      expect(entered).toHaveLength(1);
      expect(entered[0]).toHaveTextContent("four");
    },
  );

  it.each([1, 2])(
    "does not fade in %i rows that a reconnect's catch-up replays",
    async (n) => {
      const { container } = await mount(agent());
      const s = api.streams[0];
      act(() => s.opts.onResync?.());
      act(() => s.opts.onStateChange?.("reconnecting"));
      deliver(
        ...Array.from({ length: n }, (_, i) =>
          ev("message.delivered", { sender: "user:u1", text: `missed ${i}` }),
        ),
      );
      act(() => s.opts.onResync?.());
      expect(container.querySelectorAll("li[data-enter]")).toHaveLength(0);
      deliver(ev("message.delivered", { sender: "user:u1", text: "live" }));
      expect(container.querySelectorAll("li[data-enter]")).toHaveLength(1);
    },
  );

  it("does not fade in a row that a feed.gap's catch-up replays", async () => {
    const { container } = await mount(agent());
    const s = api.streams[0];
    act(() => s.opts.onResync?.());
    act(() => s.opts.onNotice?.({ ...ev("feed.gap"), seq: 0 }));
    deliver(ev("message.delivered", { sender: "user:u1", text: "missed" }));
    act(() => s.opts.onResync?.());
    expect(container.querySelectorAll("li[data-enter]")).toHaveLength(0);
  });

  it("does not fade a completed message in over its streamed copy", async () => {
    const { container } = await mount(agent());
    const s = api.streams[0];
    act(() => s.opts.onResync?.());
    act(() =>
      s.opts.onNotice?.({
        ...ev("delta", { itemId: "m1", itemKind: "message", text: "Hi" }),
        seq: 0,
      }),
    );
    expect(container.querySelectorAll("li[data-enter]")).toHaveLength(1);
    deliver(
      ev("item.completed", { itemId: "m1", itemKind: "message", text: "Hi!" }),
    );
    expect(screen.getByText("Hi!").closest("li")).not.toHaveAttribute(
      "data-enter",
    );
  });
});

describe("AgentChat lifecycle (1.8b)", () => {
  it("has no Archive or Delete in the header; the harness and state chips stay (SB4)", async () => {
    await mount(agent());
    // Archive and Delete moved to the sidebar row's menu.
    expect(screen.queryByTestId("agent-archive")).toBeNull();
    expect(screen.queryByTestId("agent-delete")).toBeNull();
    expect(
      screen.queryByRole("button", { name: /archive|delete/i }),
    ).toBeNull();
    expect(screen.getByTestId("harness-label")).toBeInTheDocument();
    expect(screen.getByText(agent().state)).toBeInTheDocument();
  });

  it("shows an archived agent's read-only history with the days left; unarchive restores the composer", async () => {
    // Archive itself is in the sidebar row's menu (SB4, AgentList tests).
    const archived = new Date(Date.now() - 5 * 86_400_000).toISOString();
    await mount(agent({ state: "archived", archived_at: archived }));
    expect(
      await screen.findByTestId("agent-archived-notice"),
    ).toHaveTextContent("History expires in 25 days.");
    expect(screen.queryByLabelText("Message")).toBeNull();

    api.unarchiveAgent.mockResolvedValue(undefined);
    api.getAgent.mockResolvedValue(agent());
    fireEvent.click(screen.getByTestId("agent-unarchive"));
    expect(await screen.findByLabelText("Message")).toBeInTheDocument();
    expect(screen.queryByTestId("agent-archived-notice")).toBeNull();
    expect(screen.queryByTestId("agent-unarchive")).toBeNull();
    expect(api.unarchiveAgent).toHaveBeenCalledTimes(1);
  });
  it("counts the days left before the 30-day expiry", () => {
    const now = Date.parse("2026-10-04T00:00:00Z");
    expect(daysLeftText("2026-10-04T00:00:00Z", now)).toBe(
      "History expires in 30 days.",
    );
    expect(daysLeftText("2026-09-04T12:00:00Z", now)).toBe(
      "History expires in 1 day.",
    );
    expect(daysLeftText("2026-08-01T00:00:00Z", now)).toBe(
      "History expires in 0 days.",
    );
  });

  it("shows the attention reason in a banner, and none once it clears", async () => {
    await mount(agent({ attention_reason: "harness_unavailable" }));
    expect(screen.getByTestId("agent-attention-banner")).toHaveTextContent(
      "Needs attention: the harness is unavailable.",
    );
    api.getAgent.mockResolvedValue(agent());
    deliver(ev("agent.state_changed"));
    await waitFor(() =>
      expect(screen.queryByTestId("agent-attention-banner")).toBeNull(),
    );
  });

  it.each([
    [
      "opencode",
      "opencode 2.0.1 is below the minimum 2.0.19; upgrade opencode",
    ],
    ["codex", "codex 0.150.0 is below the minimum 0.157.1; upgrade codex"],
    ["claude", "claude 2.1.0 is below the minimum 2.1.285; upgrade claude"],
  ])(
    "names why %s is unavailable: its version, the minimum and the upgrade",
    async (harness, reason) => {
      api.getHarness.mockResolvedValue({
        harness,
        health: { ok: false, warning: `harness_too_old: ${reason}` },
      });
      await mount(agent({ harness, attention_reason: "harness_unavailable" }));
      await waitFor(() =>
        expect(screen.getByTestId("agent-attention-banner")).toHaveTextContent(
          `Needs attention: the harness is unavailable. ${reason}`,
        ),
      );
      expect(api.getHarness).toHaveBeenCalledWith("w1", harness);
      expect(screen.queryByTestId("harness-version-warning")).toBeNull();
    },
  );

  it("rereads the harness when Attention is raised, to name the reason", async () => {
    await mount(agent());
    const reason =
      "opencode 2.0.1 is below the minimum 2.0.19; upgrade opencode";
    api.getHarness.mockResolvedValue({
      health: { ok: false, warning: `harness_too_old: ${reason}` },
    });
    api.getAgent.mockResolvedValue(
      agent({ attention_reason: "harness_unavailable" }),
    );
    deliver(ev("agent.state_changed"));
    await waitFor(() =>
      expect(screen.getByTestId("agent-attention-banner")).toHaveTextContent(
        `the harness is unavailable. ${reason}`,
      ),
    );
  });

  it.each([
    ["pending", () => new Promise(() => {})],
    ["failed", () => Promise.reject(new Error("down"))],
  ])(
    "shows no earlier reason while the reread for new Attention is %s",
    async (_, reread) => {
      api.getHarness.mockResolvedValue({
        health: {
          ok: false,
          warning:
            "harness_too_old: opencode 2.0.1 is below the minimum 2.0.19; upgrade opencode",
        },
      });
      await mount(agent({ attention_reason: "harness_unavailable" }));
      await screen.findByText(/below the minimum/);
      api.getHarness.mockImplementation(reread);
      api.getAgent.mockResolvedValue(
        agent({ attention_reason: "delivery_unknown" }),
      );
      deliver(ev("agent.state_changed"));
      await screen.findByText(/a message may not have been delivered/);
      api.getAgent.mockResolvedValue(
        agent({ attention_reason: "harness_unavailable" }),
      );
      deliver(ev("agent.state_changed"));
      await waitFor(() =>
        expect(screen.getByTestId("agent-attention-banner")).toHaveTextContent(
          /^Needs attention: the harness is unavailable\.$/,
        ),
      );
    },
  );

  it("names only a version reason, not another failure", async () => {
    api.getHarness.mockResolvedValue({
      health: {
        ok: false,
        warning:
          "harness_unavailable: codex app-server for /other failed 3 times in a row",
      },
    });
    await mount(
      agent({ harness: "codex", attention_reason: "harness_unavailable" }),
    );
    await waitFor(() => expect(api.getHarness).toHaveBeenCalled());
    await act(() => Promise.resolve());
    expect(screen.getByTestId("agent-attention-banner")).toHaveTextContent(
      /^Needs attention: the harness is unavailable\.$/,
    );
  });

  it.each([
    ["opencode", "opencode 2.0.20 is newer than the last tested 2.0.19"],
    ["codex", "codex 0.158.0 is newer than the last tested 0.157.1"],
    ["claude", "claude 2.1.286 is newer than the last tested 2.1.285"],
  ])(
    "warns, without Attention, that %s is newer than tested",
    async (harness, warning) => {
      api.getHarness.mockResolvedValue({
        harness,
        health: { ok: true, warning },
      });
      await mount(agent({ harness }));
      expect(
        await screen.findByTestId("harness-version-warning"),
      ).toHaveTextContent(warning);
      expect(screen.queryByTestId("agent-attention-banner")).toBeNull();
      expect(screen.getByLabelText("Message")).toBeEnabled();
    },
  );

  it("labels create_retrying in plain words", async () => {
    await mount(agent({ attention_reason: "create_retrying" }));
    expect(screen.getByTestId("agent-attention-banner")).toHaveTextContent(
      "Needs attention: retrying start…",
    );
  });

  it("shows history expired in place of the composer once history is purged, with no Unarchive", async () => {
    await mount(
      agent({ state: "archived", archived_at: "t", history_purged_at: "t" }),
    );
    expect(screen.getByTestId("agent-history-expired")).toBeInTheDocument();
    expect(screen.queryByLabelText("Message")).toBeNull();
    expect(screen.queryByTestId("agent-unarchive")).toBeNull();
    expect(screen.queryByTestId("agent-archived-notice")).toBeNull();
  });

  it("shows history expired after a history_expired error", async () => {
    await mount(agent({ state: "archived", archived_at: "t" }));
    api.unarchiveAgent.mockRejectedValue(
      new ApiError(410, "Gone", { error: "a1", code: "history_expired" }),
    );
    fireEvent.click(screen.getByTestId("agent-unarchive"));
    expect(
      await screen.findByTestId("agent-history-expired"),
    ).toBeInTheDocument();
  });

  it("explains a Send over the 1 MiB request limit and keeps the draft", async () => {
    await mount(agent());
    api.sendMessage.mockRejectedValue(
      new ApiError(413, "Payload Too Large", {
        error: "request body too large (max 1MB)",
      }),
    );
    const box = screen.getByLabelText("Message");
    fireEvent.change(box, { target: { value: "big" } });
    fireEvent.submit(box.closest("form")!);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "over the server's 1 MiB request limit",
    );
    expect(box).toHaveValue("big");
  });

  it("shows Agent not found, with no raw id and no composer, when the agent is gone", async () => {
    api.getAgent.mockRejectedValue(
      new ApiError(404, "Not Found", {
        error: "agt_gone",
        code: "agent_not_found",
      }),
    );
    const { container } = render(
      <MemoryRouter>
        <AgentChat workspaceId="w1" agentId="agt_gone" />
      </MemoryRouter>,
    );
    const state = await screen.findByTestId("agent-not-found");
    expect(state).toHaveTextContent("Agent not found");
    expect(state).toHaveTextContent(
      "This agent no longer exists. It may have been deleted.",
    );
    expect(
      within(state).getByRole("link", { name: "Go home" }),
    ).toHaveAttribute("href", "/ws/w1/home");
    expect(container.textContent).not.toContain("agt_gone");
    expect(screen.queryByLabelText("Message")).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("shows This agent was deleted, with no composer, when Get says state=deleted", async () => {
    api.getAgent.mockResolvedValue(agent({ state: "deleted" }));
    render(
      <MemoryRouter>
        <AgentChat workspaceId="w1" agentId="a1" />
      </MemoryRouter>,
    );
    const state = await screen.findByTestId("agent-not-found");
    expect(state).toHaveTextContent("This agent was deleted");
    expect(
      within(state).getByRole("link", { name: "Go home" }),
    ).toHaveAttribute("href", "/ws/w1/home");
    expect(screen.queryByLabelText("Message")).toBeNull();
  });

  it("shows This agent was deleted when its live agent.deleted arrives while open", async () => {
    await mount(agent());
    expect(screen.getByLabelText("Message")).toBeInTheDocument();
    const s = api.streams[api.streams.length - 1];
    act(() => s.opts.onNotice?.({ ...ev("agent.deleted"), seq: 0 }));
    expect(screen.getByTestId("agent-not-found")).toHaveTextContent(
      "This agent was deleted",
    );
    expect(screen.queryByLabelText("Message")).toBeNull();
  });

  it("keeps the chat and its error for a Get failure other than agent_not_found", async () => {
    api.getAgent.mockRejectedValue(
      new ApiError(404, "Not Found", { error: "boom", code: "not_found" }),
    );
    render(
      <MemoryRouter>
        <AgentChat workspaceId="w1" agentId="a1" />
      </MemoryRouter>,
    );
    expect(await screen.findByRole("alert")).toHaveTextContent("boom");
    expect(screen.getByLabelText("Message")).toBeInTheDocument();
    expect(screen.queryByTestId("agent-not-found")).toBeNull();
  });

  it("marks a harness switch with a context divider and keeps the earlier transcript; a failed switch shows none", async () => {
    await mount(agent());
    // A failed switch saves no harness.changed: no divider.
    deliver(
      ev("message.delivered", { text: "before the switch" }),
      ev("agent.turn_completed", { stopReason: "cancelled" }),
      ev("ask.lost", { askId: "k" }),
    );
    expect(screen.queryByTestId("harness-context-divider")).toBeNull();
    deliver(
      ev("harness.changed", { from_harness: "opencode", harness: "codex" }),
      ev("message.delivered", { text: "after the switch" }),
    );
    const divider = screen.getByTestId("harness-context-divider");
    expect(divider).toHaveTextContent("New harness context: opencode → codex");
    expect(divider).not.toHaveTextContent(/closed|ended/i);
    expect(screen.getByText("before the switch")).toBeInTheDocument();
    expect(screen.getByText("after the switch")).toBeInTheDocument();
  });
});
