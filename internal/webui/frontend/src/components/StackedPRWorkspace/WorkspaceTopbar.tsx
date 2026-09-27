/**
 * The 59px breadcrumb bar — the only header row on /prs. When the route owns
 * the app chrome it also carries the live Loom shell controls the global
 * header would have shown: workspace switch, theme, account, and (on narrow
 * screens, where the 212px navigation is hidden) Back to workspace.
 */

import type { RouteChromeControls } from "@/contexts/RouteChromeContext";
import { Icon } from "./Icon";
import styles from "./StackedPRWorkspace.module.css";

export interface WorkspaceTopbarProps {
  current: string;
  workspaceName: string;
  workspaceId: string;
  chrome: RouteChromeControls | null;
  onShowGuide: () => void;
}

export function WorkspaceTopbar({
  current,
  workspaceName,
  workspaceId,
  chrome,
  onShowGuide,
}: WorkspaceTopbarProps): JSX.Element {
  const canSwitch = Boolean(chrome && chrome.workspaces.length > 1);
  const nextTheme = chrome?.theme === "light" ? "dark" : "light";
  return (
    <header className={styles.topbar} data-testid="stacked-pr-topbar">
      {chrome ? (
        <button
          type="button"
          className={`${styles.iconBtn} ${styles.topbarBack}`}
          aria-label="Back to workspace"
          title="Back to workspace"
          onClick={chrome.onBackToWorkspace}
        >
          <Icon name="back" />
        </button>
      ) : null}
      <nav className={styles.crumbs} aria-label="Breadcrumb">
        <span className={styles.crumb}>
          <Icon name="repo" /> All repositories
        </span>
        <span className={styles.slash} aria-hidden="true">
          /
        </span>
        <span className={styles.crumbCurrent} aria-current="page">
          {current}
        </span>
      </nav>
      <span className={styles.spacer} />
      {canSwitch && chrome ? (
        <label className={styles.wsPill} data-switch>
          <i aria-hidden="true" />
          <select
            aria-label="Switch workspace"
            value={chrome.activeWorkspaceId}
            onChange={(e) => chrome.onWorkspaceSwitch(e.target.value)}
          >
            {chrome.workspaces.map((ws) => (
              <option key={ws.id} value={ws.id}>
                {ws.name || ws.id}
              </option>
            ))}
          </select>
          <Icon name="chevron" />
        </label>
      ) : (
        <span className={styles.wsPill} title={`Workspace ${workspaceId}`}>
          <i aria-hidden="true" />
          {workspaceName}
        </span>
      )}
      {chrome ? (
        <button
          type="button"
          className={styles.iconBtn}
          aria-label={`Switch to ${nextTheme} theme`}
          title={`Switch to ${nextTheme} theme`}
          onClick={chrome.onToggleTheme}
          data-testid="stacked-pr-theme-toggle"
        >
          <Icon name={chrome.theme === "light" ? "moon" : "sun"} />
        </button>
      ) : null}
      <button
        type="button"
        className={styles.iconBtn}
        aria-label="How delivery groups work"
        onClick={onShowGuide}
      >
        <Icon name="info" />
      </button>
      {chrome?.accountMenu ? (
        <span className={styles.accountSlot}>{chrome.accountMenu}</span>
      ) : null}
    </header>
  );
}
