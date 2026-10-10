import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from "react";
import type { ReactNode } from "react";
import { useMatch } from "react-router-dom";

import { useAgentRoster } from "@/hooks";
import type { Roster } from "@/hooks/agents/agentRoster";

export interface SidebarRoster {
  roster: Roster;
  error: string | null;
  /**
   * Archived or deleted from the sidebar: a busy agent stays stopping until
   * its turn ends, and a delete reaches the stream later.
   */
  gone: ReadonlySet<string>;
  hide: (id: string) => void;
}

const Ctx = createContext<SidebarRoster | null>(null);

/** The sidebar's one live roster, from the AgentRosterOwner above. */
export function useSidebarRoster(): SidebarRoster {
  const v = useContext(Ctx);
  if (!v) throw new Error("useSidebarRoster needs an AgentRosterOwner");
  return v;
}

/**
 * Runs the sidebar's one roster for the expanded list and the collapsed rail,
 * so collapsing neither re-lists nor empties the shared roster, and an agent
 * archived in one view stays hidden in the other. With no workspace it runs
 * none, as neither view shows Agent API agents then.
 */
export function AgentRosterOwner({
  workspaceId,
  children,
}: {
  workspaceId: string;
  children: ReactNode;
}): JSX.Element {
  return workspaceId ? (
    <LiveRoster workspaceId={workspaceId}>{children}</LiveRoster>
  ) : (
    <>{children}</>
  );
}

function LiveRoster({
  workspaceId,
  children,
}: {
  workspaceId: string;
  children: ReactNode;
}): JSX.Element {
  const activeId = useMatch("/ws/:ws/chat/:agentId")?.params.agentId;
  const { roster, error } = useAgentRoster(workspaceId, activeId);
  const [gone, setGone] = useState<ReadonlySet<string>>(new Set());

  useEffect(() => setGone(new Set()), [workspaceId]);
  // The stream takes over from gone once it reports the archive or delete,
  // so an agent unarchived later shows again.
  useEffect(
    () =>
      setGone((g) => {
        const pending = (id: string) => {
          const a = roster.get(id);
          return a != null && a.state !== "archived";
        };
        const next = new Set([...g].filter(pending));
        return next.size === g.size ? g : next;
      }),
    [roster, gone],
  );
  const hide = useCallback(
    (id: string) => setGone((s) => new Set(s).add(id)),
    [],
  );

  const value = useMemo(
    () => ({ roster, error, gone, hide }),
    [roster, error, gone, hide],
  );
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}
