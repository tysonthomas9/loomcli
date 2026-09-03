/**
 * @vitest-environment jsdom
 */

import { beforeEach, describe, expect, it, vi } from "vitest";

import { api } from "@/api/common";
import { getTabMetadata, listTabMetadata, patchTabMetadata } from "../terminal";

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

  describe("listTabMetadata", () => {
    it("returns the tab list on success", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: { success: true, data: [{ session_name: "lead-1" }] },
        error: undefined,
        response: new Response(null, { status: 200, statusText: "OK" }),
      } as never);

      await expect(listTabMetadata("default")).resolves.toEqual([
        { session_name: "lead-1" },
      ]);
    });

    it.each([
      [404, "Not Found"],
      [503, "Service Unavailable"],
    ])(
      "propagates %i rather than swallowing it into an empty list",
      async (status, statusText) => {
        // Swallowing these made an unavailable metadata store look identical
        // to a workspace with no tabs. Classification is the caller's job now.
        mockApiGet.mockResolvedValueOnce({
          data: undefined,
          error: { error: statusText },
          response: new Response(null, { status, statusText }),
        } as never);

        await expect(listTabMetadata("default")).rejects.toMatchObject({
          name: "ApiError",
          status,
        });
      },
    );

    it("propagates other API errors", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: undefined,
        error: { error: "boom" },
        response: new Response(null, {
          status: 500,
          statusText: "Internal Server Error",
        }),
      } as never);

      await expect(listTabMetadata("default")).rejects.toMatchObject({
        name: "ApiError",
        status: 500,
      });
    });
  });
});
