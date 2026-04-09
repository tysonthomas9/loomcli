/**
 * Loom Agent API client.
 * Fetches workspace-scoped agent status.
 */

import type { LoomAgentStatus } from "@/types";
import { get, wsUrl } from "./client";

/**
 * Fetch agents for a specific workspace by scanning its worktrees directory.
 * Returns agents scoped to the workspace.
 */
export async function fetchWorkspaceAgents(
  workspaceId: string,
): Promise<LoomAgentStatus[]> {
  const data = await get<{ agents: LoomAgentStatus[] }>(
    wsUrl(workspaceId, "/agents"),
    { timeout: 10000 },
  );
  return data.agents ?? [];
}
