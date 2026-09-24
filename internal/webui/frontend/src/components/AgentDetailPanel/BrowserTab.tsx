/**
 * BrowserTab — durable browser inventory for an interactive agent.
 *
 * Renders the agent surface tab strip `[Terminal][<browser> · <status>…]`
 * around the terminal body. Each tab is one FleetDB-backed durable browser
 * identity; this slice has no live page, so a browser is only ever shown as
 * Starting, Failed, or an explicit "Unknown status" (never Ready styling).
 *
 * - The terminal (children) stays mounted; a browser pane overlays it.
 * - New browsers appear as tabs without stealing focus or the active tab.
 * - Selecting a browser tab POSTs `select`; the inventory is then refetched.
 */

import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";

import { useAgentBrowsers, type AgentBrowsersError } from "@/hooks";
import type { AgentBrowser } from "@/types";

import styles from "./BrowserTab.module.css";

const TERMINAL_TAB = "terminal";

type StatusTone = "starting" | "failed" | "unknown";

export interface BrowserStatusView {
  label: string;
  tone: StatusTone;
}

/** Map a stored status to its display. Anything but starting/failed is unknown. */
export function browserStatusView(
  status: string | undefined,
): BrowserStatusView {
  switch ((status ?? "").trim().toLowerCase()) {
    case "starting":
      return { label: "Starting", tone: "starting" };
    case "failed":
      return { label: "Failed", tone: "failed" };
    default:
      return { label: "Unknown status", tone: "unknown" };
  }
}

const BADGE_CLASS: Record<StatusTone, string | undefined> = {
  starting: styles.badgeStarting,
  failed: styles.badgeFailed,
  unknown: styles.badgeUnknown,
};

function StatusBadge({ status }: { status: string | undefined }): JSX.Element {
  const view = browserStatusView(status);
  return (
    <span
      className={[styles.badge, BADGE_CLASS[view.tone]]
        .filter(Boolean)
        .join(" ")}
      data-status-tone={view.tone}
    >
      {view.label}
    </span>
  );
}

export interface AgentBrowserTabsProps {
  /** Interactive agents only. When false no tabs render and nothing is fetched. */
  enabled: boolean;
  workspaceId: string;
  agentName: string;
  /** The editor surface hosting this strip is visible. */
  surfaceActive?: boolean;
  /** Terminal body; always mounted. */
  children: ReactNode;
}

export function AgentBrowserTabs({
  enabled,
  workspaceId,
  agentName,
  surfaceActive = true,
  children,
}: AgentBrowserTabsProps): JSX.Element {
  const inventory = useAgentBrowsers({
    workspaceId,
    agentName,
    enabled,
    active: surfaceActive,
  });
  const { browsers, phase, error, refetch, select } = inventory;

  const [activeTab, setActiveTab] = useState<string>(TERMINAL_TAB);
  useEffect(() => {
    setActiveTab(TERMINAL_TAB);
  }, [workspaceId, agentName]);

  const activeBrowser = useMemo(
    () =>
      activeTab === TERMINAL_TAB
        ? undefined
        : browsers.find((b) => b.id === activeTab),
    [activeTab, browsers],
  );
  // A browser that left the inventory (error, auth loss, agent switch) can
  // not stay open: fall back to the terminal.
  const terminalActive = activeBrowser === undefined;

  const openBrowser = useCallback(
    (browser: AgentBrowser) => {
      setActiveTab(browser.id);
      void select(browser.id);
    },
    [select],
  );

  return (
    <div className={styles.surface}>
      {enabled ? (
        <div
          className={styles.tabStrip}
          role="tablist"
          aria-label="Agent surfaces"
        >
          <button
            type="button"
            role="tab"
            id={`agent-surface-tab-${TERMINAL_TAB}`}
            aria-selected={terminalActive}
            className={tabClass(terminalActive)}
            onClick={() => setActiveTab(TERMINAL_TAB)}
          >
            Terminal
          </button>
          {browsers.map((browser) => {
            const selected = activeBrowser?.id === browser.id;
            const view = browserStatusView(browser.status);
            return (
              <button
                key={browser.id}
                type="button"
                role="tab"
                aria-selected={selected}
                aria-label={`${browser.name} · ${view.label}`}
                title={`${browser.name} · ${view.label}`}
                data-browser-id={browser.id}
                className={tabClass(selected)}
                onClick={() => openBrowser(browser)}
              >
                <span>{browser.name}</span>
                <span className={styles.tabSeparator} aria-hidden="true">
                  ·
                </span>
                <StatusBadge status={browser.status} />
              </button>
            );
          })}
          <InventoryNotice
            phase={phase}
            count={browsers.length}
            error={error}
            onRetry={refetch}
          />
        </div>
      ) : null}
      <div className={styles.body}>
        {children}
        {activeBrowser ? (
          <div
            className={styles.pane}
            role="tabpanel"
            aria-label={`Browser ${activeBrowser.name}`}
          >
            <BrowserPane browser={activeBrowser} />
          </div>
        ) : null}
      </div>
    </div>
  );
}

function tabClass(active: boolean): string {
  return active ? `${styles.tab} ${styles.tabActive}` : (styles.tab ?? "");
}

function InventoryNotice({
  phase,
  count,
  error,
  onRetry,
}: {
  phase: string;
  count: number;
  error: AgentBrowsersError | null;
  onRetry: () => void;
}): JSX.Element | null {
  if (error) {
    if (error.code === "browser_desktop_required") {
      return (
        <span className={styles.notice} role="status">
          {error.message}
        </span>
      );
    }
    return (
      <span
        className={`${styles.notice} ${styles.noticeError}`}
        role="alert"
        data-error-code={error.code}
      >
        <span>Browsers unavailable: {error.message}</span>
        <button type="button" className={styles.retry} onClick={onRetry}>
          Retry
        </button>
      </span>
    );
  }
  if (count > 0) return null;
  if (phase === "loading") {
    return (
      <span className={styles.notice} role="status">
        Loading browsers…
      </span>
    );
  }
  if (phase === "ready") {
    return (
      <span className={styles.notice} role="status">
        No browsers
      </span>
    );
  }
  return null;
}

export function BrowserPane({
  browser,
}: {
  browser: AgentBrowser;
}): JSX.Element {
  const view = browserStatusView(browser.status);
  return (
    <div className={styles.card} data-testid="browser-pane">
      <div className={styles.eyebrow}>Durable browser</div>
      <div className={styles.title}>
        <span>{browser.name}</span>
        <StatusBadge status={browser.status} />
      </div>
      <dl className={styles.fields}>
        <dt>Name</dt>
        <dd>{browser.name}</dd>
        <dt>Browser ID</dt>
        <dd>
          <code className={styles.mono}>{browser.id}</code>
        </dd>
        <dt>Status</dt>
        <dd>{view.label}</dd>
        <dt>Owner</dt>
        <dd className={styles.mono}>{browser.owner_agent_id}</dd>
        <dt>Created by</dt>
        <dd className={styles.mono}>{browser.created_by}</dd>
        <dt>Selected</dt>
        <dd>{browser.selected ? "Yes" : "No"}</dd>
      </dl>
      <div className={styles.note}>
        The live page is not available yet. This shows the durable browser
        identity recorded in FleetDB only.
      </div>
    </div>
  );
}
