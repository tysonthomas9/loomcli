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
      <MiddlePlaceholder agentName={agentName} />
      <RightPlaceholder agentName={agentName} />
    </div>
  );
}

function MiddlePlaceholder({
  agentName,
}: {
  agentName: string | undefined;
}): JSX.Element {
  return (
    <div
      style={{
        flex: 1,
        minWidth: 0,
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        justifyContent: "center",
        color: "var(--color-text-muted, #888)",
        padding: 32,
        borderRight: "1px solid var(--color-border, #ddd)",
      }}
    >
      {!agentName ? (
        <>
          <div style={{ fontSize: 14, fontWeight: 600 }}>Select an agent</div>
          <div style={{ fontSize: 12, marginTop: 6 }}>
            Pick an agent from the rail to view their work.
          </div>
        </>
      ) : (
        <>
          <div style={{ fontSize: 14, fontWeight: 600 }}>
            Chat with {agentName} — coming in F3
          </div>
          <div style={{ fontSize: 12, marginTop: 6, textAlign: "center" }}>
            For lead agents this will embed the existing TerminalView attached
            to the lead's tmux session.
            <br />
            For task agents it will render the agent's most recent transcript
            in read-only mode.
          </div>
        </>
      )}
    </div>
  );
}

function RightPlaceholder({
  agentName,
}: {
  agentName: string | undefined;
}): JSX.Element {
  return (
    <div
      style={{
        width: 360,
        flexShrink: 0,
        padding: 16,
        color: "var(--color-text-muted, #888)",
        background: "var(--color-bg-soft, #faf8f3)",
      }}
    >
      <div
        style={{ fontSize: 11, fontWeight: 700, letterSpacing: 0.4, textTransform: "uppercase" }}
      >
        {agentName ? `${agentName}'s work` : "No selection"}
      </div>
      <div style={{ fontSize: 12, marginTop: 12 }}>
        Tasks grouped by epic — coming in F2.
      </div>
    </div>
  );
}

