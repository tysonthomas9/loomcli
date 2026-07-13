import { describe, expect, it } from "vitest";

import type { DiffFile } from "@/api/issues";
import type { FileCheckout, WorkspaceStack } from "@/api/workspace";
import { checkoutRefKey } from "@/utils/fileExplorerRefs";

import {
  buildBranchChangeGroups,
  buildChangeGroups,
  buildTaskChangeGroups,
  changeStatusFromDiffStatus,
  changeStatusFromPorcelain,
  checkoutRefFromCheckout,
} from "../changesLens";

function checkout(
  partial: Partial<FileCheckout> & Pick<FileCheckout, "kind" | "repo">,
): FileCheckout {
  return {
    kind: partial.kind,
    repo: partial.repo,
    agent: partial.agent,
    exists: partial.exists ?? true,
    change_count: partial.change_count ?? 1,
    status_error: partial.status_error,
  };
}

function diffFile(
  partial: Partial<DiffFile> & Pick<DiffFile, "path">,
): DiffFile {
  const file: DiffFile = {
    path: partial.path,
    status: partial.status ?? "M",
    additions: partial.additions ?? 0,
    deletions: partial.deletions ?? 0,
  };
  if (partial.old_path !== undefined) file.old_path = partial.old_path;
  return file;
}

