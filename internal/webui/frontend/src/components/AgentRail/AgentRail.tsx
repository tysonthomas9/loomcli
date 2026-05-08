/**
 * AgentRail — Direction J left panel.
 *
 * Lists all agents in the workspace as a vertical rail of selectable rows.
 * Each row shows: avatar (first-letter, color-deterministic), name, parsed
 * current task, and a status dot. Selection is URL-driven (the parent view
 * reads :agentName from the route).
 *
 * F1 scope: render + select. No "+ add" affordance, no repos section, no
 * orchestrator grouping (those land later as the orchestrator/worker model
 * is wired through the UI).
 */

import { useEffect, useMemo } from "react";
import { useStore } from "zustand";

import { useAgentStoreInstance } from "@/hooks";
import { type LoomAgentStatus, parseLoomStatus } from "@/types";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";

export interface AgentRailProps {
  /** Currently-selected agent name (from URL). Undefined = none. */
  selectedAgent: string | undefined;
  /** Called when the user clicks an agent row. */
  onSelectAgent: (name: string) => void;
  /** When true and no agent is selected, auto-select the first agent
   *  on first render so the page never shows an empty middle pane. */
  autoSelectFirst?: boolean;
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

export function AgentRail({
  selectedAgent,
  onSelectAgent,
  autoSelectFirst = false,
}: AgentRailProps): JSX.Element {
  const agentStore = useAgentStoreInstance();
  const agents = useStore(agentStore, (s) => s.agents);

  // Auto-select the first agent on initial load if no agent is in the URL.
  // Parent view passes autoSelectFirst=true when :agentName is missing.
  useEffect(() => {
    if (autoSelectFirst && !selectedAgent && agents.length > 0) {
      const first = agents[0];
      if (first?.name) onSelectAgent(first.name);
    }
  }, [autoSelectFirst, selectedAgent, agents, onSelectAgent]);

  const summary = useMemo(() => summarizeAgents(agents), [agents]);

  return (
    <div
      style={{
        width: 220,
        flexShrink: 0,
        borderRight: "1px solid var(--color-border, #ddd)",
        background: "var(--color-bg-soft, #faf8f3)",
        display: "flex",
        flexDirection: "column",
        minHeight: 0,
      }}
    >
      <div
        style={{
          padding: "10px 12px 6px",
          fontSize: 11,
          fontWeight: 700,
          letterSpacing: 0.4,
          textTransform: "uppercase",
          color: "var(--color-text-muted, #666)",
        }}
      >
        Agents · {agents.length}
      </div>

      <div style={{ flex: 1, overflow: "auto", padding: 4 }}>
        {agents.length === 0 ? (
          <div
            style={{
              padding: 16,
              fontSize: 12,
              color: "var(--color-text-muted, #888)",
              textAlign: "center",
            }}
          >
            No agents in this workspace.
            <div style={{ marginTop: 8, fontSize: 11 }}>
              Run <code>loom agentdef add</code> from a Terminal to register one.
            </div>
          </div>
        ) : (
          agents.map((agent) => (
            <AgentRow
              key={agent.name}
              agent={agent}
              selected={agent.name === selectedAgent}
              onClick={() => onSelectAgent(agent.name)}
            />
          ))
        )}
      </div>

      {summary && (
        <div
          style={{
            padding: "8px 12px",
            borderTop: "1px solid var(--color-border, #ddd)",
            fontSize: 11,
            color: "var(--color-text-muted, #666)",
          }}
        >
          {summary}
        </div>
      )}
    </div>
  );
}

interface AgentRowProps {
  agent: LoomAgentStatus;
  selected: boolean;
  onClick: () => void;
}

function AgentRow({ agent, selected, onClick }: AgentRowProps): JSX.Element {
  const parsed = useMemo(() => parseLoomStatus(agent.status ?? ""), [agent.status]);
  const dotColor =
    STATUS_DOT_COLOR[parsed.type] ?? "var(--color-status-idle, #888)";
  const initial = (agent.name?.[0] ?? "?").toUpperCase();
  const avatarBg = getAvatarColor(agent.name ?? "");
  const avatarFg = shouldUseWhiteText(avatarBg) ? "#fff" : "#1a1a1a";

  const subtitle =
    parsed.taskId
      ? `${parsed.type} · ${parsed.taskId}`
      : parsed.type;

  return (
    <button
      type="button"
      onClick={onClick}
      data-agent-name={agent.name}
      data-selected={selected || undefined}
      style={{
        display: "flex",
        alignItems: "center",
        gap: 8,
        width: "100%",
        padding: "8px 10px",
        marginBottom: 2,
        border: "none",
        background: selected
          ? "var(--color-selected-bg, #1a1a1a)"
          : "transparent",
        color: selected ? "var(--color-selected-fg, #fff)" : "inherit",
        borderRadius: 4,
        cursor: "pointer",
        textAlign: "left",
        fontSize: 12,
        transition: "background 120ms ease",
      }}
      onMouseEnter={(e) => {
        if (!selected)
          e.currentTarget.style.background = "var(--color-hover-bg, rgba(0,0,0,0.04))";
      }}
      onMouseLeave={(e) => {
        if (!selected) e.currentTarget.style.background = "transparent";
      }}
    >
      <span
        aria-hidden="true"
        style={{
          width: 28,
          height: 28,
          borderRadius: "50%",
          background: avatarBg,
          color: avatarFg,
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          fontWeight: 700,
          fontSize: 12,
          flexShrink: 0,
          border: "1px solid rgba(0,0,0,0.18)",
        }}
      >
        {initial}
      </span>
      <span
        style={{
          display: "flex",
          flexDirection: "column",
          minWidth: 0,
          flex: 1,
        }}
      >
        <span
          style={{
            fontSize: 13,
            fontWeight: 600,
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
        >
          {agent.name}
        </span>
        <span
          style={{
            fontSize: 10,
            opacity: 0.75,
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
        >
          {subtitle}
        </span>
      </span>
      <span
        aria-hidden="true"
        style={{
          width: 8,
          height: 8,
          borderRadius: "50%",
          background: dotColor,
          flexShrink: 0,
        }}
      />
    </button>
  );
}

function summarizeAgents(agents: LoomAgentStatus[]): string {
  if (agents.length === 0) return "";
  let working = 0;
  let errored = 0;
  for (const a of agents) {
    const parsed = parseLoomStatus(a.status ?? "");
    if (parsed.type === "working" || parsed.type === "planning") working++;
    if (parsed.type === "error") errored++;
  }
  const parts: string[] = [];
  if (working > 0) parts.push(`${working} working`);
  if (errored > 0) parts.push(`${errored} error`);
  return parts.length === 0 ? `${agents.length} idle` : parts.join(" · ");
}
