/**
 * SplitPaneSelector component.
 * Compact dropdown for choosing which tab to display in the right split pane.
 */

import { useCallback } from "react";

import styles from "./SplitPaneSelector.module.css";

interface SplitPaneSelectorTab {
  id: string;
  label: string;
  brandColor?: string | undefined;
}

interface SplitPaneSelectorProps {
  tabs: SplitPaneSelectorTab[];
  activeLeftTabId: string;
  rightPaneTabId: string;
  onTabChange: (tabId: string) => void;
}

export function SplitPaneSelector({
  tabs,
  activeLeftTabId,
  rightPaneTabId,
  onTabChange,
}: SplitPaneSelectorProps): JSX.Element | null {
  const availableTabs = tabs.filter((t) => t.id !== activeLeftTabId);

  const handleChange = useCallback(
    (e: React.ChangeEvent<HTMLSelectElement>) => {
      onTabChange(e.target.value);
    },
    [onTabChange],
  );

  if (availableTabs.length === 0) return null;

  return (
    <div className={styles.selector} data-testid="split-pane-selector">
      <select
        className={styles.select}
        value={rightPaneTabId}
        onChange={handleChange}
        aria-label="Right pane tab"
      >
        {availableTabs.map((tab) => (
          <option key={tab.id} value={tab.id}>
            {tab.label}
          </option>
        ))}
      </select>
    </div>
  );
}
