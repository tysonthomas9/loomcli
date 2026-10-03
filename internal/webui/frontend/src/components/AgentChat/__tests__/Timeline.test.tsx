/**
 * @vitest-environment jsdom
 */

import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import type { Agent, AgentEvent, AgentStreamOptions } from "@/api/agentsv1";

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
  updateAgent: () => Promise.resolve({}),
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
  const view = render(
    <MemoryRouter>
      <AgentChat workspaceId="w1" agentId="a1" />
    </MemoryRouter>,
  );
  await screen.findByText(a.name);
  return view;
}

// Pushes a live notice (a delta or tool start) through the chat's stream.
function notice(kind: string, payload: object) {
  const s = api.streams[api.streams.length - 1];
  act(() => s.opts.onNotice?.({ ...ev(kind, payload), seq: 0, event_id: "" }));
}

const writeText = vi.fn(() => Promise.resolve());

beforeEach(() => {
  vi.clearAllMocks();
  api.streams = [];
  seq = 0;
  Object.defineProperty(navigator, "clipboard", {
    value: { writeText },
    configurable: true,
  });
});

const TABLE = "| Name | Size |\n| --- | ---: |\n| a.go | 10 |\n| b.go | 20 |";
const CODE = "```go\nfunc main() {\n\treturn\n}\n```";

