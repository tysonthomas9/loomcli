/**
 * API functions for issue tab state persistence.
 * Uses openapi-fetch generated client.
 */

import { api, apiErrorFromResponse, unwrapResponse } from "@/api/common";

// ============= Types =============

export interface IssueTab {
  id: string;
  type: "details" | "logs" | "terminal" | "sessions";
  label: string;
  session_name?: string;
  backend?: string;
  sort_order: number;
}

export interface IssueTabState {
  issue_id: string;
  tabs: IssueTab[];
  active_tab_id: string;
  updated_at: string;
}

// ============= API Functions =============

/**
 * Fetch persisted tab state for an issue.
 * Returns null if no saved state exists.
 */
export async function fetchIssueTabState(
  workspaceId: string,
  issueId: string,
): Promise<IssueTabState | null> {
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/issues/{issueId}/tabs",
    {
      params: {
        path: { ws: workspaceId, issueId },
      },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return (unwrapResponse(data, response) as IssueTabState | null) ?? null;
}

/**
 * Save full tab state for an issue via PUT.
 */
export async function saveIssueTabState(
  workspaceId: string,
  issueId: string,
  tabs: IssueTab[],
  activeTabId: string,
): Promise<void> {
  const { error, response } = await api.PUT(
    "/api/workspaces/{ws}/issues/{issueId}/tabs",
    {
      params: {
        path: { ws: workspaceId, issueId },
      },
      body: {
        tabs: tabs.map((t) => {
          const tab: IssueTab = {
            id: t.id,
            type: t.type,
            label: t.label,
            sort_order: t.sort_order,
          };
          if (t.session_name !== undefined) tab.session_name = t.session_name;
          if (t.backend !== undefined) tab.backend = t.backend;
          return tab;
        }),
        active_tab_id: activeTabId,
      },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
}

/**
 * Delete persisted tab state for an issue.
 */
export async function deleteIssueTabState(
  workspaceId: string,
  issueId: string,
): Promise<void> {
  const { error, response } = await api.DELETE(
    "/api/workspaces/{ws}/issues/{issueId}/tabs",
    {
      params: {
        path: { ws: workspaceId, issueId },
      },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
}
