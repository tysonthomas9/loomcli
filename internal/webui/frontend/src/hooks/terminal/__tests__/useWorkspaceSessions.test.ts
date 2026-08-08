/**
 * @vitest-environment jsdom
 */

import { renderHook, act } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { listWorkspaceSessions } from "@/api/terminal";

import { useWorkspaceSessions } from "../useWorkspaceSessions";

vi.mock("@/api/terminal", () => ({
  listWorkspaceSessions: vi.fn(),
}));

vi.mock("@/hooks/workspace", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/workspace")>(
      "@/hooks/workspace",
    );
  return {
    ...actual,
    useWorkspaceContext: () => ({ workspaceId: "test-ws" }),
  };
});

const mockListWorkspaceSessions = vi.mocked(listWorkspaceSessions);

async function flushPromises(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
  });
}

describe("useWorkspaceSessions", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockListWorkspaceSessions.mockResolvedValue({
      sessions: [],
      total: 0,
      limit: 200,
    });
  });

  it("passes since, until, and limit filters to the workspace sessions API", async () => {
    const filters = {
      since: "2026-07-10T00:00:00.000Z",
      until: "2026-07-17T00:00:00.000Z",
      limit: 200,
    };

    renderHook(() => useWorkspaceSessions(filters));
    await flushPromises();

    expect(mockListWorkspaceSessions).toHaveBeenCalledWith("test-ws", filters);
  });
});
