import { describe, expect, it } from "vitest";

import type { FileCheckout } from "@/api/workspace";
import { checkoutRefKey } from "@/utils/fileExplorerRefs";

import {
  buildChangeGroups,
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
});
