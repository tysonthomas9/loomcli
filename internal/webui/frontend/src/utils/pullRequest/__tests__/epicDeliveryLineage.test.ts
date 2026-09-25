import { describe, expect, it } from "vitest";

import type {
  DeliveryGroupMemberView,
  DeliveryGroupView,
} from "@/api/workspace/deliveryGroups";
import type { PullRequestReadinessView } from "@/api/workspace/pullRequests";

import {
  branchBaseLinks,
  crossRepoFrom,
  isExternalMember,
  prKeysMissingReadiness,
  prsEpicHref,
  prsGroupHref,
  worstGroupReadiness,
} from "../epicDeliveryLineage";

function readiness(
  prKey: string,
  overrides: Partial<PullRequestReadinessView> = {},
  refs?: { head: string; base: string },
): PullRequestReadinessView {
  return {
    pr_key: prKey,
    freshness: "fresh",
    age_seconds: 5,
    current_verdict: "ready",
    current_reasons: [],
    ...(refs
      ? {
          snapshot: {
            pr_key: prKey,
            head_sha: "h",
            head_ref: refs.head,
            base_ref: refs.base,
            base_sha: "b",
            observed_at: "2026-09-25T00:00:00Z",
            facts: {} as never,
            verdict: "ready",
            reasons: [],
            fingerprint: "fp",
          },
        }
      : {}),
    ...overrides,
  };
}

function member(
  repo: string,
  n: number,
  overrides: Partial<DeliveryGroupMemberView> = {},
): DeliveryGroupMemberView {
  return {
    pr_key: `github:${repo}#${n}`,
    repo_name: repo,
    pr_number: n,
    source: "loom_task",
    task_id: `TASK-${n}`,
    added_at: "2026-09-24T00:00:00Z",
    ...overrides,
  };
}

function group(members: DeliveryGroupMemberView[]): DeliveryGroupView {
  return {
    workspace_key: "WS",
    id: "dg_1",
    title: "Group",
    state: "active",
    revision: 1,
    members,
    last_op_id: "op",
    created_at: "2026-09-24T00:00:00Z",
    updated_at: "2026-09-24T00:00:00Z",
  };
}

const NONE = new Map<string, PullRequestReadinessView>();

describe("isExternalMember", () => {
  it("treats members without a task as external", () => {
    const noTask = member("a/x", 1);
    delete (noTask as { task_id?: string }).task_id;
    expect(isExternalMember(noTask)).toBe(true);
    expect(isExternalMember(member("a/x", 2, { task_id: "  " }))).toBe(true);
  });

  it("treats manual, native-stack, and identity-heal sources as external", () => {
    for (const source of [
      "manual",
      "native_stack_suggestion",
      "identity_heal",
    ] as const) {
      expect(isExternalMember(member("a/x", 1, { source }))).toBe(true);
    }
  });

  it("treats loom_task and lineage_adopt members with a task as Loom-owned", () => {
    expect(isExternalMember(member("a/x", 1))).toBe(false);
    expect(
      isExternalMember(member("a/x", 1, { source: "lineage_adopt" })),
    ).toBe(false);
  });
});

describe("crossRepoFrom", () => {
  it("marks only consecutive members in different repos", () => {
    const members = [member("a/db", 1), member("a/cli", 2), member("a/cli", 3)];
    expect(crossRepoFrom(members, 0)).toBeNull();
    expect(crossRepoFrom(members, 1)).toBe("a/db");
    expect(crossRepoFrom(members, 2)).toBeNull();
  });
});

