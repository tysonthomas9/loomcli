import { createContext, useContext, useMemo } from "react";
import type { WorkspaceAgentInfo } from "@/api/workspace";
import { useWorkspaceContext } from "@/hooks";

/**
 * An agent to browse that the workspace's agent list lacks: an Agent API
 * agent. Its worktree is read-only here; the server refuses writes to it.
 */
export const ExtraBrowserAgent = createContext<WorkspaceAgentInfo | undefined>(
  undefined,
);

/**
 * The workspace's agents plus the ExtraBrowserAgent, and whether the browser
 * is read-only because it shows that agent.
 */
export function useBrowserAgents(): {
  agents: WorkspaceAgentInfo[];
  readOnly: boolean;
} {
  const { agents } = useWorkspaceContext();
  const extra = useContext(ExtraBrowserAgent);
  return useMemo(
    () => ({ agents: extra ? [...agents, extra] : agents, readOnly: !!extra }),
    [agents, extra],
  );
}
