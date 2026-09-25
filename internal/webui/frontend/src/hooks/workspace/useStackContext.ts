/**
 * Resolve compact delivery-group stack context for PR review / task detail
 * (STACKED-PRS-11). Owns by-pr fetch + 404 continuation branching so UI
 * components stay free of direct API imports.
 */

import { useEffect, useState } from "react";

import { ApiError } from "@/api/common";
import {
  getDeliveryGroupByPr,
  type DeliveryGroupView,
} from "@/api/workspace/deliveryGroups";
import { fetchPullRequests } from "@/api/workspace/pullRequests";
import { parsePrKey } from "@/utils/issue";

export type StackContextState =
  | { kind: "loading" }
  | { kind: "grouped"; group: DeliveryGroupView; index: number }
  | { kind: "standalone" }
  | { kind: "unverified" }
  | { kind: "unavailable" };

/**
 * Explicit external membership sources (aligned with epic delivery rollup):
 * manual / native_stack_suggestion / identity_heal, or any member lacking a
 * task_id. `lineage_adopt` with a task_id is internal — not External.
 */
const EXTERNAL_MEMBER_SOURCES = new Set([
  "manual",
  "native_stack_suggestion",
  "identity_heal",
]);

export function isExternalMember(member: {
  task_id?: string;
  source?: string;
}): boolean {
  if (!member.task_id) return true;
  return Boolean(member.source && EXTERNAL_MEMBER_SOURCES.has(member.source));
}

/** Match canonical (`github:…`) and legacy (`owner/repo#N`) PR key forms. */
function prKeysEqual(a: string, b: string): boolean {
  if (a === b) return true;
  const pa = parsePrKey(a);
  const pb = parsePrKey(b);
  if (!pa || !pb) return false;
  return (
    pa.owner === pb.owner && pa.repo === pb.repo && pa.number === pb.number
  );
}

/**
 * Resolve compact stack context via by-pr only. On 404, consult list
 * `standalone_continuation.complete` — never page all groups for membership.
 */
export async function resolveStackContext(
  workspaceId: string,
  prKey: string,
): Promise<StackContextState> {
  try {
    const write = await getDeliveryGroupByPr(workspaceId, prKey);
    const index = write.group.members.findIndex((m) =>
      prKeysEqual(m.pr_key, prKey),
    );
    // Never fall back to an arbitrary member when the 200 payload omits the
    // requested PR — that falsely displays another PR as "current".
    if (index < 0) {
      return { kind: "unavailable" };
    }
    return {
      kind: "grouped",
      group: write.group,
      index,
    };
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) {
      try {
        const list = await fetchPullRequests(workspaceId, {
          state: "all",
          deliveryGroupsLimit: 1,
        });
        // Membership completeness only — ignore deliveryGroups payloads here.
        if (list.standaloneContinuation?.complete === true) {
          return { kind: "standalone" };
        }
        return { kind: "unverified" };
      } catch {
        return { kind: "unverified" };
      }
    }
    return { kind: "unavailable" };
  }
}

/** Load stack context for a workspace PR key; resets to loading on key change. */
export function useStackContext(
  workspaceId: string,
  prKey: string,
): StackContextState {
  const [state, setState] = useState<StackContextState>({ kind: "loading" });

  useEffect(() => {
    let ignore = false;
    setState({ kind: "loading" });
    void resolveStackContext(workspaceId, prKey).then((next) => {
      if (!ignore) setState(next);
    });
    return () => {
      ignore = true;
    };
  }, [workspaceId, prKey]);

  return state;
}
