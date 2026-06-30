import { useEffect, useMemo, useRef, type MouseEvent } from "react";
import styles from "./FileExplorer.module.css";

interface FileTabBarProps {
  /** Open file paths, in tab order. */
  tabs: string[];
  activePath: string | null;
  onSelect: (path: string) => void;
  onClose: (path: string) => void;
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
  activePath,
  onSelect,
  onClose,
}: FileTabBarProps) {
  const activeRef = useRef<HTMLDivElement>(null);

  // Basenames that appear on more than one tab — those get a parent-dir hint.
  const duplicateNames = useMemo(() => {
    const counts = new Map<string, number>();
    for (const p of tabs) {
      const b = basename(p);
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
  }, [activePath]);

  if (tabs.length === 0) return null;

  return (
    <div className={styles.tabBar} role="tablist" aria-label="Open files">
      {tabs.map((path) => {
        const active = path === activePath;
        const name = basename(path);
        const hint =
          (duplicateNames.get(name) ?? 0) > 1 ? parentDirName(path) : "";
        const closeOnMiddle = (e: MouseEvent) => {
          if (e.button === 1) {
            e.preventDefault();
            onClose(path);
          }
        };
        return (
          <div
            key={path}
            ref={active ? activeRef : undefined}
            className={styles.tab}
            data-active={active}
            title={path}
            onAuxClick={closeOnMiddle}
          >
            <button
              type="button"
              role="tab"
              aria-selected={active}
              className={styles.tabSelect}
              onClick={() => onSelect(path)}
            >
              <span className={styles.tabName}>{name}</span>
              {hint && <span className={styles.tabHint}>{hint}</span>}
            </button>
            <button
              type="button"
              className={styles.tabClose}
              aria-label={`Close ${name}`}
              onClick={() => onClose(path)}
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
