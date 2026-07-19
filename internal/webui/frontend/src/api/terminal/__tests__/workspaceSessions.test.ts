/**
 * @vitest-environment jsdom
 */

import { beforeEach, describe, expect, it, vi } from "vitest";

import { get } from "@/api/common";

import {
  buildWorkspaceSessionsUrl,
  listWorkspaceSessions,
} from "../workspaceSessions";

vi.mock("@/api/common", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/common")>();
  return {
    ...actual,
    get: vi.fn(),
    getText: vi.fn(),
  };
});

const mockGet = vi.mocked(get);

describe("workspace session API", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("builds list query params with encoded since, until, status, agent, kind, and limit", () => {
    expect(
      buildWorkspaceSessionsUrl("ws 1", {
        since: "2026-07-10T00:00:00.000Z",
        until: "2026-07-17T00:00:00.000Z",
        status: "completed",
        agent_id: "nova/lead",
        kind: "task",
        limit: 50,
      }),
    ).toBe(
      "/api/workspaces/ws%201/sessions?since=2026-07-10T00%3A00%3A00.000Z&until=2026-07-17T00%3A00%3A00.000Z&status=completed&agent_id=nova%2Flead&kind=task&limit=50",
    );
  });

  it("passes the encoded list URL through get and unwraps sessions metadata", async () => {
    mockGet.mockResolvedValueOnce({
      success: true,
      data: {
        sessions: [],
        total: 250,
        limit: 200,
      },
    });

    const result = await listWorkspaceSessions("ws", {
      since: "2026-07-10T00:00:00.000Z",
      limit: 200,
    });

    expect(mockGet).toHaveBeenCalledWith(
      "/api/workspaces/ws/sessions?since=2026-07-10T00%3A00%3A00.000Z&limit=200",
    );
    expect(result).toEqual({ sessions: [], total: 250, limit: 200 });
  });
});
