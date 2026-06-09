/**
 * AgentsPage — agent terminal + work-context view.
 *
 * Layout (driven by App.tsx):
 *   [AgentIconRail (App-level sidebar on /agents only)]
 *   [AgentDetailMain — embedded TerminalView attached to selected agent]
 *   [right column — either AgentWorkPanel OR inline IssueDetailPanel]
 *
 * Selection model:
 * - Agent: URL-driven via :agentName (clicking an avatar in the rail
 *   navigates to /agents/<name>). Bare /agents auto-redirects to the first
 *   agent.
 * - Task: local state (selectedTask). When set, the right column hides the
 *   work panel and renders the IssueDetailPanel inline. Closing the panel
 *   restores the work panel. Switching agent in the rail clears the task
 *   selection so the page never shows stale details.
 */

import { Suspense, useCallback, useEffect, useMemo, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useStore } from "zustand";

import { ErrorBoundary, LoadingSkeleton } from "@/components";
import { AgentDetailMain } from "@/components/AgentDetailMain/AgentDetailMain";
import { AgentWorkPanel } from "@/components/AgentWorkPanel/AgentWorkPanel";
import {
  isLiveAgentRailVisible,
  orderAgentsForEpicRunner,
} from "@/components/AgentIconRail/AgentIconRail";
import { IssueDetailPanel } from "@/components/IssueDetailPanel/IssueDetailPanel";
import {
  EPIC_RUNNER_WORKFLOW_NAME,
  getWorkflowRun,
  isTerminalWorkflowRunStatus,
  startWorkflowRun,
  type WorkflowRun,
} from "@/api";
import { useAgentStoreInstance } from "@/hooks";
import { useToast } from "@/hooks/ui/useToast";
import type { Issue } from "@/types";
import type { TerminalInputRequest } from "@/components/TerminalView/TerminalView";

export function AgentsPage(): JSX.Element {
  return (
    <ErrorBoundary>
      <Suspense fallback={<LoadingSkeleton.Monitor />}>
        <AgentsPageInner />
      </Suspense>
    </ErrorBoundary>
  );
}
function AgentsPageInner(): JSX.Element {
  const { workspaceId = "", agentName } = useParams<{
    workspaceId: string;
    agentName?: string;
  }>();
  const navigate = useNavigate();
  const agentStore = useAgentStoreInstance();
  const agents = useStore(agentStore, (s) => s.agents);
  const { showToast } = useToast();

  // Auto-select first agent when URL is bare /agents.
  const firstAgentName = useMemo(
    () => orderAgentsForEpicRunner(agents).find(isLiveAgentRailVisible)?.name,
    [agents],
  );
  useEffect(() => {
    if (!agentName && firstAgentName) {
      navigate(
        `/ws/${workspaceId}/agents/${encodeURIComponent(firstAgentName)}`,
        { replace: true },
      );
    }
  }, [agentName, firstAgentName, navigate, workspaceId]);

  // Inline task-detail selection. Cleared when the user switches agents so
  // the right column starts fresh on every agent change.
  const [selectedTask, setSelectedTask] = useState<Issue | null>(null);
  const [pendingTerminalInput, setPendingTerminalInput] = useState<
    TerminalInputRequest | undefined
  >(undefined);
  const [epicRunnerRuns, setEpicRunnerRuns] = useState<
    Record<string, WorkflowRun>
  >({});
  useEffect(() => {
    setSelectedTask(null);
  }, [agentName]);
  useEffect(() => {
    setEpicRunnerRuns({});
  }, [workspaceId]);

  useEffect(() => {
    if (!workspaceId) return;
    const activeRuns = Object.entries(epicRunnerRuns).filter(
      ([, run]) => run.run_id && !isTerminalWorkflowRunStatus(run.status),
    );
    if (activeRuns.length === 0) return;

    let cancelled = false;
    const interval = window.setInterval(() => {
      void (async () => {
        const results = await Promise.allSettled(
          activeRuns.map(async ([epicId, run]) => ({
            epicId,
            run: await getWorkflowRun(workspaceId, run.run_id),
          })),
        );
        if (cancelled) return;

        const updates = results
          .filter(
            (
              result,
            ): result is PromiseFulfilledResult<{
              epicId: string;
              run: WorkflowRun;
            }> => result.status === "fulfilled",
          )
          .map((result) => result.value);
        if (updates.length === 0) return;

        setEpicRunnerRuns((prev) => {
          let next = prev;
          for (const update of updates) {
            const current = prev[update.epicId];
            if (
              current &&
              current.status === update.run.status &&
              current.updated_at === update.run.updated_at &&
              current.last_heartbeat === update.run.last_heartbeat
            ) {
              continue;
            }
            if (next === prev) next = { ...prev };
            next[update.epicId] = update.run;
          }
          return next;
        });
        void agentStore.getState().fetchData();
      })();
    }, 2500);

    return () => {
      cancelled = true;
      window.clearInterval(interval);
    };
  }, [agentStore, epicRunnerRuns, workspaceId]);

  // Queue the built-in epic runner workflow for the selected lead. The
  // workflow owns lead validation, binding, and child-task reconciliation.
  const handleRunEpic = useCallback(
    async (epicId: string) => {
      if (!agentName) return;
      try {
        const run = await startWorkflowRun(
          workspaceId,
          EPIC_RUNNER_WORKFLOW_NAME,
          {
            epicId,
            leadName: agentName,
            requestedBy: "ui",
          },
        );
        setEpicRunnerRuns((prev) => ({ ...prev, [epicId]: run }));
        await agentStore.getState().fetchData();
        showToast(`Epic runner queued for ${agentName}: ${run.run_id}`, {
          type: "success",
        });
      } catch (err) {
        showToast(`Epic runner failed: ${(err as Error).message}`, {
          type: "error",
        });
      }
    },
    [agentName, workspaceId, showToast, agentStore],
  );

  const handleAgentClick = useCallback(
    (name: string) => {
      navigate(`/ws/${workspaceId}/agents/${encodeURIComponent(name)}`);
    },
    [navigate, workspaceId],
  );

  return (
    <div
      style={{
        display: "flex",
        flexDirection: "row",
        flex: 1,
        minHeight: 0,
        height: "100%",
        background: "var(--color-bg, #fdfcf8)",
      }}
    >
      <AgentDetailMain
        agentName={agentName}
        pendingTerminalInput={pendingTerminalInput}
        onTerminalInputConsumed={() => setPendingTerminalInput(undefined)}
      />
      {selectedTask ? (
        <div
          style={{
            width: 420,
            flexShrink: 0,
            borderLeft: "1px solid var(--color-border, #ddd)",
            display: "flex",
            minHeight: 0,
            background: "var(--color-bg, #fdfcf8)",
          }}
        >
          <IssueDetailPanel
            inline
            isOpen={true}
            issue={selectedTask}
            onClose={() => setSelectedTask(null)}
          />
        </div>
      ) : (
        <AgentWorkPanel
          agentName={agentName}
          epicRunnerRuns={epicRunnerRuns}
          onTaskClick={(task) => setSelectedTask(task)}
          onRunEpic={handleRunEpic}
          onAgentClick={handleAgentClick}
        />
      )}
    </div>
  );
}
