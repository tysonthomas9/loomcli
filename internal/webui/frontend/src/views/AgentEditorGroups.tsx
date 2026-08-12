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
  | "runs"
  | "terminal"
  | "info"
  | "git"
  | "diff"
  | "files";

export interface AgentCapabilities {
  worktree: boolean;
  pty: boolean;
  runs: boolean;
  config: boolean;
}

const TAB_LABELS: Record<AgentEditorTab, string> = {
  runs: "Runs",
  terminal: "Terminal",
  info: "Info",
  git: "Git",
  diff: "Diff",
  files: "Files",
};

const FALLBACK_TABS: AgentEditorTab[] = ["info"];

type EditorGroup = {
  tabs: AgentEditorTab[];
  active: AgentEditorTab;
};

type DragPayload = {
  fromGroup: number;
  tab: AgentEditorTab;
};

export function agentTabsForCapabilities(
  capabilities: AgentCapabilities,
): AgentEditorTab[] {
  const tabs: AgentEditorTab[] = [];
  if (capabilities.runs) tabs.push("runs");
  if (capabilities.pty) tabs.push("terminal");
  if (capabilities.config) tabs.push("info");
  if (capabilities.worktree) tabs.push("git", "diff", "files");
  return tabs.length > 0 ? tabs : [...FALLBACK_TABS];
}

function storageKeyFor(resetKey: string | undefined): string | null {
  return resetKey ? `loom.agentEditorGroups.${resetKey}` : null;
}

function isAgentEditorTab(value: unknown): value is AgentEditorTab {
  return typeof value === "string" && value in TAB_LABELS;
}

function deserializeGroups(value: string | null): EditorGroup[] | null {
  if (!value) return null;
  try {
    const parsed = JSON.parse(value) as unknown;
    if (!Array.isArray(parsed)) return null;
    const groups: EditorGroup[] = [];
    for (const group of parsed) {
      if (group == null || typeof group !== "object") continue;
      const maybeGroup = group as { tabs?: unknown; active?: unknown };
      if (!Array.isArray(maybeGroup.tabs)) continue;
      const tabs = maybeGroup.tabs.filter(isAgentEditorTab);
      const active = isAgentEditorTab(maybeGroup.active)
        ? maybeGroup.active
        : tabs[0];
      if (!active) continue;
      groups.push({ tabs, active });
    }
    return groups;
  } catch {
    return null;
  }
}

function fallbackGroup(availableTabs: AgentEditorTab[]): EditorGroup {
  const tabs = availableTabs.length > 0 ? availableTabs : FALLBACK_TABS;
  return { tabs: [...tabs], active: tabs[0]! };
}

function normalizeGroups(
  groups: EditorGroup[],
  availableTabs: AgentEditorTab[],
): EditorGroup[] {
  const allowed = new Set(availableTabs);
  const seen = new Set<AgentEditorTab>();
  const normalized = groups
    .map((group) => {
      const tabs = group.tabs.filter((tab) => {
        if (!allowed.has(tab) || seen.has(tab)) return false;
        seen.add(tab);
        return true;
      });
      return {
        tabs,
        active: tabs.includes(group.active) ? group.active : tabs[0]!,
      };
    })
    .filter((group) => group.tabs.length > 0);

  const missing = availableTabs.filter((tab) => !seen.has(tab));
  if (normalized.length === 0) {
    return [fallbackGroup(availableTabs)];
  }
  if (missing.length > 0) {
    const first = normalized[0]!;
    normalized[0] = {
      ...first,
      tabs: [...first.tabs, ...missing],
      active: first.tabs.includes(first.active) ? first.active : first.tabs[0]!,
    };
  }
  return normalized;
}

function initialGroups(availableTabs: AgentEditorTab[]): EditorGroup[] {
  return normalizeGroups(
    [{ tabs: [...availableTabs], active: availableTabs[0] ?? "info" }],
    availableTabs,
  );
}

function loadInitialGroups(
  resetKey: string | undefined,
  availableTabs: AgentEditorTab[],
): EditorGroup[] {
  const storageKey = storageKeyFor(resetKey);
  if (storageKey && typeof window !== "undefined") {
    const stored = deserializeGroups(window.localStorage.getItem(storageKey));
    if (stored) return normalizeGroups(stored, availableTabs);
  }
  return initialGroups(availableTabs);
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
  tabs: AgentEditorTab[];
  renderPane: (tab: AgentEditorTab, isActive: boolean) => ReactNode;
}

export function AgentEditorGroups({
  resetKey,
  tabs,
  renderPane,
}: AgentEditorGroupsProps): JSX.Element {
  const tabsKey = tabs.join("|");
  const [groups, setGroups] = useState<EditorGroup[]>(() =>
    loadInitialGroups(resetKey, tabs),
  );
  const dragRef = useRef<DragPayload | null>(null);
  const isSplit = groups.length > 1;

  useEffect(() => {
    setGroups(loadInitialGroups(resetKey, tabs));
  }, [resetKey, tabsKey, tabs]);

  useEffect(() => {
    const storageKey = storageKeyFor(resetKey);
    if (!storageKey || typeof window === "undefined") return;
    window.localStorage.setItem(storageKey, JSON.stringify(groups));
  }, [groups, resetKey]);

  const activate = useCallback((groupIndex: number, tab: AgentEditorTab) => {
    setGroups((prev) =>
      prev.map((g, i) => (i === groupIndex ? { ...g, active: tab } : g)),
    );
  }, []);

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
          tabs,
        ),
      );
      dragRef.current = null;
    },
    [tabs],
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
