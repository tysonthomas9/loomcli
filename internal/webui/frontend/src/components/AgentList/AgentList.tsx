import { useMemo, useState } from "react";
import { Link, useMatch } from "react-router-dom";
import type { Agent } from "@/api/agentsv1";
import { AgentAvatar } from "@/components/AgentAvatar";
import { ProviderIcon } from "@/components/AgentChat";
import {
  agentColor,
  agentColorIndex,
  agentInitials,
  childrenByParent,
  useAgentRoster,
} from "@/hooks";
import {
  agentDot,
  agentRoleLabel,
  childVisible,
} from "@/hooks/agents/agentSidebar";
import styles from "./AgentList.module.css";

export interface AgentListProps {
  workspaceId: string;
}

/**
 * Agent API agents with children grouped under their lead (design v2 §9.4).
 * Every row opens the same chat and looks like the old agent rows (avatar,
 * name, role line, status dot) with the harness as a logo. At the top level
 * Leads come first and independent workers sit under a collapsible
 * Background group, as in the old Lead UI rail. A child shows only while it
 * is at work or its chat is open (SB2).
 */
export function AgentList({ workspaceId }: AgentListProps): JSX.Element {
  // The agent whose chat is open.
  const activeId = useMatch("/ws/:ws/chat/:agentId")?.params.agentId;
  const { roster, error } = useAgentRoster(workspaceId, activeId);
  const kids = useMemo(() => childrenByParent(roster), [roster]);
  const ws = encodeURIComponent(workspaceId);
  const [bgOpen, setBgOpen] = useState(true);

  const top = kids.get("") ?? [];
  const isWorker = (a: Agent) => a.role_kind === "worker";
  const isLead = (a: Agent) => a.preset === "lead";
  const main = [
    ...top.filter(isLead),
    ...top.filter((a) => !isLead(a) && !isWorker(a)),
  ];
  const background = top.filter(isWorker);

  const rows = (list: Agent[] | undefined): JSX.Element | null => {
    if (!list?.length) return null;
    return (
      <ul className={styles.list}>
        {list.map((a) => {
          const dot = agentDot(a);
          const role = agentRoleLabel(a);
          return (
            <li key={a.agent_id}>
              <Link
                className={styles.row}
                to={`/ws/${ws}/chat/${encodeURIComponent(a.agent_id)}`}
                aria-current={a.agent_id === activeId ? "page" : undefined}
                aria-label={`${a.name} ${role} ${a.harness}`}
                data-state={a.state}
              >
                {/* Name first in the DOM, so the row reads as its name; the
                    avatar is drawn first by CSS order. */}
                <span className={styles.info}>
                  <span className={styles.name} data-testid="agent-list-name">
                    {a.name}
                  </span>
                  <span className={styles.role}>{role}</span>
                </span>
                <span
                  className={styles.avatar}
                  data-dot={dot}
                  data-agent-color={agentColorIndex(a.agent_id)}
                  aria-hidden="true"
                >
                  {/* The agent's own colour, as in the Lead chat and tray. */}
                  <AgentAvatar
                    name={a.name}
                    color={agentColor(a.agent_id)}
                    initials={agentInitials(a.name)}
                    compact
                  />
                  <span className={styles.dot} />
                </span>
                <span
                  className={styles.harness}
                  role="img"
                  aria-label={a.harness}
                  title={a.harness}
                >
                  <ProviderIcon
                    providerId={a.harness}
                    providerName={a.harness}
                    size="sm"
                  />
                </span>
              </Link>
              {rows(
                kids
                  .get(a.agent_id)
                  ?.filter((k) => childVisible(k, kids, activeId)),
              )}
            </li>
          );
        })}
      </ul>
    );
  };

  return (
    <nav className={styles.root} aria-label="Agents">
      {error && <p role="alert">{error}</p>}
      {rows(main)}
      {background.length > 0 && (
        <div data-testid="agent-list-background">
          <button
            type="button"
            className={styles.group}
            aria-expanded={bgOpen}
            onClick={() => setBgOpen(!bgOpen)}
          >
            Background
          </button>
          {bgOpen && rows(background)}
        </div>
      )}
    </nav>
  );
}
