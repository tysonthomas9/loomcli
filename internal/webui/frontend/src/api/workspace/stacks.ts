import { get, unwrapResponse, wsUrl } from "@/api/common";

export interface WorkspaceStackNode {
  task_id: string;
  base_task_id?: string;
  output_branch: string;
  base_ref?: string;
  position: number;
}

export interface WorkspaceStack {
  id: string;
  repo: string;
  root_base: string;
  nodes: WorkspaceStackNode[];
}

export interface WorkspaceStacksData {
  stacks: WorkspaceStack[];
}

interface WorkspaceStacksEnvelope {
  success: boolean;
  data?: WorkspaceStacksData;
  error?: string;
}

export async function fetchWorkspaceStacks(
  workspaceId: string,
): Promise<WorkspaceStacksData> {
  const response = await get<WorkspaceStacksEnvelope>(
    wsUrl(workspaceId, "/stacks"),
  );
  return unwrapResponse(response);
}