describe("changesLens", () => {
  it.each([
    ["M ", "Modified", "modified"],
    ["MM", "Modified", "modified"],
    ["??", "New", "new"],
    ["A ", "New", "new"],
    [" D", "Deleted", "deleted"],
    ["R ", "Renamed", "renamed"],
  ])("maps porcelain %s to a friendly %s chip", (xy, label, kind) => {
    expect(changeStatusFromPorcelain(xy)).toEqual({ label, kind });
  });

  it.each([
    ["M", "Modified", "modified"],
    ["A", "New", "new"],
    ["D", "Deleted", "deleted"],
    ["R", "Renamed", "renamed"],
  ] as const)(
    "maps diff status %s to a friendly %s chip",
    (xy, label, kind) => {
      expect(changeStatusFromDiffStatus(xy)).toEqual({ label, kind });
    },
  );

  it("orders agent groups before shared repos and omits zero-count checkouts", () => {
    const checkouts: FileCheckout[] = [
      checkout({ kind: "repo", repo: "shared-b", change_count: 1 }),
      checkout({
        kind: "agent",
        agent: "zoe",
        repo: "repo-a",
        change_count: 1,
      }),
      checkout({
        kind: "agent",
        agent: "atlas",
        repo: "repo-b",
        change_count: 0,
      }),
      checkout({
        kind: "agent",
        agent: "atlas",
        repo: "repo-a",
        change_count: 2,
      }),
      checkout({ kind: "repo", repo: "shared-a", change_count: 1 }),
    ];
    const statuses = Object.fromEntries(
      checkouts.map((item) => [
        checkoutRefKey(checkoutRefFromCheckout(item)),
        { "src/main.ts": " M" },
      ]),
    );

    expect(
      buildChangeGroups(checkouts, statuses).map((group) => group.label),
    ).toEqual([
      "atlas · repo-a · 2",
      "zoe · repo-a · 1",
      "shared-a · shared checkout · 1",
      "shared-b · shared checkout · 1",
    ]);
  });

  it("omits unavailable and missing checkouts", () => {
    const checkouts: FileCheckout[] = [
      checkout({ kind: "repo", repo: "healthy", change_count: 1 }),
      checkout({
        kind: "agent",
        agent: "local-coder",
        repo: "broken",
        change_count: 4,
        status_error: true,
      }),
      checkout({
        kind: "repo",
        repo: "missing",
        exists: false,
        change_count: 3,
      }),
    ];
    const statuses = Object.fromEntries(
      checkouts.map((item) => [
        checkoutRefKey(checkoutRefFromCheckout(item)),
        { "src/main.ts": " M" },
      ]),
    );

    expect(buildChangeGroups(checkouts, statuses)).toHaveLength(1);
    expect(buildChangeGroups(checkouts, statuses)[0]?.label).toBe(
      "healthy · shared checkout · 1",
    );
  });

  it("builds branch groups for available agent checkouts only", () => {
    const checkouts: FileCheckout[] = [
      checkout({ kind: "repo", repo: "shared", change_count: 4 }),
      checkout({
        kind: "agent",
        agent: "zoe",
        repo: "repo-b",
        change_count: 0,
      }),
      checkout({
        kind: "agent",
        agent: "atlas",
        repo: "repo-a",
        change_count: 0,
      }),
      checkout({
        kind: "agent",
        agent: "empty",
        repo: "repo-a",
        change_count: 0,
      }),
      checkout({
        kind: "agent",
        agent: "broken",
        repo: "repo-a",
        change_count: 0,
        status_error: true,
      }),
    ];
    const atlasKey = checkoutRefKey(checkoutRefFromCheckout(checkouts[2]!));
    const emptyKey = checkoutRefKey(checkoutRefFromCheckout(checkouts[3]!));
    const repoKey = checkoutRefKey(checkoutRefFromCheckout(checkouts[0]!));

    const groups = buildBranchChangeGroups(checkouts, {
      [atlasKey]: [
        diffFile({
          path: "zeta.ts",
          status: "M",
          additions: 3,
          deletions: 1,
        }),
        diffFile({
          path: "src/new-name.ts",
          status: "R",
          old_path: "src/old-name.ts",
          additions: 5,
          deletions: 2,
        }),
      ],
      [emptyKey]: [],
      [repoKey]: [diffFile({ path: "shared.ts", status: "A" })],
    });

    expect(groups.map((group) => group.label)).toEqual([
      "atlas · repo-a · 2",
      "zoe · repo-b · 0",
    ]);
    expect(groups[0]).toMatchObject({
      loaded: true,
      changeCount: 2,
      items: [
        {
          path: "src/new-name.ts",
          name: "new-name.ts",
          parentPath: "src",
          status: { kind: "renamed", label: "Renamed" },
          additions: 5,
          deletions: 2,
        },
        {
          path: "zeta.ts",
          name: "zeta.ts",
          parentPath: "",
          status: { kind: "modified", label: "Modified" },
          additions: 3,
          deletions: 1,
        },
      ],
    });
    expect(groups[1]).toMatchObject({
      loaded: false,
      changeCount: 0,
      items: [],
    });
  });

  it("builds task groups from stack node diffs", () => {
    const stacks: WorkspaceStack[] = [
      {
        id: "epic:E",
        repo: "loomcli",
        root_base: "main",
        nodes: [
          {
            task_id: "T1",
            output_branch: "task/T1",
            base_ref: "main",
            position: 0,
          },
          {
            task_id: "missing-base",
            output_branch: "task/missing-base",
            position: 1,
          },
          {
            task_id: "empty",
            output_branch: "task/empty",
            base_ref: "task/T1",
            position: 2,
          },
          {
            task_id: "failed",
            output_branch: "task/failed",
            base_ref: "task/T1",
            position: 3,
          },
        ],
      },
    ];

    const groups = buildTaskChangeGroups(stacks, {
      "epic:E/T1": [
        diffFile({
          path: "src/task.ts",
          status: "A",
          additions: 2,
          deletions: 0,
        }),
      ],
      "epic:E/empty": [],
      "epic:E/failed": null,
    });

    expect(groups.map((group) => group.label)).toEqual([
      "T1 · loomcli · 1",
      "failed · loomcli · 0",
    ]);
    expect(groups[0]).toMatchObject({
      id: "epic:E/T1",
      ref: { scope: "repo", target: "loomcli" },
      loaded: true,
      changeCount: 1,
      diffFrom: "main",
      diffTo: "task/T1",
      diffTitle: "T1",
      items: [
        {
          path: "src/task.ts",
          name: "task.ts",
          parentPath: "src",
          status: { kind: "new", label: "New" },
          additions: 2,
          deletions: 0,
        },
      ],
    });
    expect(groups[1]).toMatchObject({
      id: "epic:E/failed",
      loaded: true,
      unavailable: true,
      items: [],
    });
  });
});
