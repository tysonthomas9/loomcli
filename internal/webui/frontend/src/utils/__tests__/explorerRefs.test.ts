import { describe, expect, it } from "vitest";

import { checkoutRefKey } from "../fileExplorerRefs";
import {
  asCheckoutRef,
  checkoutExplorerRef,
  explorerRefKey,
  skillsExplorerRef,
  tabIdentityKey,
} from "../explorerRefs";

describe("explorerRefs", () => {
  it("keeps every checkout key byte-identical", () => {
    const checkout = {
      scope: "agent" as const,
      target: "skills",
      repo: "loomcli",
    };
    expect(explorerRefKey(checkoutExplorerRef(checkout))).toBe(
      checkoutRefKey(checkout),
    );
  });

  it("keeps skills identities disjoint from checkouts named skills", () => {
    expect(
      explorerRefKey(skillsExplorerRef({ kind: "role", role: "reviewer" })),
    ).not.toBe(
      explorerRefKey(checkoutExplorerRef({ scope: "repo", target: "skills" })),
    );
  });

  it("bridges only checkout refs back to file transport identities", () => {
    const checkout = checkoutExplorerRef({ scope: "workspace" });
    expect(asCheckoutRef(checkout)).toEqual({ scope: "workspace" });
    expect(asCheckoutRef(skillsExplorerRef({ kind: "workspace" }))).toBeNull();
  });

  it("includes the skills scope group in tab identity", () => {
    const workspace = tabIdentityKey({
      ref: skillsExplorerRef({ kind: "workspace" }),
      path: "audit/SKILL.md",
    });
    const role = tabIdentityKey({
      ref: skillsExplorerRef({ kind: "role", role: "reviewer" }),
      path: "audit/SKILL.md",
    });
    expect(workspace).not.toBe(role);
  });
});
