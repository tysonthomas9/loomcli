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
import { useAgentStoreInstance } from "@/hooks";
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
  const selectedAgent = useMemo(
    () => (agentName ? agents.find((a) => a.name === agentName) : undefined),
    [agentName, agents],
  );

  // Auto-select first agent when URL is bare /agents.
  const firstAgentName = useMemo(
    () =>
      orderAgentsForEpicRunner(agents).find(isLiveAgentRailVisible)?.name,
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

  const handleRunEpic = useCallback(
    (epicId: string) => {
      if (!agentName) return;
      const orchestratorSessionId =
        selectedAgent?.orchestrator_session_id ||
        `agent-${sanitizeTerminalSessionPart(agentName)}`;
      const envPrefix = `LOOM_ORCHESTRATOR_SESSION_ID=${shellQuote(orchestratorSessionId)}`;
      const command = `${envPrefix} loom epic run --parent ${shellQuote(epicId)} --lead ${shellQuote(agentName)}\n`;
      setPendingTerminalInput({
        id: `${Date.now()}-${agentName}-${epicId}`,
        text: command,
        targetAgentName: agentName,
      });
    },
    [agentName, selectedAgent?.orchestrator_session_id],
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

function sanitizeTerminalSessionPart(value: string): string {
  return value.replace(/\./g, "-").replace(/[^a-zA-Z0-9_-]/g, "");
}

function shellQuote(value: string): string {
  if (/^[A-Za-z0-9_./:-]+$/.test(value)) return value;
  return `'${value.replace(/'/g, "'\\''")}'`;
}
