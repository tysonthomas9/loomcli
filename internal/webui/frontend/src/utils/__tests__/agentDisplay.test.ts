import { describe, expect, it } from "vitest";

import {
  agentCompactAvatarLabel,
  agentDisplayRoleLabel,
  agentDisplayTitle,
  derivePRReviewerDisplayName,
  prReviewRefFromAgent,
} from "../agentDisplay";

describe("agentDisplay", () => {
  it("derives repo#number from hashed reviewer names", () => {
    expect(
      derivePRReviewerDisplayName(
        "review-tysonthomas9-loomcli-3a8e1ebe-pr-220",
      ),
    ).toBe("loomcli#220");
  });

  it("prefers API display_name and role_label", () => {
    expect(
      agentDisplayTitle({
        name: "review-tysonthomas9-loomcli-3a8e1ebe-pr-220",
        role: "pr-reviewer",
        display_name: "loomcli#220",
      }),
    ).toBe("loomcli#220");
    expect(
      agentDisplayRoleLabel({
        role: "pr-reviewer",
        role_label: "Review",
      }),
    ).toBe("Review");
  });

  it("falls back to derived title and Review role for pr-reviewer", () => {
    expect(
      agentDisplayTitle({
        name: "review-tysonthomas9-loomcli-3a8e1ebe-pr-220",
        role: "pr-reviewer",
      }),
    ).toBe("loomcli#220");
    expect(agentDisplayRoleLabel({ role: "pr-reviewer" })).toBe("Review");
  });

  it("builds owner/repo#number review-pr refs for hashed reviewer agents", () => {
    expect(
      prReviewRefFromAgent({
        name: "review-tysonthomas9-loomcli-3a8e1ebe-pr-220",
        role: "pr-reviewer",
        display_name: "loomcli#220",
      }),
    ).toBe("tysonthomas9/loomcli#220");
    expect(
      prReviewRefFromAgent({
        name: "codex-coder",
        role: "task",
      }),
    ).toBeNull();
  });

  it("uses #N as the compact avatar label for PR reviewers", () => {
    expect(
      agentCompactAvatarLabel({
        name: "review-tysonthomas9-loomcli-3a8e1ebe-pr-220",
        role: "pr-reviewer",
        display_name: "loomcli#220",
      }),
    ).toBe("#220");
    expect(
      agentCompactAvatarLabel({
        name: "review-tysonthomas9-loomcli-3a8e1ebe-pr-220",
        role: "pr-reviewer",
      }),
    ).toBe("#220");
    expect(
      agentCompactAvatarLabel({
        name: "local-coder",
        role: "task",
      }),
    ).toBe("");
  });
});
