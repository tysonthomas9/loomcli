import { useEffect, useMemo, useRef, type MouseEvent } from "react";
import type { FileBrowserTab } from "@/stores";
import { checkoutSuffix, tabIdentityKey } from "@/utils/fileExplorerRefs";
import styles from "./FileExplorer.module.css";

interface FileTabBarProps {
  /** Open file tabs, in tab order. */
  tabs: FileBrowserTab[];
  activeKey: string | null;
  dirtyPaths?: Record<string, boolean> | undefined;
  groupLabel?: string | undefined;
  onSelect: (tabKey: string) => void;
  onClose: (tabKey: string) => void;
}

function basename(p: string): string {
  return p.split("/").pop() || p;
}

/** Immediate parent folder name, used to disambiguate same-named tabs. */
function parentDirName(p: string): string {
  const parts = p.split("/");
  return parts.length >= 2 ? (parts[parts.length - 2] ?? "") : "";
}

/**
 * FileTabBar renders the open-files tab strip for the workspace file browser.
 * Click a tab to activate it, the × (or middle-click) to close it.
 */
export function FileTabBar({
  tabs,
  activeKey,
  dirtyPaths,
  groupLabel,
  onSelect,
  onClose,
}: FileTabBarProps) {
  const activeRef = useRef<HTMLDivElement>(null);

  // Basenames that appear on more than one tab — those get a parent-dir hint.
  const duplicateNames = useMemo(() => {
    const counts = new Map<string, number>();
    for (const tab of tabs) {
      const b = basename(tab.path);
      counts.set(b, (counts.get(b) ?? 0) + 1);
    }
    return counts;
  }, [tabs]);

  // Keep the active tab visible when it changes or the strip overflows.
  useEffect(() => {
    const el = activeRef.current;
    if (el && typeof el.scrollIntoView === "function") {
      el.scrollIntoView({ block: "nearest", inline: "nearest" });
    }
  }, [activeKey]);

  if (tabs.length === 0) return null;

  return (
    <div
      className={styles.tabBar}
      role="tablist"
      aria-label={groupLabel ? `Open files ${groupLabel}` : "Open files"}
    >
      {tabs.map((tab) => {
        const key = tabIdentityKey(tab);
        const active = key === activeKey;
        const name = basename(tab.path);
        const hint =
          (duplicateNames.get(name) ?? 0) > 1
            ? checkoutSuffix(tab.ref) || parentDirName(tab.path)
            : "";
        const closeOnMiddle = (e: MouseEvent) => {
          if (e.button === 1) {
            e.preventDefault();
            onClose(key);
          }
        };
        return (
          <div
            key={key}
            ref={active ? activeRef : undefined}
            className={styles.tab}
            data-active={active}
            title={`${checkoutSuffix(tab.ref)}: ${tab.path}`}
            onAuxClick={closeOnMiddle}
          >
            <button
              type="button"
              role="tab"
              aria-selected={active}
              className={styles.tabSelect}
              onClick={() => onSelect(key)}
            >
              {dirtyPaths?.[key] && (
                <span className={styles.tabDirty} aria-hidden="true" />
              )}
              <span className={styles.tabName}>{name}</span>
              {hint && <span className={styles.tabHint}>{hint}</span>}
            </button>
            <button
              type="button"
              className={styles.tabClose}
              aria-label={`Close ${name}`}
              onClick={() => onClose(key)}
            >
              <svg
                viewBox="0 0 16 16"
                width="10"
                height="10"
                aria-hidden="true"
              >
                <path
                  d="M4 4l8 8M12 4l-8 8"
                  stroke="currentColor"
                  strokeWidth="1.5"
                />
              </svg>
            </button>
          </div>
        );
      })}
    </div>
  );
}
