/** @vitest-environment jsdom */

import { act, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  getMeta: vi.fn(),
  getHistory: vi.fn(),
  virtualizerCounts: [] as number[],
}));

vi.mock("@/hooks/workspace", () => ({
  useWorkspaceContext: () => ({ workspaceId: "ws-1" }),
}));

vi.mock("@/hooks/api", () => ({
  getTerminalHistoryMeta: mocks.getMeta,
  getTerminalHistory: mocks.getHistory,
}));

vi.mock("@tanstack/react-virtual", () => ({
  useVirtualizer: ({ count }: { count: number }) => {
    mocks.virtualizerCounts.push(count);
    return {
      getVirtualItems: () =>
        Array.from({ length: 6 }, (_, offset) => ({
          index: 420 + offset,
          key: 420 + offset,
          start: (420 + offset) * 20,
          size: 20,
        })),
      getTotalSize: () => count * 20,
    };
  },
}));

import {
  cacheTerminalHistoryRange,
  VirtualTerminalHistory,
} from "../VirtualTerminalHistory";

describe("VirtualTerminalHistory", () => {
  beforeEach(() => {
    mocks.getMeta.mockReset();
    mocks.getHistory.mockReset();
    mocks.virtualizerCounts.length = 0;
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

  it("does not duplicate the finalized screen above a still-mounted xterm", async () => {
    mocks.getMeta.mockResolvedValue({
      generation: "generation-current",
      totalLines: 100_000,
      firstScreenLine: 100_000,
      startedAt: 1,
      cols: 120,
      rows: 20,
      altScreen: false,
      gaps: 0,
      unhandledSequences: { count: 0, prefixes: {} },
      historyLimited: false,
      recordingStopped: false,
      closed: true,
    });

    render(
      <VirtualTerminalHistory
        sessionName="term-1"
        isActive
        recordingEpoch={0}
        firstScreenLine={99_980}
      >
        <div data-testid="live-terminal">final screen</div>
      </VirtualTerminalHistory>,
    );

    await waitFor(() => {
      expect(mocks.virtualizerCounts.at(-1)).toBe(99_980);
    });
    expect(screen.getByTestId("live-terminal")).toBeTruthy();
  });
});

describe("terminal history range cache", () => {
  it("evicts per-line entries with the oldest cached window", () => {
    const lines = new Map<number, { i: number }>();
    const ranges = new Map<string, { from: number; count: number }>();
    for (let window = 0; window < 13; window += 1) {
      const from = window * 200;
      cacheTerminalHistoryRange(
        lines,
        ranges,
        `generation:${from}:200`,
        { from, count: 200 },
        Array.from({ length: 200 }, (_, offset) => ({ i: from + offset })),
      );
    }

    expect(ranges.size).toBe(12);
    expect(lines.size).toBe(12 * 200);
    expect(lines.has(0)).toBe(false);
    expect(lines.has(200)).toBe(true);
    expect(lines.has(12 * 200)).toBe(true);
  });
});

describe("historyNoticeText", () => {
  it("omits the unsupported-sequence counter from the history notice", async () => {
    const { historyNoticeText } = await import("../VirtualTerminalHistory");
    const text = historyNoticeText({
      generation: "generation-a",
      totalLines: 1000,
      firstScreenLine: 960,
      startedAt: 1,
      cols: 140,
      rows: 35,
      altScreen: false,
      gaps: 2,
      unhandledSequences: { count: 50581, prefixes: {} },
      historyLimited: false,
      recordingStopped: false,
      closed: false,
    });
    expect(text).toContain("Current size 140×35");
    expect(text).toContain("2 output gaps");
    expect(text).not.toContain("unsupported terminal sequences");
  });
});
