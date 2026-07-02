/**
 * TerminalSection lists global terminal tabs in the workspace sidebar while
 * the Terminal view is active.
 */

import { useCallback, useEffect, useState } from "react";

import { BACKEND_BRAND_COLORS } from "@/utils/workspace";
import {
  DEFAULT_TERMINAL_WORKTREE_GROUP_ID,
  requestTerminalNewTab,
  requestTerminalTabSelect,
  TERMINAL_SIDEBAR_SYNC_EVENT,
  type TerminalSidebarState,
  type TerminalSidebarTab,
} from "@/utils/terminalSidebarBridge";

import styles from "./AgentSection.module.css";
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

export function TerminalSection(): JSX.Element {
  const [state, setState] = useState<TerminalSidebarState>(EMPTY_STATE);

  const handleSync = useCallback((event: Event) => {
    const detail = (event as CustomEvent<TerminalSidebarState>).detail;
    if (detail) setState(detail);
  }, []);

  useEffect(() => {
    window.addEventListener(TERMINAL_SIDEBAR_SYNC_EVENT, handleSync);
    return () => {
      window.removeEventListener(TERMINAL_SIDEBAR_SYNC_EVENT, handleSync);
    };
  }, [handleSync]);

  return (
    <div
      className={`${styles.section} terminalSection`}
      data-testid="terminal-section"
    >
      <div className={`${styles.header} terminalSectionHeader`}>
        <span>Terminals</span>
      </div>
      <div className={styles.list}>
        {state.tabs.length === 0 ? (
          <p className={terminalStyles.emptyHint}>No terminal sessions</p>
        ) : (
          state.tabs.map((tab) => (
            <TerminalRow
              key={tab.id}
              tab={tab}
              selected={tab.id === state.activeTabId}
            />
          ))
        )}
      </div>
      <button
        type="button"
        className={styles.addButton}
        onClick={() => requestTerminalNewTab(state.activeGroupId)}
        data-testid="sidebar-new-terminal"
      >
        + New terminal
      </button>
    </div>
  );
}
