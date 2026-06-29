/**
 * Card agent view-model: the single, pure, semantic resolution of "what is the
 * agent state of this Kanban card?" — combining the two joins (live-claimant by
 * task id, assignee-named agent by name) and the card precedence (incl. review
 * suppression) in ONE place. AgentRow renders from the result as a total
 * function, so the prior three-identity prop conflation — and the stale
 * last_error_class leak it enabled — become unrepresentable.
 *
 * This is column/card-aware (presentation-domain), so it lives next to IssueCard
 * rather than under types/agent. It stays pure (no React, no DOM) and is
 * unit-tested directly. It imports only agent-domain types/selectors from
 * @/types — nothing from other components or color/util modules.
 */

import type { LoomAgentStatus, ParsedLoomStatus } from "@/types";
import {
  effectiveAgentStatus,
  parseLoomStatus,
  resolveAgentByName,
  resolveAgentForTask,
} from "@/types";

const H_PREFIX = /^\[H\]\s*/;

function stripHumanPrefix(name: string): string {
  return name.replace(H_PREFIX, "");
}

function isHumanAssignee(assignee: string): boolean {
  return H_PREFIX.test(assignee);
}

function isAgentAssignee(assignee: string): boolean {
  if (!assignee || isHumanAssignee(assignee)) return false;
  // Synthetic workflow assignee — not a loom agent display name.
  if (assignee.startsWith("driver-run:")) return false;
  return true;
}

/** Footer avatar on Kanban cards: working agent vs human owner fallback. */
export type CardFooterBadge =
  | { kind: "agent"; name: string }
  | { kind: "owner"; name: string };

/**
 * Resolve the footer badge for an IssueCard.
 *
 * Precedence:
 * 1. Agent claiming this task (active_task_id / current_task_id join) — ONLY in
 *    the in_progress column. current_task_id is lock-derived and may be a stale
 *    lock, so done/review/blocked/backlog cards must not surface a live
 *    "Working" badge from it. Mirrors resolveCardAgent's column gating.
 * 2. Non-human assignee (agent name on the issue)
 * 3. Human owner, then human assignee
 */
export function resolveCardFooterBadge(
  agents: readonly LoomAgentStatus[],
  issue: {
    id: string;
    assignee?: string | null | undefined;
    owner?: string | null | undefined;
  },
  columnId?: string,
): CardFooterBadge | null {
  if (columnId === "in_progress") {
    const claimant = resolveAgentForTask(agents, issue.id);
    if (claimant?.name) {
      return { kind: "agent", name: claimant.name };
    }
  }

  const assignee = issue.assignee?.trim();
  if (assignee && isAgentAssignee(assignee)) {
    return { kind: "agent", name: assignee };
  }

  const owner = issue.owner?.trim();
  if (owner) {
    return { kind: "owner", name: stripHumanPrefix(owner) };
  }

  if (assignee) {
    return { kind: "owner", name: stripHumanPrefix(assignee) };
  }

  return null;
}

/**
 * The fully-resolved agent state for one Kanban card. Discriminated on `kind`;
 * `displayName` is always the assignee (the product invariant: the card shows
 * who it's assigned to, even when a different agent does the work).
 */
export type CardAgentView =
  | {
      /**
       * An agent claims this task (matched by active_task_id or current_task_id).
       * "claimed", NOT "live": a current_task_id match is lock-derived and may be
       * a stale lock, so this kind asserts a claim, not lease-verified liveness.
       * The two claim sources are intentionally rendered identically — on the
       * daemon path a current-only claim is the normal working case.
       */
      kind: "claimed";
      displayName: string;
      /** Parsed status of the claimant (working/planning/… — drives dot + label). */
      status: ParsedLoomStatus;
      /**
       * Claimant's last PTY-observation timestamp. `null` = no observation yet
       * (AgentRow renders "awaiting activity"). Coalesced from the agent's
       * optional last_activity_at to match prior IssueCard behavior.
       */
      lastActivityAt: string | null;
    }
  | {
      /**
       * in_progress card with an assignee but NO resolved claimant (neither
       * active_task_id nor current_task_id matches) — orphaned. errorClass
       * explains WHY via the assignee-named agent's last failed run, when known.
       * This is the ONLY kind that surfaces an error, and it is unreachable while
       * a claimant exists — which is what kills the stale-error-over-live bug.
       */
      kind: "missing";
      displayName: string;
      errorClass: string | undefined;
    }
  | {
      /** Review column: liveness is intentionally suppressed (agent released at handoff). */
      kind: "review";
      displayName: string;
    }
  | {
      /** No agent row should render for this card. */
      kind: "none";
    };

/**
 * Resolve the single coherent agent view-model for a Kanban card.
 *
 * Precedence is decided HERE, so "claimed" and "missing" are mutually exclusive
 * by construction: a resolved claimant always wins over a stale assignee error.
 *
 * NOTE on errorClass freshness: fleet-db's last_error_class is the agent's
 * most-recent terminal session's class (agent-global, idle-only, cleared by a
 * later success) — NOT task-scoped. On an orphaned card the assignee was working
 * THIS task and is now idle/gone, so its last failure is very likely this task's,
 * but this is a best-effort hint ("the assigned agent's last run failed"), not a
 * proof of task ownership. Task-scoped precision would need a fleet-db addition.
 */
export function resolveCardAgent(
  agents: readonly LoomAgentStatus[],
  issue: { id: string; assignee?: string | null | undefined },
  columnId: string | undefined,
): CardAgentView {
  const assignee = issue.assignee;
  if ((columnId !== "in_progress" && columnId !== "review") || !assignee) {
    return { kind: "none" };
  }

  // displayName stays raw (with any [H] prefix); AgentRow strips it for display.
  const displayName = assignee;

  if (columnId === "review") {
    return { kind: "review", displayName };
  }

  // in_progress
  const claimant = resolveAgentForTask(agents, issue.id);
  if (claimant) {
    return {
      kind: "claimed",
      displayName,
      status: parseLoomStatus(effectiveAgentStatus(claimant)),
      lastActivityAt: claimant.last_activity_at ?? null,
    };
  }

  // No resolved claimant → orphaned. Explain why via the assignee-named agent
  // (strip [H] for the name match — a stalled agent isn't on the task).
  const named = resolveAgentByName(agents, assignee.replace(H_PREFIX, ""));
  return { kind: "missing", displayName, errorClass: named?.last_error_class };
}
