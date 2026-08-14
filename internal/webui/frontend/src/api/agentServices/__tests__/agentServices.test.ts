import { beforeEach, describe, expect, it, vi } from "vitest";

import { get } from "@/api/common";
import type { ApiError } from "@/api/common";

import { listAgentServiceRuns, listAgentServices } from "../agentServices";

vi.mock("@/api/common", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/common")>();
  return { ...actual, get: vi.fn() };
});

const mockGet = vi.mocked(get);

describe("agent-services API", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("lists workspace agent services from the standard envelope", async () => {
    mockGet.mockResolvedValueOnce({
      success: true,
      data: [
        {
          id: "scout",
          name: "Scout",
          kind: "scripted",
          enabled: true,
          behavior: { driverId: "scout", driverVersionId: "v1" },
          bindings: [
            {
              id: "binding-scout-weekly",
              sourceKind: "cron",
              schedule: "@weekly",
              enabled: true,
              routeKey: "cron.scout.weekly",
            },
          ],
          nextFireAt: "2026-08-17T00:00:00Z",
          lastRunStatus: "succeeded",
          consecutiveFailures: 0,
          errors: [],
          createdAt: "2026-08-14T00:00:00Z",
          updatedAt: "2026-08-14T00:00:00Z",
        },
      ],
      total: 1,
    });

    const result = await listAgentServices("Workspace A");

    expect(result.total).toBe(1);
    expect(result.data[0]?.bindings[0]?.schedule).toBe("@weekly");
    expect(mockGet).toHaveBeenCalledWith(
      "/api/workspaces/Workspace%20A/agent-services",
    );
  });

  it("lists camelCase run DTOs with the default and requested limits", async () => {
    const envelope = {
      success: true as const,
      data: [
        {
          workspaceKey: "Workspace A",
          runId: "run/1",
          driverId: "scout",
          driverVersionId: "v1",
          agentServiceId: "scout/id",
          status: "succeeded" as const,
          createdAt: "2026-08-14T00:00:00Z",
          updatedAt: "2026-08-14T00:01:00Z",
        },
      ],
      total: 1,
    };
    mockGet.mockResolvedValue(envelope);

    await listAgentServiceRuns("Workspace A", "scout/id");
    await listAgentServiceRuns("Workspace A", "scout/id", 100);

    expect(mockGet).toHaveBeenNthCalledWith(
      1,
      "/api/workspaces/Workspace%20A/agent-services/scout%2Fid/runs",
    );
    expect(mockGet).toHaveBeenNthCalledWith(
      2,
      "/api/workspaces/Workspace%20A/agent-services/scout%2Fid/runs?limit=100",
    );
  });

  it("surfaces a failed response envelope", async () => {
    mockGet.mockResolvedValueOnce({
      success: false,
      error: "agent service unavailable",
    });

    await expect(listAgentServices("WS")).rejects.toMatchObject({
      name: "ApiError",
      message: "agent service unavailable",
    } satisfies Partial<ApiError>);
  });
});
