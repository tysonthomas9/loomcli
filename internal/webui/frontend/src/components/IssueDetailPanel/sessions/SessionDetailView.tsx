/**
 * SessionDetailView — task Runs-tab wrapper around the shared session renderer.
 */

import { SessionRunDetail } from "@/components/SessionRunDetail";
import type { SessionRecord } from "@/types/agent";

export interface SessionDetailViewProps {
  taskId: string;
  session: SessionRecord;
}

export function SessionDetailView({
  taskId,
  session,
}: SessionDetailViewProps): JSX.Element {
  return <SessionRunDetail taskId={taskId} session={session} />;
}
