import { beforeEach, describe, expect, it, vi } from "vitest";

import { del, get, patch, post } from "@/api/common";
import type { ApiError } from "@/api/common";

import {
  createAgentService,
  deleteAgentService,
  getAgentServiceJournal,
  getDriverRunLog,
  getTaskRunLog,
  getTaskRunTranscript,
  listAgentServiceRunTasks,
  listAgentServiceRuns,
  listAgentServices,
  listInstantiableScriptedRoles,
  listRunEvents,
  patchAgentService,
} from "../agentServices";

vi.mock("@/api/common", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/common")>();
  return {
    ...actual,
    get: vi.fn(),
    post: vi.fn(),
    patch: vi.fn(),
    del: vi.fn(),
  };
});

const mockGet = vi.mocked(get);
const mockPost = vi.mocked(post);
const mockPatch = vi.mocked(patch);
const mockDelete = vi.mocked(del);

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
          triggerKind: "cron",
          enabled: true,
          behavior: {
            roleName: "scout",
            roleDisplayName: "Scout",
            workflowName: "scout",
            scripted: true,
          },
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

  it("lists instantiable scripted roles from the literal catalog route", async () => {
    mockGet.mockResolvedValueOnce([
      {
        roleName: "scout",
        displayName: "Scout",
      },
    ]);

    await expect(listInstantiableScriptedRoles("Workspace A")).resolves.toEqual(
      [expect.objectContaining({ roleName: "scout" })],
    );
    expect(mockGet).toHaveBeenCalledWith(
      "/api/workspaces/Workspace%20A/agent-services/scripted-roles",
    );
  });

  it("creates, patches, and deletes an agent service", async () => {
    const created = {
      id: "scout-west",
      name: "Scout West",
      triggerKind: "cron" as const,
      enabled: true,
      behavior: { roleName: "scout", scripted: true },
      bindings: [],
      nextFireAt: null,
      lastRunStatus: "",
      consecutiveFailures: 0,
      errors: [],
      createdAt: "2026-08-15T00:00:00Z",
      updatedAt: "2026-08-15T00:00:00Z",
    };
    mockPost.mockResolvedValueOnce({ success: true, data: created });
    mockPatch.mockResolvedValueOnce({
      success: true,
      data: { ...created, enabled: false },
    });
    mockDelete.mockResolvedValueOnce(undefined);

    await createAgentService("Workspace A", {
      id: "scout-west",
      role: "scout",
      binding: { schedule: "@daily" },
    });
    await patchAgentService("Workspace A", "scout/west", {
      desiredState: "stopped",
    });
    await deleteAgentService("Workspace A", "scout/west");

    expect(mockPost).toHaveBeenCalledWith(
      "/api/workspaces/Workspace%20A/agent-services",
      {
        id: "scout-west",
        role: "scout",
        binding: { schedule: "@daily" },
      },
    );
    expect(mockPatch).toHaveBeenCalledWith(
      "/api/workspaces/Workspace%20A/agent-services/scout%2Fwest",
      { desiredState: "stopped" },
    );
    expect(mockDelete).toHaveBeenCalledWith(
      "/api/workspaces/Workspace%20A/agent-services/scout%2Fwest",
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

  it("lists task runs for one agent-service driver run", async () => {
    mockGet.mockResolvedValueOnce({
      success: true,
      data: [
        {
          taskRunId: "task/1",
          taskId: "WS-1",
          status: "completed",
          runner: "scout-task-runner",
          errorClass: "",
          startedAt: "2026-08-14T23:38:00Z",
          finishedAt: "2026-08-14T23:39:00Z",
          logsAvailable: true,
          transcriptAvailable: true,
        },
      ],
      total: 1,
    });

    const result = await listAgentServiceRunTasks(
      "Workspace A",
      "scout/id",
      "run/1",
    );

    expect(result.data[0]?.logsAvailable).toBe(true);
    expect(result.data[0]?.transcriptAvailable).toBe(true);
    expect(mockGet).toHaveBeenCalledWith(
      "/api/workspaces/Workspace%20A/agent-services/scout%2Fid/runs/run%2F1/tasks",
    );
  });

  it("gets task and driver logs from standard item envelopes", async () => {
    const envelope = {
      success: true as const,
      data: {
        content: "AI output",
        modifiedAt: "2026-08-14T23:39:22.442Z",
        truncated: false,
      },
    };
    mockGet.mockResolvedValue(envelope);

    await getTaskRunLog("Workspace A", "task/1");
    await getDriverRunLog("Workspace A", "run/1");

    expect(mockGet).toHaveBeenNthCalledWith(
      1,
      "/api/workspaces/Workspace%20A/task-runs/task%2F1/log",
    );
    expect(mockGet).toHaveBeenNthCalledWith(
      2,
      "/api/workspaces/Workspace%20A/runs/run%2F1/log",
    );
  });

  it("gets a task-run transcript from the session transcript envelope", async () => {
    mockGet.mockResolvedValueOnce({
      success: true,
      data: {
        session_id: "task/1",
        entries: [
          {
            seq: 1,
            timestamp: "2026-08-15T12:00:00Z",
            role: "assistant",
            type: "text",
            text: "analysis complete",
          },
        ],
      },
    });

    const result = await getTaskRunTranscript("Workspace A", "task/1");

    expect(result).toHaveLength(1);
    expect(result[0]?.text).toBe("analysis complete");
    expect(mockGet).toHaveBeenCalledWith(
      "/api/workspaces/Workspace%20A/task-runs/task%2F1/transcript",
    );
  });
});
