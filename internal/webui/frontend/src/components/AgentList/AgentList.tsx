import { useMemo, useState } from "react";
import { Link, useMatch } from "react-router-dom";
import type { Agent } from "@/api/agentsv1";
import { childrenByParent, useAgentRoster } from "@/hooks";
import styles from "./AgentList.module.css";

export interface AgentListProps {
  workspaceId: string;
}

/**
 * Agent API agents with children grouped under their lead (design v2 §9.4).
 * Every row opens the same chat; the harness is only a label. At the top
 * level Leads come first and independent workers sit under a collapsible
 * Background group, as in the old Lead UI rail.
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
        {list.map((a) => (
          <li key={a.agent_id}>
            <Link
              className={styles.row}
              to={`/ws/${ws}/chat/${encodeURIComponent(a.agent_id)}`}
              aria-current={a.agent_id === activeId ? "page" : undefined}
            >
              <span className={styles.name}>{a.name}</span>
              <span className={styles.label}>{a.harness}</span>
              <span className={styles.label}>{a.state}</span>
            </Link>
            {rows(kids.get(a.agent_id))}
          </li>
        ))}
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
