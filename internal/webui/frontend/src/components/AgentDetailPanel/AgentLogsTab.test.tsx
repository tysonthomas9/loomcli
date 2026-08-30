/**
 * @vitest-environment jsdom
 */

import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import { AgentLogsTab } from "./AgentLogsTab";

const getAgentTerminalInfo = vi.fn();
const ensureAgentTerminalSession = vi.fn();
const useLogStream = vi.fn();

vi.mock("@/hooks/api", () => ({
  getAgentTerminalInfo: (...args: unknown[]) => getAgentTerminalInfo(...args),
  ensureAgentTerminalSession: (...args: unknown[]) =>
    ensureAgentTerminalSession(...args),
}));

vi.mock("@/hooks/terminal/useLogStream", () => ({
  useLogStream: (...args: unknown[]) => useLogStream(...args),
}));

vi.mock("@/hooks/workspace", () => ({
  useWorkspaceContext: () => ({ workspaceId: "default" }),
}));

vi.mock("@/components/EmbeddedTerminal", () => ({
  EmbeddedTerminal: () => <div data-testid="embedded-terminal" />,
}));

function statusEl(): Element | null {
  return screen.getByTestId("log-viewer").querySelector("[data-state]");
}

describe("AgentLogsTab", () => {
  beforeEach(() => {
    getAgentTerminalInfo.mockReset();
    ensureAgentTerminalSession.mockReset();
    useLogStream.mockReset();
    useLogStream.mockReturnValue({
      content: "",
      state: "disconnected",
      error: null,
    });
  });

  it("shows an explicit empty state when a connected live stream has no bytes", async () => {
    getAgentTerminalInfo.mockResolvedValue("archive");
    useLogStream.mockReturnValue({
      content: "",
      state: "connected",
      error: null,
    });

    render(<AgentLogsTab agentName="ember" isActive />);

    const empty = await screen.findByTestId("archive-empty");
    expect(empty).toHaveTextContent(/no logs available/i);
    expect(statusEl()).toHaveAttribute("data-state", "empty");
    expect(screen.queryByTestId("terminal-container")).toBeNull();
  });

  it("uses the live stream for visible archive-mode agents", async () => {
    getAgentTerminalInfo.mockResolvedValue("archive");
    useLogStream.mockReturnValue({
      content: "alpha\nbeta",
      state: "connected",
      error: null,
    });

    render(<AgentLogsTab agentName="ember" isActive />);

    const pre = await screen.findByTestId("terminal-container");
    expect(pre).toHaveTextContent("alpha");
    expect(pre).toHaveTextContent("beta");
    expect(screen.getByRole("heading", { name: "Live log" })).toBeVisible();
    await waitFor(() =>
      expect(useLogStream).toHaveBeenLastCalledWith({
        workspaceId: "default",
        streamPath: "/agents/ember/logs/stream",
        enabled: true,
      }),
    );
  });

  it("does not stream while the tab is hidden", () => {
    render(<AgentLogsTab agentName="ember" isActive={false} />);

    expect(getAgentTerminalInfo).not.toHaveBeenCalled();
    expect(useLogStream).toHaveBeenLastCalledWith({
      workspaceId: "default",
      streamPath: "/agents/ember/logs/stream",
      enabled: false,
    });
  });

  it("keeps tmux preferred when a session exists", async () => {
    getAgentTerminalInfo.mockResolvedValue("tmux");
    ensureAgentTerminalSession.mockResolvedValue({
      session_name: "loom-ember",
      backend: "codex",
      agent_id: "ember",
    });

    render(<AgentLogsTab agentName="ember" isActive />);

    expect(await screen.findByTestId("embedded-terminal")).toBeVisible();
    expect(
      screen.getByRole("heading", { name: "Live terminal" }),
    ).toBeVisible();
    expect(useLogStream).toHaveBeenLastCalledWith({
      workspaceId: "default",
      streamPath: "/agents/ember/logs/stream",
      enabled: false,
    });
  });

  it("reports disconnected when the terminal-info lookup fails", async () => {
    getAgentTerminalInfo.mockRejectedValue(new Error("boom"));

    render(<AgentLogsTab agentName="ember" isActive />);

    await waitFor(() => {
      expect(statusEl()).toHaveAttribute("data-state", "disconnected");
    });
    expect(screen.queryByTestId("archive-empty")).toBeNull();
  });
});