describe("AgentChat timeline (UI3)", () => {
  it("renders a markdown table as a table that copies as markdown", async () => {
    await mount(agent());
    deliver(ev("item.completed", { itemKind: "message", text: TABLE }));
    const table = screen.getByRole("table");
    expect(table.querySelectorAll("tbody tr")).toHaveLength(2);
    expect(screen.queryByText(/\| Name \|/)).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Copy as Markdown" }));
    expect(writeText).toHaveBeenCalledWith(
      "| Name | Size |\n| --- | ---: |\n| a.go | 10 |\n| b.go | 20 |",
    );
  });

  it("renders a fenced code block with its language, highlighting and copy", async () => {
    await mount(agent());
    deliver(ev("item.completed", { itemKind: "message", text: CODE }));
    const block = screen.getByTestId("chat-codeblock");
    expect(block).toHaveAttribute("data-language", "go");
    expect(block).toHaveTextContent("func main()");
    // The go grammar loads, then the keyword is its own highlighted run.
    await vi.waitFor(() =>
      expect(within(block).getByText("func").tagName).toBe("SPAN"),
    );
    fireEvent.click(within(block).getByRole("button", { name: "Copy code" }));
    expect(writeText).toHaveBeenCalledWith("func main() {\n\treturn\n}");
  });

  it("copies a finished agent message", async () => {
    await mount(agent());
    deliver(ev("item.completed", { itemKind: "message", text: "**done**" }));
    expect(screen.getByText("done").tagName).toBe("STRONG");
    // The copy action is an icon: the message's text is the message only.
    const copy = screen.getByRole("button", { name: "Copy message" });
    expect(copy).toHaveTextContent(/^$/);
    expect(copy.closest("li")).toHaveTextContent(/^done$/);
    fireEvent.click(copy);
    expect(writeText).toHaveBeenCalledWith("**done**");
  });

  it("groups tool calls under a summary that expands to each call's input and output", async () => {
    await mount(agent());
    deliver(
      ev("item.completed", {
        itemId: "m/tool/1",
        itemKind: "tool",
        tool: { name: "bash", input: '{"command":"ls -la"}', output: "a.go" },
      }),
      ev("item.completed", {
        itemId: "m/tool/2",
        itemKind: "tool",
        tool: {
          name: "read",
          input: '{"filePath":"x.go"}',
          output: "no such file",
          failed: true,
        },
      }),
      ev("item.completed", { itemKind: "message", text: "done" }),
    );
    const group = screen.getByTestId("tool-group");
    expect(group).toHaveTextContent("Ran 1 command and read 1 file");
    expect(group).toHaveTextContent("Tool calls (2)");
    expect(group).toHaveAccessibleName(
      "Ran 1 command and read 1 file, 2 tool calls, tool call failed",
    );
    expect(screen.queryByTestId("tool-call")).toBeNull();

    fireEvent.click(group);
    const [bash, read] = screen.getAllByTestId("tool-call");
    expect(bash).toHaveTextContent("Bashls -la");
    expect(read).toHaveAttribute("data-status", "failed");
    expect(
      within(read!).getByRole("img", { name: "Tool call failed" }),
    ).toBeTruthy();

    fireEvent.click(within(bash!).getByRole("button"));
    expect(bash).toHaveTextContent("Input");
    expect(bash!.querySelector("pre")).toHaveTextContent('"command": "ls -la"');
    expect(bash).toHaveTextContent("Outputa.go");
    fireEvent.click(within(read!).getByRole("button"));
    expect(read).toHaveTextContent("Errorno such file");
  });

  it("shows a running tool and live reasoning until they complete", async () => {
    await mount(agent({ running_turn_id: "t1" }));
    notice("delta", { itemId: "r", itemKind: "reasoning", text: "let me see" });
    expect(screen.getByTestId("thinking")).toHaveTextContent("Thinking");
    notice("tool.started", {
      itemId: "m/tool/1",
      itemKind: "tool",
      tool: { name: "bash", input: '{"command":"sleep 5"}' },
    });
    expect(screen.getByTestId("tool-live")).toHaveTextContent("Bash sleep 5");

    deliver(
      ev("item.completed", {
        itemId: "r",
        itemKind: "reasoning",
        text: "let me see",
      }),
      ev("item.completed", {
        itemId: "m/tool/1",
        itemKind: "tool",
        tool: { name: "bash", input: '{"command":"sleep 5"}', output: "" },
      }),
    );
    expect(screen.queryByTestId("thinking")).toBeNull();
    expect(screen.queryByTestId("tool-live")).toBeNull();
    // Reasoning then a tool call: the last entry shows, the rest folds.
    expect(screen.getByTestId("tool-call")).toHaveTextContent("Bashsleep 5");
    const more = screen.getByTestId("work-toggle");
    expect(more).toHaveTextContent("+1 previous log entry");
    fireEvent.click(more);
    const reasoning = screen.getByTestId("reasoning");
    fireEvent.click(within(reasoning).getByRole("button"));
    expect(reasoning.querySelector("pre")).toHaveTextContent("let me see");
  });

  it("keeps a completed tool completed when its start notice comes late", async () => {
    await mount(agent({ running_turn_id: "t1" }));
    deliver(
      ev("item.completed", {
        itemId: "m/tool/1",
        itemKind: "tool",
        tool: { name: "bash", input: '{"command":"ls"}', output: "a.go" },
      }),
    );
    notice("tool.started", {
      itemId: "m/tool/1",
      itemKind: "tool",
      tool: { name: "bash", input: '{"command":"ls"}' },
    });
    expect(screen.queryByTestId("tool-live")).toBeNull();
    expect(screen.getByTestId("tool-group")).toHaveTextContent("Ran 1 command");
  });

  it("previews reasoning as plain text and leads '+N previous' with its chevron", async () => {
    await mount(agent());
    deliver(
      ev("item.completed", {
        itemId: "r",
        itemKind: "reasoning",
        text: "**Planning the `ls` call**\n\nThen read it.",
      }),
      ev("item.completed", {
        itemId: "m/tool/1",
        itemKind: "tool",
        tool: { name: "bash", input: '{"command":"ls"}', output: "a.go" },
      }),
    );
    const more = screen.getByTestId("work-toggle");
    // Real models (gpt-5.5 on OpenCode) title reasoning in bold: the
    // preview drops the marks; the chevron sits before the label.
    expect(more.firstElementChild).toHaveAttribute("data-lead", "true");
    fireEvent.click(more);
    const row = within(screen.getByTestId("reasoning")).getByRole("button");
    expect(row).toHaveTextContent("ThinkingPlanning the ls call");
    expect(row).not.toHaveTextContent("*");
  });
});
