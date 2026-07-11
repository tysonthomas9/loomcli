import { useCallback, useRef, useState } from "react";
import { useStore } from "zustand";

import type { Issue } from "@/types";
import { useAgentStoreInstance } from "@/hooks/common";
import { useLocalSettings, useWorkspaceContext } from "@/hooks/workspace";
import { startEpicRunnerForIssue } from "@/hooks/workspace/startEpicRunnerForIssue";

export interface UseRunEpicWorkflowOptions {
  showToast: (message: string, options: { type: "success" | "error" }) => void;
}

export function useRunEpicWorkflow({ showToast }: UseRunEpicWorkflowOptions): {
  runEpic: (issue: Issue) => Promise<void>;
  isRunningEpic: (epicId: string) => boolean;
} {
  const {
    workspaceId,
    repos,
    agents: workspaceAgents,
    upsertAgent,
  } = useWorkspaceContext();
  const { settings: localSettings } = useLocalSettings();
  const agentStore = useAgentStoreInstance();
  const fleetAgents = useStore(agentStore, (s) => s.agents);
  const [runningEpicIds, setRunningEpicIds] = useState<Set<string>>(
    () => new Set(),
  );
  // Synchronous re-entrancy guard. setRunningEpicIds is async, so two clicks in
  // the same frame would both pass a runningEpicIds.has() check and both submit.
  const inFlightRef = useRef<Set<string>>(new Set());

  const isRunningEpic = useCallback(
    (epicId: string) => runningEpicIds.has(epicId),
    [runningEpicIds],
  );

  const runEpic = useCallback(
    async (issue: Issue) => {
      if (issue.issue_type !== "epic" || inFlightRef.current.has(issue.id)) {
        return;
      }

      inFlightRef.current.add(issue.id);
      setRunningEpicIds((prev) => new Set(prev).add(issue.id));
      try {
        const workspaceAgentNames = new Set<string>([
          ...workspaceAgents.map((agent) => agent.name),
          ...fleetAgents.map((agent) => agent.name),
        ]);
        const { run, leadAgentName } = await startEpicRunnerForIssue({
          workspaceId,
          issue,
          repos,
          workspaceAgentNames,
          localSettings,
          upsertAgent,
        });
        await agentStore.getState().fetchData();
        showToast(`Epic runner queued for ${leadAgentName}: ${run.run_id}`, {
          type: "success",
        });
        // Intentionally leave issue.id in runningEpicIds on success: the lead
        // we just created has no parent yet (the epic->lead binding happens
        // server-side), so the claim-derived badge that hides this button has
        // not flipped. Keeping the button disabled until the claim lands stops
        // a second click from starting a duplicate run.
      } catch (err) {
        showToast(
          `Epic runner failed: ${
            err instanceof Error ? err.message : "Unable to start workflow"
          }`,
          { type: "error" },
        );
        // Start failed — re-enable so the user can retry.
        setRunningEpicIds((prev) => {
          const next = new Set(prev);
          next.delete(issue.id);
          return next;
        });
      } finally {
        inFlightRef.current.delete(issue.id);
      }
    },
    [
      agentStore,
      fleetAgents,
      localSettings,
      repos,
      showToast,
      upsertAgent,
      workspaceAgents,
      workspaceId,
    ],
  );

  return { runEpic, isRunningEpic };
}
