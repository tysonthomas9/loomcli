import { useOutletContext } from "react-router-dom";

import type { RepoInfo } from "@/api/workspace";
import type { ToastOptions } from "@/hooks/ui";
import type { Issue, LoomAgentStatus } from "@/types";

export interface SourceControlRouteContext {
  workspaceId: string;
  repos: RepoInfo[];
  issues: Issue[];
  agents: LoomAgentStatus[];
  refetchIssues: () => void | Promise<void>;
  openIssue: (issue: Issue) => void;
  showToast: (message: string, options?: ToastOptions) => string;
}

interface WorkspaceOutletContext {
  sourceControl?: SourceControlRouteContext;
}

/**
 * Read the app-composed inputs for the Source Control route.
 *
 * The feature deliberately does not import WorkspaceViewContext or workspace
 * hooks. App owns shell composition and passes only the data/actions this
 * vertical slice needs through React Router's outlet context.
 */
export function useSourceControlContext(): SourceControlRouteContext {
  const value = useOutletContext<WorkspaceOutletContext>().sourceControl;
  if (!value) {
    throw new Error("Source Control route is missing app composition");
  }
  return value;
}
