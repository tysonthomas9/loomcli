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
    api: { GET: vi.fn(), POST: vi.fn() },
    apiErrorFromResponse: vi.fn((error: unknown, response?: Response) => {
      return new MockApiError(
        response?.status ?? 0,
        response?.statusText ?? "Network error",
        error,
      );
    }),
    unwrapResponse: vi.fn(
      <T>(
        envelope:
          | {
              success: boolean;
              data?: T;
              error?: string;
            }
          | null
          | undefined,
        response?: Response,
      ) => {
        if (envelope == null) {
          throw new MockApiError(
            response?.status ?? 0,
            response?.statusText ?? "Invalid API response",
            "missing response envelope",
          );
        }
        if (!envelope.success) {
          throw new MockApiError(
            response?.status ?? 0,
            response?.statusText ?? envelope.error ?? "Unknown error",
            envelope.error,
          );
        }
        return envelope.data as T;
      },
    ),
    ApiError: MockApiError,
  };
});

let getPullRequestDiff: typeof import("../prReview").getPullRequestDiff;
let getPullRequestDetail: typeof import("../prReview").getPullRequestDetail;
let getReviewerConversation: typeof import("../prReview").getReviewerConversation;
let postPullRequestReview: typeof import("../prReview").postPullRequestReview;
let mockApiGet: ReturnType<typeof vi.fn>;
let mockApiPost: ReturnType<typeof vi.fn>;

describe("prReview API", () => {
  beforeEach(async () => {
    vi.clearAllMocks();
    vi.resetModules();

    const common = await import("@/api/common");
    mockApiGet = vi.mocked(common.api.GET);
    mockApiPost = vi.mocked(common.api.POST);

    const prReview = await import("../prReview");
    getPullRequestDiff = prReview.getPullRequestDiff;
    getPullRequestDetail = prReview.getPullRequestDetail;
    getReviewerConversation = prReview.getReviewerConversation;
    postPullRequestReview = prReview.postPullRequestReview;
  });

  it("fetches and unwraps a pull request diff", async () => {
    const diff = {
      files: [
        {
          path: "src/main.ts",
          status: "modified",
          additions: 2,
          deletions: 1,
          patch: "@@ -1,1 +1,2 @@\n-old\n+new",
        },
      ],
      diff: "diff --git a/src/main.ts b/src/main.ts",
    };
    mockApiGet.mockResolvedValueOnce({
      data: { success: true, data: diff },
      error: undefined,
      response: new Response(null, { status: 200, statusText: "OK" }),
    });

    const result = await getPullRequestDiff("WS", "octocat", "hello", 7);

    expect(mockApiGet).toHaveBeenCalledTimes(1);
    expect(mockApiGet).toHaveBeenCalledWith(
      "/api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/diff",
      {
        params: {
          path: { ws: "WS", owner: "octocat", repo: "hello", number: 7 },
        },
      },
    );
    expect(result).toEqual(diff);
  });

  it("fetches and unwraps pull request detail", async () => {
    const detail = {
      number: 7,
      state: "OPEN",
      title: "Add connector diff",
      is_draft: false,
      head_ref_name: "feature",
      base_ref_name: "main",
      head_sha: "abc123",
      merged: false,
    };
    mockApiGet.mockResolvedValueOnce({
      data: { success: true, data: detail },
      error: undefined,
      response: new Response(null, { status: 200, statusText: "OK" }),
    });

    const result = await getPullRequestDetail("WS", "octocat", "hello", 7);

    expect(mockApiGet).toHaveBeenCalledWith(
      "/api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}",
      {
        params: {
          path: { ws: "WS", owner: "octocat", repo: "hello", number: 7 },
        },
      },
    );
    expect(result).toEqual(detail);
  });

  it("fetches and unwraps the reviewer conversation", async () => {
    const conversation = {
      state: "idle",
      messages: [
        {
          turn_id: "t1",
          item_id: "i1",
          role: "user",
          text: "hello",
        },
      ],
    };
    mockApiGet.mockResolvedValueOnce({
      data: { success: true, data: conversation },
      error: undefined,
      response: new Response(null, { status: 200, statusText: "OK" }),
    });

    const result = await getReviewerConversation("WS", "octocat", "hello", 7);

    expect(mockApiGet).toHaveBeenCalledWith(
      "/api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/conversation",
      {
        params: {
          path: { ws: "WS", owner: "octocat", repo: "hello", number: 7 },
        },
      },
    );
    expect(result).toEqual(conversation);
  });

  it("posts and unwraps a pull request review", async () => {
    const review = {
      review_id: 123,
      state: "APPROVED",
    };
    mockApiPost.mockResolvedValueOnce({
      data: { success: true, data: review },
      error: undefined,
      response: new Response(null, { status: 200, statusText: "OK" }),
    });

    const result = await postPullRequestReview("WS", "octocat", "hello", 7, {
      event: "approve",
      body: "Looks good",
      expected_head_sha: "abc123",
    });

    expect(mockApiPost).toHaveBeenCalledTimes(1);
    expect(mockApiPost).toHaveBeenCalledWith(
      "/api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/review",
      {
        params: {
          path: { ws: "WS", owner: "octocat", repo: "hello", number: 7 },
        },
        body: {
          event: "approve",
          body: "Looks good",
          expected_head_sha: "abc123",
        },
      },
    );
    expect(result).toEqual(review);
  });

  it("surfaces openapi-fetch errors as ApiError", async () => {
    mockApiGet.mockResolvedValueOnce({
      data: undefined,
      error: { error: "connector unavailable" },
      response: new Response(null, {
        status: 503,
        statusText: "Service Unavailable",
      }),
    });

    await expect(
      getPullRequestDiff("WS", "octocat", "hello", 7),
    ).rejects.toMatchObject({
      name: "ApiError",
      status: 503,
      statusText: "Service Unavailable",
      body: { error: "connector unavailable" },
    });
  });
});
