import { beforeEach, describe, expect, it, vi } from "vitest";

import { api, apiErrorFromResponse } from "@/api/common";

import { getIssueEvents } from "../events";

vi.mock("@/api/common", () => ({
  api: { GET: vi.fn() },
  apiErrorFromResponse: vi.fn(),
}));

const mockGet = api.GET as ReturnType<typeof vi.fn>;

describe("getIssueEvents", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGet.mockResolvedValue({
      data: { success: true, data: [] },
      error: undefined,
      response: new Response(),
    });
  });

  it("sends the requested history limit", async () => {
    await getIssueEvents("workspace-1", "loom-1", 175);

    expect(mockGet).toHaveBeenCalledWith(
      "/api/workspaces/{ws}/issues/{id}/events",
      {
        params: {
          path: { ws: "workspace-1", id: "loom-1" },
          query: { limit: 175 },
        },
      },
    );
  });
});

const validEvent = {
  id: "opaque-cursor",
  issue_id: "loom-1",
  event_type: "issue.update",
  actor: "",
  created_at: "2026-09-05T12:00:00Z",
};

it("passes cancellation and preserves successful history records", async () => {
  const controller = new AbortController();
  mockGet.mockResolvedValue({ data: { success: true, data: [validEvent] } });
  await expect(
    getIssueEvents("workspace-1", "loom-1", 15, { signal: controller.signal }),
  ).resolves.toEqual([validEvent]);
  expect(mockGet.mock.calls.at(-1)?.[1].signal).toBe(controller.signal);
});

it("accepts a successful empty history", async () => {
  mockGet.mockResolvedValue({ data: { success: true, data: [] } });
  await expect(getIssueEvents("workspace-1", "loom-1")).resolves.toEqual([]);
});

it.each([
  undefined,
  null,
  {},
  [null],
  [{ ...validEvent, id: "" }],
  [{ ...validEvent, issue_id: "other-issue" }],
  [{ ...validEvent, actor: 7 }],
  [{ ...validEvent, created_at: "invalid" }],
  [{ ...validEvent, summary: {} }],
  [{ ...validEvent, changes: {} }],
  [{ ...validEvent, changes: [null] }],
  [{ ...validEvent, changes: [{ field: "status", after: 7 }] }],
  [{ ...validEvent, metadata: { assignee: 7 } }],
])("rejects malformed or wrong-scope history %j", async (events) => {
  mockGet.mockResolvedValue({ data: { success: true, data: events } });
  await expect(getIssueEvents("workspace-1", "loom-1")).rejects.toThrow(
    "Invalid issue events response",
  );
});

it.each([false, "true", undefined])(
  "requires successful envelope %j",
  async (success) => {
    mockGet.mockResolvedValue({ data: { success, data: [] } });
    await expect(getIssueEvents("workspace-1", "loom-1")).rejects.toThrow(
      "Failed to fetch events",
    );
  },
);

it("propagates an unavailable history source", async () => {
  const failure = new Error("History unavailable");
  vi.mocked(apiErrorFromResponse).mockReturnValue(failure);
  mockGet.mockResolvedValue({
    error: { error: "unavailable" },
    response: new Response(null, { status: 503 }),
  });
  await expect(getIssueEvents("workspace-1", "loom-1")).rejects.toBe(failure);
});
