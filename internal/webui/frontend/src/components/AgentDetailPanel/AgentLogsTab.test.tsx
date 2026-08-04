/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for AgentLogsTab.
 *
 * Regression guard: an archive that comes back empty (e.g. a daemon-supervised
 * agent whose log file does not exist yet, which getAgentLogArchive turns into
 * an empty line list) must render an explicit "no logs" state — never the
 * misleading "connected" label over a blank viewer.
 */

import { render, screen, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import { AgentLogsTab } from "./AgentLogsTab";

const getAgentTerminalInfo = vi.fn();
const getAgentLogArchive = vi.fn();

vi.mock("@/hooks/api", () => ({
  getAgentTerminalInfo: (...args: unknown[]) => getAgentTerminalInfo(...args),
  getAgentLogArchive: (...args: unknown[]) => getAgentLogArchive(...args),
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
    getAgentLogArchive.mockReset();
  });

  it("shows an explicit empty state (not 'connected') when the archive is empty", async () => {
    getAgentTerminalInfo.mockResolvedValue("archive");
    getAgentLogArchive.mockResolvedValue({
      lines: [],
      lineCount: 0,
      startLine: 1,
    });

    render(<AgentLogsTab agentName="ember" isActive />);

    const empty = await screen.findByTestId("archive-empty");
    expect(empty).toHaveTextContent(/no logs available/i);
    expect(statusEl()).toHaveAttribute("data-state", "empty");
    // The crux of the bug: it must NOT claim a populated, connected log.
    expect(statusEl()).not.toHaveAttribute("data-state", "connected");
    expect(screen.queryByTestId("terminal-container")).toBeNull();
  });

  it("shows the snapshot and 'connected' when the archive has lines", async () => {
    getAgentTerminalInfo.mockResolvedValue("archive");
    getAgentLogArchive.mockResolvedValue({
      lines: ["alpha", "beta"],
      lineCount: 2,
      startLine: 1,
    });

    render(<AgentLogsTab agentName="ember" isActive />);

    const pre = await screen.findByTestId("terminal-container");
    expect(pre).toHaveTextContent("alpha");
    expect(pre).toHaveTextContent("beta");
    expect(statusEl()).toHaveAttribute("data-state", "connected");
    expect(screen.queryByTestId("archive-empty")).toBeNull();
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
