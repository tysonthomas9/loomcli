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

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import { AgentLogsTab } from "./AgentLogsTab";

const getAgentTerminalInfo = vi.fn();
const getAgentLogArchive = vi.fn();

type ArchiveFixture = {
  lines: string[];
  lineCount: number;
  startLine: number;
};

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
  return screen.getByTestId("log-status");
}

function archiveScrollContainer(): HTMLElement {
  const container =
    screen.getByTestId("log-viewer").parentElement?.parentElement;
  if (!(container instanceof HTMLElement)) {
    throw new Error("expected archive scroll container");
  }
  return container;
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

    // findByTestId resolves on the container's FIRST render, which can happen
    // after the terminal-info resolve but before the archive lines land — the
    // immediate content/state assertions then race the second update (seen as
    // a CI-only flake). Await the content and the state themselves.
    const pre = await screen.findByTestId("terminal-container");
    await waitFor(() => expect(pre).toHaveTextContent("alpha"));
    expect(pre).toHaveTextContent("beta");
    await waitFor(() =>
      expect(statusEl()).toHaveAttribute("data-state", "connected"),
    );
    expect(screen.queryByTestId("archive-empty")).toBeNull();
  });

  it("scrolls the archive container to the bottom after load and refresh", async () => {
    getAgentTerminalInfo.mockResolvedValue("archive");

    let resolveInitialArchive: ((archive: ArchiveFixture) => void) | undefined;
    let resolveRefreshArchive: ((archive: ArchiveFixture) => void) | undefined;

    getAgentLogArchive
      .mockReturnValueOnce(
        new Promise((resolve) => {
          resolveInitialArchive = resolve;
        }),
      )
      .mockReturnValueOnce(
        new Promise((resolve) => {
          resolveRefreshArchive = resolve;
        }),
      );

    render(<AgentLogsTab agentName="ember" isActive />);

    const scrollContainer = archiveScrollContainer();
    let scrollHeight = 480;
    let scrollTop = 0;
    Object.defineProperties(scrollContainer, {
      scrollHeight: {
        configurable: true,
        get: () => scrollHeight,
      },
      scrollTop: {
        configurable: true,
        get: () => scrollTop,
        set: (value: number) => {
          scrollTop = value;
        },
      },
    });

    resolveInitialArchive?.({
      lines: ["alpha", "beta"],
      lineCount: 2,
      startLine: 1,
    });

    await screen.findByTestId("terminal-container");
    await waitFor(() => {
      expect(scrollContainer.scrollTop).toBe(480);
    });

    scrollHeight = 960;
    scrollContainer.scrollTop = 0;
    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));

    resolveRefreshArchive?.({
      lines: ["alpha", "beta", "gamma"],
      lineCount: 3,
      startLine: 1,
    });

    await waitFor(() => {
      expect(screen.getByTestId("terminal-container")).toHaveTextContent(
        "gamma",
      );
    });
    await waitFor(() => {
      expect(scrollContainer.scrollTop).toBe(960);
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
