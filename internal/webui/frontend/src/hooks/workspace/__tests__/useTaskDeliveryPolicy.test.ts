/**
 * @vitest-environment jsdom
 */

import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { updateWorkspaceTaskDelivery } from "@/api/workspace";

import { useTaskDeliveryPolicy } from "../useTaskDeliveryPolicy";
import { useWorkspaceContext } from "../useWorkspaceContext";

vi.mock("@/api/workspace", () => ({
  updateWorkspaceTaskDelivery: vi.fn(),
}));

vi.mock("../useWorkspaceContext", () => ({
  useWorkspaceContext: vi.fn(),
}));

const mockUpdateWorkspaceTaskDelivery = vi.mocked(
  updateWorkspaceTaskDelivery,
);
const mockUseWorkspaceContext = vi.mocked(useWorkspaceContext);

describe("useTaskDeliveryPolicy", () => {
  const refetch = vi.fn();

  beforeEach(() => {
    vi.clearAllMocks();
    mockUseWorkspaceContext.mockReturnValue({
      workspaceId: "ALPHA",
      refetch,
    } as ReturnType<typeof useWorkspaceContext>);
  });

  it("updates a repository override and refetches topology", async () => {
    mockUpdateWorkspaceTaskDelivery.mockResolvedValue({} as never);
    const { result } = renderHook(() => useTaskDeliveryPolicy());

    let ok = false;
    await act(async () => {
      ok = await result.current.updateRequirement("pull_request", "app");
    });

    expect(ok).toBe(true);
    expect(mockUpdateWorkspaceTaskDelivery).toHaveBeenCalledWith(
      "ALPHA",
      "pull_request",
      "app",
    );
    expect(refetch).toHaveBeenCalledOnce();
  });

  it("supports clearing a repository override", async () => {
    mockUpdateWorkspaceTaskDelivery.mockResolvedValue({} as never);
    const { result } = renderHook(() => useTaskDeliveryPolicy());

    await act(async () => {
      await result.current.updateRequirement("", "app");
    });

    expect(mockUpdateWorkspaceTaskDelivery).toHaveBeenCalledWith(
      "ALPHA",
      "",
      "app",
    );
  });
});
