/**
 * @vitest-environment jsdom
 */

import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/api/common", () => {
  class MockApiError extends Error {
    status: number;
    statusText: string;
    body?: unknown;

    constructor(status: number, statusText: string, body?: unknown) {
      super(`API Error: ${status} ${statusText}`);
      this.name = "ApiError";
      this.status = status;
      this.statusText = statusText;
      this.body = body;
    }
  }

  return {
    api: { GET: vi.fn(), POST: vi.fn(), PATCH: vi.fn(), PUT: vi.fn() },
    apiErrorFromResponse: vi.fn((error: unknown, response?: Response) => {
      return new MockApiError(
        response?.status ?? 0,
        response?.statusText ?? "Network error",
        error,
      );
    }),
    unwrapResponse: vi.fn(
      <T>(
        envelope:
          | { success: boolean; data?: T; error?: string }
          | null
          | undefined,
        response?: Response,
      ) => {
        if (envelope == null || !envelope.success) {
          throw new MockApiError(
            response?.status ?? 0,
            response?.statusText ?? "Invalid",
            envelope?.error,
          );
        }
        return envelope.data as T;
      },
    ),
    ApiError: MockApiError,
  };
});

describe("deliveryGroups API", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.resetModules();
  });

  it("lists delivery groups with state query", async () => {
    const common = await import("@/api/common");
    const mockGet = vi.mocked(common.api.GET);
    mockGet.mockResolvedValue({
      data: {
        success: true,
        data: {
          delivery_groups: [],
          count: 0,
          has_more: false,
        },
      },
      error: undefined,
      response: new Response(null, { status: 200 }),
    } as never);

    const { listDeliveryGroups } = await import("../deliveryGroups");
    const result = await listDeliveryGroups("WS", { state: "active", limit: 20 });
    expect(mockGet).toHaveBeenCalledWith(
      "/api/workspaces/{ws}/delivery-groups",
      expect.objectContaining({
        params: expect.objectContaining({
          path: { ws: "WS" },
          query: expect.objectContaining({ state: "active", limit: 20 }),
        }),
      }),
    );
    expect(result.count).toBe(0);
  });

  it("sends If-Match and intent key on setMembers", async () => {
    const common = await import("@/api/common");
    const mockPut = vi.mocked(common.api.PUT);
    mockPut.mockResolvedValue({
      data: {
        success: true,
        data: {
          group: {
            workspace_key: "WS",
            id: "dg_01HABCDEFGHJKLMNPQRSTUVWXY",
            title: "G",
            state: "active",
            revision: 4,
            members: [],
            last_op_id: "op",
            created_at: "2026-09-24T00:00:00Z",
            updated_at: "2026-09-24T00:00:00Z",
          },
        },
      },
      error: undefined,
      response: new Response(null, { status: 200 }),
    } as never);

    const { setDeliveryGroupMembers } = await import("../deliveryGroups");
    await setDeliveryGroupMembers(
      "WS",
      "dg_01HABCDEFGHJKLMNPQRSTUVWXY",
      { members: [{ pr_key: "github:o/r#1" }] },
      3,
      "intent-1",
    );
    expect(mockPut).toHaveBeenCalledWith(
      "/api/workspaces/{ws}/delivery-groups/{group_id}/members",
      expect.objectContaining({
        params: expect.objectContaining({
          header: {
            "If-Match": "3",
            "X-Idempotency-Key": "intent-1",
          },
        }),
      }),
    );
  });

  it("classifies stale revision without dropping group payload", async () => {
    const { ApiError } = await import("@/api/common");
    const { classifyDeliveryGroupWriteError } = await import("../deliveryGroups");
    const group = {
      workspace_key: "WS",
      id: "dg_01HABCDEFGHJKLMNPQRSTUVWXY",
      title: "G",
      state: "active" as const,
      revision: 5,
      members: [
        {
          pr_key: "github:o/r#1",
          repo_name: "r",
          pr_number: 1,
          source: "manual" as const,
          added_at: "2026-09-24T00:00:00Z",
        },
      ],
      last_op_id: "op",
      created_at: "2026-09-24T00:00:00Z",
      updated_at: "2026-09-24T00:00:00Z",
    };
    const err = new ApiError(412, "Precondition Failed", {
      code: "stale_revision",
      message: "revision moved",
      data: { group },
    });
    const classified = classifyDeliveryGroupWriteError(err);
    expect(classified.kind).toBe("stale_revision");
    expect(classified.group?.members).toHaveLength(1);
  });
});
