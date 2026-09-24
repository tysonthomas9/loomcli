/**
 * @vitest-environment jsdom
 */

/**
 * BrowserTab / useAgentBrowsers: durable browser inventory for interactive
 * agents. The API module and the SSE event context are module-mocked; the
 * real useAgentBrowsers hook runs.
 */

import { act, fireEvent, render, screen } from "@testing-library/react";
import "@testing-library/jest-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { AgentBrowser, MutationPayload } from "@/types";

import { AgentBrowserTabs } from "./BrowserTab";

const api = vi.hoisted(() => ({
  list: vi.fn(),
  select: vi.fn(),
  lost: new Set<(workspace: string) => void>(),
}));

const events = vi.hoisted(() => ({
  subscribers: new Set<{
    cb: { current: (m: MutationPayload) => void };
    entityTypes: string[] | undefined;
  }>(),
  state: "connected" as string,
}));

vi.mock("@/api/agents/browsers", async () => {
  const actual = await vi.importActual<typeof import("@/api/agents/browsers")>(
    "@/api/agents/browsers",
  );
  return {
    BrowserApiError: actual.BrowserApiError,
    listAgentBrowsers: api.list,
    selectAgentBrowser: api.select,
    onBrowserOperatorSessionLost: (listener: (ws: string) => void) => {
      api.lost.add(listener);
      return () => api.lost.delete(listener);
    },
  };
});

vi.mock("@/hooks/common", async () => {
  const React = await vi.importActual<typeof import("react")>("react");
  return {
    useEventSubscription: (
      callback: (m: MutationPayload) => void,
      options?: { entityTypes?: string[] },
    ) => {
      const ref = React.useRef(callback);
      ref.current = callback;
      const key = options?.entityTypes?.join(",") ?? "";
      React.useEffect(() => {
        const entry = {
          cb: ref,
          entityTypes: key ? key.split(",") : undefined,
        };
        events.subscribers.add(entry);
        return () => {
          events.subscribers.delete(entry);
        };
      }, [key]);
    },
    useEventContext: () => ({ state: events.state }),
  };
});

vi.mock("@/hooks", async () => {
  const actual = await vi.importActual<
    typeof import("@/hooks/agents/useAgentBrowsers")
  >("@/hooks/agents/useAgentBrowsers");
  return { useAgentBrowsers: actual.useAgentBrowsers };
});

const { BrowserApiError } = await vi.importActual<
  typeof import("@/api/agents/browsers")
>("@/api/agents/browsers");

