/**
 * Unit tests for the diff-stat API client (diff-stat.ts).
 *
 * These tests verify that fetchIssueDiffStat calls the correct URL
 * and handles responses properly.
 */

import { describe, it, expect, vi, beforeEach } from "vitest";

import { fetchIssueDiffStat } from "../diff-stat";
import { get } from "@/api/common";

vi.mock("@/api/common", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/common")>();
  return {
    ...actual,
    get: vi.fn(),
  };
});

const mockGet = get as ReturnType<typeof vi.fn>;

describe("fetchIssueDiffStat", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("returns parsed data on success", async () => {
    const diffStat = {
      branch: "feature-x",
      added: 42,
      removed: 7,
    };
    mockGet.mockResolvedValue(diffStat);

    const result = await fetchIssueDiffStat("test-ws-id", "issue-123");

    expect(result).toEqual(diffStat);
    expect(mockGet).toHaveBeenCalledWith(
      "/api/workspaces/test-ws-id/issues/issue-123/git/diff-stat",
    );
  });

  it("encodes issue ID with special characters in URL", async () => {
    mockGet.mockResolvedValue({ branch: "main", added: 0, removed: 0 });

    await fetchIssueDiffStat("test-ws-id", "issue/with spaces");

    expect(mockGet).toHaveBeenCalledWith(
      "/api/workspaces/test-ws-id/issues/issue%2Fwith%20spaces/git/diff-stat",
    );
  });

  it("throws on failure response", async () => {
    mockGet.mockRejectedValue(
      new Error("API Error: 500 Internal Server Error"),
    );

    await expect(fetchIssueDiffStat("bad-issue")).rejects.toThrow(
      "API Error: 500 Internal Server Error",
    );
  });

  it("propagates network errors", async () => {
    mockGet.mockRejectedValue(new Error("Network error"));

    await expect(fetchIssueDiffStat("issue-123")).rejects.toThrow(
      "Network error",
    );
  });
});
