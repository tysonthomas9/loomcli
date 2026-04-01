import type { ReactNode, RefObject } from "react";

import {
  SplitDivider,
  SplitPaneSelector,
} from "@/components/TerminalView/layout";
import {
  BACKEND_BRAND_COLORS,
  type TabState,
} from "@/components/TerminalView/tabs";
import styles from "./TerminalPaneArea.module.css";

interface TerminalPaneAreaProps {
  tabs: TabState[];
  activeTabId: string;
  isSplitView: boolean;
  rightPaneTabId: string;
  splitRatio: number;
  splitContainerRef: RefObject<HTMLDivElement>;
  onSplitRatioChange: (ratio: number) => void;
  onRightPaneTabChange: (tabId: string) => void;
  renderPane: (tab: TabState, pane: "left" | "right" | null) => ReactNode;
}

export function TerminalPaneArea({
  tabs,
  activeTabId,
  isSplitView,
  rightPaneTabId,
  splitRatio,
  splitContainerRef,
  onSplitRatioChange,
  onRightPaneTabChange,
  renderPane,
}: TerminalPaneAreaProps): JSX.Element {
  return (
    <div className={styles.terminalsContainer}>
      {isSplitView && rightPaneTabId ? (
        <div
          ref={splitContainerRef}
          className={styles.splitContainer}
          style={{
            gridTemplateColumns: `${splitRatio}fr auto ${1 - splitRatio}fr`,
          }}
          data-testid="split-container"
        >
          <div className={styles.splitPaneLeft}>
            {tabs.map((tab) => {
              const active = tab.id === activeTabId;
              return (
                <div
                  key={tab.id}
                  className={styles.terminalPaneSplit}
                  style={{
                    visibility: active ? "visible" : "hidden",
                    position: active ? "relative" : "absolute",
                    inset: active ? undefined : 0,
                  }}
                  role="tabpanel"
                  aria-hidden={!active}
                  {...(!active && { inert: "" })}
                  id={`terminal-panel-${tab.id}`}
                  aria-labelledby={`terminal-tab-${tab.id}`}
                >
                  {renderPane(tab, "left")}
                </div>
              );
            })}
          </div>
          <SplitDivider
            onRatioChange={onSplitRatioChange}
            containerRef={splitContainerRef}
          />
          <div className={styles.splitPaneRight}>
            <SplitPaneSelector
              tabs={tabs.map((t) => ({
                id: t.id,
                label: t.label,
                brandColor: BACKEND_BRAND_COLORS[t.backendName],
              }))}
              activeLeftTabId={activeTabId}
              rightPaneTabId={rightPaneTabId}
              onTabChange={onRightPaneTabChange}
            />
            {tabs.map((tab) => {
              const active = tab.id === rightPaneTabId;
              return (
                <div
                  key={tab.id}
                  className={styles.terminalPaneSplit}
                  style={{
                    visibility: active ? "visible" : "hidden",
                    position: active ? "relative" : "absolute",
                    inset: active ? undefined : 0,
                  }}
                  role="tabpanel"
                  aria-hidden={!active}
                  {...(!active && { inert: "" })}
                  id={`terminal-panel-right-${tab.id}`}
                >
                  {renderPane(tab, "right")}
                </div>
              );
            })}
          </div>
        </div>
      ) : (
        tabs.map((tab) => {
          const active = tab.id === activeTabId;
          return (
            <div
              key={tab.id}
              className={styles.terminalPane}
              style={{
                visibility: active ? "visible" : "hidden",
                position: active ? "relative" : "absolute",
                inset: active ? undefined : 0,
              }}
              role="tabpanel"
              aria-hidden={!active}
              {...(!active && { inert: "" })}
              id={`terminal-panel-${tab.id}`}
              aria-labelledby={`terminal-tab-${tab.id}`}
            >
              {renderPane(tab, null)}
            </div>
          );
        })
      )}
    </div>
  );
}
