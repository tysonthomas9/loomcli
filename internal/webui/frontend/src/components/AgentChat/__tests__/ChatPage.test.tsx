/**
 * @vitest-environment jsdom
 */

import { act, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import type { Agent, AgentEvent, AgentStreamOptions } from "@/api/agentsv1";
import { turnStartedAt } from "@/hooks/agents/useAgentChat";

const api = vi.hoisted(() => ({
  getAgent: vi.fn(),
  sendMessage: vi.fn(),
  updateAgent: vi.fn(),
  streams: [] as { opts: AgentStreamOptions; events: AgentEvent[] }[],
  ids: 0,
}));

vi.mock("@/contexts/AuthContext", () => ({
  useAuth: () => ({ user: null }),
}));

vi.mock("@/api/agentsv1", () => ({
  getAgent: api.getAgent,
  sendMessage: api.sendMessage,
  withdrawMessage: vi.fn(),
  respondToAsk: vi.fn(),
  updateAgent: api.updateAgent,
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

import { AgentChat } from "../AgentChat";
import {
  ChatComposer,
  PROMPT_MAX_HEIGHT_PX,
  PROMPT_MIN_HEIGHT_PX,
  sendDisabledReason,
  type ChatComposerProps,
} from "../ChatComposer";
import { ChatHeader, resolveRenameCommit } from "../ChatHeader";
import {
  formatElapsed,
  shouldCollapseUserMessage,
  UserMessage,
} from "../MessageRows";

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

function ev(kind: string, turn: string, at: string): AgentEvent {
  return {
    agent_id: "a1",
    seq: 1,
    event_id: `${kind}:${turn}`,
    kind,
    turn_id: turn,
    payload: {},
    created_at: at,
  };
}

function composer(over: Partial<ChatComposerProps> = {}) {
  const props: ChatComposerProps = {
    draft: "",
    onDraftChange: vi.fn(),
    editing: false,
    sending: false,
    running: false,
    onSubmit: vi.fn(),
    onCancelEdit: vi.fn(),
    onStop: vi.fn(),
    controls: <span>pickers</span>,
    ...over,
  };
  return { props, ...render(<ChatComposer {...props} />) };
}

beforeEach(() => {
  vi.clearAllMocks();
  api.streams = [];
});

describe("ChatComposer (T3's composer card)", () => {
  it("grows the prompt with its text between T3's min and max height", () => {
    const { rerender, props } = composer({ draft: "one line" });
    const input = screen.getByLabelText("Message") as HTMLTextAreaElement;
    // jsdom has no layout: drive scrollHeight by hand.
    let content = 20;
    Object.defineProperty(input, "scrollHeight", {
      configurable: true,
      get: () => content,
    });
    rerender(<ChatComposer {...props} draft="two" />);
    expect(input.style.height).toBe(`${PROMPT_MIN_HEIGHT_PX}px`);
    expect(input.style.overflowY).toBe("hidden");
    content = 120;
    rerender(<ChatComposer {...props} draft={"a\nb\nc\nd"} />);
    expect(input.style.height).toBe("120px");
    content = 900;
    rerender(<ChatComposer {...props} draft={"x\n".repeat(60)} />);
    expect(input.style.height).toBe(`${PROMPT_MAX_HEIGHT_PX}px`);
    expect(input.style.overflowY).toBe("auto");
    // No resize handle: the card owns the height.
    expect(input).toHaveAttribute("rows", "1");
  });

  it("sends on Enter, adds a line on Shift+Enter, and not while composing", () => {
    const { props } = composer({ draft: "hi" });
    const input = screen.getByLabelText("Message");
    fireEvent.keyDown(input, { key: "Enter", shiftKey: true });
    expect(props.onSubmit).not.toHaveBeenCalled();
    fireEvent.keyDown(input, { key: "Enter", isComposing: true });
    expect(props.onSubmit).not.toHaveBeenCalled();
    fireEvent.keyDown(input, { key: "Enter" });
    expect(props.onSubmit).toHaveBeenCalledTimes(1);
  });

  it("is a round Send icon button with its disabled reason as the tooltip", () => {
    composer();
    const send = screen.getByRole("button", { name: "Send" });
    expect(send).toBeDisabled();
    expect(send).toHaveAttribute("title", "Type a message to send");
    expect(send).toHaveAttribute("type", "submit");
    expect(screen.queryByRole("button", { name: "Stop" })).toBeNull();
    expect(screen.getByText("pickers")).toBeInTheDocument();
  });

  it("shows the sending state as busy and disabled", () => {
    composer({ draft: "hi", sending: true });
    const send = screen.getByRole("button", { name: "Send" });
    expect(send).toBeDisabled();
    expect(send).toHaveAttribute("aria-busy", "true");
    expect(send).toHaveAttribute("title", "Sending…");
  });

  it("swaps Send for Stop while a turn runs, and brings Send back once there is text", () => {
    const { props, rerender } = composer({ running: true });
    expect(screen.queryByRole("button", { name: "Send" })).toBeNull();
    const stop = screen.getByRole("button", { name: "Stop" });
    // The AFT reads the button's text, so it stays "Stop".
    expect(stop.textContent).toBe("Stop");
    fireEvent.click(stop);
    expect(props.onStop).toHaveBeenCalledTimes(1);
    rerender(<ChatComposer {...props} draft="queued" />);
    expect(screen.getByRole("button", { name: "Stop" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Send" })).toBeEnabled();
  });

  it("saves and cancels while editing the waiting message", () => {
    const { props } = composer({ draft: "mine", editing: true });
    expect(screen.getByLabelText("Message")).toHaveAttribute(
      "placeholder",
      "Edit your waiting message",
    );
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(props.onCancelEdit).toHaveBeenCalledTimes(1);
    fireEvent.submit(screen.getByRole("button", { name: "Save" }));
    expect(props.onSubmit).toHaveBeenCalledTimes(1);
  });

  it("names its disabled reasons", () => {
    expect(sendDisabledReason({ draft: " ", sending: false })).toBe(
      "Type a message to send",
    );
    expect(sendDisabledReason({ draft: "x", sending: true })).toBe("Sending…");
    expect(sendDisabledReason({ draft: "x", sending: false })).toBeNull();
  });
});

describe("ChatHeader (T3's title with inline rename)", () => {
  const rename = () => vi.fn(() => Promise.resolve());

  it("renames on Enter through onRename, trimmed", () => {
    const onRename = rename();
    render(<ChatHeader agentId="a1" agent={agent()} onRename={onRename} />);
    fireEvent.click(screen.getByRole("button", { name: "Rename agent" }));
    const input = screen.getByLabelText("Agent name");
    expect(input).toHaveValue("lead");
    fireEvent.change(input, { target: { value: "  new name " } });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(onRename).toHaveBeenCalledWith("new name");
    expect(screen.queryByLabelText("Agent name")).toBeNull();
  });

  it("starts on double-click; Escape cancels and blur commits", () => {
    const onRename = rename();
    render(<ChatHeader agentId="a1" agent={agent()} onRename={onRename} />);
    fireEvent.doubleClick(screen.getByRole("heading", { name: "lead" }));
    let input = screen.getByLabelText("Agent name");
    fireEvent.change(input, { target: { value: "dropped" } });
    fireEvent.keyDown(input, { key: "Escape" });
    expect(onRename).not.toHaveBeenCalled();
    fireEvent.doubleClick(screen.getByRole("heading", { name: "lead" }));
    input = screen.getByLabelText("Agent name");
    fireEvent.change(input, { target: { value: "on blur" } });
    fireEvent.blur(input);
    expect(onRename).toHaveBeenCalledWith("on blur");
  });

  it("sends nothing for an empty or unchanged name", () => {
    expect(resolveRenameCommit({ title: "  ", originalTitle: "a" })).toEqual({
      action: "reject-empty",
    });
    expect(resolveRenameCommit({ title: " a ", originalTitle: "a" })).toEqual({
      action: "noop",
    });
    const onRename = rename();
    render(<ChatHeader agentId="a1" agent={agent()} onRename={onRename} />);
    fireEvent.click(screen.getByRole("button", { name: "Rename agent" }));
    const input = screen.getByLabelText("Agent name");
    fireEvent.change(input, { target: { value: "" } });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(onRename).not.toHaveBeenCalled();
  });

  it("shows the harness label and the state pill, and no rename before the agent loads", () => {
    const { rerender } = render(
      <ChatHeader agentId="a1" agent={null} onRename={rename()} />,
    );
    expect(screen.getByRole("heading", { name: "a1" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Rename agent" })).toBeNull();
    rerender(
      <ChatHeader
        agentId="a1"
        agent={agent({ harness: "codex", state: "active" })}
        onRename={rename()}
      />,
    );
    expect(screen.getByTestId("harness-label")).toHaveTextContent("codex");
    expect(screen.getByRole("banner")).toHaveTextContent("active");
  });
});

describe("Messages (T3's user bubble and working row)", () => {
  it("folds a long user message until Show full message", () => {
    const text = Array.from({ length: 12 }, (_, i) => `line ${i}`).join("\n");
    expect(shouldCollapseUserMessage(text)).toBe(true);
    expect(shouldCollapseUserMessage("short")).toBe(false);
    expect(shouldCollapseUserMessage("x".repeat(601))).toBe(true);
    const { container } = render(<UserMessage text={text} />);
    const body = container.querySelector("[data-user-message-collapsed]");
    expect(body).toHaveAttribute("data-user-message-collapsed", "true");
    fireEvent.click(screen.getByRole("button", { name: "Show full message" }));
    expect(body).toHaveAttribute("data-user-message-collapsed", "false");
    expect(screen.getByRole("button", { name: "Show less" })).toHaveAttribute(
      "aria-expanded",
      "true",
    );
    expect(
      screen.getByRole("button", { name: "Copy your message" }),
    ).toBeInTheDocument();
  });

  it("formats the working time like T3", () => {
    const t0 = Date.parse("2026-10-03T00:00:00Z");
    const at = (s: number) =>
      formatElapsed("2026-10-03T00:00:00Z", t0 + s * 1000);
    expect(at(5)).toBe("5s");
    expect(at(60)).toBe("1m");
    expect(at(65)).toBe("1m 5s");
    expect(at(3600)).toBe("1h");
    expect(at(3720)).toBe("1h 2m");
    expect(formatElapsed("not a date")).toBe("0s");
  });

  it("finds the running turn's saved start", () => {
    const events = [
      ev("turn.started", "t1", "2026-10-03T00:00:00Z"),
      ev("turn.started", "t2", "2026-10-03T00:01:00Z"),
    ];
    expect(turnStartedAt(events, "t2")).toBe("2026-10-03T00:01:00Z");
    expect(turnStartedAt(events, "t3")).toBeNull();
    expect(turnStartedAt(events, null)).toBeNull();
  });
});

describe("AgentChat page", () => {
  async function mount(a: Agent) {
    api.getAgent.mockResolvedValue(a);
    const view = render(
      <MemoryRouter>
        <AgentChat workspaceId="w1" agentId="a1" />
      </MemoryRouter>,
    );
    await screen.findByRole("heading", { name: a.name });
    return view;
  }

  it("shows Working for Ns from the running turn's saved start", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      vi.setSystemTime(Date.parse("2026-10-03T00:00:07Z"));
      await mount(agent({ running_turn_id: "t1", state: "active" }));
      expect(screen.getByTestId("working-row")).toHaveTextContent("Working...");
      const s = api.streams[api.streams.length - 1];
      const started = ev("turn.started", "t1", "2026-10-03T00:00:00Z");
      s.events.push(started);
      act(() => s.opts.onEvents?.([started]));
      expect(screen.getByTestId("working-row")).toHaveTextContent(
        "Working for 7s",
      );
    } finally {
      vi.useRealTimers();
    }
  });

  it("shows no working row while idle, and the empty prompt", async () => {
    await mount(agent());
    expect(screen.queryByTestId("working-row")).toBeNull();
    expect(
      screen.getByText("Send a message to start the conversation."),
    ).toBeInTheDocument();
  });

  it("renames the agent with PATCH name", async () => {
    api.updateAgent.mockResolvedValue({});
    await mount(agent());
    fireEvent.click(screen.getByRole("button", { name: "Rename agent" }));
    const input = screen.getByLabelText("Agent name");
    fireEvent.change(input, { target: { value: "renamed" } });
    fireEvent.keyDown(input, { key: "Enter" });
    await vi.waitFor(() =>
      expect(api.updateAgent).toHaveBeenCalledWith(
        "w1",
        "a1",
        { name: "renamed" },
        expect.any(String),
      ),
    );
  });

  it("keeps the header first in the chat section (the AFT reads its text)", async () => {
    const { container } = await mount(agent({ state: "idle" }));
    const header = container.querySelector(
      'section[aria-label="Agent chat"] header',
    );
    expect(header?.textContent).toContain("lead");
    expect(header?.textContent).toContain("idle");
  });
});
