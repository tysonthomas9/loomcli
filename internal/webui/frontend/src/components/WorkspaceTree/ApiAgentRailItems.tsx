import { useMemo } from "react";
import type { ReactNode } from "react";
import { Link, useMatch } from "react-router-dom";

import type { Agent } from "@/api/agentsv1";
import { CompactRailHost } from "@/components/CompactRail";
import { agentColor, agentInitials } from "@/hooks";
import {
  agentDot,
  agentRoleLabel,
  sidebarAgents,
  storedAgentApiOrder,
} from "@/hooks/agents/agentSidebar";

import { useSidebarRoster } from "./AgentRosterOwner";
import styles from "./CollapsedAgentRail.module.css";

/**
 * The Agent API agents the expanded tree's AgentList shows, in its order,
 * from the same live roster: Leads, plus a child while it is at work or its
 * chat is open (SB2); one archived there stays hidden here too. Each opens
 * its chat, and the open one is highlighted.
 * With none to show it renders `empty`; a failed list shows as an error, as
 * the expanded list's alert does, not as no agents.
 */
export function ApiAgentRailItems({
  workspaceId,
  empty,
}: {
  workspaceId: string;
  empty?: ReactNode;
}): JSX.Element {
  const activeId = useMatch("/ws/:ws/chat/:agentId")?.params.agentId;
  const { roster, error, gone } = useSidebarRoster();
  const agents = useMemo(
    () =>
      sidebarAgents(roster, activeId, storedAgentApiOrder(workspaceId), gone),
    [roster, activeId, workspaceId, gone],
  );
  const ws = encodeURIComponent(workspaceId);
  const alert = error ? (
    <CompactRailHost
      role="alert"
      label={`Agent API agents unavailable: ${error}`}
      className={styles.errorHint}
    >
      !
    </CompactRailHost>
  ) : null;
  if (agents.length === 0) return <>{alert ?? empty}</>;
  return (
    <>
      {agents.map((a) => (
        <ApiAgentAvatarLink
          key={a.agent_id}
          agent={a}
          to={`/ws/${ws}/chat/${encodeURIComponent(a.agent_id)}`}
          selected={a.agent_id === activeId}
        />
      ))}
      {alert}
    </>
  );
}

function ApiAgentAvatarLink({
  agent: a,
  to,
  selected,
}: {
  agent: Agent;
  to: string;
  selected: boolean;
}): JSX.Element {
  const dot = agentDot(a);
  // The status is in the name too, as the dot is only seen.
  const status = dot.charAt(0).toUpperCase() + dot.slice(1);
  return (
    <CompactRailHost
      as={Link}
      to={to}
      label={`${a.name} — ${agentRoleLabel(a)} · ${a.harness} · ${status}`}
      aria-current={selected ? "page" : undefined}
      data-agent-id={a.agent_id}
      data-selected={selected || undefined}
      data-dot={dot}
      data-state={a.state}
      className={styles.apiAgent}
    >
      {/* The agent's own colour, as in the expanded row and the Lead chat. */}
      <span
        aria-hidden="true"
        className={styles.apiAvatar}
        style={{ background: agentColor(a.agent_id) }}
      >
        {agentInitials(a.name)}
      </span>
      <span aria-hidden="true" className={styles.dot} />
    </CompactRailHost>
  );
}
