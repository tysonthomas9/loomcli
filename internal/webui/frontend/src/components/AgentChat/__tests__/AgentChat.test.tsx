/**
 * @vitest-environment jsdom
 */

import { act, fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import type { Agent, AgentEvent, AgentStreamOptions } from "@/api/agentsv1";
import { ApiError } from "@/types/common";

const api = vi.hoisted(() => ({
  getAgent: vi.fn(),
  sendMessage: vi.fn(),
  withdrawMessage: vi.fn(),
  respondToAsk: vi.fn(),
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

import { AgentChat } from "../AgentChat";
import { LONG_TEXT_LIMIT } from "../LongText";

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

async function mount(a: Agent) {
  api.getAgent.mockResolvedValue(a);
  const view = render(<AgentChat workspaceId="w1" agentId="a1" />);
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
  api.streams = [];
  api.user = null;
  seq = 0;
});

describe("AgentChat", () => {
  it("renders untrusted text as text, never as markup", async () => {
    const { container } = await mount(agent());
    deliver(...fixture());
    expect(screen.getByText(XSS)).toBeInTheDocument();
    expect(container.querySelector("img, script")).toBeNull();
    expect((window as { pwned?: number }).pwned).toBeUndefined();
  });

  it("cuts a very long message until the user expands it", async () => {
    await mount(agent());
    const long = "x".repeat(LONG_TEXT_LIMIT + 10);
    deliver(ev("item.completed", { itemKind: "message", text: long }));
    expect(screen.queryByText(long)).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /Show all/ }));
    expect(screen.getByText(long)).toBeInTheDocument();
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
    fireEvent.click(screen.getByRole("button", { name: "Allow once" }));
    fireEvent.click(screen.getByRole("button", { name: "Deny" }));
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

  it("answers a question, and hides a card answered elsewhere", async () => {
    api.respondToAsk.mockRejectedValue(
      new ApiError(404, "Not Found", { error: "x", code: "ask_not_found" }),
    );
    await mount(
      agent({ open_asks: [{ id: "Q1", type: "question", about: "Which?" }] }),
    );
    fireEvent.change(screen.getByLabelText("Answer"), {
      target: { value: "B" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Answer" }));
    await vi.waitFor(() => expect(screen.queryByTestId("ask-card")).toBeNull());
    expect(api.respondToAsk.mock.calls[0][3]).toEqual({ answer: "B" });
    expect(screen.queryByRole("alert")).toBeNull();
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
    expect(screen.getByText("Hello")).toBeInTheDocument();
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
});