function browser(overrides: Partial<AgentBrowser> = {}): AgentBrowser {
  return {
    id: "11111111-1111-4111-8111-111111111111",
    workspace_key: "E2E",
    owner_agent_id: "lead",
    created_by: "lead",
    name: "Research",
    desired_state: "running",
    status: "starting",
    request_id: "req-1",
    selected: false,
    created_at: "2026-09-23T10:00:00Z",
    updated_at: "2026-09-23T10:00:00Z",
    ...overrides,
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (err: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function emit(mutation: Partial<MutationPayload>) {
  const full = {
    type: "create",
    timestamp: new Date().toISOString(),
    ...mutation,
  } as MutationPayload;
  act(() => {
    for (const sub of events.subscribers) {
      if (
        sub.entityTypes &&
        !sub.entityTypes.includes(full.entity_type ?? "")
      ) {
        continue;
      }
      sub.cb.current(full);
    }
  });
}

function renderTabs(
  props: Partial<{
    agentName: string;
    workspaceId: string;
    enabled: boolean;
  }> = {},
) {
  const view = (p: typeof props) => (
    <AgentBrowserTabs
      enabled={p.enabled ?? true}
      workspaceId={p.workspaceId ?? "E2E"}
      agentName={p.agentName ?? "lead"}
    >
      <textarea data-testid="terminal-input" aria-label="terminal" />
    </AgentBrowserTabs>
  );
  const result = render(view(props));
  return {
    ...result,
    rerenderWith: (next: typeof props) => result.rerender(view(next)),
  };
}

function browserTabs() {
  return screen
    .queryAllByRole("tab")
    .filter((tab) => tab.hasAttribute("data-browser-id"));
}

beforeEach(() => {
  api.list.mockReset();
  api.select.mockReset();
  api.lost.clear();
  events.subscribers.clear();
  events.state = "connected";
});

afterEach(() => {
  vi.useRealTimers();
});

describe("AgentBrowserTabs rendering", () => {
  it("renders Starting and Failed tabs next to the Terminal tab", async () => {
    api.list.mockResolvedValue([
      browser(),
      browser({
        id: "22222222-2222-4222-8222-222222222222",
        name: "Docs",
        status: "failed",
      }),
    ]);
    renderTabs();

    expect(
      await screen.findByRole("tab", { name: "Research · Starting" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("tab", { name: "Docs · Failed" }),
    ).toBeInTheDocument();
    const tabs = screen.getAllByRole("tab");
    expect(tabs[0]).toHaveTextContent("Terminal");
    expect(tabs[0]).toHaveAttribute("aria-selected", "true");
    expect(api.list).toHaveBeenCalledWith("E2E", "lead", expect.anything());
  });

  it("renders any other status as an explicit Unknown status, never Ready", async () => {
    api.list.mockResolvedValue([
      browser({ status: "ready" }),
      browser({
        id: "33333333-3333-4333-8333-333333333333",
        name: "Other",
        status: "paused",
      }),
    ]);
    renderTabs();

    expect(
      await screen.findByRole("tab", { name: "Research · Unknown status" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("tab", { name: "Other · Unknown status" }),
    ).toBeInTheDocument();
    expect(screen.queryByText(/ready/i)).not.toBeInTheDocument();

    fireEvent.click(
      screen.getByRole("tab", { name: "Research · Unknown status" }),
    );
    const pane = screen.getByTestId("browser-pane");
    expect(pane).not.toHaveTextContent(/ready/i);
    expect(pane.querySelector("[data-status-tone='unknown']")).not.toBeNull();
  });

  it("shows an empty state when the agent has no browsers", async () => {
    api.list.mockResolvedValue([]);
    renderTabs();
    expect(await screen.findByText("No browsers")).toBeInTheDocument();
    expect(browserTabs()).toHaveLength(0);
  });

  it("shows a visible error and no entries on a FleetDB outage", async () => {
    api.list.mockRejectedValue(
      new BrowserApiError(503, "browser_unavailable", "FleetDB unreachable"),
    );
    renderTabs();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("FleetDB unreachable");
    expect(alert).toHaveAttribute("data-error-code", "browser_unavailable");
    expect(browserTabs()).toHaveLength(0);
  });

  it("shows the Loom Desktop state when there is no native bridge", async () => {
    api.list.mockRejectedValue(
      new BrowserApiError(
        401,
        "browser_desktop_required",
        "Open this workspace in Loom Desktop to view agent browsers",
      ),
    );
    renderTabs();
    expect(
      await screen.findByText(
        "Open this workspace in Loom Desktop to view agent browsers",
      ),
    ).toBeInTheDocument();
    expect(browserTabs()).toHaveLength(0);
  });

  it("renders nothing and fetches nothing when disabled (worker agents)", () => {
    renderTabs({ enabled: false });
    expect(screen.queryByRole("tablist")).not.toBeInTheDocument();
    expect(screen.getByTestId("terminal-input")).toBeInTheDocument();
    expect(api.list).not.toHaveBeenCalled();
  });

  it("shows durable fields in the browser pane", async () => {
    api.list.mockResolvedValue([browser({ selected: true })]);
    api.select.mockResolvedValue(browser({ selected: true }));
    renderTabs();
    fireEvent.click(
      await screen.findByRole("tab", { name: "Research · Starting" }),
    );
    const pane = await screen.findByTestId("browser-pane");
    expect(pane).toHaveTextContent("11111111-1111-4111-8111-111111111111");
    expect(pane).toHaveTextContent("Starting");
    expect(pane).toHaveTextContent("Owner");
    expect(pane).toHaveTextContent("Created by");
    expect(pane).toHaveTextContent(/live page is not available/i);
    // Terminal stays mounted underneath the pane.
    expect(screen.getByTestId("terminal-input")).toBeInTheDocument();
  });
});

describe("AgentBrowserTabs state safety", () => {
  it("clears the inventory when a later request fails with 401", async () => {
    api.list.mockResolvedValueOnce([browser()]);
    renderTabs();
    expect(
      await screen.findByRole("tab", { name: "Research · Starting" }),
    ).toBeInTheDocument();

    api.list.mockRejectedValueOnce(
      new BrowserApiError(
        401,
        "browser_operator_session_expired",
        "Operator session expired",
      ),
    );
    emit({ entity_type: "browser", entity_id: "lead", type: "update" });

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Operator session expired",
    );
    expect(browserTabs()).toHaveLength(0);
    expect(screen.queryByText("Research")).not.toBeInTheDocument();
  });

  it("clears immediately when the operator session is lost", async () => {
    api.list.mockResolvedValueOnce([browser()]);
    renderTabs();
    await screen.findByRole("tab", { name: "Research · Starting" });

    const pending = deferred<AgentBrowser[]>();
    api.list.mockReturnValueOnce(pending.promise);
    act(() => {
      for (const listener of api.lost) listener("E2E");
    });
    expect(browserTabs()).toHaveLength(0);
    expect(screen.getByText("Loading browsers…")).toBeInTheDocument();

    await act(async () => pending.resolve([]));
    expect(await screen.findByText("No browsers")).toBeInTheDocument();
  });

  it("clears A's inventory immediately when switching to agent B", async () => {
    api.list.mockResolvedValueOnce([browser({ name: "A-browser" })]);
    const view = renderTabs({ agentName: "lead-a" });
    await screen.findByRole("tab", { name: "A-browser · Starting" });

    const pendingB = deferred<AgentBrowser[]>();
    api.list.mockReturnValueOnce(pendingB.promise);
    view.rerenderWith({ agentName: "lead-b" });

    expect(screen.queryByText("A-browser")).not.toBeInTheDocument();
    expect(browserTabs()).toHaveLength(0);

    await act(async () =>
      pendingB.resolve([
        browser({
          id: "44444444-4444-4444-8444-444444444444",
          name: "B-browser",
          owner_agent_id: "lead-b",
        }),
      ]),
    );
    expect(
      await screen.findByRole("tab", { name: "B-browser · Starting" }),
    ).toBeInTheDocument();
  });

  it("resolves out-of-order A/B responses to B (A settles last)", async () => {
    const pendingA = deferred<AgentBrowser[]>();
    const pendingB = deferred<AgentBrowser[]>();
    api.list.mockReturnValueOnce(pendingA.promise);
    api.list.mockReturnValueOnce(pendingB.promise);
    const view = renderTabs({ agentName: "lead-a" });
    view.rerenderWith({ agentName: "lead-b" });

    await act(async () => pendingB.resolve([browser({ name: "B-browser" })]));
    await act(async () => pendingA.resolve([browser({ name: "A-browser" })]));

    expect(
      screen.getByRole("tab", { name: "B-browser · Starting" }),
    ).toBeInTheDocument();
    expect(screen.queryByText("A-browser")).not.toBeInTheDocument();
  });

  it("resolves out-of-order A/B responses to B (A settles first)", async () => {
    const pendingA = deferred<AgentBrowser[]>();
    const pendingB = deferred<AgentBrowser[]>();
    api.list.mockReturnValueOnce(pendingA.promise);
    api.list.mockReturnValueOnce(pendingB.promise);
    const view = renderTabs({ agentName: "lead-a" });
    view.rerenderWith({ agentName: "lead-b" });

    await act(async () => pendingA.resolve([browser({ name: "A-browser" })]));
    expect(screen.queryByText("A-browser")).not.toBeInTheDocument();
    await act(async () =>
      pendingB.reject(new BrowserApiError(503, "browser_unavailable", "down")),
    );

    expect(screen.queryByText("A-browser")).not.toBeInTheDocument();
    expect(await screen.findByRole("alert")).toHaveTextContent("down");
  });
});

describe("AgentBrowserTabs realtime + select", () => {
  it("adds a tab on an SSE browser mutation without leaving the Terminal", async () => {
    api.list.mockResolvedValueOnce([]);
    renderTabs();
    await screen.findByText("No browsers");

    const terminal = screen.getByTestId("terminal-input");
    terminal.focus();
    expect(terminal).toHaveFocus();

    api.list.mockResolvedValueOnce([browser()]);
    emit({ entity_type: "browser", entity_id: "lead", workspace_id: "E2E" });

    expect(
      await screen.findByRole("tab", { name: "Research · Starting" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Terminal" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(screen.queryByTestId("browser-pane")).not.toBeInTheDocument();
    expect(terminal).toHaveFocus();
    expect(api.list).toHaveBeenCalledTimes(2);
  });

  it("ignores SSE browser mutations for another agent or workspace", async () => {
    api.list.mockResolvedValue([]);
    renderTabs();
    await screen.findByText("No browsers");

    emit({ entity_type: "browser", entity_id: "someone-else" });
    emit({ entity_type: "browser", entity_id: "lead", workspace_id: "OTHER" });
    emit({ entity_type: "agent", entity_id: "lead" });

    expect(api.list).toHaveBeenCalledTimes(1);
  });

  it("refetches on SSE reconnect and window focus", async () => {
    api.list.mockResolvedValue([]);
    const view = renderTabs();
    await screen.findByText("No browsers");

    events.state = "reconnecting";
    view.rerenderWith({});
    events.state = "connected";
    view.rerenderWith({});
    expect(api.list).toHaveBeenCalledTimes(2);

    act(() => {
      window.dispatchEvent(new Event("focus"));
    });
    expect(api.list).toHaveBeenCalledTimes(3);
  });

  it("selects exactly one browser via POST and refetches the durable state", async () => {
    const first = browser({ selected: true });
    const second = browser({
      id: "55555555-5555-4555-8555-555555555555",
      name: "Second",
    });
    api.list.mockResolvedValueOnce([first, second]);
    renderTabs();
    await screen.findByRole("tab", { name: "Second · Starting" });

    api.select.mockResolvedValueOnce({ ...second, selected: true });
    api.list.mockResolvedValueOnce([
      { ...first, selected: false },
      { ...second, selected: true },
    ]);
    fireEvent.click(screen.getByRole("tab", { name: "Second · Starting" }));

    expect(api.select).toHaveBeenCalledTimes(1);
    expect(api.select).toHaveBeenCalledWith("E2E", "lead", second.id);
    await vi.waitFor(() => expect(api.list).toHaveBeenCalledTimes(2));

    const pane = await screen.findByTestId("browser-pane");
    await vi.waitFor(() => expect(pane).toHaveTextContent(/Selected\s*Yes/));
    const refetched = (await api.list.mock.results[1]!.value) as AgentBrowser[];
    expect(refetched.filter((b) => b.selected)).toHaveLength(1);
    expect(
      screen.getByRole("tab", { name: "Second · Starting" }),
    ).toHaveAttribute("aria-selected", "true");
  });

  it("clears the inventory when select fails authentication", async () => {
    api.list.mockResolvedValueOnce([browser()]);
    renderTabs();
    const tab = await screen.findByRole("tab", { name: "Research · Starting" });
    api.select.mockRejectedValueOnce(
      new BrowserApiError(401, "browser_operator_session_required", "Sign in"),
    );
    fireEvent.click(tab);
    await vi.waitFor(() => expect(browserTabs()).toHaveLength(0));
    expect(screen.queryByTestId("browser-pane")).not.toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Terminal" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
  });
});
