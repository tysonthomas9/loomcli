/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for abort signal forwarding in issue API functions.
 *
 * Verifies that getReadyIssues, getKanbanIssues, and fetchGraphIssues
 * forward the requestOptions.signal to the underlying get() call.
 */

import { describe, it, expect, beforeEach, vi } from "vitest";

import { getReadyIssues, getKanbanIssues, fetchGraphIssues } from "../issues";

// Mock the API client module
vi.mock("../client", () => ({
  get: vi.fn(),
  post: vi.fn(),
  patch: vi.fn(),
  del: vi.fn(),
  ApiError: class extends Error {
    status: number;
    statusText: string;
    constructor(status: number, statusText: string) {
      super(`API Error: ${status} ${statusText}`);
      this.name = "ApiError";
      this.status = status;
      this.statusText = statusText;
    }
  },
  wsUrl: (workspaceId: string, path: string) =>
    `/api/workspaces/${encodeURIComponent(workspaceId)}${path}`,
}));

import { get } from "../client";

const mockGet = vi.mocked(get);

const WORKSPACE_ID = "ws-test";

describe("getReadyIssues signal forwarding", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("does not pass requestOptions when none provided", async () => {
    mockGet.mockResolvedValueOnce({ success: true, data: [] });

    await getReadyIssues(WORKSPACE_ID);

    expect(mockGet).toHaveBeenCalledTimes(1);
    expect(mockGet).toHaveBeenCalledWith(expect.stringContaining("/ready"));
    // Called with only one argument (the URL), no options
    expect(mockGet.mock.calls[0]).toHaveLength(1);
  });

  it("forwards signal to get() when requestOptions is provided", async () => {
    mockGet.mockResolvedValueOnce({ success: true, data: [] });
    const controller = new AbortController();

    await getReadyIssues(WORKSPACE_ID, {}, { signal: controller.signal });

    expect(mockGet).toHaveBeenCalledTimes(1);
    expect(mockGet).toHaveBeenCalledWith(expect.stringContaining("/ready"), {
      signal: controller.signal,
    });
  });

  it("propagates AbortError when signal is aborted", async () => {
    const controller = new AbortController();
    const abortError = new DOMException(
      "The operation was aborted.",
      "AbortError",
    );
    mockGet.mockRejectedValueOnce(abortError);

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

  it("does not pass requestOptions when none provided", async () => {
    mockGet.mockResolvedValueOnce({ success: true, data: [] });

    await getKanbanIssues(WORKSPACE_ID);

    expect(mockGet).toHaveBeenCalledTimes(1);
    expect(mockGet).toHaveBeenCalledWith(expect.stringContaining("/issues"));
    // Called with only one argument (the URL), no options
    expect(mockGet.mock.calls[0]).toHaveLength(1);
  });

  it("forwards signal to get() when requestOptions is provided", async () => {
    mockGet.mockResolvedValueOnce({ success: true, data: [] });
    const controller = new AbortController();

    await getKanbanIssues(WORKSPACE_ID, {}, { signal: controller.signal });

    expect(mockGet).toHaveBeenCalledTimes(1);
    expect(mockGet).toHaveBeenCalledWith(expect.stringContaining("/issues"), {
      signal: controller.signal,
    });
  });

  it("propagates AbortError when signal is aborted", async () => {
    const controller = new AbortController();
    const abortError = new DOMException(
      "The operation was aborted.",
      "AbortError",
    );
    mockGet.mockRejectedValueOnce(abortError);

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

  it("does not pass requestOptions when none provided", async () => {
    mockGet.mockResolvedValueOnce({ success: true, issues: [] });

    await fetchGraphIssues(WORKSPACE_ID);

    expect(mockGet).toHaveBeenCalledTimes(1);
    expect(mockGet).toHaveBeenCalledWith(
      expect.stringContaining("/issues/graph"),
    );
    // Called with only one argument (the URL), no options
    expect(mockGet.mock.calls[0]).toHaveLength(1);
  });

  it("forwards signal to get() when requestOptions is provided", async () => {
    mockGet.mockResolvedValueOnce({ success: true, issues: [] });
    const controller = new AbortController();

    await fetchGraphIssues(WORKSPACE_ID, {}, { signal: controller.signal });

    expect(mockGet).toHaveBeenCalledTimes(1);
    expect(mockGet).toHaveBeenCalledWith(
      expect.stringContaining("/issues/graph"),
      { signal: controller.signal },
    );
  });

  it("propagates AbortError when signal is aborted", async () => {
    const controller = new AbortController();
    const abortError = new DOMException(
      "The operation was aborted.",
      "AbortError",
    );
    mockGet.mockRejectedValueOnce(abortError);

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
