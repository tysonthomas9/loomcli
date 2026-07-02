/**
 * TerminalSection lists global terminal tabs in the workspace sidebar while
 * the Terminal view is active.
 */

import { useCallback, useEffect, useMemo, useState } from "react";

import { useWorkspaceContext } from "@/hooks/workspace";
import { BACKEND_BRAND_COLORS } from "@/utils/workspace";
import {
  DEFAULT_TERMINAL_WORKTREE_GROUP_ID,
  requestTerminalGroupSelect,
  requestTerminalNewTab,
  requestTerminalTabSelect,
  TERMINAL_SIDEBAR_SYNC_EVENT,
  type TerminalSidebarState,
  type TerminalSidebarTab,
  type TerminalWorktreeGroup,
} from "@/utils/terminalSidebarBridge";

import styles from "./AgentSection.module.css";
import { CreateTerminalWorktreeModal } from "./CreateTerminalWorktreeModal";
import terminalStyles from "./TerminalSection.module.css";

const EMPTY_STATE: TerminalSidebarState = {
  groups: [],
  tabs: [],
  activeTabId: "",
  activeGroupId: DEFAULT_TERMINAL_WORKTREE_GROUP_ID,
};

function TerminalRow({
  tab,
  selected,
}: {
  tab: TerminalSidebarTab;
  selected: boolean;
}): JSX.Element {
  const dotColor =
    BACKEND_BRAND_COLORS[tab.backendName] ?? "var(--terminal-text-muted, #888)";

  return (
    <button
      type="button"
      className={`${terminalStyles.row} ${selected ? terminalStyles.rowSelected : ""}`}
      data-selected={selected || undefined}
      data-testid={`sidebar-terminal-${tab.id}`}
      onClick={() => requestTerminalTabSelect(tab.id)}
      aria-current={selected ? "page" : undefined}
    >
      <span
        className={terminalStyles.statusDot}
        data-status={tab.connectionState}
        style={{ backgroundColor: dotColor }}
        aria-hidden="true"
      />
      <span className={terminalStyles.label}>{tab.label}</span>
    </button>
  );
}

function memberSummary(group: TerminalWorktreeGroup): string {
  return group.members
    .map((member) => {
      if (member.reusedBranch && !member.baseBranch) {
        return `${member.repoName} \u2190 existing branch`;
      }
      const flags = [
        member.baseDetached ? "detached" : "",
        member.reusedBranch ? "existing branch" : "",
      ].filter(Boolean);
      const base = member.baseBranch || (member.reusedBranch ? "" : "HEAD");
      const suffix = flags.length > 0 ? ` (${flags.join(", ")})` : "";
      return `${member.repoName} \u2190 ${base}${suffix}`;
    })
    .join(" \u00b7 ");
}

function normalizeSidebarState(
  detail: Partial<TerminalSidebarState>,
): TerminalSidebarState {
  return {
    groups: Array.isArray(detail.groups) ? detail.groups : [],
    tabs: Array.isArray(detail.tabs) ? detail.tabs : [],
    activeTabId: detail.activeTabId ?? "",
    activeGroupId: detail.activeGroupId ?? DEFAULT_TERMINAL_WORKTREE_GROUP_ID,
  };
}

