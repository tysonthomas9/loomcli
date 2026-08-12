import { useEffect, useMemo, useState } from "react";

import { getIssue } from "@/api";
import { ApiError } from "@/types";
import type { Issue } from "@/types";

export const ACTIVE_ISSUE_LOOKUP_RETRY_MS = 5_000;
export const ACTIVE_ISSUE_LOOKUP_TIMEOUT_MS = 10_000;

export interface ActiveIssueLookup {
  status: "found" | "missing" | "error";
  issue?: Issue;
}

export interface UseActiveIssueLookupsReturn {
  results: Map<string, ActiveIssueLookup>;
}

interface LookupSnapshot {
  key: string;
  results: Map<string, ActiveIssueLookup>;
}

const EMPTY_LOOKUPS = new Map<string, ActiveIssueLookup>();

/**
 * Resolve active issue IDs that are absent from a capped or type-filtered list.
 *
 * A 404 is the only authoritative "missing" result. Transport failures stay
 * unknown to callers and are retried while the ID remains active. Missing IDs
 * are also revalidated so transient read-model lag cannot disable a live task
 * for the rest of its run.
 */
export function useActiveIssueLookups(
  workspaceId: string,
  issueIDs: string[],
): UseActiveIssueLookupsReturn {
  const issueIDsKey = useMemo(
    () => [...issueIDs].sort().join("\u0000"),
    [issueIDs],
  );
  const requestKey = `${workspaceId}\u0001${issueIDsKey}`;
  const [snapshot, setSnapshot] = useState<LookupSnapshot>({
    key: "",
    results: EMPTY_LOOKUPS,
  });

  useEffect(() => {
    let canceled = false;
    const retryTimers = new Map<string, ReturnType<typeof setTimeout>>();
    const requestTimeouts = new Map<string, ReturnType<typeof setTimeout>>();
    const controllers = new Map<string, AbortController>();
    const ids = issueIDsKey ? issueIDsKey.split("\u0000") : [];

    if (!workspaceId || ids.length === 0) {
      setSnapshot({ key: requestKey, results: EMPTY_LOOKUPS });
      return () => {
        canceled = true;
      };
    }

    // Current key + no result means lookup pending. Callers keep the row
    // visible but non-clickable until a direct result arrives.
    setSnapshot({ key: requestKey, results: EMPTY_LOOKUPS });

    const lookupOne = async (issueID: string): Promise<void> => {
      const controller = new AbortController();
      controllers.set(issueID, controller);
      requestTimeouts.set(
        issueID,
        setTimeout(() => controller.abort(), ACTIVE_ISSUE_LOOKUP_TIMEOUT_MS),
      );

      let result: ActiveIssueLookup;
      try {
        const issue = await getIssue(workspaceId, issueID, {
          signal: controller.signal,
        });
        // IssueDetails intentionally widens dependency metadata, while this UI
        // only reads the shared identity/title/status/parent fields.
        result = { status: "found", issue: issue as unknown as Issue };
      } catch (err) {
        result =
          err instanceof ApiError && err.status === 404
            ? { status: "missing" }
            : { status: "error" };
      } finally {
        const timeout = requestTimeouts.get(issueID);
        if (timeout !== undefined) clearTimeout(timeout);
        requestTimeouts.delete(issueID);
        if (controllers.get(issueID) === controller) {
          controllers.delete(issueID);
        }
      }

      if (canceled) return;
      setSnapshot((current) => {
        if (current.key !== requestKey) return current;
        const results = new Map(current.results);
        results.set(issueID, result);
        return { key: requestKey, results };
      });

      if (result.status !== "found") {
        retryTimers.set(
          issueID,
          setTimeout(
            () => void lookupOne(issueID),
            ACTIVE_ISSUE_LOOKUP_RETRY_MS,
          ),
        );
      }
    };

    for (const issueID of ids) {
      void lookupOne(issueID);
    }

    return () => {
      canceled = true;
      for (const timer of retryTimers.values()) clearTimeout(timer);
      for (const timer of requestTimeouts.values()) clearTimeout(timer);
      for (const controller of controllers.values()) controller.abort();
      retryTimers.clear();
      requestTimeouts.clear();
      controllers.clear();
    };
  }, [issueIDsKey, requestKey, workspaceId]);

  return {
    results: snapshot.key === requestKey ? snapshot.results : EMPTY_LOOKUPS,
  };
}
