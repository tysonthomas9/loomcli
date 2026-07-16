/**
 * @vitest-environment jsdom
 */

import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { updateWorkspaceDesignFormat } from "@/api/workspace";

import { useWorkspaceContext } from "../useWorkspaceContext";
import { useWorkspaceDesignFormat } from "../useWorkspaceDesignFormat";

vi.mock("@/api/workspace", () => ({
  updateWorkspaceDesignFormat: vi.fn(),
}));

vi.mock("../useWorkspaceContext", () => ({
  useWorkspaceContext: vi.fn(),
}));

const mockUpdateWorkspaceDesignFormat = vi.mocked(updateWorkspaceDesignFormat);
const mockUseWorkspaceContext = vi.mocked(useWorkspaceContext);

describe("useWorkspaceDesignFormat", () => {
  const refetch = vi.fn();

  beforeEach(() => {
    vi.clearAllMocks();
    mockUseWorkspaceContext.mockReturnValue({
      workspaceId: "ALPHA",
      refetch,
    } as ReturnType<typeof useWorkspaceContext>);
  });

  it("updates the active workspace and refetches its topology", async () => {
    mockUpdateWorkspaceDesignFormat.mockResolvedValue({} as never);
    const { result } = renderHook(() => useWorkspaceDesignFormat());

    let ok = false;
    await act(async () => {
      ok = await result.current.updateDesignFormat("html");
    });

    expect(ok).toBe(true);
    expect(mockUpdateWorkspaceDesignFormat).toHaveBeenCalledWith(
      "ALPHA",
      "html",
    );
    expect(refetch).toHaveBeenCalledOnce();
    expect(result.current.error).toBeNull();
  });

  it("reports save errors without refetching", async () => {
    mockUpdateWorkspaceDesignFormat.mockRejectedValue(new Error("save failed"));
    const { result } = renderHook(() => useWorkspaceDesignFormat());

    let ok = true;
    await act(async () => {
      ok = await result.current.updateDesignFormat("markdown");
    });

    expect(ok).toBe(false);
    expect(result.current.error).toBe("save failed");
    expect(refetch).not.toHaveBeenCalled();
  });
});
