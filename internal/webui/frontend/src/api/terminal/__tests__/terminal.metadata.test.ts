/**
 * @vitest-environment jsdom
 */

import { beforeEach, describe, expect, it, vi } from "vitest";

import { api, get, ApiError } from "@/api/common";
import {
  getTabMetadata,
  patchTabMetadata,
  listTabMetadata,
  listSessionsByIssue,
} from "../terminal";

vi.mock("@/api/common", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/common")>();
  return {
    ...actual,
    get: vi.fn(),
    api: {
      GET: vi.fn(),
      PATCH: vi.fn(),
      PUT: vi.fn(),
      DELETE: vi.fn(),
      use: vi.fn(),
    },
  };
});

const mockApiGet = vi.mocked(api.GET);
const mockApiPatch = vi.mocked(api.PATCH);

describe("terminal tab metadata API", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("throws ApiError instead of TypeError when get metadata returns no envelope", async () => {
    mockApiGet.mockResolvedValueOnce({
      data: undefined,
      error: undefined,
      response: new Response(null, { status: 200, statusText: "OK" }),
    } as never);

    await expect(getTabMetadata("default", "lead-1")).rejects.toMatchObject({
      name: "ApiError",
      status: 200,
      statusText: "OK",
      body: "missing response envelope",
    });
  });

  it("throws ApiError instead of TypeError when patch metadata returns no envelope", async () => {
    mockApiPatch.mockResolvedValueOnce({
      data: undefined,
      error: undefined,
      response: new Response(null, { status: 200, statusText: "OK" }),
    } as never);

    await expect(
      patchTabMetadata("default", "lead-1", { label: "Lead" }),
    ).rejects.toMatchObject({
      status: 200,
      statusText: "OK",
      body: "missing response envelope",
    });
  });
});

describe("strict session collection reads", () => {
  it.each([null, { session_name: "t", pty_alive: "false" }])(
    "rejects malformed tab records",
    async (item) => {
      mockApiGet.mockResolvedValueOnce({
        data: { success: true, data: [item] },
        response: new Response(),
      } as never);
      await expect(listTabMetadata("ws")).rejects.toThrow(
        "Invalid terminal tab list response",
      );
    },
  );

  beforeEach(() => vi.resetAllMocks());
  it.each([404, 503])(
    "rejects unavailable tab storage (%s)",
    async (status) => {
      mockApiGet.mockResolvedValueOnce({
        error: { error: "unavailable" },
        response: new Response(null, { status }),
      } as never);
      await expect(listTabMetadata("ws")).rejects.toMatchObject({ status });
    },
  );
  it("accepts a successful empty tab list and forwards cancellation", async () => {
    const signal = new AbortController().signal;
    mockApiGet.mockResolvedValueOnce({
      data: { success: true, data: [] },
      response: new Response(),
    } as never);
    await expect(listTabMetadata("ws", { signal })).resolves.toEqual([]);
    expect(mockApiGet).toHaveBeenCalledWith(
      "/api/workspaces/{ws}/terminal/tabs",
      { params: { path: { ws: "ws" } }, signal },
    );
  });
  it.each([
    { success: false, data: {} },
    { success: true },
    { success: true, data: null },
    { success: true, data: { task: "invalid" } },
  ])("rejects a false or malformed map", async (data) => {
    vi.mocked(get).mockResolvedValueOnce(data);
    await expect(listSessionsByIssue("ws")).rejects.toThrow(
      "Invalid sessions-by-issue response",
    );
  });
  it("propagates unavailable map storage", async () => {
    vi.mocked(get).mockRejectedValueOnce(new ApiError(503, "unavailable"));
    await expect(listSessionsByIssue("ws")).rejects.toMatchObject({
      status: 503,
    });
  });
  it("accepts a successful empty map", async () => {
    vi.mocked(get).mockResolvedValueOnce({ success: true, data: {} });
    await expect(listSessionsByIssue("ws")).resolves.toEqual({});
  });
});
