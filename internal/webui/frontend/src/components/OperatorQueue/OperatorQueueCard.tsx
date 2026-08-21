import { useEffect, useMemo, useState } from "react";

import { useElapsedTime } from "@/hooks/common";
import type { OperatorQueueItem } from "@/hooks/issues";
import type { Issue, LoomAgentStatus } from "@/types";
import { effectiveAgentStatus, parseLoomStatus } from "@/types/agent";
import { hasDesign, NEEDS_REVISION_LABEL } from "@/utils/issue";

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
): string | undefined {
  return agents.find(isIdleImplementationAgent)?.name ?? agents[0]?.name;
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
  const defaultAgentName = useMemo(
    () => pickDefaultAgentName(agents),
    [agents],
  );
  const [selectedAgentName, setSelectedAgentName] = useState(defaultAgentName);
  const [isActing, setIsActing] = useState(false);
  const age = useElapsedTime(
    Number.isFinite(waitingSince) ? waitingSince : null,
  );

  useEffect(() => {
    setSelectedAgentName((current) =>
      current && agents.some((agent) => agent.name === current)
        ? current
        : defaultAgentName,
    );
  }, [agents, defaultAgentName]);

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
              <>. No agent is available to route to</>
            )}
            . Nothing else moves until then.
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
                  className={styles.primaryButton}
                  data-testid="queue-approve"
                  disabled={isActing}
                  onClick={() =>
                    void runAction(() => onApprove(issue, selectedAgentName))
                  }
                >
                  {selectedAgentName
                    ? `Approve → ${selectedAgentName}`
                    : "Approve — no agent to route to"}
                </button>
                {agents.length > 0 && (
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
                      {agents.map((agent) => (
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
