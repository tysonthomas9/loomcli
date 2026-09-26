/**
 * API functions for issue events (audit trail).
 * Uses openapi-fetch generated client.
 */

import type { Event } from "@/types";

import { api, apiErrorFromResponse } from "@/api/common";

/**
 * Fetch events for an issue.
 * Returns audit trail events (status changes, label changes, dependency changes, etc.)
 */
export async function getIssueEvents(
  workspaceId: string,
  issueId: string,
  limit = 100,
): Promise<Event[]> {
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/issues/{id}/events",
    {
      params: {
        path: { ws: workspaceId, id: issueId },
        query: { limit },
      },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  if (!data || !data.success) {
    throw new Error("Failed to fetch events");
  }
  return data.data as unknown as Event[];
}
