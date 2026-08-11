/** @vitest-environment jsdom */

import { act, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  getMeta: vi.fn(),
  getHistory: vi.fn(),
}));

vi.mock("@/hooks/workspace", () => ({
  useWorkspaceContext: () => ({ workspaceId: "ws-1" }),
}));

vi.mock("@/hooks/api", () => ({
  getTerminalHistoryMeta: mocks.getMeta,
  getTerminalHistory: mocks.getHistory,
}));

vi.mock("@tanstack/react-virtual", () => ({
  useVirtualizer: () => ({
    getVirtualItems: () =>
      Array.from({ length: 6 }, (_, offset) => ({
        index: 420 + offset,
        key: 420 + offset,
        start: (420 + offset) * 20,
        size: 20,
      })),
    getTotalSize: () => 100_000 * 20,
  }),
}));

import { VirtualTerminalHistory } from "../VirtualTerminalHistory";

describe("VirtualTerminalHistory", () => {
  beforeEach(() => {
    mocks.getMeta.mockReset();
    mocks.getHistory.mockReset();
    mocks.getMeta.mockResolvedValue({
      generation: "generation-current",
      totalLines: 100_000,
      firstScreenLine: 99_980,
      startedAt: 1,
      cols: 120,
      rows: 40,
      altScreen: false,
      gaps: 0,
      unhandledSequences: { count: 0, prefixes: {} },
      historyLimited: false,
      closed: false,
    });
    mocks.getHistory.mockResolvedValue({
      generation: "generation-current",
      lines: [],
    });
  });

  it("requests only the bounded window containing visible virtual rows", async () => {
    render(
      <VirtualTerminalHistory
        sessionName="term-1"
        isActive={false}
        recordingEpoch={0}
      >
        <div data-testid="live-terminal" />
      </VirtualTerminalHistory>,
    );

    expect(screen.getByTestId("live-terminal")).toBeTruthy();
    await waitFor(() => expect(mocks.getHistory).toHaveBeenCalledTimes(1));
    expect(mocks.getHistory).toHaveBeenCalledWith(
      "ws-1",
      "term-1",
      "generation-current",
      400,
      200,
      expect.any(AbortSignal),
    );
    expect(mocks.getHistory).not.toHaveBeenCalledWith(
      "ws-1",
      "term-1",
      "generation-current",
      0,
      100_000,
      expect.anything(),
    );
  });

  it("lays out each immutable row at the width recorded for that row", async () => {
    mocks.getHistory.mockResolvedValue({
      generation: "generation-current",
      lines: [{ i: 420, t: 1, cols: 132, runs: [{ text: "wide row" }] }],
    });
    const { container } = render(
      <VirtualTerminalHistory
        sessionName="term-1"
        isActive={false}
        recordingEpoch={0}
      >
        <div data-testid="live-terminal" />
      </VirtualTerminalHistory>,
    );

    await waitFor(() => {
      const row = container.querySelector('[data-line-index="420"]');
      expect(row?.getAttribute("data-line-cols")).toBe("132");
      expect((row as HTMLElement | null)?.style.minWidth).toContain("132ch");
    });
  });

  it("binds range requests and rendered rows to the metadata generation", async () => {
    let resolveStaleRange: ((value: unknown) => void) | undefined;
    const staleRange = new Promise((resolve) => {
      resolveStaleRange = resolve;
    });
    const baseMeta = {
      totalLines: 100_000,
      firstScreenLine: 99_980,
      startedAt: 1,
      cols: 120,
      rows: 40,
      altScreen: false,
      gaps: 0,
      unhandledSequences: { count: 0, prefixes: {} },
      historyLimited: false,
      closed: true,
    };
    mocks.getMeta
      .mockResolvedValueOnce({ ...baseMeta, generation: "generation-a" })
      .mockResolvedValue({ ...baseMeta, generation: "generation-b" });
    mocks.getHistory.mockImplementation(
      (_workspace: string, _session: string, generation: string) =>
        generation === "generation-a"
          ? staleRange
          : Promise.resolve({
              generation: "generation-b",
              lines: [
                {
                  i: 420,
                  t: 2,
                  cols: 120,
                  runs: [{ text: "current generation row" }],
                },
              ],
            }),
    );

    const { rerender } = render(
      <VirtualTerminalHistory
        sessionName="term-1"
        isActive={false}
        recordingEpoch={0}
      >
        <div data-testid="live-terminal" />
      </VirtualTerminalHistory>,
    );

    await waitFor(() =>
      expect(mocks.getHistory).toHaveBeenCalledWith(
        "ws-1",
        "term-1",
        "generation-a",
        400,
        200,
        expect.any(AbortSignal),
      ),
    );

    rerender(
      <VirtualTerminalHistory sessionName="term-1" isActive recordingEpoch={1}>
        <div data-testid="live-terminal" />
      </VirtualTerminalHistory>,
    );
    await waitFor(() =>
      expect(mocks.getHistory).toHaveBeenCalledWith(
        "ws-1",
        "term-1",
        "generation-b",
        400,
        200,
        expect.any(AbortSignal),
      ),
    );
    await waitFor(() =>
      expect(screen.getByText("current generation row")).toBeTruthy(),
    );

    await act(async () => {
      resolveStaleRange?.({
        generation: "generation-a",
        lines: [
          {
            i: 420,
            t: 1,
            cols: 120,
            runs: [{ text: "stale generation row" }],
          },
        ],
      });
      await staleRange;
    });

    expect(screen.queryByText("stale generation row")).toBeNull();
    expect(screen.getByText("current generation row")).toBeTruthy();
  });
});
