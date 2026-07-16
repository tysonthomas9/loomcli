/** @vitest-environment jsdom */

import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  getFileCapabilities: vi.fn(),
}));

vi.mock("@/api/workspace", () => ({
  getFileCapabilities: mocks.getFileCapabilities,
}));

import {
  FileCapabilitiesProvider,
  useFileCapabilities,
} from "../useFileCapabilities";

function wrapper(workspaceId: string) {
  function CapabilityTestWrapper({ children }: { children: ReactNode }) {
    return (
      <FileCapabilitiesProvider workspaceId={workspaceId}>
        {children}
      </FileCapabilitiesProvider>
    );
  }
  return CapabilityTestWrapper;
}

describe("useFileCapabilities", () => {
  beforeEach(() => vi.clearAllMocks());

  it("exposes loading and editor capabilities", async () => {
    let resolve!: (value: {
      read: boolean;
      write: boolean;
      sensitive: boolean;
    }) => void;
    mocks.getFileCapabilities.mockReturnValue(
      new Promise((next) => {
        resolve = next;
      }),
    );
    const hook = renderHook(() => useFileCapabilities(), {
      wrapper: wrapper("ws-1"),
    });
    expect(hook.result.current).toMatchObject({
      capabilities: null,
      isLoading: true,
      error: null,
    });

    act(() => resolve({ read: true, write: true, sensitive: false }));
    await waitFor(() => expect(hook.result.current.isLoading).toBe(false));
    expect(hook.result.current.capabilities).toEqual({
      read: true,
      write: true,
      sensitive: false,
    });
  });

  it("fails closed on errors and retries explicitly", async () => {
    mocks.getFileCapabilities
      .mockRejectedValueOnce(new Error("permissions unavailable"))
      .mockResolvedValueOnce({ read: true, write: false, sensitive: false });
    const hook = renderHook(() => useFileCapabilities(), {
      wrapper: wrapper("ws-1"),
    });
    await waitFor(() =>
      expect(hook.result.current.error).toBe("permissions unavailable"),
    );
    expect(hook.result.current.capabilities).toBeNull();

    act(() => hook.result.current.retry());
    await waitFor(() =>
      expect(hook.result.current.capabilities).toEqual({
        read: true,
        write: false,
        sensitive: false,
      }),
    );
    expect(mocks.getFileCapabilities).toHaveBeenCalledTimes(2);
  });
});
