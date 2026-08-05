/**
 * @vitest-environment jsdom
 */

import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { fetchInteractivePrompts } from "@/api/workspace";

import { useInteractivePrompts } from "../useInteractivePrompts";

vi.mock("@/api/workspace", () => ({
  fetchInteractivePrompts: vi.fn(),
}));

const mockFetchInteractivePrompts = vi.mocked(fetchInteractivePrompts);

async function flushPromises(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
  });
}

describe("useInteractivePrompts", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("does not fetch until an explicitly disabled consumer becomes active", async () => {
    mockFetchInteractivePrompts.mockResolvedValueOnce([
      { id: "lead", label: "Lead" },
    ]);

    const { result, rerender } = renderHook(
      ({ enabled }: { enabled: boolean }) =>
        useInteractivePrompts("WS", enabled),
      { initialProps: { enabled: false } },
    );
    await flushPromises();

    expect(mockFetchInteractivePrompts).not.toHaveBeenCalled();
    expect(result.current.prompts).toEqual([]);
    expect(result.current.isLoading).toBe(false);

    rerender({ enabled: true });
    await flushPromises();

    expect(mockFetchInteractivePrompts).toHaveBeenCalledWith("WS");
    expect(result.current.prompts).toEqual([{ id: "lead", label: "Lead" }]);
  });
});
