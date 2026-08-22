import { useEffect, useState } from "react";

import { useWorkspaceContext } from "@/hooks/workspace";
import { isAgentActive } from "@/types";
import type { LoomAgentStatus } from "@/types";
import { plural } from "@/utils/plural";

import styles from "./HomeTopStrip.module.css";

function formatClock(now: Date): string {
  return now.toLocaleTimeString("en-GB", { hour12: false });
}

export interface HomeTopStripProps {
  workspaceId: string;
  agents: readonly LoomAgentStatus[];
}

export function HomeTopStrip({
  workspaceId,
  agents,
}: HomeTopStripProps): JSX.Element {
  const { workspace, repos, isLoading } = useWorkspaceContext();
  const [now, setNow] = useState(() => new Date());
  const [soleRepo] = repos;
  const repoLabel = isLoading
    ? "loading repos…"
    : repos.length === 1
      ? (soleRepo?.name ?? "no repos")
      : repos.length > 1
        ? `${repos.length} repos`
        : "no repos";
  const liveCount = agents.filter(isAgentActive).length;

  useEffect(() => {
    const interval = setInterval(() => setNow(new Date()), 1000);
    return () => clearInterval(interval);
  }, []);

  return (
    <header className={styles.strip} data-testid="home-top-strip">
      <div className={styles.crumb}>
        <span className={styles.workspace}>
          {workspace?.name ?? workspaceId}
        </span>
        <span className={styles.separator}>·</span>
        <span className={styles.repo} data-testid="strip-repos">
          {repoLabel}
        </span>
      </div>
      <div className={styles.right}>
        <span className={styles.separator}>·</span>
        <span>
          {agents.length} {plural(agents.length, "agent", "agents")} ·{" "}
          {liveCount} live
        </span>
        <span className={styles.separator}>·</span>
        <time>{formatClock(now)}</time>
      </div>
    </header>
  );
}
