import { createContext, useContext, useMemo } from "react";
import type { WorkspaceAgentInfo } from "@/api/workspace";
import { useWorkspaceContext } from "@/hooks";

/** An agent to browse that the workspace's agent list lacks (an Agent API agent). */
export const ExtraBrowserAgent = createContext<WorkspaceAgentInfo | undefined>(
  undefined,
);

/** The workspace's agents, plus the ExtraBrowserAgent when one is given. */
export function useBrowserAgents(): WorkspaceAgentInfo[] {
  const { agents } = useWorkspaceContext();
  const extra = useContext(ExtraBrowserAgent);
  return useMemo(() => (extra ? [...agents, extra] : agents), [agents, extra]);
}
