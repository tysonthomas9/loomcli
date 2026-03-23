/**
 * @vitest-environment jsdom
 */

import { describe, it, expect, vi, beforeEach } from "vitest";

const { mockDeleteTabMetadata } = vi.hoisted(() => ({
  mockDeleteTabMetadata: vi.fn().mockResolvedValue(undefined),
}));

vi.mock("@/api/terminal", () => ({
  deleteTabMetadata: mockDeleteTabMetadata,
}));

import { cleanupTerminalTabs } from "../cleanupTerminalTabs";
import type { CleanupTab } from "../cleanupTerminalTabs";

describe("cleanupTerminalTabs", () => {
  beforeEach(() => {
    mockDeleteTabMetadata.mockClear();
  });

  it("calls deleteTabMetadata for terminal tabs with sessionName", () => {
    const tabs: CleanupTab[] = [
      { type: "terminal", metadata: { sessionName: "sess-1" } },
      { type: "terminal", metadata: { sessionName: "sess-2" } },
    ];

    cleanupTerminalTabs(tabs);

    expect(mockDeleteTabMetadata).toHaveBeenCalledTimes(2);
    expect(mockDeleteTabMetadata).toHaveBeenCalledWith("sess-1");
    expect(mockDeleteTabMetadata).toHaveBeenCalledWith("sess-2");
  });

  it("skips non-terminal tabs", () => {
    const tabs: CleanupTab[] = [
      { type: "details" },
      { type: "sessions" },
      { type: "terminal", metadata: { sessionName: "sess-1" } },
    ];

    cleanupTerminalTabs(tabs);

    expect(mockDeleteTabMetadata).toHaveBeenCalledTimes(1);
    expect(mockDeleteTabMetadata).toHaveBeenCalledWith("sess-1");
  });

  it("skips terminal tabs without sessionName", () => {
    const tabs: CleanupTab[] = [
      { type: "terminal", metadata: {} },
      { type: "terminal" },
    ];

    cleanupTerminalTabs(tabs);

    expect(mockDeleteTabMetadata).not.toHaveBeenCalled();
  });

  it("is a no-op for empty array", () => {
    cleanupTerminalTabs([]);

    expect(mockDeleteTabMetadata).not.toHaveBeenCalled();
  });

  it("does not throw when deleteTabMetadata rejects", () => {
    mockDeleteTabMetadata.mockRejectedValueOnce(new Error("Network error"));

    const tabs: CleanupTab[] = [
      { type: "terminal", metadata: { sessionName: "sess-1" } },
    ];

    // Should not throw
    expect(() => cleanupTerminalTabs(tabs)).not.toThrow();
  });

  it("handles multiple terminal tabs with some missing sessionName", () => {
    const tabs: CleanupTab[] = [
      { type: "terminal", metadata: { sessionName: "sess-1" } },
      { type: "terminal", metadata: {} },
      { type: "terminal", metadata: { sessionName: "sess-3" } },
    ];

    cleanupTerminalTabs(tabs);

    expect(mockDeleteTabMetadata).toHaveBeenCalledTimes(2);
    expect(mockDeleteTabMetadata).toHaveBeenCalledWith("sess-1");
    expect(mockDeleteTabMetadata).toHaveBeenCalledWith("sess-3");
  });
});
