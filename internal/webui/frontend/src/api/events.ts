/**
 * API functions for issue events (audit trail).
 */

import type { Event } from "@/types";

import { get, ApiError } from "./client";

interface ApiSuccess<T> {
  success: true;
  data: T;
}

interface ApiFailure {
  success: false;
  error: string;
}

type ApiResult<T> = ApiSuccess<T> | ApiFailure;

function unwrap<T>(response: ApiResult<T>): T {
  if (!response.success) {
    throw new ApiError(0, response.error);
  }
  return response.data;
}

/**
 * Fetch events for an issue.
 * Returns audit trail events (status changes, label changes, dependency changes, etc.)
 */
export async function getIssueEvents(
  issueId: string,
  limit = 100,
): Promise<Event[]> {
  const params = limit !== 100 ? `?limit=${limit}` : "";
  const response = await get<ApiResult<Event[]>>(
    `/api/issues/${encodeURIComponent(issueId)}/events${params}`,
  );
  return unwrap(response);
}
