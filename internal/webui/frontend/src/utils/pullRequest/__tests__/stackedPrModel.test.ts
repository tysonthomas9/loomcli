import { describe, expect, it } from "vitest";

import type { GitPullRequest } from "@/api/workspace/pullRequests";
import type { DeliveryGroupView } from "@/api/workspace/deliveryGroups";
import type { PullRequestReadinessView } from "@/api/workspace/pullRequests";
import {
  buildHistoryEntries,
  buildWorkspaceItems,
  computeTabCounts,
  matchDeliveryGroup,
  matchStandalone,
  repoOptionsFromItems,
  statusKeyForItem,
  type StandalonePRItem,
  type DeliveryGroupItem,
  type WorkspaceItem,
} from "../stackedPrModel";

function pr(
  overrides: Partial<GitPullRequest> & { number: number },
): GitPullRequest {
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

function mergedReadiness(prKey: string): PullRequestReadinessView {
  return {
    pr_key: prKey,
    freshness: "fresh",
    age_seconds: 1,
    current_verdict: "merged",
    current_reasons: [],
  };
}

function group(
  members: Array<{ pr_key: string; repo_name: string }>,
): DeliveryGroupView {
  return {
    workspace_key: "WS",
    id: "dg_01HABCDEFGHJKLMNPQRSTUVWXY",
    title: "Cross-repo delivery",
    epic_id: "EPIC-1",
    state: "active",
    revision: 3,
    members: members.map((m, i) => ({
      pr_key: m.pr_key,
      repo_name: m.repo_name,
      pr_number: i + 1,
      source: "manual",
      added_at: "2026-09-24T00:00:00Z",
    })),
    last_op_id: "op1",
    created_at: "2026-09-24T00:00:00Z",
    updated_at: "2026-09-24T00:00:00Z",
  };
}

describe("statusKeyForItem group history", () => {
  it("classifies all-merged groups as merged (never Ready)", () => {
    const item: DeliveryGroupItem = {
      kind: "group",
      group: {
        ...group([
          { pr_key: "github:acme/loomcli#1", repo_name: "acme/loomcli" },
          { pr_key: "github:acme/fleet-db#2", repo_name: "acme/fleet-db" },
        ]),
        members: [
          {
            pr_key: "github:acme/loomcli#1",
            repo_name: "acme/loomcli",
            pr_number: 1,
            source: "manual",
            added_at: "2026-09-24T00:00:00Z",
            readiness: mergedReadiness("github:acme/loomcli#1"),
          },
          {
            pr_key: "github:acme/fleet-db#2",
            repo_name: "acme/fleet-db",
            pr_number: 2,
            source: "manual",
            added_at: "2026-09-24T00:00:00Z",
            readiness: mergedReadiness("github:acme/fleet-db#2"),
          },
        ],
      },
    };
    expect(statusKeyForItem(item, new Map())).toBe("merged");
    const counts = computeTabCounts(
      [item],
      {
        query: "",
        repos: new Set(),
        epics: new Set(),
        kinds: new Set(["group"]),
        mine: false,
      },
      new Map(),
    );
    expect(counts.ready).toBe(0);
    expect(counts.merged).toBe(1);
  });

  it("classifies empty groups as unknown (never Ready)", () => {
    const item: DeliveryGroupItem = {
      kind: "group",
      group: group([]),
    };
    expect(statusKeyForItem(item, new Map())).toBe("unknown");
    const counts = computeTabCounts(
      [item],
      {
        query: "",
        repos: new Set(),
        epics: new Set(),
        kinds: new Set(["group"]),
        mine: false,
      },
      new Map(),
    );
    expect(counts.ready).toBe(0);
    expect(counts.attention).toBe(1);
  });
});

describe("matchDeliveryGroup repo filter", () => {
  it("keeps the group and dims out-of-filter members using full owner/repo", () => {
    const item: DeliveryGroupItem = {
      kind: "group",
      group: group([
        { pr_key: "github:acme/loomcli#1", repo_name: "acme/loomcli" },
        { pr_key: "github:acme/fleet-db#2", repo_name: "acme/fleet-db" },
      ]),
    };
    const match = matchDeliveryGroup(
      item,
      {
        tab: "all",
        query: "",
        repos: new Set(["acme/loomcli"]),
        epics: new Set(),
        kinds: new Set(["group"]),
        mine: false,
      },
      new Map(),
    );
    expect(match.matches).toBe(true);
    expect(match.memberDimmed.get("github:acme/loomcli#1")).toBe(false);
    expect(match.memberDimmed.get("github:acme/fleet-db#2")).toBe(true);
  });

  it("does not collide distinct owners that share a basename", () => {
    const item: DeliveryGroupItem = {
      kind: "group",
      group: group([
        { pr_key: "github:acme/loomcli#1", repo_name: "acme/loomcli" },
        { pr_key: "github:other/loomcli#2", repo_name: "other/loomcli" },
      ]),
    };
    const matchAcme = matchDeliveryGroup(
      item,
      {
        tab: "all",
        query: "",
        repos: new Set(["acme/loomcli"]),
        epics: new Set(),
        kinds: new Set(["group"]),
        mine: false,
      },
      new Map(),
    );
    expect(matchAcme.matches).toBe(true);
    expect(matchAcme.memberDimmed.get("github:acme/loomcli#1")).toBe(false);
    expect(matchAcme.memberDimmed.get("github:other/loomcli#2")).toBe(true);

    // Short basename must not match either owner/repo identity.
    const matchShort = matchDeliveryGroup(
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
    expect(matchShort.matches).toBe(false);
  });
});

describe("repoOptionsFromItems", () => {
  it("counts full owner/repo identities separately when basenames collide", () => {
    const items: WorkspaceItem[] = [
      {
        kind: "standalone",
        prKey: "github:acme/loomcli#1",
        pr: pr({
          number: 1,
          repo_name: "acme/loomcli",
          source_repo: "loomcli",
          pr_key: "github:acme/loomcli#1",
        }),
      },
      {
        kind: "standalone",
        prKey: "github:other/loomcli#2",
        pr: pr({
          number: 2,
          repo_name: "other/loomcli",
          source_repo: "loomcli",
          pr_key: "github:other/loomcli#2",
          url: "https://github.com/other/loomcli/pull/2",
        }),
      },
    ];
    const options = repoOptionsFromItems(items, new Map());
    expect(options).toEqual([
      ["acme/loomcli", 1],
      ["other/loomcli", 1],
    ]);
  });
});

describe("matchStandalone", () => {
  it("hides standalone outside selected repos", () => {
    const item: StandalonePRItem = {
      kind: "standalone",
      prKey: "github:org/repo#9",
      pr: pr({ number: 9, source_repo: "other", repo_name: "org/other" }),
    };
    const match = matchStandalone(
      item,
      {
        tab: "all",
        query: "",
        repos: new Set(["org/repo"]),
        epics: new Set(),
        kinds: new Set(["standalone"]),
        mine: false,
      },
      new Map(),
    );
    expect(match.matches).toBe(false);
  });

  it("supports mine filter via verified GitHub author login", () => {
    const item: StandalonePRItem = {
      kind: "standalone",
      prKey: "github:org/repo#9",
      pr: pr({ number: 9, author_login: "tysonthomas9" }),
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
          githubLogin: "tysonthomas9",
          loomActor: "Tyson",
        },
        new Map(),
      ).matches,
    ).toBe(true);
    // Display name must never match GitHub author_login.
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
          githubLogin: null,
          loomActor: "Tyson",
        },
        new Map(),
      ).matches,
    ).toBe(false);
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
          githubLogin: "tyson",
          loomActor: null,
        },
        new Map(),
      ).matches,
    ).toBe(false);
  });

  it("excludes external authors when Mine is on", () => {
    const item: StandalonePRItem = {
      kind: "standalone",
      prKey: "github:org/repo#9",
      pr: pr({ number: 9, author_login: "dependabot" }),
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
          githubLogin: "tysonthomas9",
          loomActor: "t@example.com",
        },
        new Map(),
      ).matches,
    ).toBe(false);
  });

  it("matches Loom assignee without conflating display name as GitHub login", () => {
    const item: StandalonePRItem = {
      kind: "standalone",
      prKey: "github:org/repo#9",
      pr: pr({ number: 9, author_login: "dependabot" }),
      assignee: "t@example.com",
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
          githubLogin: null,
          loomActor: "t@example.com",
        },
        new Map(),
      ).matches,
    ).toBe(true);
  });

  it("matches delivery group via Loom owner separately from GitHub authors", () => {
    const item: DeliveryGroupItem = {
      kind: "group",
      group: {
        workspace_key: "WS",
        id: "dg1",
        title: "Group",
        state: "active",
        revision: 1,
        owner: "t@example.com",
        created_by: "other@example.com",
        members: [
          {
            pr_key: "github:org/repo#1",
            repo_name: "org/repo",
            pr_number: 1,
            source: "manual",
            added_at: "2026-09-24T00:00:00Z",
          },
        ],
        last_op_id: "op1",
        created_at: "2026-09-24T00:00:00Z",
        updated_at: "2026-09-24T00:00:00Z",
      },
    };
    const prByKey = new Map([
      ["github:org/repo#1", pr({ number: 1, author_login: "dependabot" })],
    ]);
    expect(
      matchDeliveryGroup(
        item,
        {
          tab: "all",
          query: "",
          repos: new Set(),
          epics: new Set(),
          kinds: new Set(["group"]),
          mine: true,
          githubLogin: "tysonthomas9",
          loomActor: "t@example.com",
        },
        new Map(),
        prByKey,
      ).matches,
    ).toBe(true);
    expect(
      matchDeliveryGroup(
        item,
        {
          tab: "all",
          query: "",
          repos: new Set(),
          epics: new Set(),
          kinds: new Set(["group"]),
          mine: true,
          githubLogin: null,
          loomActor: "Tyson",
        },
        new Map(),
        prByKey,
      ).matches,
    ).toBe(false);
  });

  it("supports open-mode author Mine when viewer is available", () => {
    const item: StandalonePRItem = {
      kind: "standalone",
      prKey: "github:org/repo#9",
      pr: pr({ number: 9, author_login: "tysonthomas9" }),
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
          githubLogin: "tysonthomas9",
          loomActor: null,
        },
        new Map(),
      ).matches,
    ).toBe(true);
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
      deliveryGroups: [
        group([{ pr_key: "github:o/loomcli#1", repo_name: "o/loomcli" }]),
      ],
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
      deliveryGroups: [
        group([{ pr_key: "github:o/loomcli#1", repo_name: "o/loomcli" }]),
      ],
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
