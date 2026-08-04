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
    api: { GET: vi.fn() },
    apiErrorFromResponse: vi.fn((error: unknown, response?: Response) => {
      return new MockApiError(
        response?.status ?? 0,
        response?.statusText ?? "Network error",
        error,
      );
    }),
    get: vi.fn(),
    post: vi.fn(),
    patch: vi.fn(),
    put: vi.fn(),
    del: vi.fn(),
    ApiError: MockApiError,
  };
});

let fetchWorkspaceApi: typeof import("../workspace").fetchWorkspaceApi;
let mockGet: ReturnType<typeof vi.fn>;

describe("workspace API errors", () => {
  beforeEach(async () => {
    vi.clearAllMocks();
    vi.resetModules();

    const common = await import("@/api/common");
    mockGet = vi.mocked(common.api.GET);

    const workspaceMod = await import("../workspace");
    fetchWorkspaceApi = workspaceMod.fetchWorkspaceApi;
  });

  it("preserves HTTP status when the workspace envelope reports failure", async () => {
    mockGet.mockResolvedValueOnce({
      data: { success: false, error: "workspace not found" },
      response: new Response(null, { status: 404, statusText: "Not Found" }),
    });

    try {
      await fetchWorkspaceApi("missing-workspace");
      throw new Error("expected fetchWorkspaceApi to reject");
    } catch (error) {
      expect(error).toMatchObject({
        name: "ApiError",
        status: 404,
        statusText: "Not Found",
        body: "workspace not found",
      });
    }
  });

  it("throws ApiError instead of TypeError when a successful response has no envelope", async () => {
    mockGet.mockResolvedValueOnce({
      data: undefined,
      response: new Response(null, { status: 200, statusText: "OK" }),
    });

    try {
      await fetchWorkspaceApi("missing-envelope");
      throw new Error("expected fetchWorkspaceApi to reject");
    } catch (error) {
      expect(error).toMatchObject({
        name: "ApiError",
        status: 200,
        statusText: "OK",
        body: "missing response envelope",
      });
    }
  });
});
