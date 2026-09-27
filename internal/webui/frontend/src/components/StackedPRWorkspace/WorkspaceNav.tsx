/**
 * Labeled workspace navigation for the stacked PR workspace — the 212px
 * reference sidebar adapted to Loom: queue vs merge history, repository
 * filters with real counts, the delivery-group explainer, and shortcuts.
 */

import type { RouteChromeControls } from "@/contexts/RouteChromeContext";
import type { QueueMode } from "@/utils/pullRequest/stackedPrModel";
import {
  initialsFor,
  repoBasename,
} from "@/utils/pullRequest/stackedPrPresentation";
import { Icon } from "./Icon";
import styles from "./StackedPRWorkspace.module.css";

export interface WorkspaceNavProps {
  workspaceName: string;
  mode: QueueMode;
  onModeChange: (mode: QueueMode) => void;
  queueCount: number;
  historyCount: number;
  repoOptions: ReadonlyArray<readonly [string, number]>;
  selectedRepos: ReadonlySet<string>;
  onToggleRepo: (repo: string) => void;
  onClearRepos: () => void;
  onShowGuide: () => void;
  onShowShortcuts: () => void;
  userName: string | null;
  userSub: string | null;
  /** Live Loom shell controls when /prs owns the app chrome. */
  chrome?: RouteChromeControls | null;
}

export function WorkspaceNav({
  workspaceName,
  mode,
  onModeChange,
  queueCount,
  historyCount,
  repoOptions,
  selectedRepos,
  onToggleRepo,
  onClearRepos,
  onShowGuide,
  onShowShortcuts,
  userName,
  userSub,
  chrome = null,
}: WorkspaceNavProps): JSX.Element {
  return (
    <nav
      className={styles.wsNav}
      aria-label="Pull request workspace"
      data-testid="stacked-pr-nav"
    >
      <div className={styles.brand}>
        <span className={styles.brandMark} aria-hidden="true">
          ◇
        </span>
        <span className={styles.brandName}>Loom</span>
      </div>

      <div className={styles.wsCard}>
        <span className={styles.wsMark} aria-hidden="true">
          {initialsFor(workspaceName).slice(0, 1)}
        </span>
        <span className={styles.wsCopy}>
          <span className={styles.wsName} title={workspaceName}>
            {workspaceName}
          </span>
          <span className={styles.wsSub}>Loom workspace</span>
        </span>
      </div>

      <p className={styles.sectionLabel}>Workspace</p>
      {chrome ? (
        <button
          type="button"
          className={styles.navItem}
          onClick={chrome.onBackToWorkspace}
          title="Back to workspace"
        >
          <Icon name="back" />
          <span className={styles.navText}>Back to workspace</span>
        </button>
      ) : null}
      <button
        type="button"
        className={styles.navItem}
        aria-pressed={mode === "queue"}
        data-active={mode === "queue" || undefined}
        onClick={() => onModeChange("queue")}
      >
        <Icon name="pr" />
        <span className={styles.navText}>Pull requests</span>
        <span className={styles.navCount}>{queueCount}</span>
      </button>
      <button
        type="button"
        className={styles.navItem}
        aria-pressed={mode === "history"}
        data-active={mode === "history" || undefined}
        onClick={() => onModeChange("history")}
      >
        <Icon name="history" />
        <span className={styles.navText}>Merge history</span>
        <span className={styles.navCount}>{historyCount}</span>
      </button>

      {repoOptions.length > 0 ? (
        <div className={styles.repoGroup}>
          <p className={styles.sectionLabel}>
            Repositories
            {selectedRepos.size > 0 ? (
              <button
                type="button"
                className={styles.sectionClear}
                onClick={onClearRepos}
              >
                Clear
              </button>
            ) : null}
          </p>
          {repoOptions.map(([repo, count]) => {
            const on = selectedRepos.has(repo);
            return (
              <button
                key={repo}
                type="button"
                className={styles.navItem}
                aria-pressed={on}
                aria-label={`Filter repository ${repo}`}
                data-active={on || undefined}
                title={repo}
                onClick={() => onToggleRepo(repo)}
              >
                <span className={styles.repoDot} aria-hidden="true" />
                <span className={styles.navText}>{repoBasename(repo)}</span>
                <span className={styles.navCount}>{count}</span>
              </button>
            );
          })}
        </div>
      ) : null}

      <div className={styles.navSpacer} />

      <div className={styles.navNote}>
        <strong>
          <Icon name="stack" /> Small PRs. Clear order.
        </strong>
        Delivery groups show each change in merge order, from its first step to
        main.
      </div>

      <div className={styles.navFooter}>
        <button type="button" className={styles.navItem} onClick={onShowGuide}>
          <Icon name="help" />
          <span className={styles.navText}>How delivery groups work</span>
        </button>
        <button
          type="button"
          className={styles.navItem}
          onClick={onShowShortcuts}
          aria-label="Keyboard shortcuts"
        >
          <Icon name="keyboard" />
          <span className={styles.navText}>Shortcuts</span>
          <kbd className={styles.kbd}>?</kbd>
        </button>
        {userName ? (
          <div className={styles.navUser}>
            <span className={styles.avatar} aria-hidden="true">
              {initialsFor(userName)}
            </span>
            <span className={styles.wsCopy}>
              <span className={styles.wsName}>{userName}</span>
              {userSub ? <span className={styles.wsSub}>{userSub}</span> : null}
            </span>
          </div>
        ) : null}
      </div>
    </nav>
  );
}
