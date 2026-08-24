/**
 * @vitest-environment jsdom
 */

import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import * as api from "@/api";

import { useWorkflowVersions } from "../useWorkflowVersions";

vi.mock("@/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api")>();
  return {
    ...actual,
    listWorkflowVersions: vi.fn(),
    approveWorkflowVersion: vi.fn(),
    activateWorkflowVersion: vi.fn(),
    syncBuiltinWorkflow: vi.fn(),
  };
});

const emptyResponse = { driver_id: "epic-runner", versions: [] };

describe("useWorkflowVersions", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.listWorkflowVersions).mockResolvedValue(emptyResponse);
  });

  it("loads versions on mount", async () => {
    const { result } = renderHook(() =>
      useWorkflowVersions("W", "epic-runner"),
    );
    await waitFor(() => expect(result.current.data).toEqual(emptyResponse));
    expect(api.listWorkflowVersions).toHaveBeenCalledWith("W", "epic-runner");
  });

  it("approve calls the API then refetches", async () => {
    vi.mocked(api.approveWorkflowVersion).mockResolvedValue(
      {} as Awaited<ReturnType<typeof api.approveWorkflowVersion>>,
    );
    const { result } = renderHook(() =>
      useWorkflowVersions("W", "epic-runner"),
    );
    await waitFor(() => expect(result.current.data).toEqual(emptyResponse));
    vi.mocked(api.listWorkflowVersions).mockClear();

    await act(async () => {
      await result.current.approve("v1");
    });
    expect(api.approveWorkflowVersion).toHaveBeenCalledWith("W", "epic-runner", "v1");
    // refetch fired after the action.
    expect(api.listWorkflowVersions).toHaveBeenCalledTimes(1);
  });

  it("adoptBuiltin syncs on the auto track", async () => {
    vi.mocked(api.syncBuiltinWorkflow).mockResolvedValue(
      {} as Awaited<ReturnType<typeof api.syncBuiltinWorkflow>>,
    );
    const { result } = renderHook(() =>
      useWorkflowVersions("W", "epic-runner"),
    );
    await waitFor(() => expect(result.current.data).toEqual(emptyResponse));

    await act(async () => {
      await result.current.adoptBuiltin();
    });
    expect(api.syncBuiltinWorkflow).toHaveBeenCalledWith("W", "epic-runner", "auto");
  });

  it("records an action error when the API rejects", async () => {
    vi.mocked(api.activateWorkflowVersion).mockRejectedValue(new Error("nope"));
    const { result } = renderHook(() =>
      useWorkflowVersions("W", "epic-runner"),
    );
    await waitFor(() => expect(result.current.data).toEqual(emptyResponse));

    await act(async () => {
      await expect(result.current.activate("v1")).rejects.toThrow("nope");
    });
    await waitFor(() =>
      expect(result.current.actionError?.message).toBe("nope"),
    );
  });
});
