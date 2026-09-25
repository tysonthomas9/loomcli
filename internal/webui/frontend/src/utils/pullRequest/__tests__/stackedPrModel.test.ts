import { describe, expect, it } from "vitest";

import type { GitPullRequest } from "@/api/workspace/pullRequests";
import type { DeliveryGroupView } from "@/api/workspace/deliveryGroups";
import {
  buildHistoryEntries,
  buildWorkspaceItems,
  computeTabCounts,
  matchDeliveryGroup,
  matchStandalone,
  type StandalonePRItem,
  type DeliveryGroupItem,
} from "../stackedPrModel";

function pr(overrides: Partial<GitPullRequest> & { number: number }): GitPullRequest {
  return {
    title: "PR",
    url: `https://github.com/org/repo/pull/${overrides.number}`,
    state: "OPEN",
    is_draft: false,
    head_ref_name: "feat",
    base_ref_name: "main",
    repo_name: "org/repo",
    source_repo: "repo",
    pr_key: `github:org/repo#${overrides.number}`,
    ...overrides,
  };
}

function group(members: string[]): DeliveryGroupView {
  return {
    workspace_key: "WS",
    id: "dg_01HABCDEFGHJKLMNPQRSTUVWXY",
    title: "Cross-repo delivery",
    epic_id: "EPIC-1",
    state: "active",
    revision: 3,
    members: members.map((pr_key, i) => ({
      pr_key,
      repo_name: pr_key.includes("fleet") ? "fleet-db" : "loomcli",
      pr_number: i + 1,
      source: "manual",
      added_at: "2026-09-24T00:00:00Z",
    })),
    last_op_id: "op1",
    created_at: "2026-09-24T00:00:00Z",
    updated_at: "2026-09-24T00:00:00Z",
  };
}

describe("matchDeliveryGroup repo filter", () => {
  it("keeps the group and dims out-of-filter members", () => {
    const item: DeliveryGroupItem = {
      kind: "group",
      group: group(["github:o/loomcli#1", "github:o/fleet-db#2"]),
    };
    const match = matchDeliveryGroup(
      item,
      {
        tab: "all",
        query: "",
        repos: new Set(["loomcli"]),
        epics: new Set(),
        kinds: new Set(["group"]),
        mine: false,
      },
      new Map(),
    );
    expect(match.matches).toBe(true);
    expect(match.memberDimmed.get("github:o/loomcli#1")).toBe(false);
    expect(match.memberDimmed.get("github:o/fleet-db#2")).toBe(true);
  });
});

describe("matchStandalone", () => {
  it("hides standalone outside selected repos", () => {
    const item: StandalonePRItem = {
      kind: "standalone",
      prKey: "github:org/repo#9",
      pr: pr({ number: 9, source_repo: "other" }),
    };
    const match = matchStandalone(
      item,
      {
        tab: "all",
        query: "",
        repos: new Set(["repo"]),
        epics: new Set(),
        kinds: new Set(["standalone"]),
        mine: false,
      },
      new Map(),
    );
    expect(match.matches).toBe(false);
  });

  it("supports mine filter via author", () => {
    const item: StandalonePRItem = {
      kind: "standalone",
      prKey: "github:org/repo#9",
      pr: pr({ number: 9, author_login: "nova" }),
    };
    expect(
      matchStandalone(
        item,
        {
          tab: "all",
          query: "",
          repos: new Set(),
          epics: new Set(),
          kinds: new Set(["standalone"]),
          mine: true,
          mineIdentity: "nova",
        },
        new Map(),
      ).matches,
    ).toBe(true);
    expect(
      matchStandalone(
        item,
        {
          tab: "all",
          query: "",
          repos: new Set(),
          epics: new Set(),
          kinds: new Set(["standalone"]),
          mine: true,
          mineIdentity: "other",
        },
        new Map(),
      ).matches,
    ).toBe(false);
  });
});

describe("computeTabCounts", () => {
  it("keeps tab counts consistent with filters", () => {
    const items = [
      {
        kind: "standalone" as const,
        prKey: "github:org/repo#1",
        pr: pr({ number: 1, state: "MERGED" }),
      },
      {
        kind: "standalone" as const,
        prKey: "github:org/repo#2",
        pr: pr({ number: 2 }),
      },
    ];
    const counts = computeTabCounts(
      items,
      {
        query: "",
        repos: new Set(),
        epics: new Set(),
        kinds: new Set(["standalone"]),
        mine: false,
      },
      new Map(),
    );
    expect(counts.merged).toBe(1);
    expect(counts.all).toBe(2);
  });
});

describe("buildWorkspaceItems", () => {
  it("marks standalone unverified when membership is incomplete", () => {
    const items = buildWorkspaceItems({
      deliveryGroups: [],
      pullRequests: [pr({ number: 3 })],
      membershipComplete: false,
    });
    expect(items).toHaveLength(1);
    expect(items[0]).toMatchObject({
      kind: "standalone",
      membershipUnverified: true,
    });
  });

  it("keeps groups and standalone separate", () => {
    const items = buildWorkspaceItems({
      deliveryGroups: [group(["github:o/loomcli#1"])],
      pullRequests: [pr({ number: 9, pr_key: "github:org/repo#9" })],
      membershipComplete: true,
    });
    expect(items.filter((i) => i.kind === "group")).toHaveLength(1);
    expect(items.filter((i) => i.kind === "standalone")).toHaveLength(1);
  });
});

describe("buildHistoryEntries", () => {
  it("includes group membership and merged standalone events", () => {
    const entries = buildHistoryEntries({
      deliveryGroups: [group(["github:o/loomcli#1"])],
      standalone: [
        {
          kind: "standalone",
          prKey: "github:org/repo#9",
          pr: pr({
            number: 9,
            state: "MERGED",
            updated_at: "2026-09-24T12:00:00Z",
          }),
        },
      ],
    });
    expect(entries.some((e) => e.text.includes("Created delivery group"))).toBe(
      true,
    );
    expect(entries.some((e) => e.text.includes("observed merged"))).toBe(true);
  });
});
