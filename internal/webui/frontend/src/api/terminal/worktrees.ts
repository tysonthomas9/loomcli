import { api, apiErrorFromResponse } from "@/api/common";
import type { components } from "@/types/generated/openapi";

export type TerminalWorktreeResultStatus =
  components["schemas"]["TerminalWorktreeResult"]["status"];

export type TerminalWorktreeResult =
  components["schemas"]["TerminalWorktreeResult"];

export type TerminalWorktreeMember =
  components["schemas"]["TerminalWorktreeMember"];

export type TerminalWorktreeGroup =
  components["schemas"]["TerminalWorktreeGroup"];

export type ListTerminalWorktreesResponse =
  components["schemas"]["TerminalWorktreeListResponse"];

export type CreateTerminalWorktreeRequest =
  components["schemas"]["TerminalWorktreeCreateRequest"];

export type CreateTerminalWorktreeResponse =
  components["schemas"]["TerminalWorktreeCreateResponse"];

export type TerminalWorktreeErrorBody =
  components["schemas"]["TerminalWorktreeErrorResponse"];

export async function listTerminalWorktrees(
  workspaceId: string,
): Promise<ListTerminalWorktreesResponse> {
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/terminal/worktrees",
    {
      params: { path: { ws: workspaceId } },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return data!;
}

export async function createTerminalWorktree(
  workspaceId: string,
  request: CreateTerminalWorktreeRequest,
): Promise<CreateTerminalWorktreeResponse> {
  const { data, error, response } = await api.POST(
    "/api/workspaces/{ws}/terminal/worktrees",
    {
      params: { path: { ws: workspaceId } },
      body: request,
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return data!;
}
