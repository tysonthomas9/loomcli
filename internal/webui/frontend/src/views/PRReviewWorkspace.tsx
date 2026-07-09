/**
 * PRReviewWorkspace — the design's full-screen PR Review Workspace
 * (review-ws), GitHub-PR-review style: the file diff is the focus, with a
 * compact identity header (state · branch · resolves), an Approve /
 * Request-changes decision bar, and a review-agent control that can assign
 * an existing agent or create a brand-new PR review agent.
 *
 * Fully data-backed:
 *   - Diff: the review agent's real branch diff (PRFilesTab → /agents/{name}/diff/*)
 *   - Decisions: real status transitions (closed / open)
 *   - New review agent: real createWorkspaceAgent → assign → startAgent
 */

import { useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";

import { createIssue, updateIssue } from "@/api";
import {
  getPullRequestDetail,
  postPullRequestReview,
} from "@/api/workspace/prReview";
import { startAgent } from "@/hooks/api";
import { CreateAgentModal } from "@/components/CreateAgentModal/CreateAgentModal";
import {
  buildWorkerByTaskId,
  isWorkerTerminalOpenable,
} from "@/components/AgentWorkPanel/AgentWorkPanel";
import { PRDiscussionPanel } from "@/components/PRDiscussionPanel";
import { PRCompareDiffPane, PRFilesTab } from "@/components/IssueDetailPanel";
import { TaskSessionDiffPane } from "@/components/IssueDetailPanel/sessions/TaskSessionDiffPane";
import {
  useWorkspaceViewData,
  useWorkspaceViewActions,
} from "@/contexts/WorkspaceViewContext";
import { useWorkspaceContext } from "@/hooks/workspace";
import type { GitPullRequest } from "@/api/workspace";
import type { Issue, LoomAgentStatus } from "@/types";
import { ApiError } from "@/types/common/errors";
import { parseLoomStatus } from "@/types";
import { isPRUrl } from "@/utils/issue";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";

import styles from "./PRReviewWorkspace.module.css";

export interface PRReviewWorkspaceProps {
  issue?: Issue;
  /** GitHub metadata when the PR list was loaded. */
  pullRequest?: GitPullRequest | undefined;
  /** Return to the PR list. */
  onBack: () => void;
  onLinkedTicket?: (issueId: string) => void;
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

interface PullRequestRepoRef {
  owner: string;
  repo: string;
}

function repoRefFromName(
  repoName: string | undefined,
): PullRequestRepoRef | null {
  const [owner, repo, extra] = repoName?.split("/") ?? [];
  if (!owner || !repo || extra) return null;
  return { owner, repo };
}

function repoRefFromUrl(url: string | undefined): PullRequestRepoRef | null {
  if (!url) return null;

  try {
    const parsed = new URL(url);
    const [owner, repo, marker] = parsed.pathname.split("/").filter(Boolean);
    if (owner && repo && marker === "pull") {
      return { owner, repo };
    }
  } catch {
    return null;
  }

  return null;
}

function resolvePullRequestRepo(
  pullRequest: GitPullRequest | undefined,
): PullRequestRepoRef | null {
  return (
    repoRefFromName(pullRequest?.repo_name) ?? repoRefFromUrl(pullRequest?.url)
  );
}

export function PRReviewWorkspace({
  issue,
  pullRequest,
  onBack,
  onLinkedTicket,
}: PRReviewWorkspaceProps): JSX.Element {
  const navigate = useNavigate();
  const { agents, issues } = useWorkspaceViewData();
  const { refetch, showToast, updateIssueStatus, handleIssueClick } =
    useWorkspaceViewActions();
  const { repos, workspaceId } = useWorkspaceContext();

  const [agentMenuOpen, setAgentMenuOpen] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);
  const [isAssigning, setIsAssigning] = useState(false);
  const [isDeciding, setIsDeciding] = useState(false);
  const [headSha, setHeadSha] = useState<string | null>(null);
  const [reviewComment, setReviewComment] = useState("");
  const [stale, setStale] = useState(false);
  const [creatingTicket, setCreatingTicket] = useState(false);
  const [discussOpen, setDiscussOpen] = useState(false);

  const diffAgent = useMemo(
    () => (issue ? resolveDiffAgentForIssue(issue, agents) : undefined),
    [issue, agents],
  );

  const reviewer = useMemo(
    () =>
      issue?.assignee
        ? agents.find((a) => a.name === issue.assignee)
        : undefined,
    [agents, issue?.assignee],
  );

  // One-agent-one-job: agents already on a live task are offered disabled.
  const busyAgentTask = useMemo(() => {
    const m = new Map<string, string>();
    for (const t of issues) {
      if (
        t.issue_type !== "epic" &&
        (t.status ?? "open") !== "closed" &&
        t.assignee &&
        t.id !== issue?.id &&
        !m.has(t.assignee)
      ) {
        m.set(t.assignee, t.id);
      }
    }
    return m;
  }, [issues, issue?.id]);

  const prUrl =
    pullRequest?.url ||
    (issue && isPRUrl(issue.external_ref) ? issue.external_ref : null);
  const prNumber =
    pullRequest?.number?.toString() ??
    prUrl?.match(/\/pulls?\/(\d+)/)?.[1] ??
    null;
  const pullRequestRepo = useMemo(
    () => resolvePullRequestRepo(pullRequest),
    [pullRequest],
  );
  const displayTitle = pullRequest?.title || issue?.title || "Pull request";
  const reviewStateLabel = (() => {
    if (pullRequest?.is_draft) return "Draft";
    if (pullRequest?.state === "MERGED") return "Merged";
    if (pullRequest?.review_decision === "CHANGES_REQUESTED") {
      return "Changes requested";
    }
    if (pullRequest?.review_decision === "APPROVED") return "Approved";
    return "Review";
  })();

  useEffect(() => {
    let ignore = false;
    const number = prNumber ? Number(prNumber) : NaN;
    if (!pullRequestRepo || !Number.isFinite(number)) {
      setHeadSha(null);
      return;
    }

    void (async () => {
      try {
        const detail = await getPullRequestDetail(
          workspaceId,
          pullRequestRepo.owner,
          pullRequestRepo.repo,
          number,
        );
        if (!ignore) setHeadSha(detail.head_sha);
      } catch {
        if (!ignore) setHeadSha(null);
      }
    })();

    return () => {
      ignore = true;
    };
  }, [workspaceId, pullRequestRepo, prNumber]);

  // Fetch the PR's authoritative head sha (the value the review precondition
  // compares against), commit it to state, and return it — so a decision can
  // fetch on demand when the mount-time effect hasn't resolved yet. Returns
  // null when the PR head can't be determined.
  const loadHeadSha = async (
    owner: string,
    repo: string,
    number: number,
  ): Promise<string | null> => {
    try {
      const detail = await getPullRequestDetail(
        workspaceId,
        owner,
        repo,
        number,
      );
      setHeadSha(detail.head_sha);
      return detail.head_sha;
    } catch {
      setHeadSha(null);
      return null;
    }
  };

  const assignReviewer = async (agentName: string): Promise<void> => {
    if (!issue) return;
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

  const applyLocalDecision = async (
    decision: "approve" | "changes",
  ): Promise<void> => {
    if (!issue) {
      showToast("No ticket to update", { type: "error" });
      return;
    }
    setIsDeciding(true);
    try {
      if (decision === "approve") {
        await updateIssueStatus(issue.id, "closed");
        showToast(`${issue.id} approved and closed`);
      } else {
        await updateIssueStatus(issue.id, "open");
        showToast(`${issue.id} sent back — changes requested`);
      }
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

  const decide = async (decision: "approve" | "changes"): Promise<void> => {
    const number = prNumber ? Number(prNumber) : NaN;
    // Only fall back to a local-only board flip when there is genuinely no PR
    // to review (issue-only tickets). A resolvable PR whose head sha simply
    // hasn't loaded must NOT silently flip the board without a GitHub review —
    // we fetch the sha on demand below and hard-fail if it can't be obtained.
    if (!pullRequestRepo || !Number.isFinite(number)) {
      if (issue) {
        await applyLocalDecision(decision);
      } else {
        showToast("No pull request to review", { type: "error" });
      }
      return;
    }

    const event = decision === "approve" ? "approve" : "request_changes";
    const body = reviewComment.trim();
    if (event === "request_changes" && body === "") {
      showToast("Add a comment to request changes", { type: "warning" });
      return;
    }

    setIsDeciding(true);
    try {
      const sha =
        headSha ??
        (await loadHeadSha(
          pullRequestRepo.owner,
          pullRequestRepo.repo,
          number,
        ));
      if (!sha) {
        showToast("Couldn't verify the PR's current head — try again.", {
          type: "error",
        });
        return;
      }
      await postPullRequestReview(
        workspaceId,
        pullRequestRepo.owner,
        pullRequestRepo.repo,
        number,
        {
          event,
          expected_head_sha: sha,
          ...(body ? { body } : {}),
        },
      );
      setStale(false);
      if (issue) {
        await updateIssueStatus(
          issue.id,
          decision === "approve" ? "closed" : "open",
        );
        showToast(
          decision === "approve"
            ? "Approved on GitHub — ticket closed"
            : "Changes requested on GitHub — ticket reopened",
        );
      } else {
        showToast(
          decision === "approve"
            ? "Approved on GitHub"
            : "Changes requested on GitHub",
        );
      }
      onBack();
    } catch (err) {
      if (
        err instanceof ApiError &&
        (err.status === 409 || err.status === 428)
      ) {
        setStale(true);
        await loadHeadSha(pullRequestRepo.owner, pullRequestRepo.repo, number);
        showToast(
          "The PR changed since you loaded it — refreshed. Review again.",
          {
            type: "warning",
          },
        );
      } else {
        showToast(
          err instanceof Error ? err.message : "Failed to record decision",
          { type: "error" },
        );
      }
    } finally {
      setIsDeciding(false);
    }
  };

  const createTicket = async (): Promise<void> => {
    if (!pullRequest) return;
    setCreatingTicket(true);
    try {
      const created = await createIssue(workspaceId, {
        title: pullRequest.title,
        external_ref: pullRequest.url,
        source_repo: pullRequest.repo_name,
        issue_type: "task",
        priority: 3,
      });
      try {
        await updateIssue(workspaceId, created.id, { status: "review" });
      } catch {
        // Non-fatal: the new ticket still exists and can be linked.
      }
      // Refetch before navigating so the freshly-created issue is present in
      // the issues list when the parent switches to ?review=<newId> (matches
      // App.handleCreateIssueSuccess). Without this the review gate misses the
      // not-yet-loaded issue and bounces back to the PR queue.
      await refetch();
      showToast(`Created ${created.id} for this pull request`);
      onLinkedTicket?.(created.id);
    } catch (err) {
      showToast(
        err instanceof Error ? err.message : "Failed to create ticket",
        {
          type: "error",
        },
      );
    } finally {
      setCreatingTicket(false);
    }
  };

  const canDiscussPR = Boolean(pullRequestRepo && prNumber);
  const freeAgents = agents.filter((a) => !busyAgentTask.has(a.name));
  const showDiscussion =
    discussOpen &&
    pullRequestRepo &&
    prNumber &&
    Number.isFinite(Number(prNumber));

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
              {issue && <code className={styles.metaMono}>{issue.id}</code>}
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
              {issue && (
                <button
                  type="button"
                  className={styles.ticketLink}
                  onClick={() => handleIssueClick(issue)}
                >
                  Open ticket
                </button>
              )}
            </p>
          </div>
          <div className={styles.spacer} />

          {/* Review agent control: reviewer chip, or assign/create menu. */}
          {issue ? (
            reviewer ? (
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
            )
          ) : (
            <button
              type="button"
              className={styles.agentButton}
              data-testid="pr-create-ticket"
              disabled={creatingTicket}
              onClick={() => void createTicket()}
            >
              {creatingTicket ? "Creating ticket…" : "＋ Create ticket"}
            </button>
          )}

          {/* Conversational reviewer: start a codex agent checked out at the PR
              head and open its terminal. Available for any resolvable PR. */}
          {canDiscussPR && (
            <button
              type="button"
              className={styles.agentButton}
              data-testid="pr-discuss-button"
              onClick={() => setDiscussOpen((v) => !v)}
              title="Open a PR-aware review discussion"
            >
              Discuss PR
            </button>
          )}

          {/* Decision bar (design rw-decision, adapted to real statuses). */}
          <div className={styles.decisionBar}>
            {stale && (
              <div
                className={styles.staleBanner}
                data-testid="pr-review-stale-banner"
              >
                <span>
                  This PR was updated after you opened it. The diff/head were
                  refreshed — submit your review again.
                </span>
                <button type="button" onClick={() => setStale(false)}>
                  Dismiss
                </button>
              </div>
            )}
            <textarea
              className={styles.reviewComment}
              data-testid="pr-review-comment"
              value={reviewComment}
              onChange={(event) => setReviewComment(event.target.value)}
              placeholder="Add a review comment (required to request changes)"
            />
            <div className={styles.decisionActions}>
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
          </div>
        </div>
      </header>

      {/* The focus: full-bleed file diff (design DiffPane). */}
      <div
        className={
          showDiscussion
            ? `${styles.diffArea} ${styles.diffAreaSplit}`
            : styles.diffArea
        }
      >
        <div className={styles.diffPane}>
          {diffAgent && issue ? (
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
          ) : pullRequest && pullRequestRepo ? (
            <PRCompareDiffPane
              workspaceId={workspaceId}
              owner={pullRequestRepo.owner}
              repo={pullRequestRepo.repo}
              number={pullRequest.number}
            />
          ) : issue ? (
            <TaskSessionDiffPane taskId={issue.id} />
          ) : (
            <div className={styles.diffEmpty}>
              No diff available for this pull request.
            </div>
          )}
        </div>
        {showDiscussion && (
          <PRDiscussionPanel
            workspaceId={workspaceId}
            owner={pullRequestRepo.owner}
            repo={pullRequestRepo.repo}
            number={Number(prNumber)}
            onClose={() => setDiscussOpen(false)}
          />
        )}
      </div>

      {/* Real agent creation, seeded as a PR review agent; on success the
          new agent is assigned this PR and started on it. */}
      {issue && (
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
      )}
    </div>
  );
}
