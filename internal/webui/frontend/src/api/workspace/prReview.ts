import { api, apiErrorFromResponse, unwrapResponse } from "@/api/common";
import type { components } from "@/types/generated/openapi";

export type PullRequestDetail = components["schemas"]["PullRequestDetail"];
export type PullRequestDiff = components["schemas"]["PullRequestDiff"];
export type PullRequestReviewResult =
  components["schemas"]["PullRequestReviewResult"];
export type PullRequestReviewEvent = "approve" | "request_changes" | "comment";
export type ReviewerEnsureResult =
  components["schemas"]["ReviewerEnsureResult"];
export type ReviewerMessageResult =
  components["schemas"]["ReviewerMessageResult"];
export type ReviewerConversation =
  components["schemas"]["ReviewerConversation"];
export type ReviewerMessage = components["schemas"]["ReviewerMessage"];

export async function getPullRequestDetail(
  ws: string,
  owner: string,
  repo: string,
  number: number,
): Promise<PullRequestDetail> {
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}",
    {
      params: { path: { ws, owner, repo, number } },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return unwrapResponse(data, response);
}

export async function getPullRequestDiff(
  ws: string,
  owner: string,
  repo: string,
  number: number,
): Promise<PullRequestDiff> {
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/diff",
    {
      params: { path: { ws, owner, repo, number } },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return unwrapResponse(data, response);
}

export async function postPullRequestReview(
  ws: string,
  owner: string,
  repo: string,
  number: number,
  input: {
    event: PullRequestReviewEvent;
    body?: string;
    expected_head_sha: string;
  },
): Promise<PullRequestReviewResult> {
  const { data, error, response } = await api.POST(
    "/api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/review",
    {
      params: { path: { ws, owner, repo, number } },
      body: input,
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return unwrapResponse(data, response);
}

/** Stand up (or reuse) the per-PR codex review agent on a PR-head checkout. */
export async function ensureReviewer(
  ws: string,
  owner: string,
  repo: string,
  number: number,
): Promise<ReviewerEnsureResult> {
  const { data, error, response } = await api.POST(
    "/api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/reviewer",
    {
      params: { path: { ws, owner, repo, number } },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return unwrapResponse(data, response);
}

/** Send a chat turn to the per-PR review agent (must be started first). */
export async function sendReviewerMessage(
  ws: string,
  owner: string,
  repo: string,
  number: number,
  text: string,
): Promise<ReviewerMessageResult> {
  const { data, error, response } = await api.POST(
    "/api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/messages",
    {
      params: { path: { ws, owner, repo, number } },
      body: { text },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return unwrapResponse(data, response);
}

/**
 * Fetch the reviewer conversation snapshot. Pass `after` — the opaque cursor of
 * the last message the client already holds — to receive only newer messages
 * (`reset: false`); omit it for a full snapshot (`reset: true`).
 */
export async function getReviewerConversation(
  ws: string,
  owner: string,
  repo: string,
  number: number,
  after?: string,
): Promise<ReviewerConversation> {
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/conversation",
    {
      params: after
        ? { path: { ws, owner, repo, number }, query: { after } }
        : { path: { ws, owner, repo, number } },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return unwrapResponse(data, response);
}
