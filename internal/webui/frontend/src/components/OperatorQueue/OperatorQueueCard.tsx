import { useEffect, useMemo, useState } from "react";

import { useElapsedTime } from "@/hooks/common";
import type { OperatorQueueItem } from "@/hooks/issues";
import { useWorkspaceContext } from "@/hooks/workspace";
import type { Issue, LoomAgentStatus } from "@/types";
import { effectiveAgentStatus, parseLoomStatus } from "@/types/agent";
import { hasDesign, NEEDS_REVISION_LABEL } from "@/utils/issue";
import { repoNameForSource } from "@/utils/workspace/repoPresentation";

import styles from "./OperatorQueueCard.module.css";

const IMPLEMENTATION_ROLES = new Set([
  "coder",
  "dev",
  "developer",
  "implementation",
  "implementer",
  "task",
  "worker",
]);

const KIND_LABEL = {
  "design-gate": "Design gate",
  blocked: "Blocked",
  "needs-revision": "Needs revision",
} as const;

export interface OperatorQueueCardProps {
  item: OperatorQueueItem;
  agents: readonly LoomAgentStatus[];
  onApprove: (issue: Issue, assignee?: string) => Promise<void>;
  onUnblock: (issue: Issue) => Promise<void>;
  onOpenIssue: (issue: Issue) => void;
}

function isIdleImplementationAgent(agent: LoomAgentStatus): boolean {
  const role = (agent.role ?? "").trim().toLowerCase();
  const status = parseLoomStatus(effectiveAgentStatus(agent)).type;
  return (
    IMPLEMENTATION_ROLES.has(role) && (status === "idle" || status === "ready")
  );
}

export function pickDefaultAgentName(
  agents: readonly LoomAgentStatus[],
  sourceRepo: string | undefined,
): string | undefined {
  const servingAgents = sourceRepo
    ? agents.filter((agent) => agent.repo === sourceRepo)
    : [];
  return (
    servingAgents.find(isIdleImplementationAgent)?.name ??
    servingAgents[0]?.name
  );
}

function stripBlockedPrefix(notes: string): string {
  return notes.replace(/^\s*BLOCKED:\s*/i, "").trim();
}

function formatClock(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "an unknown time";
  return date.toLocaleTimeString([], {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  });
}

function actorSentence(item: OperatorQueueItem): JSX.Element {
  const { issue, kind } = item;
  const actor = issue.assignee?.trim();
  const time = formatClock(issue.updated_at);

  if (kind === "design-gate") {
    return (
      <>
        {actor ? (
          <>
            <span className={styles.actor}>{actor}</span> attached a design and
            moved the task to <strong>review</strong> at {time}.
          </>
        ) : (
          <>
            A design was attached and the task moved to <strong>review</strong>{" "}
            at {time}.
          </>
        )}{" "}
        It stays parked until you approve and route it.
      </>
    );
  }
  if (kind === "blocked") {
    return (
      <>
        {actor ? (
          <>
            <span className={styles.actor}>{actor}</span> declared the task
            blocked at {time}.
          </>
        ) : (
          <>The task was declared blocked at {time}.</>
        )}{" "}
        It stays parked until the blocker is resolved.
      </>
    );
  }
  return (
    <>
      {actor ? (
        <>
          <span className={styles.actor}>{actor}</span> sent the task back for
          revision at {time}.
        </>
      ) : (
        <>Sent back for revision at {time}.</>
      )}{" "}
      Waiting for operator arbitration.
    </>
  );
}

