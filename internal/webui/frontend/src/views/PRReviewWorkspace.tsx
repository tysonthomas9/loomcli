/**
 * PRReviewWorkspace — the design's full-screen PR Review Workspace
 * (review-ws), GitHub-PR-review style: the file diff is the focus, with a
 * compact identity header (state · branch · resolves), an Approve /
 * Request-changes decision bar, and a review-agent control that can assign
 * an existing agent or create a brand-new PR review agent.
 *
 * Fully data-backed:
 *   - Diff: the review agent's real branch diff (PRFilesTab → /agents/{name}/diff/*)
 *   - Decisions: one idempotent server-coordinated review operation
 *   - New review agent: real createWorkspaceAgent → assign → startAgent
 */

import { useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";

import { applyReviewDecision, updateIssue } from "@/api";
import { startAgent } from "@/hooks/api";
import { CreateAgentModal } from "@/components/CreateAgentModal/CreateAgentModal";
import {
  buildWorkerByTaskId,
  isWorkerTerminalOpenable,
} from "@/components/AgentWorkPanel/AgentWorkPanel";
import { PRFilesTab } from "@/components/IssueDetailPanel";
import { TaskSessionDiffPane } from "@/components/IssueDetailPanel/sessions/TaskSessionDiffPane";
import {
  useWorkspaceViewData,
  useWorkspaceViewActions,
} from "@/contexts/WorkspaceViewContext";
import { useWorkspaceContext } from "@/hooks/workspace";
import type { GitPullRequest } from "@/api/workspace";
import type { Issue, LoomAgentStatus } from "@/types";
import { parseLoomStatus } from "@/types";
import { isPRUrl } from "@/utils/issue";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";

import styles from "./PRReviewWorkspace.module.css";

export interface PRReviewWorkspaceProps {
  issue: Issue;
  /** GitHub metadata when the PR list was loaded. */
  pullRequest?: GitPullRequest | undefined;
  /** Return to the PR list. */
  onBack: () => void;
}

/** Agents linked to a task issue (worker, status/task_id match, assignee). */
export function agentsLinkedToIssue(
  issue: Issue,
  agents: LoomAgentStatus[],
): LoomAgentStatus[] {
  const byName = new Map<string, LoomAgentStatus>();
  const worker = buildWorkerByTaskId(agents).get(issue.id);
  if (worker) byName.set(worker.name, worker);

  for (const agent of agents) {
    const taskId =
      agent.task_id || parseLoomStatus(agent.status ?? "").taskId || "";
    if (taskId === issue.id) {
      byName.set(agent.name, agent);
    }
  }

  if (issue.assignee) {
    const assignee = agents.find((a) => a.name === issue.assignee);
    if (assignee) byName.set(assignee.name, assignee);
  }

  return [...byName.values()];
}

/** Prefer a branch with commits; fall back to any linked worker/assignee. */
export function resolveDiffAgentForIssue(
  issue: Issue,
  agents: LoomAgentStatus[],
): LoomAgentStatus | undefined {
  const linked = agentsLinkedToIssue(issue, agents);
  if (linked.length === 0) return undefined;

  const withAhead = linked
    .filter((a) => (a.ahead ?? 0) > 0)
    .sort((a, b) => (b.ahead ?? 0) - (a.ahead ?? 0));
  if (withAhead.length > 0) return withAhead[0];

  const openable = linked.find((a) => isWorkerTerminalOpenable(a));
  if (openable) return openable;

  return linked[0];
}

export function PRReviewWorkspace({
  issue,
  pullRequest,
  onBack,
}: PRReviewWorkspaceProps): JSX.Element {
  const navigate = useNavigate();
  const { agents, issues, workspaceId } = useWorkspaceViewData();
  const { refetch, showToast, handleIssueClick } = useWorkspaceViewActions();
  const { repos } = useWorkspaceContext();

  const [agentMenuOpen, setAgentMenuOpen] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);
  const [isAssigning, setIsAssigning] = useState(false);
  const [isDeciding, setIsDeciding] = useState(false);
  const decisionIntentRef = useRef<{ fingerprint: string; id: string } | null>(
    null,
  );

  const diffAgent = useMemo(
    () => resolveDiffAgentForIssue(issue, agents),
    [issue, agents],
  );

  const reviewer = useMemo(
    () =>
      issue.assignee
        ? agents.find((a) => a.name === issue.assignee)
        : undefined,
    [agents, issue.assignee],
  );

  // One-agent-one-job: agents already on a live task are offered disabled.
  const busyAgentTask = useMemo(() => {
    const m = new Map<string, string>();
    for (const t of issues) {
      if (
        t.issue_type !== "epic" &&
        (t.status ?? "open") !== "closed" &&
        t.assignee &&
        t.id !== issue.id &&
        !m.has(t.assignee)
      ) {
        m.set(t.assignee, t.id);
      }
    }
    return m;
  }, [issues, issue.id]);

  const prUrl =
    pullRequest?.url ||
    (isPRUrl(issue.external_ref) ? issue.external_ref : null);
  const prNumber =
    pullRequest?.number?.toString() ??
    prUrl?.match(/\/pulls?\/(\d+)/)?.[1] ??
    null;
  const displayTitle = pullRequest?.title || issue.title;
  const reviewStateLabel = (() => {
    if (pullRequest?.is_draft) return "Draft";
    if (pullRequest?.state === "MERGED") return "Merged";
    if (pullRequest?.review_decision === "CHANGES_REQUESTED") {
      return "Changes requested";
    }
    if (pullRequest?.review_decision === "APPROVED") return "Approved";
    return "Review";
  })();

  const assignReviewer = async (agentName: string): Promise<void> => {
    setAgentMenuOpen(false);
    setIsAssigning(true);
    try {
      // Keep the issue in review — the reviewer joins; the PR stays queued.
      await updateIssue(workspaceId, issue.id, { assignee: agentName });
      try {
        await startAgent(workspaceId, agentName, { taskId: issue.id });
      } catch {
        // The agent may not be startable right now (e.g. backend cold);
        // assignment itself succeeded, so just surface a soft note.
        showToast(`Assigned ${agentName} — agent start deferred`, {
          type: "warning",
        });
      }
      refetch();
      showToast(`${agentName} is reviewing ${issue.id}`);
    } catch (err) {
      showToast(
        err instanceof Error ? err.message : "Failed to assign reviewer",
        { type: "error" },
      );
    } finally {
      setIsAssigning(false);
    }
  };

  const decide = async (decision: "approve" | "changes"): Promise<void> => {
    setIsDeciding(true);
    try {
      const action = decision === "approve" ? "approve" : "request_changes";
      const reason =
        decision === "approve"
          ? "PR approved after code review"
          : "Changes requested from review workspace";
      const fingerprint = `${issue.id}:${action}:${reason}`;
      if (decisionIntentRef.current?.fingerprint !== fingerprint) {
        decisionIntentRef.current = {
          fingerprint,
          id: `review-${crypto.randomUUID()}`,
        };
      }
      await applyReviewDecision(
        workspaceId,
        issue.id,
        action,
        reason,
        decisionIntentRef.current.id,
      );
      if (decision === "approve") {
        showToast(`${issue.id} approved and closed`);
      } else {
        showToast(`${issue.id} sent back — changes requested`);
      }
      refetch();
      onBack();
    } catch (err) {
      showToast(
        err instanceof Error ? err.message : "Failed to record decision",
        { type: "error" },
      );
    } finally {
      setIsDeciding(false);
    }
  };

  const freeAgents = agents.filter((a) => !busyAgentTask.has(a.name));

  return (
    <div className={styles.workspace} data-testid="pr-review-workspace">
      {/* Identity header (design rw-identity / rw-meta) */}
      <header className={styles.head}>
        <div className={styles.identity}>
          <button
            type="button"
            className={styles.backButton}
            onClick={onBack}
            aria-label="Back to pull requests"
          >
            ←
          </button>
          <div className={styles.titleWrap}>
            <h1 className={styles.title}>{displayTitle}</h1>
            <p className={styles.meta}>
              <span className={styles.stateTag}>{reviewStateLabel}</span>
              <code className={styles.metaMono}>{issue.id}</code>
              {diffAgent?.branch && (
                <span className={styles.branch}>
                  <code className={styles.metaMono}>{diffAgent.branch}</code>
                </span>
              )}
              {prNumber && prUrl && (
                <a
                  className={styles.hostLink}
                  href={prUrl}
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  #{prNumber} ↗
                </a>
              )}
              <button
                type="button"
                className={styles.ticketLink}
                onClick={() => handleIssueClick(issue)}
              >
                Open ticket
              </button>
            </p>
          </div>
          <div className={styles.spacer} />

          {/* Review agent control: reviewer chip, or assign/create menu. */}
          {reviewer ? (
            <button
              type="button"
              className={styles.reviewerChip}
              onClick={() =>
                navigate(
                  `/ws/${encodeURIComponent(workspaceId)}/agents?agent=${encodeURIComponent(reviewer.name)}`,
                )
              }
              title={`Open ${reviewer.name}'s workspace`}
            >
              <span
                className={styles.reviewerAvatar}
                style={{
                  backgroundColor: getAvatarColor(reviewer.name),
                  color: shouldUseWhiteText(getAvatarColor(reviewer.name))
                    ? "#fff"
                    : "#171717",
                }}
              >
                {reviewer.name.charAt(0).toUpperCase()}
              </span>
              Reviewing: {reviewer.name} →
            </button>
          ) : (
            <div className={styles.agentControl}>
              <button
                type="button"
                className={styles.agentButton}
                disabled={isAssigning}
                aria-haspopup="menu"
                aria-expanded={agentMenuOpen}
                onClick={() => setAgentMenuOpen((v) => !v)}
                data-testid="review-agent-button"
              >
                {isAssigning ? "Assigning…" : "Review agent ▾"}
              </button>
              {agentMenuOpen && (
                <div className={styles.agentMenu} role="menu">
                  <div className={styles.agentMenuHead}>
                    ASSIGN A REVIEW AGENT
                  </div>
                  {agents.map((a) => {
                    const busyOn = busyAgentTask.get(a.name);
                    return (
                      <button
                        key={a.name}
                        type="button"
                        role="menuitem"
                        className={styles.agentOption}
                        disabled={Boolean(busyOn)}
                        title={
                          busyOn ? `Already on ${busyOn}` : `Assign ${a.name}`
                        }
                        onClick={() => void assignReviewer(a.name)}
                      >
                        {a.name}
                        {busyOn && (
                          <span className={styles.agentBusy}>on task</span>
                        )}
                      </button>
                    );
                  })}
                  {agents.length > 0 && freeAgents.length === 0 && (
                    <p className={styles.agentEmpty}>
                      All agents are busy — create a fresh one.
                    </p>
                  )}
                  <button
                    type="button"
                    role="menuitem"
                    className={styles.agentCreate}
                    onClick={() => {
                      setAgentMenuOpen(false);
                      setCreateOpen(true);
                    }}
                    data-testid="new-review-agent"
                  >
                    ＋ New review agent…
                  </button>
                </div>
              )}
            </div>
          )}

          {/* Decision bar (design rw-decision, adapted to real statuses). */}
          <button
            type="button"
            className={styles.changesButton}
            disabled={isDeciding}
            onClick={() => void decide("changes")}
          >
            ✗ Request changes
          </button>
          <button
            type="button"
            className={styles.approveButton}
            disabled={isDeciding}
            onClick={() => void decide("approve")}
          >
            ✓ Approve
          </button>
        </div>
      </header>

      {/* The focus: full-bleed file diff (design DiffPane). */}
      <div className={styles.diffArea}>
        {diffAgent ? (
          <PRFilesTab
            agent={diffAgent}
            isActive
            emptyFallback={
              <TaskSessionDiffPane
                taskId={issue.id}
                worktreeAgentName={diffAgent.name}
              />
            }
          />
        ) : (
          <TaskSessionDiffPane taskId={issue.id} />
        )}
      </div>

      {/* Real agent creation, seeded as a PR review agent; on success the
          new agent is assigned this PR and started on it. */}
      <CreateAgentModal
        isOpen={createOpen}
        workspaceId={workspaceId}
        repos={repos}
        defaultName={`review-${issue.id.toLowerCase()}`}
        defaultRoleName="task"
        onClose={() => setCreateOpen(false)}
        onSuccess={(agent) => {
          setCreateOpen(false);
          void assignReviewer(agent.name);
        }}
      />
    </div>
  );
}
