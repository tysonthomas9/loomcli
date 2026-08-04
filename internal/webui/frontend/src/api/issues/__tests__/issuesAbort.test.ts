/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for abort signal forwarding in issue API functions.
 *
 * Verifies that getReadyIssues, getKanbanIssues, and fetchGraphIssues
 * forward the requestOptions.signal to the underlying api.GET() call.
 */

import { describe, it, expect, beforeEach, vi } from "vitest";

import { getReadyIssues, getKanbanIssues, fetchGraphIssues } from "../issues";

import { api } from "@/api/common";

// Mock the API client module
vi.mock("@/api/common", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/common")>();
  return {
    ...actual,
    get: vi.fn(),
    post: vi.fn(),
    patch: vi.fn(),
    del: vi.fn(),
    api: {
      GET: vi.fn(),
      POST: vi.fn(),
      PATCH: vi.fn(),
      PUT: vi.fn(),
      DELETE: vi.fn(),
      use: vi.fn(),
    },
  };
});

const mockApiGet = vi.mocked(api.GET);

const WORKSPACE_ID = "ws-test";

describe("getReadyIssues signal forwarding", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("does not pass signal when none provided", async () => {
    mockApiGet.mockResolvedValueOnce({
      data: { success: true, data: [] },
      error: undefined,
      response: new Response(),
    } as never);

    await getReadyIssues(WORKSPACE_ID);

    expect(mockApiGet).toHaveBeenCalledTimes(1);
    expect(mockApiGet).toHaveBeenCalledWith(
      "/api/workspaces/{ws}/ready",
      expect.objectContaining({
        params: expect.objectContaining({
          path: { ws: WORKSPACE_ID },
        }),
      }),
    );
    // When no signal is provided, the call should NOT include a signal property
    const callArgs = mockApiGet.mock.calls[0][1]!;
    expect(callArgs).not.toHaveProperty("signal");
  });

  it("forwards signal to api.GET() when requestOptions is provided", async () => {
    mockApiGet.mockResolvedValueOnce({
      data: { success: true, data: [] },
      error: undefined,
      response: new Response(),
    } as never);
    const controller = new AbortController();

    await getReadyIssues(WORKSPACE_ID, {}, { signal: controller.signal });

    expect(mockApiGet).toHaveBeenCalledTimes(1);
    expect(mockApiGet).toHaveBeenCalledWith(
      "/api/workspaces/{ws}/ready",
      expect.objectContaining({
        signal: controller.signal,
      }),
    );
  });

  it("propagates AbortError when signal is aborted", async () => {
    const controller = new AbortController();
    const abortError = new DOMException(
      "The operation was aborted.",
      "AbortError",
    );
    mockApiGet.mockRejectedValueOnce(abortError);

    controller.abort();

    const rejection = getReadyIssues(
      WORKSPACE_ID,
      {},
      { signal: controller.signal },
    );
    await expect(rejection).rejects.toThrow(DOMException);
    await expect(rejection).rejects.toHaveProperty("name", "AbortError");
  });
});

describe("getKanbanIssues signal forwarding", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("does not pass signal when none provided", async () => {
    mockApiGet.mockResolvedValueOnce({
      data: { success: true, data: [] },
      error: undefined,
      response: new Response(),
    } as never);

    await getKanbanIssues(WORKSPACE_ID);

    expect(mockApiGet).toHaveBeenCalledTimes(1);
    expect(mockApiGet).toHaveBeenCalledWith(
      "/api/workspaces/{ws}/issues",
      expect.objectContaining({
        params: expect.objectContaining({
          path: { ws: WORKSPACE_ID },
        }),
      }),
    );
    // When no signal is provided, the call should NOT include a signal property
    const callArgs = mockApiGet.mock.calls[0][1]!;
    expect(callArgs).not.toHaveProperty("signal");
  });

  it("forwards signal to api.GET() when requestOptions is provided", async () => {
    mockApiGet.mockResolvedValueOnce({
      data: { success: true, data: [] },
      error: undefined,
      response: new Response(),
    } as never);
    const controller = new AbortController();

    await getKanbanIssues(WORKSPACE_ID, {}, { signal: controller.signal });

    expect(mockApiGet).toHaveBeenCalledTimes(1);
    expect(mockApiGet).toHaveBeenCalledWith(
      "/api/workspaces/{ws}/issues",
      expect.objectContaining({
        signal: controller.signal,
      }),
    );
  });

  it("propagates AbortError when signal is aborted", async () => {
    const controller = new AbortController();
    const abortError = new DOMException(
      "The operation was aborted.",
      "AbortError",
    );
    mockApiGet.mockRejectedValueOnce(abortError);

    controller.abort();

    const rejection = getKanbanIssues(
      WORKSPACE_ID,
      {},
      { signal: controller.signal },
    );
    await expect(rejection).rejects.toThrow(DOMException);
    await expect(rejection).rejects.toHaveProperty("name", "AbortError");
  });
});

describe("fetchGraphIssues signal forwarding", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("does not pass signal when none provided", async () => {
    mockApiGet.mockResolvedValueOnce({
      data: { success: true, data: [] },
      error: undefined,
      response: new Response(),
    } as never);

    await fetchGraphIssues(WORKSPACE_ID);

    expect(mockApiGet).toHaveBeenCalledTimes(1);
    expect(mockApiGet).toHaveBeenCalledWith(
      "/api/workspaces/{ws}/issues/graph",
      expect.objectContaining({
        params: expect.objectContaining({
          path: { ws: WORKSPACE_ID },
        }),
      }),
    );
    // When no signal is provided, the call should NOT include a signal property
    const callArgs = mockApiGet.mock.calls[0][1]!;
    expect(callArgs).not.toHaveProperty("signal");
  });

  it("forwards signal to api.GET() when requestOptions is provided", async () => {
    mockApiGet.mockResolvedValueOnce({
      data: { success: true, data: [] },
      error: undefined,
      response: new Response(),
    } as never);
    const controller = new AbortController();

    await fetchGraphIssues(WORKSPACE_ID, {}, { signal: controller.signal });

    expect(mockApiGet).toHaveBeenCalledTimes(1);
    expect(mockApiGet).toHaveBeenCalledWith(
      "/api/workspaces/{ws}/issues/graph",
      expect.objectContaining({
        signal: controller.signal,
      }),
    );
  });

  it("propagates AbortError when signal is aborted", async () => {
    const controller = new AbortController();
    const abortError = new DOMException(
      "The operation was aborted.",
      "AbortError",
    );
    mockApiGet.mockRejectedValueOnce(abortError);

    controller.abort();

    const rejection = fetchGraphIssues(
      WORKSPACE_ID,
      {},
      { signal: controller.signal },
    );
    await expect(rejection).rejects.toThrow(DOMException);
    await expect(rejection).rejects.toHaveProperty("name", "AbortError");
  });
});
