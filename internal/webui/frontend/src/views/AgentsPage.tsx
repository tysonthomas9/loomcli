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
import { runEpic } from "@/api";
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
  useEffect(() => {
    setSelectedTask(null);
  }, [agentName]);

  // handleRunEpic calls the shared backend command path
  // (POST /api/workspaces/:ws/epics/:id/run). The backend validates that
  // the selected agent is a lead, enforces "one epic per lead" + "one lead
  // per epic", and binds agent.parent atomically. The lead's terminal
  // picks up the new binding on next render. This replaces the prior
  // "paste a shell command into the terminal" path, which silently no-op'd
  // when the terminal was running an AI CLI like codex instead of a
  // shell; see lead-agent-epic-runner-spec.md:37.
  const handleRunEpic = useCallback(
    async (epicId: string) => {
      if (!agentName) return;
      try {
        const result = await runEpic(workspaceId, epicId, agentName);
        await agentStore.getState().fetchData();
        const state = result.state === "resumed" ? "resumed" : "assigned";
        showToast(`Epic ${epicId} ${state} for ${agentName}`, {
          type: "success",
        });
      } catch (err) {
        showToast(`run-epic failed: ${(err as Error).message}`, {
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
          onTaskClick={(task) => setSelectedTask(task)}
          onRunEpic={handleRunEpic}
          onAgentClick={handleAgentClick}
        />
      )}
    </div>
  );
}
