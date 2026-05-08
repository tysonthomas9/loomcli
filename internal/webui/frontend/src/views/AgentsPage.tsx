/**
 * AgentsPage — agent terminal + work-context view.
 *
 * Layout (driven by App.tsx):
 *   [WorkspaceTree sidebar (App-level)]   ← agent picker lives here on this view too
 *   [AgentDetailMain — embedded TerminalView attached to the selected agent]
 *   [AgentWorkPanel — right panel, scope depends on whether the selected
 *    agent has an active epic vs is idle]
 *
 * The selected agent is URL-driven (:agentName). Clicks on the WorkspaceTree's
 * AGENTS section are intercepted by App's handleAgentClick when activeView ===
 * "agents" so they navigate to /agents/<name> instead of opening the slide-out
 * panel. If the URL has no :agentName, the first agent is auto-selected.
 */

import { Suspense, useEffect, useMemo } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useStore } from "zustand";

import { ErrorBoundary, LoadingSkeleton } from "@/components";
import { AgentDetailMain } from "@/components/AgentDetailMain/AgentDetailMain";
import { AgentWorkPanel } from "@/components/AgentWorkPanel/AgentWorkPanel";
import { useAgentStoreInstance } from "@/hooks";

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

  // Auto-select first agent when URL is bare /agents.
  // The page never shows a blank middle pane.
  const firstAgentName = useMemo(
    () => (agents.length > 0 ? agents[0]?.name : undefined),
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
      <AgentDetailMain agentName={agentName} />
      <AgentWorkPanel agentName={agentName} />
    </div>
  );
}
