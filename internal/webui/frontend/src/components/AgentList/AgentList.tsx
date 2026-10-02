import { useMemo } from "react";
import { Link } from "react-router-dom";
import { childrenByParent, useAgentRoster } from "@/hooks";
import styles from "./AgentList.module.css";

export interface AgentListProps {
  workspaceId: string;
  /** The agent whose chat is open. */
  activeId?: string;
}

/**
 * Agent API agents with children grouped under their lead (design v2 §9.4).
 * Every row opens the same chat; the harness is only a label.
 */
export function AgentList({
  workspaceId,
  activeId,
}: AgentListProps): JSX.Element {
  const { roster, error } = useAgentRoster(workspaceId);
  const kids = useMemo(() => childrenByParent(roster), [roster]);
  const ws = encodeURIComponent(workspaceId);

  const rows = (parent: string): JSX.Element | null => {
    const list = kids.get(parent);
    if (!list) return null;
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
            {rows(a.agent_id)}
          </li>
        ))}
      </ul>
    );
  };

  return (
    <nav className={styles.root} aria-label="Agents">
      {error && <p role="alert">{error}</p>}
      {rows("")}
    </nav>
  );
}
