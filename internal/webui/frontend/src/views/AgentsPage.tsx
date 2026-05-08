/**
 * AgentsPage — Direction J layout: agent rail · selected agent's chat ·
 * tasks grouped by epic.
 *
 * F1 scope (this commit): route shell + left rail + URL-driven selection.
 * Middle and right panels render placeholders until F2 (tasks-by-epic) and
 * F3 (TerminalView/transcript) ship.
 *
 * Route: /ws/:workspaceId/agents/:agentName?
 *   - no agent in URL → first agent gets auto-selected
 *   - agent name not found → show "agent not found" state in middle pane
 */

import { Suspense, useMemo } from "react";
import { useNavigate, useParams } from "react-router-dom";

import { ErrorBoundary, LoadingSkeleton } from "@/components";
import { AgentRail } from "@/components/AgentRail/AgentRail";
import { AgentDetailMain } from "@/components/AgentDetailMain/AgentDetailMain";
import { AgentWorkPanel } from "@/components/AgentWorkPanel/AgentWorkPanel";

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

  const selectAgent = useMemo(
    () => (name: string) => {
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
      <AgentRail
        selectedAgent={agentName}
        onSelectAgent={selectAgent}
        autoSelectFirst={!agentName}
      />
      <AgentDetailMain agentName={agentName} />
      <AgentWorkPanel agentName={agentName} />
    </div>
  );
}



