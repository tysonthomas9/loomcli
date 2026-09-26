import { beforeEach, describe, expect, it, vi } from "vitest";

import { api } from "@/api/common";

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
