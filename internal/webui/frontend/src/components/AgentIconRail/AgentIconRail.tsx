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
import {
  agentCompactAvatarLabel,
  agentDisplayTitle,
} from "@/utils/agentDisplay";
import { orderAgentsForEpicRunner } from "@/utils/agentRole";
import { getCompactAvatarInitials } from "@/utils/compactAvatarInitials";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";

import { CompactRailHost } from "@/components/CompactRail";

import styles from "./AgentIconRail.module.css";

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
    <nav aria-label="Agents" className={styles.rail}>
      <div className={styles.railLabel} aria-hidden="true">
        Agents
      </div>
      {orderedAgents.length === 0 ? (
        <div className={styles.railEmpty}>No live agents</div>
      ) : (
        orderedAgents.map((agent) => (
          <AgentAvatarButton
            key={agent.name}
            agent={agent}
            selected={agent.name === selectedName}
            onClick={() => handleClick(agent.name)}
          />
        ))
      )}
      {onAddClick ? (
        <CompactRailHost
          as="button"
          type="button"
          label="Add agent"
          className={styles.addButton}
          onClick={onAddClick}
        >
          +
        </CompactRailHost>
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

export { orderAgentsForEpicRunner } from "@/utils/agentRole";

export function agentAvatarTooltip(agent: LoomAgentStatus): string {
  const title = agentDisplayTitle(agent);
  const parsed = parseLoomStatus(agent.status ?? "");
  if (parsed.taskId && parsed.taskId.length > 0) {
    return `${title} — ${parsed.type} · ${parsed.taskId}`;
  }
  if (agent.parent) {
    return `${title} — ${parsed.type || "idle"} · ${agent.parent}`;
  }
  return `${title} — ${parsed.type || "idle"}`;
}

function avatarLabelFontSize(label: string, size: number): number {
  if (label.length >= 4) return size <= 32 ? 8 : 9;
  if (label.length > 1) return size <= 32 ? 10 : 11;
  return size <= 32 ? 12 : 14;
}

export function AgentAvatarButton({
  agent,
  selected,
  onClick,
  size = 38,
}: {
  agent: LoomAgentStatus;
  selected: boolean;
  onClick: () => void;
  size?: number;
}): JSX.Element {
  const parsed = useMemo(
    () => parseLoomStatus(agent.status ?? ""),
    [agent.status],
  );
  const dotColor = STATUS_DOT_COLOR[parsed.type] ?? STATUS_DOT_COLOR["idle"];
  const prLabel = agentCompactAvatarLabel(agent);
  const initial =
    prLabel || getCompactAvatarInitials(agent.name ?? "");
  const avatarBg = getAvatarColor(agent.name ?? "");
  const avatarFg = shouldUseWhiteText(avatarBg) ? "#fff" : "#1a1a1a";
  const tooltip = agentAvatarTooltip(agent);

  return (
    <CompactRailHost
      as="button"
      type="button"
      label={tooltip}
      onClick={onClick}
      aria-current={selected ? "page" : undefined}
      data-agent-name={agent.name}
      data-selected={selected || undefined}
      className={styles.avatarButton}
      style={{
        width: size,
        height: size,
        fontSize: avatarLabelFontSize(initial, size),
        background: avatarBg,
        color: avatarFg,
        border: selected
          ? "2px solid var(--color-accent, #c96442)"
          : "1px solid rgba(0,0,0,0.18)",
        boxShadow: selected ? "0 0 0 2px rgba(201,100,66,0.18)" : "none",
      }}
    >
      {initial}
      <span
        aria-hidden="true"
        className={styles.statusDot}
        style={{ background: dotColor }}
      />
    </CompactRailHost>
  );
}
