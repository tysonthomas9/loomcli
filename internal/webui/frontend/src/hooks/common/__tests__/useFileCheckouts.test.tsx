/** @vitest-environment jsdom */
import { act, renderHook, waitFor } from "@testing-library/react";
import { useEffect, type ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  QueryRecoveryContext,
  QueryRecoveryCoordinator,
} from "../queryRecovery";
import { useFileCheckouts } from "../useFileCheckouts";
import { listFileCheckouts } from "@/hooks/api";
vi.mock("@/hooks/api", () => ({ listFileCheckouts: vi.fn() }));
const api = vi.mocked(listFileCheckouts);
const empty = { checkouts: [], partial: false, limit_hit: false, errors: [] };
const row = {
  kind: "repo" as const,
  repo: "repo-a",
  exists: false,
  change_count: 0,
};
function wrapper(coordinator: QueryRecoveryCoordinator) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return (
      <QueryRecoveryContext.Provider value={coordinator}>
        {children}
      </QueryRecoveryContext.Provider>
    );
  };
}
function useMounted(workspace: string, enabled = true) {
  const result = useFileCheckouts(workspace, enabled);
  const { refreshCheckouts } = result;
  useEffect(() => {
    void refreshCheckouts();
  }, [refreshCheckouts]);
  return result;
}
describe("file checkout metadata recovery", () => {
  beforeEach(() => {
    api.mockReset();
  });
  it("preserves partial ordinary catalog but rejects it for recovery", async () => {
    api.mockResolvedValue({ ...empty, partial: true, checkouts: [row] });
    const coordinator = new QueryRecoveryCoordinator();
    const { result } = renderHook(() => useMounted("ws"), {
      wrapper: wrapper(coordinator),
    });
    await waitFor(() => expect(result.current.checkouts).toEqual([row]));
    await act(async () => {
      await expect(coordinator.refresh()).rejects.toThrow("incomplete");
    });
    expect(result.current.checkouts).toEqual([row]);
    expect(result.current.checkoutError).toContain("incomplete");
    await act(async () => {
      await expect(result.current.refreshCheckoutsForRepair()).rejects.toThrow(
        "incomplete",
      );
    });
  });
  it("rejects API failure instead of acknowledging recovery", async () => {
    api.mockResolvedValue(empty);
    const coordinator = new QueryRecoveryCoordinator();
    const { result } = renderHook(() => useMounted("ws"), {
      wrapper: wrapper(coordinator),
    });
    await waitFor(() => expect(result.current.checkoutsSettled).toBe(true));
    api.mockRejectedValue(new Error("offline"));
    await act(async () => {
      await expect(coordinator.refresh()).rejects.toThrow("offline");
    });
  });
  it("fences A to B to A responses and withdraws disabled metadata", async () => {
    let finish!: (data: typeof empty) => void;
    api
      .mockReturnValueOnce(
        new Promise((resolve) => {
          finish = resolve;
        }),
      )
      .mockResolvedValue(empty);
    const coordinator = new QueryRecoveryCoordinator();
    const { result, rerender } = renderHook(
      ({ ws, enabled }) => useMounted(ws, enabled),
      {
        wrapper: wrapper(coordinator),
        initialProps: { ws: "A", enabled: true },
      },
    );
    await waitFor(() => expect(api).toHaveBeenCalledTimes(1));
    const oldSignal = api.mock.calls[0]?.[1]?.signal;
    rerender({ ws: "B", enabled: true });
    await waitFor(() => expect(api).toHaveBeenCalledTimes(2));
    rerender({ ws: "A", enabled: true });
    await waitFor(() => expect(api).toHaveBeenCalledTimes(3));
    await act(async () =>
      finish({ ...empty, checkouts: [row] } as unknown as typeof empty),
    );
    expect(oldSignal?.aborted).toBe(true);
    expect(result.current.checkouts).toEqual([]);
    rerender({ ws: "A", enabled: false });
    await act(async () => coordinator.refresh());
    expect(api).toHaveBeenCalledTimes(3);
  });
});
