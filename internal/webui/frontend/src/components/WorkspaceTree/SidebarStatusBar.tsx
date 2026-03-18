/**
 * SidebarStatusBar displays agent activity counts at the bottom of the sidebar.
 * Shows "N working · N reviewing · N idle" with colored status dots.
 */

import { useMemo } from "react";

import type { LoomAgentStatus } from "@/types";
import { parseLoomStatus } from "@/types/agent";

import styles from "./SidebarStatusBar.module.css";

export interface SidebarStatusBarProps {
  agents: LoomAgentStatus[];
}

export function SidebarStatusBar({
  agents,
}: SidebarStatusBarProps): JSX.Element | null {
  const counts = useMemo(() => {
    let working = 0;
    let reviewing = 0;
    let idle = 0;

    for (const agent of agents) {
      const parsed = parseLoomStatus(agent.status);
      switch (parsed.type) {
        case "working":
        case "planning":
          working++;
          break;
        case "review":
          reviewing++;
          break;
        default:
          // ready, idle, done, error, dirty, changes
          idle++;
          break;
      }
    }

    return { working, reviewing, idle };
  }, [agents]);

  if (agents.length === 0) {
    return null;
  }

  return (
    <div className={styles.statusBar}>
      <span className={styles.segment}>
        <span className={`${styles.dot} ${styles.dotWorking}`} />
        {counts.working} working
      </span>
      <span className={styles.separator}>·</span>
      <span className={styles.segment}>
        <span className={`${styles.dot} ${styles.dotReview}`} />
        {counts.reviewing} reviewing
      </span>
      <span className={styles.separator}>·</span>
      <span className={styles.segment}>
        <span className={`${styles.dot} ${styles.dotIdle}`} />
        {counts.idle} idle
      </span>
    </div>
  );
}
