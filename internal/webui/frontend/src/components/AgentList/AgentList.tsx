import type React from "react";
import { Fragment, useCallback, useEffect, useMemo, useState } from "react";
import { Link, useMatch } from "react-router-dom";
import type { Agent } from "@/api/agentsv1";
import { AgentAvatar } from "@/components/AgentAvatar";
import { ProviderIcon } from "@/components/AgentChat";
import {
  SortableAgentList,
  type SortableAgentItem,
} from "@/components/WorkspaceTree/SortableAgentList";
import { SortableAgentRow } from "@/components/WorkspaceTree/SortableAgentRow";
import { AgentContextMenu } from "@/components/WorkspaceTree/menus/AgentContextMenu";
import {
  agentColor,
  agentColorIndex,
  agentInitials,
  childrenByParent,
  useAgentRoster,
  useArchiveAgent,
} from "@/hooks";
import {
  agentDot,
  agentRoleLabel,
  childVisible,
} from "@/hooks/agents/agentSidebar";
import { useToast } from "@/hooks/ui";
import {
  mergeAgentSectionOrder,
  parseStoredAgentSectionOrder,
} from "@/utils/agentSectionOrder";
import { wsGet, wsSet } from "@/utils/scopedStorage";
import styles from "./AgentList.module.css";

/** The top-level rows' order by agent id, saved as the fleet rows' is. */
const SK_AGENT_API_ORDER = "agent-api-order";

const storedOrder = (workspaceId: string): string[] =>
  parseStoredAgentSectionOrder(wsGet(workspaceId, SK_AGENT_API_ORDER)) ?? [];

export interface AgentListProps {
  workspaceId: string;
}

/**
 * Agent API agents with children grouped under their lead (design v2 §9.4).
 * Every row opens the same chat and looks like the old agent rows (avatar,
 * name, role line, status dot) with the harness as a logo. At the top level
 * Leads come first and independent workers sit under a collapsible
 * Background group, as in the old Lead UI rail. A child shows only while it
 * is at work or its chat is open (SB2). Top-level rows drag to reorder
 * (children move with their row), and every row archives from a hover
 * action or its right-click menu, as the fleet rows do (SB4).
 */
export function AgentList({ workspaceId }: AgentListProps): JSX.Element {
  // The agent whose chat is open.
  const activeId = useMatch("/ws/:ws/chat/:agentId")?.params.agentId;
  const { roster, error } = useAgentRoster(workspaceId, activeId);
  const kids = useMemo(() => childrenByParent(roster), [roster]);
  const ws = encodeURIComponent(workspaceId);
  const [bgOpen, setBgOpen] = useState(true);
  const { showToast } = useToast();
  const archiveAgent = useArchiveAgent(workspaceId);
  const [order, setOrder] = useState(() => storedOrder(workspaceId));
  useEffect(() => setOrder(storedOrder(workspaceId)), [workspaceId]);
  // Archived here: a busy agent stays stopping until its turn ends.
  const [archived, setArchived] = useState<ReadonlySet<string>>(new Set());
  const [menu, setMenu] = useState<{ id: string; x: number; y: number }>();

  const shown = (a: Agent) =>
    a.state !== "archived" && !archived.has(a.agent_id);
  const top = (kids.get("") ?? []).filter(shown);
  const isWorker = (a: Agent) => a.role_kind === "worker";
  const isLead = (a: Agent) => a.preset === "lead";
  const fullOrder = mergeAgentSectionOrder(
    [
      ...top.filter(isLead),
      ...top.filter((a) => !isLead(a) && !isWorker(a)),
      ...top.filter(isWorker),
    ].map((a) => a.agent_id),
    order,
  );
  const ordered = fullOrder.map((id) => roster.get(id)!);
  const main = ordered.filter((a) => !isWorker(a));
  const background = ordered.filter(isWorker);

  const reorder = useCallback(
    (next: string[]) => {
      setOrder(next);
      wsSet(workspaceId, SK_AGENT_API_ORDER, JSON.stringify(next));
    },
    [workspaceId],
  );

  // The chat header's Archive call.
  const archive = useCallback(
    async (id: string) => {
      setMenu(undefined);
      try {
        await archiveAgent(id);
        setArchived((s) => new Set(s).add(id));
      } catch (err) {
        const why = err instanceof Error ? `: ${err.message}` : "";
        showToast(`Failed to archive agent${why}`, { type: "error" });
      }
    },
    [archiveAgent, showToast],
  );
  const openMenu = useCallback(
    (e: React.MouseEvent, id: string) =>
      setMenu({ id, x: e.clientX, y: e.clientY }),
    [],
  );

  const link = (a: Agent): JSX.Element => {
    const dot = agentDot(a);
    const role = agentRoleLabel(a);
    return (
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
    );
  };

  // A row's children, pinned under it: they archive but do not drag.
  const children = (a: Agent): JSX.Element | null => {
    const list = kids
      .get(a.agent_id)
      ?.filter((k) => shown(k) && childVisible(k, kids, activeId));
    if (!list?.length) return null;
    return (
      <div
        role="group"
        aria-label={`${a.name} children`}
        className={styles.children}
      >
        {list.map((k) => (
          <Fragment key={k.agent_id}>
            <SortableAgentRow
              id={k.agent_id}
              label={k.name}
              pinned
              onArchive={archive}
              onContextMenu={openMenu}
            >
              {link(k)}
            </SortableAgentRow>
            {children(k)}
          </Fragment>
        ))}
      </div>
    );
  };

  const rows = (list: Agent[]): JSX.Element => (
    <SortableAgentList
      items={list.map(
        (a): SortableAgentItem => ({
          id: a.agent_id,
          label: a.name,
          content: link(a),
          after: children(a),
        }),
      )}
      fullOrder={fullOrder}
      onReorder={reorder}
      listClassName={styles.list}
      onArchive={archive}
      onAgentContextMenu={openMenu}
    />
  );

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
      <AgentContextMenu
        isOpen={menu != null}
        position={{ x: menu?.x ?? 0, y: menu?.y ?? 0 }}
        onArchive={() => {
          if (menu) void archive(menu.id);
        }}
        onClose={() => setMenu(undefined)}
      />
    </nav>
  );
}