describe("branchBaseLinks", () => {
  it("links same-repo base_ref to another member's head_ref without reordering", () => {
    // Delivery order puts the child first; ancestry must not renumber it.
    const child = member("a/cli", 2, {
      readiness: readiness(
        "github:a/cli#2",
        {},
        { head: "feat/b", base: "feat/a" },
      ),
    });
    const base = member("a/cli", 1, {
      readiness: readiness(
        "github:a/cli#1",
        {},
        { head: "feat/a", base: "main" },
      ),
    });
    const g = group([child, base]);
    const links = branchBaseLinks(g, NONE);
    expect(links).toEqual([
      {
        childIndex: 0,
        baseIndex: 1,
        repo: "a/cli",
        branch: "feat/a",
        lastKnown: false,
      },
    ]);
    expect(g.members.map((m) => m.pr_number)).toEqual([2, 1]);
  });

  it("ignores matching branch names across different repositories", () => {
    const g = group([
      member("a/db", 1, {
        readiness: readiness(
          "github:a/db#1",
          {},
          { head: "feat/a", base: "main" },
        ),
      }),
      member("a/cli", 2, {
        readiness: readiness(
          "github:a/cli#2",
          {},
          { head: "feat/b", base: "feat/a" },
        ),
      }),
    ]);
    expect(branchBaseLinks(g, NONE)).toEqual([]);
  });

  it("uses separately read readiness and flags non-fresh refs as last known", () => {
    const g = group([member("a/cli", 1), member("a/cli", 2)]);
    const extra = new Map([
      [
        "github:a/cli#1",
        readiness(
          "github:a/cli#1",
          { freshness: "stale" },
          { head: "feat/a", base: "main" },
        ),
      ],
      [
        "github:a/cli#2",
        readiness("github:a/cli#2", {}, { head: "feat/b", base: "feat/a" }),
      ],
    ]);
    const links = branchBaseLinks(g, extra);
    expect(links).toHaveLength(1);
    expect(links[0]).toMatchObject({
      childIndex: 1,
      baseIndex: 0,
      lastKnown: true,
    });
  });

  it("yields nothing when refs were never observed", () => {
    const g = group([member("a/cli", 1), member("a/cli", 2)]);
    expect(branchBaseLinks(g, NONE)).toEqual([]);
  });
});

describe("worstGroupReadiness", () => {
  it("never summarizes a group as Ready when any member is stale", () => {
    const g = group([
      member("a/cli", 1, { readiness: readiness("github:a/cli#1") }),
      member("a/cli", 2, {
        readiness: readiness("github:a/cli#2", {
          freshness: "stale",
          current_verdict: "unknown",
        }),
      }),
    ]);
    const worst = worstGroupReadiness(g, NONE);
    expect(worst?.key).toBe("stale");
    expect(worst?.label).not.toBe("Ready");
  });

  it("treats a member without readiness as not observed", () => {
    const g = group([
      member("a/cli", 1, { readiness: readiness("github:a/cli#1") }),
      member("a/cli", 2),
    ]);
    expect(worstGroupReadiness(g, NONE)?.label).toBe("Not observed");
  });

  it("returns null for a group with no members", () => {
    expect(worstGroupReadiness(group([]), NONE)).toBeNull();
  });
});

describe("prKeysMissingReadiness", () => {
  it("lists only undecorated members not already read", () => {
    const g = group([
      member("a/cli", 1, { readiness: readiness("github:a/cli#1") }),
      member("a/cli", 2),
      member("a/cli", 3),
    ]);
    const known = new Map([["github:a/cli#3", readiness("github:a/cli#3")]]);
    expect(prKeysMissingReadiness([g], known)).toEqual(["github:a/cli#2"]);
  });
});

describe("deep links", () => {
  it("targets /prs with group and pr", () => {
    const href = prsGroupHref("STACKED PRS", "dg_1", "github:a/cli#2");
    const url = new URL(href, "http://x");
    expect(url.pathname).toBe("/ws/STACKED%20PRS/prs");
    expect(url.searchParams.get("group")).toBe("dg_1");
    expect(url.searchParams.get("pr")).toBe("github:a/cli#2");
  });

  it("targets /prs filtered to the epic", () => {
    const url = new URL(prsEpicHref("WS", "EPIC-1"), "http://x");
    expect(url.pathname).toBe("/ws/WS/prs");
    expect(url.searchParams.get("epic")).toBe("EPIC-1");
  });
});
