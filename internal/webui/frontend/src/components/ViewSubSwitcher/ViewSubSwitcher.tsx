/**
 * ViewSubSwitcher component.
 * Inline tab pair for toggling between Kanban and List sub-views
 * within the Workspaces NavRail group.
 */

import type { KeyboardEvent } from "react";

import type { ViewMode } from "@/components/ViewSwitcher";

import styles from "./ViewSubSwitcher.module.css";

export interface ViewSubSwitcherProps {
  activeView: ViewMode;
  onChange: (view: ViewMode) => void;
}

const SUB_VIEWS: { id: ViewMode; label: string }[] = [
  { id: "kanban", label: "Kanban" },
  { id: "table", label: "List" },
];

export function ViewSubSwitcher({
  activeView,
  onChange,
}: ViewSubSwitcherProps): JSX.Element | null {
  if (activeView !== "kanban" && activeView !== "table") {
    return null;
  }

  function handleKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    const currentIndex = SUB_VIEWS.findIndex((v) => v.id === activeView);
    let nextIndex = -1;

    if (event.key === "ArrowRight" || event.key === "ArrowDown") {
      nextIndex = (currentIndex + 1) % SUB_VIEWS.length;
    } else if (event.key === "ArrowLeft" || event.key === "ArrowUp") {
      nextIndex = (currentIndex - 1 + SUB_VIEWS.length) % SUB_VIEWS.length;
    }

    if (nextIndex >= 0) {
      event.preventDefault();
      const next = SUB_VIEWS[nextIndex];
      if (next) {
        onChange(next.id);
      }
    }
  }

  return (
    <div
      className={styles.subSwitcher}
      role="tablist"
      aria-label="View mode"
      onKeyDown={handleKeyDown}
    >
      {SUB_VIEWS.map((view) => {
        const isActive = activeView === view.id;
        return (
          <button
            key={view.id}
            type="button"
            role="tab"
            className={styles.tab}
            data-active={isActive || undefined}
            aria-selected={isActive}
            tabIndex={isActive ? 0 : -1}
            onClick={() => onChange(view.id)}
          >
            {view.label}
          </button>
        );
      })}
    </div>
  );
}
