/**
 * VS Code-style editor groups for the Agents main panel (Aether wireframe).
 * Split moves the active tab into a new right column; tabs drag between columns.
 */

import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";

import styles from "./AgentEditorGroups.module.css";

export type AgentEditorTab =
  | "terminal"
  | "chat"
  | "info"
  | "git"
  | "diff"
  | "files";

const ALL_TABS: AgentEditorTab[] = ["terminal", "info", "git", "diff", "files"];

const TAB_LABELS: Record<AgentEditorTab, string> = {
  terminal: "Terminal",
  chat: "Chat",
  info: "Info",
  git: "Git",
  diff: "Diff",
  files: "Files",
};

type EditorGroup = {
  tabs: AgentEditorTab[];
  active: AgentEditorTab;
};

type DragPayload = {
  fromGroup: number;
  tab: AgentEditorTab;
};

function normalizeGroups(
  groups: EditorGroup[],
  first: AgentEditorTab,
): EditorGroup[] {
  const kept = groups.filter((g) => g.tabs.length > 0);
  if (kept.length === 0) return [{ tabs: [first], active: first }];
  return kept.map((g) => ({
    tabs: g.tabs,
    active: g.tabs.includes(g.active) ? g.active : g.tabs[0]!,
  }));
}

function initialGroups(
  tabs: AgentEditorTab[],
  active: AgentEditorTab | undefined,
): EditorGroup[] {
  const first = active && tabs.includes(active) ? active : tabs[0]!;
  return [{ tabs: [...tabs], active: first }];
}

/** Aether wireframe "columns" icon — split active tab into a right editor group. */
function SplitEditorRightIcon(): JSX.Element {
  return (
    <svg
      className={styles.stripIcon}
      width={15}
      height={15}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.7}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M4 4h16v16H4z" />
      <path d="M12 4v16" />
    </svg>
  );
}

export interface AgentEditorGroupsProps {
  /** Resets layout when the selected agent changes. */
  resetKey: string | undefined;
  renderPane: (tab: AgentEditorTab, isActive: boolean) => ReactNode;
  /** The tabs, in order; the first is the default. v5 agents get Terminal first. */
  tabs?: AgentEditorTab[];
  /** The tab to open on (for example from the URL); else the first tab. */
  initialTab?: AgentEditorTab | undefined;
  /** Called when the user picks a tab, so the page can remember it. */
  onTabChange?: (tab: AgentEditorTab) => void;
}

export function AgentEditorGroups({
  resetKey,
  renderPane,
  tabs = ALL_TABS,
  initialTab,
  onTabChange,
}: AgentEditorGroupsProps): JSX.Element {
  const [groups, setGroups] = useState<EditorGroup[]>(() =>
    initialGroups(tabs, initialTab),
  );
  const dragRef = useRef<DragPayload | null>(null);
  const isSplit = groups.length > 1;
  const firstTab = tabs[0]!;

  // The layout resets when the agent changes, not when the opening tab does.
  useEffect(() => {
    setGroups(initialGroups(tabs, initialTab));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [resetKey]);

  const activate = useCallback(
    (groupIndex: number, tab: AgentEditorTab) => {
      setGroups((prev) =>
        prev.map((g, i) => (i === groupIndex ? { ...g, active: tab } : g)),
      );
      onTabChange?.(tab);
    },
    [onTabChange],
  );

  const splitActiveTab = useCallback(() => {
    setGroups((prev) => {
      if (prev.length > 1) return prev;
      const group = prev[0];
      if (!group || group.tabs.length < 2) return prev;
      const moving = group.active;
      const remaining = group.tabs.filter((t) => t !== moving);
      const leftActive =
        remaining[Math.max(0, group.tabs.indexOf(moving) - 1)] ?? remaining[0]!;
      return [
        { tabs: remaining, active: leftActive },
        { tabs: [moving], active: moving },
      ];
    });
  }, []);

  const moveTab = useCallback(
    (toGroup: number) => {
      const payload = dragRef.current;
      if (!payload) return;
      const { fromGroup, tab } = payload;
      if (fromGroup === toGroup) return;

      setGroups((prev) =>
        normalizeGroups(
          prev.map((g, i) => {
            if (i === fromGroup) {
              const tabs = g.tabs.filter((t) => t !== tab);
              const active =
                g.active === tab ? (tabs[0] ?? g.active) : g.active;
              return { tabs, active };
            }
            if (i === toGroup) {
              if (g.tabs.includes(tab)) {
                return { ...g, active: tab };
              }
              return { tabs: [...g.tabs, tab], active: tab };
            }
            return g;
          }),
          firstTab,
        ),
      );
      dragRef.current = null;
    },
    [firstTab],
  );

  const handleDragStart = useCallback(
    (fromGroup: number, tab: AgentEditorTab) => {
      dragRef.current = { fromGroup, tab };
    },
    [],
  );

  const handleDragEnd = useCallback(() => {
    dragRef.current = null;
  }, []);

  const handleDrop = useCallback(
    (toGroup: number) => {
      moveTab(toGroup);
    },
    [moveTab],
  );

  const handleDragOver = useCallback((event: React.DragEvent) => {
    event.preventDefault();
  }, []);

  return (
    <div
      className={`${styles.panes} ${isSplit ? styles.split : ""}`}
      data-testid="agent-editor-groups"
      data-split={isSplit ? "true" : undefined}
    >
      {groups.map((group, groupIndex) => (
        <div
          key={groupIndex}
          className={styles.paneCol}
          onDragOver={handleDragOver}
          onDrop={() => handleDrop(groupIndex)}
        >
          <div
            className={styles.strip}
            onDragOver={handleDragOver}
            onDrop={() => handleDrop(groupIndex)}
          >
            {group.tabs.map((tab) => (
              <button
                key={tab}
                type="button"
                draggable
                className={styles.editorTab}
                data-active={group.active === tab || undefined}
                aria-current={group.active === tab ? "page" : undefined}
                onClick={() => activate(groupIndex, tab)}
                onDragStart={() => handleDragStart(groupIndex, tab)}
                onDragEnd={handleDragEnd}
              >
                {TAB_LABELS[tab]}
              </button>
            ))}
            <span className={styles.stripSpacer} />
            {groupIndex === 0 && !isSplit && group.tabs.length >= 2 ? (
              <div className={styles.stripControls}>
                <button
                  type="button"
                  className={styles.stripControl}
                  onClick={splitActiveTab}
                  aria-label="Split editor right"
                  title="Move active tab to the right pane"
                  data-testid="agent-editor-split"
                >
                  <SplitEditorRightIcon />
                </button>
              </div>
            ) : null}
          </div>
          <div className={styles.paneBody}>
            {group.tabs.map((tab) => (
              <div
                key={tab}
                className={styles.paneSlot}
                data-hidden={group.active !== tab ? "true" : undefined}
                role="tabpanel"
                aria-hidden={group.active !== tab}
              >
                {renderPane(tab, group.active === tab)}
              </div>
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}
