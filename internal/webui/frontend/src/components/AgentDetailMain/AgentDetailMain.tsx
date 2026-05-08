/**
 * AgentDetailMain — Direction J middle panel (live terminal).
 *
 * Renders a slim agent header (avatar, name, status, branch, role) plus an
 * embedded TerminalView that auto-focuses the selected agent's tmux session
 * via pendingAgentName. When the user switches agents in the rail, the
 * pendingAgentName change tells TerminalView to switch tabs to that agent.
 *
 * The App-level TerminalView is conditionally skipped when activeView ===
 * "agents" (see App.tsx) so only one TerminalView instance is mounted at a
 * time. Switching between /terminal and /agents causes one reconnect, but
 * within the agents view itself the terminal stays mounted across rail
 * selection changes (only pendingAgentName updates).
 */

import { lazy, Suspense, useCallback, useEffect, useMemo, useState } from "react";
import { useStore } from "zustand";

import { LoadingSkeleton } from "@/components";
import { useAgentStoreInstance } from "@/hooks";
import { type LoomAgentStatus, parseLoomStatus } from "@/types";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";

const TerminalView = lazy(() =>
  import("@/components/TerminalView/TerminalView").then((m) => ({
    default: m.TerminalView,
  })),
);

interface AgentDetailMainProps {
  agentName: string | undefined;
}

const STATUS_DOT_COLOR: Record<string, string> = {
  ready: "var(--color-status-ready, #aab)",
  working: "var(--color-status-working, #d99700)",
  planning: "var(--color-status-planning, #d99700)",
  review: "var(--color-status-review, #4477aa)",
  done: "var(--color-status-done, #3aa76d)",
  idle: "var(--color-status-idle, #888)",
  error: "var(--color-status-error, #d14545)",
  dirty: "var(--color-status-warn, #c96442)",
  changes: "var(--color-status-warn, #c96442)",
};

export function AgentDetailMain({ agentName }: AgentDetailMainProps): JSX.Element {
  const agentStore = useAgentStoreInstance();
  const agents = useStore(agentStore, (s) => s.agents);

  const agent = useMemo<LoomAgentStatus | undefined>(
    () => agents.find((a) => a.name === agentName),
    [agents, agentName],
  );

  // Drive TerminalView's pendingAgentName from rail selection. Cleared via the
  // onAgentNameConsumed callback so TerminalView only switches tabs once per
  // selection change.
  const [pendingAgentName, setPendingAgentName] = useState<string | undefined>(
    agentName,
  );
  useEffect(() => {
    if (agentName) setPendingAgentName(agentName);
  }, [agentName]);
  const handleAgentNameConsumed = useCallback(
    () => setPendingAgentName(undefined),
    [],
  );

  if (!agentName) {
    return (
      <EmptyState
        message="Select an agent"
        detail="Pick an agent from the rail to attach to their terminal session."
      />
    );
  }

  return (
    <div
      style={{
        flex: 1,
        minWidth: 0,
        display: "flex",
        flexDirection: "column",
        borderRight: "1px solid var(--color-border, #ddd)",
        background: "var(--color-bg, #fdfcf8)",
        minHeight: 0,
      }}
    >
      <Header agent={agent} agentName={agentName} />
      <div
        style={{
          flex: 1,
          minHeight: 0,
          minWidth: 0,
          display: "flex",
          flexDirection: "column",
          position: "relative",
        }}
      >
        <Suspense fallback={<LoadingSkeleton.Terminal />}>
          <TerminalView
            isActive={true}
            pendingAgentName={pendingAgentName}
            onAgentNameConsumed={handleAgentNameConsumed}
          />
        </Suspense>
      </div>
    </div>
  );
}

function Header({
  agent,
  agentName,
}: {
  agent: LoomAgentStatus | undefined;
  agentName: string;
}): JSX.Element {
  const parsed = useMemo(
    () => parseLoomStatus(agent?.status ?? ""),
    [agent?.status],
  );
  const dotColor = STATUS_DOT_COLOR[parsed.type] ?? "var(--color-status-idle, #888)";
  const initial = (agentName[0] ?? "?").toUpperCase();
  const avatarBg = getAvatarColor(agentName);
  const avatarFg = shouldUseWhiteText(avatarBg) ? "#fff" : "#1a1a1a";

  return (
    <div
      style={{
        padding: "10px 16px",
        borderBottom: "1px solid var(--color-border, #ddd)",
        display: "flex",
        alignItems: "center",
        gap: 10,
        flexShrink: 0,
      }}
    >
      <span
        aria-hidden="true"
        style={{
          width: 32,
          height: 32,
          borderRadius: "50%",
          background: avatarBg,
          color: avatarFg,
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          fontWeight: 700,
          fontSize: 14,
          flexShrink: 0,
          border: "1px solid rgba(0,0,0,0.18)",
        }}
      >
        {initial}
      </span>
      <div style={{ display: "flex", flexDirection: "column", flex: 1, minWidth: 0 }}>
        <div style={{ fontSize: 14, fontWeight: 700 }}>{agentName}</div>
        <div
          style={{
            fontSize: 11,
            color: "var(--color-text-muted, #666)",
            display: "flex",
            alignItems: "center",
            gap: 6,
          }}
        >
          <span
            aria-hidden="true"
            style={{
              width: 7,
              height: 7,
              borderRadius: "50%",
              background: dotColor,
              display: "inline-block",
            }}
          />
          <span>{parsed.type || "unknown"}</span>
          {agent?.branch ? (
            <>
              <span>·</span>
              <code style={{ fontFamily: "var(--font-mono, ui-monospace, monospace)" }}>
                {agent.branch}
              </code>
            </>
          ) : null}
          {agent?.role ? (
            <>
              <span>·</span>
              <span>{agent.role}</span>
            </>
          ) : null}
          {parsed.taskId ? (
            <>
              <span>·</span>
              <code style={{ fontFamily: "var(--font-mono, ui-monospace, monospace)" }}>
                {parsed.taskId}
              </code>
            </>
          ) : null}
        </div>
      </div>
    </div>
  );
}

function EmptyState({
  message,
  detail,
}: {
  message: string;
  detail: string;
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
        background: "var(--color-bg, #fdfcf8)",
      }}
    >
      <div style={{ fontSize: 14, fontWeight: 600 }}>{message}</div>
      <div style={{ fontSize: 12, marginTop: 6, textAlign: "center", maxWidth: 360 }}>
        {detail}
      </div>
    </div>
  );
}
