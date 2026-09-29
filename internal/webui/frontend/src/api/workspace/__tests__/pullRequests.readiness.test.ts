/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for the read-only PR readiness API client functions.
 * Verifies URL construction (repeated `pr` params, `force`) and that the
 * envelope's `data` is returned unchanged.
 */

import { beforeEach, describe, expect, it, vi } from "vitest";

import {
  fetchPullRequestReadiness,
  fetchPullRequestReadinessPreview,
  type PullRequestReadinessList,
  type PullRequestReadinessPreviewResponse,
} from "../pullRequests";

const mockGet = vi.fn();

vi.mock("@/api/common", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/common")>();
  return {
    ...actual,
    get: (...args: unknown[]) => mockGet(...args),
  };
});

const listData: PullRequestReadinessList = {
  server_now: "2026-09-24T12:00:00Z",
  fresh_for_s: 60,
  stale_after_s: 600,
  pull_requests: [
    {
      pr_key: "github:octocat/hello#7",
      freshness: "unknown",
      age_seconds: 0,
      current_verdict: "unknown",
      current_reasons: ["not_observed"],
    },
  ],
  repo_errors: [],
};

const previewData: PullRequestReadinessPreviewResponse = {
  server_now: "2026-09-24T12:00:00Z",
  fresh_for_s: 60,
  stale_after_s: 600,
  preview: { members: [], ready_count: 0, fingerprint: "abc" },
  repo_errors: [],
};

function calledUrl(): URL {
  expect(mockGet).toHaveBeenCalledTimes(1);
  return new URL(mockGet.mock.calls[0][0] as string, "http://localhost");
}

describe("fetchPullRequestReadiness", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("builds a URL with one repeated pr param per key and returns data", async () => {
    mockGet.mockResolvedValue({ success: true, data: listData });

    const result = await fetchPullRequestReadiness("test-ws", [
      "github:octocat/hello#7",
      "github:octocat/world#12",
    ]);

    const url = calledUrl();
    expect(url.pathname).toBe(
      "/api/workspaces/test-ws/pull-requests/readiness",
    );
    expect(url.searchParams.getAll("pr")).toEqual([
      "github:octocat/hello#7",
      "github:octocat/world#12",
    ]);
    expect(url.searchParams.has("force")).toBe(false);
    expect(result).toEqual(listData);
  });

  it("percent-encodes the # in PR keys so it is not a URL fragment", async () => {
    mockGet.mockResolvedValue({ success: true, data: listData });

    await fetchPullRequestReadiness("test-ws", ["github:octocat/hello#7"]);

    const raw = mockGet.mock.calls[0][0] as string;
    expect(raw).not.toContain("#");
    expect(raw).toContain("pr=github%3Aoctocat%2Fhello%237");
  });

  it("adds force=true when requested", async () => {
    mockGet.mockResolvedValue({ success: true, data: listData });

    await fetchPullRequestReadiness("test-ws", ["github:octocat/hello#7"], {
      force: true,
    });

    expect(calledUrl().searchParams.get("force")).toBe("true");
  });

  it("omits force when force is false", async () => {
    mockGet.mockResolvedValue({ success: true, data: listData });

    await fetchPullRequestReadiness("test-ws", ["github:octocat/hello#7"], {
      force: false,
    });

    expect(calledUrl().searchParams.has("force")).toBe(false);
  });

  it("encodes the workspace id", async () => {
    mockGet.mockResolvedValue({ success: true, data: listData });

    await fetchPullRequestReadiness("my ws", ["github:octocat/hello#7"]);

    expect(mockGet.mock.calls[0][0]).toMatch(
      /^\/api\/workspaces\/my%20ws\/pull-requests\/readiness\?/,
    );
  });

  it("propagates errors from get", async () => {
    mockGet.mockRejectedValue(new Error("boom"));

    await expect(
      fetchPullRequestReadiness("test-ws", ["github:octocat/hello#7"]),
    ).rejects.toThrow("boom");
  });
});

describe("fetchPullRequestReadinessPreview", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("keeps key order in repeated pr params and returns data", async () => {
    mockGet.mockResolvedValue({ success: true, data: previewData });

    const result = await fetchPullRequestReadinessPreview("test-ws", [
      "github:octocat/hello#9",
      "github:octocat/hello#3",
      "github:octocat/world#1",
    ]);

    const url = calledUrl();
    expect(url.pathname).toBe(
      "/api/workspaces/test-ws/pull-requests/readiness/preview",
    );
    expect(url.searchParams.getAll("pr")).toEqual([
      "github:octocat/hello#9",
      "github:octocat/hello#3",
      "github:octocat/world#1",
    ]);
    expect(url.searchParams.has("force")).toBe(false);
    expect(result).toEqual(previewData);
  });
});
