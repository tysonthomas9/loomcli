/**
 * @vitest-environment jsdom
 */

import { beforeEach, describe, expect, it, vi } from "vitest";

import { api } from "@/api/common";
import {
  dismissTabRestartNotice,
  getTabMetadata,
  patchTabMetadata,
} from "../terminal";

vi.mock("@/api/common", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/common")>();
  return {
    ...actual,
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

  it("dismissTabRestartNotice patches replaced_at to the empty string", async () => {
    mockApiPatch.mockResolvedValueOnce({
      data: { success: true, data: { session_name: "lead-1" } },
      error: undefined,
      response: new Response(null, { status: 200, statusText: "OK" }),
    } as never);

    await dismissTabRestartNotice("default", "lead-1");

    expect(mockApiPatch).toHaveBeenCalledWith(
      "/api/workspaces/{ws}/terminal/tabs/{session}",
      {
        params: { path: { ws: "default", session: "lead-1" } },
        body: { replaced_at: "" },
      },
    );
  });

  it("patchTabMetadata accepts replaced_at (type-level coverage)", async () => {
    mockApiPatch.mockResolvedValueOnce({
      data: { success: true, data: { session_name: "lead-1" } },
      error: undefined,
      response: new Response(null, { status: 200, statusText: "OK" }),
    } as never);

    await patchTabMetadata("default", "lead-1", {
      replaced_at: "2026-08-15T10:00:00Z",
    });

    expect(mockApiPatch).toHaveBeenCalledWith(
      "/api/workspaces/{ws}/terminal/tabs/{session}",
      expect.objectContaining({
        body: { replaced_at: "2026-08-15T10:00:00Z" },
      }),
    );
  });
});
