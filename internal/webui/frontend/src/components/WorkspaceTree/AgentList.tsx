import type React from "react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useMatch, useNavigate } from "react-router-dom";
import type { Agent } from "@/api/agentsv1";
import { AgentAvatar } from "@/components/AgentAvatar";
import { ProviderIcon } from "@/components/AgentChat";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { SortableAgentList, type SortableAgentItem } from "./SortableAgentList";
import { SortableAgentRow } from "./SortableAgentRow";
import { useSidebarRoster } from "./AgentRosterOwner";
import { AgentContextMenu } from "./menus/AgentContextMenu";
import {
  agentColor,
  agentColorIndex,
  agentInitials,
  useArchiveAgent,
  useDeleteAgent,
  type DeleteRefusal,
} from "@/hooks";
import {
  SK_AGENT_API_ORDER,
  agentDot,
  agentRoleLabel,
  sidebarRows,
  storedAgentApiOrder as storedOrder,
  visibleChildren,
} from "@/hooks/agents/agentSidebar";
import { useToast } from "@/hooks/ui";
import { wsSet } from "@/utils/scopedStorage";
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
 * is at work or its chat is open (SB2). Top-level rows drag to reorder
 * (children move with their row), and every row archives from a hover
 * action or its right-click menu, as the fleet rows do; the menu also
 * deletes, after a confirm, with DA1's Delete anyway on an unsaved-work
 * refusal (SB4).
 */
export function AgentList({ workspaceId }: AgentListProps): JSX.Element {
  // The agent whose chat is open.
  const activeId = useMatch("/ws/:ws/chat/:agentId")?.params.agentId;
  // Archived or deleted here stay hidden until the stream reports it,
  // shared with the collapsed rail.
  const { roster, error, gone, hide } = useSidebarRoster();
  const ws = encodeURIComponent(workspaceId);
  const [bgOpen, setBgOpen] = useState(true);
  const { showToast } = useToast();
  const archiveAgent = useArchiveAgent(workspaceId);
  const deleteAgent = useDeleteAgent(workspaceId);
  const navigate = useNavigate();
  const [order, setOrder] = useState(() => storedOrder(workspaceId));
  const [menu, setMenu] = useState<{ id: string; x: number; y: number }>();
  // The agent whose Delete waits on its confirm.
  const [confirming, setConfirming] = useState<Agent>();
  // An unsaved-work refusal, which offers Delete anyway (DA1).
  const [refused, setRefused] = useState<DeleteRefusal & { agent: Agent }>();

  // A new workspace starts from its own saved order, with nothing open.
  useEffect(() => {
    setOrder(storedOrder(workspaceId));
    setMenu(undefined);
    setConfirming(undefined);
    setRefused(undefined);
  }, [workspaceId]);
  // Children of a hidden (archived) parent rise to the top while at work.
  const { kids, fullOrder, main, background } = useMemo(
    () => sidebarRows(roster, activeId, order, gone),
    [roster, activeId, order, gone],
  );

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
        hide(id);
      } catch (err) {
        const why = err instanceof Error ? `: ${err.message}` : "";
        showToast(`Failed to archive agent${why}`, { type: "error" });
      }
    },
    [archiveAgent, hide, showToast],
  );
  // Delete anyway sends the refusal's fingerprint, with no second confirm.
  const remove = async (a: Agent, fingerprint?: string) => {
    setConfirming(undefined);
    setRefused(undefined);
    const refusal = await deleteAgent(a.agent_id, fingerprint);
    if (!refusal) {
      hide(a.agent_id);
      if (a.agent_id === activeId) navigate(`/ws/${ws}/home`);
    } else if (refusal.unsaved) {
      setRefused({ ...refusal, agent: a });
    } else {
      showToast(`Not deleted: ${refusal.error}`, { type: "error" });
    }
  };
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
    const list = visibleChildren(a, kids, activeId);
    if (!list.length) return null;
    return (
      <div
        role="group"
        aria-label={`${a.name} children`}
        className={styles.children}
      >
        {list.map((k) => (
          <SortableAgentRow
            key={k.agent_id}
            id={k.agent_id}
            label={k.name}
            pinned
            below={children(k)}
            onArchive={archive}
            onContextMenu={openMenu}
          >
            {link(k)}
          </SortableAgentRow>
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
          below: children(a),
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
        onDelete={() => setConfirming(menu && roster.get(menu.id))}
        onClose={() => setMenu(undefined)}
      />
      {confirming && (
        <ConfirmDialog
          isOpen
          title="Delete agent"
          message={`Delete ${confirming.name}? This cannot be undone.`}
          confirmLabel="Delete agent"
          variant="danger"
          onConfirm={() => void remove(confirming)}
          onCancel={() => setConfirming(undefined)}
        />
      )}
      {refused && (
        <ConfirmDialog
          isOpen
          title="Not deleted"
          // The title says Not deleted, so the body starts with the reason.
          message={`${refused.error.charAt(0).toUpperCase()}${refused.error.slice(1)}. Delete anyway loses these changes.`}
          confirmLabel="Delete anyway"
          confirmTestId="agent-delete-anyway"
          cancelLabel="Keep agent"
          variant="danger"
          onConfirm={() =>
            void remove(refused.agent, refused.unsaved ?? undefined)
          }
          onCancel={() => setRefused(undefined)}
        />
      )}
    </nav>
  );
}
