import { describe, expect, it } from "vitest";

import {
  agentCompactAvatarLabel,
  agentDisplayRoleLabel,
  agentDisplayTitle,
  derivePRReviewerDisplayName,
  prReviewRefFromAgent,
  resolvePRReviewRef,
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

  it("preserves dotted and underscored repo names in reviewer deep links", () => {
    expect(
      prReviewRefFromAgent({
        name: "review-octocat-hello-world-3a8e1ebe-pr-220",
        role: "pr-reviewer",
        repo: "hello.world",
      }),
    ).toBe("octocat/hello.world#220");
    expect(
      prReviewRefFromAgent({
        name: "review-octocat-hello-world-3a8e1ebe-pr-221",
        role: "pr-reviewer",
        repo: "hello_world",
      }),
    ).toBe("octocat/hello_world#221");
  });

  describe("resolvePRReviewRef", () => {
    const liveReviewer = {
      name: "review-tysonthomas9-loomcli-3a8e1ebe-pr-220",
      role: "pr-reviewer",
      repo: "loomcli",
    };

    it("resolves from the live agent store when present", () => {
      expect(
        resolvePRReviewRef(
          "review-tysonthomas9-loomcli-3a8e1ebe-pr-220",
          [liveReviewer],
          [],
        ),
      ).toBe("tysonthomas9/loomcli#220");
    });

    it("falls back to a configured-but-not-running placeholder (role_name/repos)", () => {
      // Not in the live store; only present as a workspace-config agent, which
      // carries role_name/repos rather than the normalized role/repo.
      expect(
        resolvePRReviewRef(
          "review-tysonthomas9-loomcli-3a8e1ebe-pr-247",
          [],
          [
            {
              name: "review-tysonthomas9-loomcli-3a8e1ebe-pr-247",
              role_name: "pr-reviewer",
              repos: ["loomcli"],
            },
          ],
        ),
      ).toBe("tysonthomas9/loomcli#247");
    });

    it("prefers the live store over the config list", () => {
      expect(
        resolvePRReviewRef(
          "review-tysonthomas9-loomcli-3a8e1ebe-pr-220",
          [liveReviewer],
          [{ name: "unrelated", role_name: "pr-reviewer", repos: ["other"] }],
        ),
      ).toBe("tysonthomas9/loomcli#220");
    });

    it("returns null when the name is in neither list", () => {
      expect(resolvePRReviewRef("ghost-agent", [liveReviewer], [])).toBeNull();
    });

    it("returns null for a non-reviewer config agent", () => {
      expect(
        resolvePRReviewRef(
          "local-coder",
          [],
          [{ name: "local-coder", role_name: "task", repos: ["loomcli"] }],
        ),
      ).toBeNull();
    });
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
