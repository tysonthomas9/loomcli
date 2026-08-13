import { api, apiErrorFromResponse } from "@/api/common";
import type { components } from "@/types/generated/openapi";

export type TerminalHistoryRun = components["schemas"]["TerminalHistoryRun"];
export type TerminalHistoryLine = components["schemas"]["TerminalHistoryLine"];
export type TerminalHistoryResponse =
  components["schemas"]["TerminalHistoryResponse"];
export type TerminalHistoryMeta = components["schemas"]["TerminalHistoryMeta"];

export async function getTerminalHistory(
  workspaceId: string,
  sessionName: string,
  generation: string,
  from: number,
  count: number,
  signal?: AbortSignal,
): Promise<TerminalHistoryResponse> {
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/terminal/sessions/{session}/history",
    {
      params: {
        path: { ws: workspaceId, session: sessionName },
        query: { generation, from, count },
      },
      ...(signal ? { signal } : {}),
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return data as TerminalHistoryResponse;
}

export async function getTerminalHistoryMeta(
  workspaceId: string,
  sessionName: string,
  signal?: AbortSignal,
): Promise<TerminalHistoryMeta> {
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/terminal/sessions/{session}/history/meta",
    {
      params: { path: { ws: workspaceId, session: sessionName } },
      ...(signal ? { signal } : {}),
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return data as TerminalHistoryMeta;
}