export function OperatorQueueCard({
  item,
  agents,
  onApprove,
  onUnblock,
  onOpenIssue,
}: OperatorQueueCardProps): JSX.Element {
  const { issue, kind, waitingSince } = item;
  const { repos } = useWorkspaceContext();
  const sourceRepo = issue.source_repo?.trim() || undefined;
  const repoLabel = repoNameForSource(repos, sourceRepo);
  const routingAgents = useMemo(
    () =>
      sourceRepo ? agents.filter((agent) => agent.repo === sourceRepo) : [],
    [agents, sourceRepo],
  );
  const defaultAgentName = useMemo(
    () => pickDefaultAgentName(agents, sourceRepo),
    [agents, sourceRepo],
  );
  const [selectedAgentName, setSelectedAgentName] = useState(defaultAgentName);
  const [isActing, setIsActing] = useState(false);
  const age = useElapsedTime(
    Number.isFinite(waitingSince) ? waitingSince : null,
  );

  useEffect(() => {
    setSelectedAgentName((current) =>
      current && routingAgents.some((agent) => agent.name === current)
        ? current
        : defaultAgentName,
    );
  }, [routingAgents, defaultAgentName]);

  const runAction = async (action: () => Promise<void>): Promise<void> => {
    setIsActing(true);
    try {
      await action();
    } finally {
      setIsActing(false);
    }
  };

  const status = issue.status ?? "open";
  const labels = issue.labels ?? [];
  const carriedAssignee = agents.find((agent) => agent.name === issue.assignee);
  const assigneeServesOtherRepo = Boolean(
    sourceRepo && carriedAssignee && carriedAssignee.repo !== sourceRepo,
  );
  const carriedAssigneeRepo = repoNameForSource(repos, carriedAssignee?.repo);

  return (
    <article
      className={styles.card}
      data-testid="queue-card"
      data-kind={kind}
      data-issue-id={issue.id}
    >
      <div className={styles.topRow}>
        <span className={styles.kind}>{KIND_LABEL[kind]}</span>
        <span
          className={styles.waited}
          title={`Waiting since last update: ${issue.updated_at}`}
        >
          waiting ~{age || "unknown"}
        </span>
        <span className={styles.issueId}>{issue.id}</span>
        <span
          className={styles.repoChip}
          data-no-repo={!sourceRepo || undefined}
          data-testid="queue-repo"
          title={
            sourceRepo
              ? undefined
              : "this task has no source_repo; no agent will claim it"
          }
        >
          {repoLabel}
        </span>
      </div>

      <div className={styles.body}>
        <h3 className={styles.title}>{issue.title}</h3>
        <p className={styles.summary}>{actorSentence(item)}</p>

        <div className={styles.metaRow}>
          <span className={styles.statusPill} data-status={status}>
            {status.replace(/_/g, " ")}
          </span>
          {labels.map((label) => (
            <span className={styles.labelPill} key={label}>
              {label}
            </span>
          ))}
          {hasDesign(issue) && (
            <span className={styles.metaPill}>design attached</span>
          )}
        </div>

        {kind === "blocked" && issue.notes && (
          <blockquote className={styles.blockedNote}>
            “{stripBlockedPrefix(issue.notes)}”
          </blockquote>
        )}
      </div>

      <footer className={styles.footer}>
        {kind === "design-gate" && (
          <p className={styles.writeNote}>
            Approve is one atomic write: <code>reopen</code> ·{" "}
            <code>-label {NEEDS_REVISION_LABEL}</code>
            {selectedAgentName ? (
              <>
                {" "}
                · <code>assignee = {selectedAgentName}</code>
              </>
            ) : (
              <>
                .{" "}
                <span data-testid="queue-no-agent-for-repo">
                  No agent serves {repoLabel}, so this is not routed.
                </span>
              </>
            )}
            {selectedAgentName && "."} Nothing else moves until then.
          </p>
        )}
        {kind === "blocked" && (
          <p className={styles.writeNote}>
            Unblock is one write: <code>reopen</code>
            {issue.assignee ? (
              <>
                {" "}
                · <code>assignee = {issue.assignee}</code> — the agent resumes
                from the ready queue.
                {assigneeServesOtherRepo && (
                  <>
                    {" "}
                    — {issue.assignee} serves {carriedAssigneeRepo}, so it will
                    not resume this task.
                  </>
                )}
              </>
            ) : (
              <> — no agent held it.</>
            )}
          </p>
        )}

        <div className={styles.actions}>
          {kind === "design-gate" && (
            <>
              <div className={styles.splitControl}>
                <button
                  type="button"
                  className={
                    selectedAgentName
                      ? styles.primaryButton
                      : styles.secondaryButton
                  }
                  data-testid="queue-approve"
                  data-routed={Boolean(selectedAgentName)}
                  disabled={isActing}
                  title={
                    selectedAgentName
                      ? undefined
                      : `No agent serves ${repoLabel}; the task returns to the backlog unrouted`
                  }
                  onClick={() =>
                    void runAction(() => onApprove(issue, selectedAgentName))
                  }
                >
                  {selectedAgentName
                    ? `Approve → ${selectedAgentName}`
                    : `Approve without routing — no agent serves ${repoLabel}`}
                </button>
                {routingAgents.length > 0 && (
                  <label className={styles.pickerControl}>
                    <span aria-hidden="true">▾</span>
                    <select
                      value={selectedAgentName ?? ""}
                      data-testid="queue-agent-picker"
                      aria-label={`Route ${issue.id} to agent`}
                      disabled={isActing}
                      onChange={(event) =>
                        setSelectedAgentName(event.target.value)
                      }
                    >
                      {routingAgents.map((agent) => (
                        <option value={agent.name} key={agent.name}>
                          {agent.name}
                        </option>
                      ))}
                    </select>
                  </label>
                )}
              </div>
              <button
                type="button"
                className={styles.secondaryButton}
                onClick={() => onOpenIssue(issue)}
              >
                Read design
              </button>
              <button
                type="button"
                className={styles.ghostButton}
                title="coming later"
                disabled
              >
                Send back with note
              </button>
            </>
          )}

          {kind === "blocked" && (
            <>
              <button
                type="button"
                className={styles.primaryButton}
                data-testid="queue-unblock"
                disabled={isActing}
                onClick={() => void runAction(() => onUnblock(issue))}
              >
                Unblock
              </button>
              <button
                type="button"
                className={styles.secondaryButton}
                onClick={() => onOpenIssue(issue)}
              >
                Open issue
              </button>
            </>
          )}

          {kind === "needs-revision" && (
            <button
              type="button"
              className={styles.primaryButton}
              onClick={() => onOpenIssue(issue)}
            >
              Review task
            </button>
          )}
        </div>
      </footer>
    </article>
  );
}
