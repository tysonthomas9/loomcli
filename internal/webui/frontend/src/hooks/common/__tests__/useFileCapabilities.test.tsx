/** @vitest-environment jsdom */

import { act, renderHook, waitFor } from "@testing-library/react";
import {
  QueryRecoveryCoordinator,
  QueryRecoveryContext,
} from "../queryRecovery";
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

    await waitFor(() =>
      expect(mocks.getFileCapabilities).toHaveBeenCalledTimes(1),
    );
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
  it("requires fresh success for recovery and withdraws when disabled", async () => {
    const coordinator = new QueryRecoveryCoordinator();
    let enabled = true;
    function Wrapper({ children }: { children: ReactNode }) {
      return (
        <QueryRecoveryContext.Provider value={coordinator}>
          <FileCapabilitiesProvider workspaceId="ws" enabled={enabled}>
            {children}
          </FileCapabilitiesProvider>
        </QueryRecoveryContext.Provider>
      );
    }
    mocks.getFileCapabilities.mockResolvedValue({
      read: true,
      write: true,
      sensitive: false,
    });
    const { result, rerender } = renderHook(() => useFileCapabilities(), {
      wrapper: Wrapper,
    });
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    mocks.getFileCapabilities.mockRejectedValue(new Error("denied"));
    await act(async () => {
      await expect(coordinator.refresh()).rejects.toThrow("denied");
    });
    expect(result.current.capabilities).toBeNull();
    enabled = false;
    rerender();
    const calls = mocks.getFileCapabilities.mock.calls.length;
    await act(async () => coordinator.refresh());
    expect(mocks.getFileCapabilities).toHaveBeenCalledTimes(calls);
  });
  it("ignores old capability responses through workspace A to B to A", async () => {
    let workspaceId = "A";
    let finish!: (value: {
      read: boolean;
      write: boolean;
      sensitive: boolean;
    }) => void;
    mocks.getFileCapabilities
      .mockReturnValueOnce(
        new Promise((resolve) => {
          finish = resolve;
        }),
      )
      .mockResolvedValue({ read: true, write: false, sensitive: false });
    function Wrapper({ children }: { children: ReactNode }) {
      return (
        <FileCapabilitiesProvider workspaceId={workspaceId}>
          {children}
        </FileCapabilitiesProvider>
      );
    }
    const { result, rerender } = renderHook(() => useFileCapabilities(), {
      wrapper: Wrapper,
    });
    await waitFor(() =>
      expect(mocks.getFileCapabilities).toHaveBeenCalledTimes(1),
    );
    const oldSignal = mocks.getFileCapabilities.mock.calls[0]?.[1]?.signal;
    workspaceId = "B";
    rerender();
    await waitFor(() =>
      expect(mocks.getFileCapabilities).toHaveBeenCalledTimes(2),
    );
    workspaceId = "A";
    rerender();
    await waitFor(() =>
      expect(mocks.getFileCapabilities).toHaveBeenCalledTimes(3),
    );
    await act(async () => finish({ read: true, write: true, sensitive: true }));
    expect(oldSignal.aborted).toBe(true);
    expect(result.current.capabilities).toEqual({
      read: true,
      write: false,
      sensitive: false,
    });
  });
});