export function TerminalSection(): JSX.Element {
  const { activeWorkspaceName, workspace, repos, workspaceId } =
    useWorkspaceContext();
  const [state, setState] = useState<TerminalSidebarState>(EMPTY_STATE);
  const [collapsedGroups, setCollapsedGroups] = useState<Set<string>>(
    () => new Set(),
  );
  const [isCreateOpen, setIsCreateOpen] = useState(false);

  const handleSync = useCallback((event: Event) => {
    const detail = (event as CustomEvent<Partial<TerminalSidebarState>>).detail;
    if (detail) setState(normalizeSidebarState(detail));
  }, []);

  useEffect(() => {
    window.addEventListener(TERMINAL_SIDEBAR_SYNC_EVENT, handleSync);
    return () => {
      window.removeEventListener(TERMINAL_SIDEBAR_SYNC_EVENT, handleSync);
    };
  }, [handleSync]);

  const workspaceLabel = activeWorkspaceName ?? workspace?.name ?? "Workspace";
  const groups = useMemo<TerminalWorktreeGroup[]>(() => {
    const incomingGroups = state.groups.length > 0 ? state.groups : [];
    const hasDefault = incomingGroups.some(
      (group) => group.id === DEFAULT_TERMINAL_WORKTREE_GROUP_ID,
    );
    const defaultGroup: TerminalWorktreeGroup = {
      id: DEFAULT_TERMINAL_WORKTREE_GROUP_ID,
      label: workspaceLabel,
      isDefault: true,
      members: [],
    };
    return hasDefault ? incomingGroups : [defaultGroup, ...incomingGroups];
  }, [state.groups, workspaceLabel]);

  const tabsByGroup = useMemo(() => {
    const grouped = new Map<string, TerminalSidebarTab[]>();
    for (const tab of state.tabs) {
      const groupId = tab.groupId || DEFAULT_TERMINAL_WORKTREE_GROUP_ID;
      const tabs = grouped.get(groupId) ?? [];
      tabs.push(tab);
      grouped.set(groupId, tabs);
    }
    return grouped;
  }, [state.tabs]);

  const toggleGroup = (groupId: string): void => {
    setCollapsedGroups((current) => {
      const next = new Set(current);
      if (next.has(groupId)) {
        next.delete(groupId);
      } else {
        next.add(groupId);
      }
      return next;
    });
  };

  const handleGroupClick = (groupId: string): void => {
    requestTerminalGroupSelect(groupId);
    toggleGroup(groupId);
  };

  const localRepos = useMemo(
    () =>
      repos.filter(
        (repo) => repo.path.trim().length > 0 && !repo.is_linked_worktree,
      ),
    [repos],
  );
  const hasRepos = localRepos.length > 0;

  return (
    <div
      className={`${styles.section} terminalSection`}
      data-testid="terminal-section"
    >
      <div className={`${styles.header} terminalSectionHeader`}>
        <span>Terminals</span>
      </div>
      <div className={`${styles.list} ${terminalStyles.groupList}`}>
        {groups.map((group) => {
          const isDefault = group.id === DEFAULT_TERMINAL_WORKTREE_GROUP_ID;
          const isActive = group.id === state.activeGroupId;
          const isCollapsed = collapsedGroups.has(group.id);
          const groupTabs = tabsByGroup.get(group.id) ?? [];
          const summary = isDefault ? "" : memberSummary(group);
          return (
            <div
              key={group.id}
              className={terminalStyles.group}
              data-active={isActive || undefined}
            >
              <button
                type="button"
                className={terminalStyles.groupHeader}
                onClick={() => handleGroupClick(group.id)}
                aria-expanded={!isCollapsed}
                data-active={isActive || undefined}
                data-testid={
                  isDefault
                    ? "terminal-group-workspace"
                    : `terminal-group-${group.id}`
                }
              >
                <span
                  className={terminalStyles.collapseChevron}
                  data-expanded={!isCollapsed}
                  aria-hidden="true"
                >
                  &gt;
                </span>
                <span className={terminalStyles.groupText}>
                  <span className={terminalStyles.groupLabel}>
                    {isDefault ? workspaceLabel : group.label}
                  </span>
                  {summary ? (
                    <span className={terminalStyles.memberSummary}>
                      {summary}
                    </span>
                  ) : null}
                </span>
              </button>
              {!isCollapsed ? (
                <div className={terminalStyles.groupBody}>
                  {groupTabs.length === 0 ? (
                    <p className={terminalStyles.emptyHint}>
                      No terminal sessions
                    </p>
                  ) : (
                    groupTabs.map((tab) => (
                      <TerminalRow
                        key={tab.id}
                        tab={tab}
                        selected={tab.id === state.activeTabId}
                      />
                    ))
                  )}
                  <button
                    type="button"
                    className={terminalStyles.newTerminalButton}
                    onClick={() => requestTerminalNewTab(group.id)}
                    data-testid={`sidebar-new-terminal-${group.id}`}
                  >
                    + New terminal
                  </button>
                </div>
              ) : null}
            </div>
          );
        })}
      </div>
      <button
        type="button"
        className={styles.addButton}
        onClick={() => setIsCreateOpen(true)}
        disabled={!hasRepos}
        data-testid="sidebar-new-worktree"
      >
        + New worktree
      </button>
      <CreateTerminalWorktreeModal
        isOpen={isCreateOpen}
        workspaceId={workspaceId}
        repos={localRepos}
        onClose={() => setIsCreateOpen(false)}
      />
    </div>
  );
}
