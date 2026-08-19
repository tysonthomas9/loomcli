import { beforeEach, describe, expect, it, vi } from "vitest";

import { get } from "@/api/common";
import type { ApiError } from "@/api/common";

import {
  getAgentServiceJournal,
  listAgentServiceRuns,
  listAgentServices,
  listRunEvents,
} from "../agentServices";

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
          lastRunStatus: "completed",
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
          status: "completed" as const,
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

  it("lists snake_case run events from the workflows endpoint", async () => {
    mockGet.mockResolvedValueOnce({
      events: [
        {
          id: "1786750762442-0",
          timestamp: "2026-08-14T23:39:22.442Z",
          actor: "api",
          action: "driver_run.create",
          entity_type: "driver_run",
          entity_id: "run/1",
          workspace_id: "Workspace A",
          metadata: { source: "manual" },
        },
      ],
      cursor: "1786750762442-0",
    });

    const result = await listRunEvents("Workspace A", "run/1");

    expect(result.events[0]?.entity_type).toBe("driver_run");
    expect(mockGet).toHaveBeenCalledWith(
      "/api/workspaces/Workspace%20A/runs/run%2F1/events",
    );
  });

  it("gets a journal from the standard item envelope", async () => {
    mockGet.mockResolvedValueOnce({
      success: true,
      data: {
        serviceId: "scout/id",
        filename: "history.md",
        content: "# Journal",
        modifiedAt: "2026-08-14T23:39:22.442Z",
        truncated: false,
      },
    });

    const result = await getAgentServiceJournal("Workspace A", "scout/id");

    expect(result.filename).toBe("history.md");
    expect(mockGet).toHaveBeenCalledWith(
      "/api/workspaces/Workspace%20A/agent-services/scout%2Fid/journal",
    );
  });
});
