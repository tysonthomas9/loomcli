/**
 * AgentIconRail — narrow avatar-only sidebar for the /agents view.
 *
 * Replaces the App-level WorkspaceTree on this view to maximize space for
 * the terminal + work panel. ~60px wide. Each agent is a circular avatar
 * with a status dot; full name + parsed status appear in a hover tooltip.
 * Click navigates to /agents/<name> via the same URL-driven selection the
 * page already uses.
 *
 * Other views (kanban, table, monitor, ...) continue to render the full
 * WorkspaceTree — this rail is /agents-only.
 */

import { useMemo } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useStore } from "zustand";

import { useAgentStoreInstance } from "@/hooks";
import { type LoomAgentStatus, parseLoomStatus } from "@/types";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";

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

export interface AgentIconRailProps {
  onAddClick?: (() => void) | undefined;
}

export function AgentIconRail({ onAddClick }: AgentIconRailProps): JSX.Element {
  const { workspaceId = "", agentName: selectedName } = useParams<{
    workspaceId: string;
    agentName?: string;
  }>();
  const navigate = useNavigate();
  const agentStore = useAgentStoreInstance();
  const agents = useStore(agentStore, (s) => s.agents);
  const orderedAgents = useMemo(
    () => orderAgentsForEpicRunner(agents).filter(isLiveAgentRailVisible),
    [agents],
  );

  const handleClick = useMemo(
    () => (name: string) =>
      navigate(`/ws/${workspaceId}/agents/${encodeURIComponent(name)}`),
    [navigate, workspaceId],
  );

  return (
    <nav
      aria-label="Agents"
      style={{
        width: 60,
        flexShrink: 0,
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        gap: 6,
        padding: "10px 0",
        background: "var(--color-bg-soft, #faf8f3)",
        borderRight: "1px solid var(--color-border, #ddd)",
        overflow: "auto",
        height: "100%",
      }}
    >
      <div
        style={{
          fontSize: 9,
          fontWeight: 700,
          letterSpacing: 0.4,
          textTransform: "uppercase",
          color: "var(--color-text-muted, #666)",
          marginBottom: 4,
        }}
        aria-hidden="true"
      >
        Agents
      </div>
      {orderedAgents.length === 0 ? (
        <div
          style={{
            fontSize: 9,
            color: "var(--color-text-muted, #888)",
            textAlign: "center",
            padding: "12px 4px",
          }}
        >
          No live agents
        </div>
      ) : (
        orderedAgents.map((agent) => (
          <AgentIcon
            key={agent.name}
            agent={agent}
            selected={agent.name === selectedName}
            onClick={() => handleClick(agent.name)}
          />
        ))
      )}
      {onAddClick ? (
        <button
          type="button"
          onClick={onAddClick}
          title="Add agent"
          aria-label="Add agent"
          style={{
            width: 38,
            height: 38,
            padding: 0,
            borderRadius: "50%",
            background: "var(--color-bg, #fff)",
            color: "var(--color-text-primary, #333)",
            border: "1px dashed var(--color-border-strong, #aaa)",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            fontSize: 20,
            lineHeight: 1,
            fontWeight: 600,
            cursor: "pointer",
            flexShrink: 0,
          }}
        >
          +
        </button>
      ) : null}
    </nav>
  );
}

export function isLiveAgentRailVisible(agent: LoomAgentStatus): boolean {
  if (agent.mode !== "ephemeral") return true;
  const state = String(agent.state ?? "")
    .trim()
    .toLowerCase();
  const desiredState = String(agent.desired_state ?? "")
    .trim()
    .toLowerCase();
  return state !== "stopped" && state !== "dead" && desiredState !== "stopped";
}

export function orderAgentsForEpicRunner(
  agents: LoomAgentStatus[],
): LoomAgentStatus[] {
  return [...agents].sort((a, b) => {
    const aRank = agentRailRank(a);
    const bRank = agentRailRank(b);
    if (aRank !== bRank) return aRank - bRank;
    const aParent = a.parent ?? "";
    const bParent = b.parent ?? "";
    if (aParent !== bParent) return aParent.localeCompare(bParent);
    return a.name.localeCompare(b.name);
  });
}

function agentRailRank(agent: LoomAgentStatus): number {
  if (isLeadRole(agent.role)) return 0;
  if (agent.orchestrator_session_id || agent.parent) return 1;
  return 2;
}

function isLeadRole(role: string | undefined): boolean {
  const normalized = (role ?? "").trim().toLowerCase();
  return normalized === "lead" || normalized === "orchestrator";
}

function AgentIcon({
  agent,
  selected,
  onClick,
}: {
  agent: LoomAgentStatus;
  selected: boolean;
  onClick: () => void;
}): JSX.Element {
  const parsed = useMemo(
    () => parseLoomStatus(agent.status ?? ""),
    [agent.status],
  );
  const dotColor = STATUS_DOT_COLOR[parsed.type] ?? STATUS_DOT_COLOR["idle"];
  const initial = (agent.name?.[0] ?? "?").toUpperCase();
  const avatarBg = getAvatarColor(agent.name ?? "");
  const avatarFg = shouldUseWhiteText(avatarBg) ? "#fff" : "#1a1a1a";
  const tooltip =
    parsed.taskId && parsed.taskId.length > 0
      ? `${agent.name} — ${parsed.type} · ${parsed.taskId}`
      : agent.parent
        ? `${agent.name} — ${parsed.type || "idle"} · ${agent.parent}`
        : `${agent.name} — ${parsed.type || "idle"}`;

  return (
    <button
      type="button"
      onClick={onClick}
      title={tooltip}
      aria-label={tooltip}
      aria-current={selected ? "page" : undefined}
      data-agent-name={agent.name}
      data-selected={selected || undefined}
      style={{
        position: "relative",
        width: 38,
        height: 38,
        padding: 0,
        borderRadius: "50%",
        background: avatarBg,
        color: avatarFg,
        border: selected
          ? "2px solid var(--color-accent, #c96442)"
          : "1px solid rgba(0,0,0,0.18)",
        boxShadow: selected ? "0 0 0 2px rgba(201,100,66,0.18)" : "none",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        fontSize: 14,
        fontWeight: 700,
        cursor: "pointer",
        flexShrink: 0,
        transition: "border-color 120ms ease, box-shadow 120ms ease",
      }}
    >
      {initial}
      <span
        aria-hidden="true"
        style={{
          position: "absolute",
          right: -2,
          bottom: -2,
          width: 10,
          height: 10,
          borderRadius: "50%",
          background: dotColor,
          border: "2px solid var(--color-bg-soft, #faf8f3)",
        }}
      />
    </button>
  );
}
