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
  const { workspace, repos, activeRepoNames, isLoading } =
    useWorkspaceContext();
  const [now, setNow] = useState(() => new Date());
  const activeRepo = activeRepoNames[0];
  const repo =
    repos.find((candidate) => candidate.name === activeRepo) ?? repos[0];
  const repoLabel =
    repo?.name ??
    (repos.length > 0
      ? `${repos.length} repos`
      : isLoading
        ? "loading repos…"
        : "no repos");
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
        <span className={styles.separator}>/</span>
        <span className={styles.repo}>{repoLabel}</span>
      </div>
      {repo?.default_branch && (
        <span className={styles.branch}>{repo.default_branch}</span>
      )}
      <div className={styles.right}>
        <span>
          {agents.length} {plural(agents.length, "agent", "agents")} ·{" "}
          {liveCount} live
        </span>
        <time>{formatClock(now)}</time>
      </div>
    </header>
  );
}
