import { useEffect, useMemo, useState } from "react";
import { useStore } from "zustand";

import { useIssueStoreInstance } from "@/hooks/common";
import { useAgentSessions } from "@/hooks/terminal";
import type { Issue } from "@/types";
import type { SessionRecord } from "@/types/agent";
import { sessionTotalTokens } from "@/utils/sessionUsage";

import { SessionDetailView } from "./SessionDetailView";
import { SessionTimeline, type RunRailSummary } from "./SessionTimeline";
import type { SessionRowLabel } from "./SessionTimelineRow";
import styles from "./SessionsTab.module.css";

export function AgentRunsPanel({
  agentName,
  onTaskClick,
}: {
  agentName: string;
  onTaskClick?: (issue: Issue) => void;
}): JSX.Element {
  const { sessions, isLoading, error } = useAgentSessions(agentName);
  const issueStore = useIssueStoreInstance();
  const issuesMap = useStore(issueStore, (state) => state.issuesMap);
  const [selectedSessionId, setSelectedSessionId] = useState<string | null>(
    null,
  );

  useEffect(() => {
    if (sessions.length === 0) {
      setSelectedSessionId(null);
      return;
    }
    if (!sessions.some((session) => session.session_id === selectedSessionId)) {
      setSelectedSessionId(sessions[0]!.session_id);
    }
  }, [selectedSessionId, sessions]);

  const selectedSession =
    sessions.find((session) => session.session_id === selectedSessionId) ??
    null;
  const summary = useMemo(() => computeSummary(sessions), [sessions]);
  const getTicketLabel = (session: SessionRecord): SessionRowLabel =>
    agentRunTicketLabel(session, issuesMap.get(session.task_id)?.title);
  const selectedIssue = selectedSession
    ? issuesMap.get(selectedSession.task_id)
    : undefined;

  if (error && sessions.length === 0) {
    return (
      <div className={styles.emptyState} role="alert">
        Failed to load runs: {error.message}
      </div>
    );
  }
  if (!isLoading && sessions.length === 0) {
    return (
      <div className={styles.emptyState} data-testid="agent-runs-empty">
        No runs recorded for this agent yet
      </div>
    );
  }

  return (
    <div className={styles.outerContainer} data-testid="agent-runs-panel">
      <div className={styles.container}>
        <SessionTimeline
          sessions={sessions}
          selectedId={selectedSessionId}
          onSelect={setSelectedSessionId}
          isLoading={isLoading}
          summary={summary}
          getRowLabel={getTicketLabel}
          compactRows
        />
        {selectedSession ? (
          <SessionDetailView
            agentName={agentName}
            session={selectedSession}
            contextLabel={getTicketLabel(selectedSession)}
            {...(selectedIssue && onTaskClick
              ? { onContextClick: () => onTaskClick(selectedIssue) }
              : {})}
          />
        ) : (
          <div className={styles.detailEmpty}>Select a run to view details</div>
        )}
      </div>
    </div>
  );
}

export function agentRunTicketLabel(
  session: SessionRecord,
  issueTitle?: string,
): SessionRowLabel {
  const taskId = session.task_id.trim();
  if (!taskId) return { primary: "Unassigned run" };

  const title = issueTitle?.trim();
  if (!title) return { primary: taskId };
  return { primary: title, secondary: taskId };
}

function computeSummary(sessions: SessionRecord[]): RunRailSummary {
  return sessions.reduce<RunRailSummary>(
    (summary, session) => ({
      count: summary.count + 1,
      totalTokens: summary.totalTokens + sessionTotalTokens(session),
      totalCost: summary.totalCost + (session.estimated_cost_usd ?? 0),
      activeSessions: summary.activeSessions + (session.is_active ? 1 : 0),
      failedSessions:
        summary.failedSessions + (session.status === "failed" ? 1 : 0),
    }),
    {
      count: 0,
      totalTokens: 0,
      totalCost: 0,
      activeSessions: 0,
      failedSessions: 0,
    },
  );
}
