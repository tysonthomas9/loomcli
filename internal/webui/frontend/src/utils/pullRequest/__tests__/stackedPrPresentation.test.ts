import { describe, expect, it } from "vitest";

import type { PullRequestReadinessView } from "@/api/workspace/pullRequests";
import {
  changeSummary,
  checkCountLabel,
  initialsFor,
  relativeAge,
  repoBasename,
  requirementLines,
  toneForKey,
} from "../stackedPrPresentation";

function view(
  overrides: Partial<PullRequestReadinessView> = {},
  facts: Record<string, unknown> = {},
): PullRequestReadinessView {
  const known = (value: string) => ({ status: "known" as const, value });
  return {
    pr_key: "github:acme/loomcli#3",
    freshness: "fresh",
    age_seconds: 10,
    current_verdict: "ready",
    current_reasons: [],
    snapshot: {
      pr_key: "github:acme/loomcli#3",
      head_sha: "a",
      head_ref: "feat/x",
      base_ref: "main",
      base_sha: "b",
      observed_at: "2026-09-26T00:00:00Z",
      verdict: "ready",
      reasons: [],
      fingerprint: "f",
      facts: {
        lifecycle: known("open"),
        conflicts: known("none"),
        merge_state: known("clean"),
        review: known("approved"),
        required_checks: known("passing"),
        required_check_counts: { passed: 8, pending: 0, failed: 0, total: 8 },
        optional_checks: known("none"),
        optional_check_counts: { passed: 0, pending: 0, failed: 0, total: 0 },
        queue: known("not_queued"),
        ...facts,
      },
    },
    ...overrides,
  } as PullRequestReadinessView;
}

describe("requirementLines", () => {
  it("reports fresh, known facts as met", () => {
    const lines = requirementLines(view());
    expect(lines.map((l) => l.state)).toEqual(["met", "met", "met"]);
    expect(lines[1]?.label).toBe("All 8 checks passing");
  });

  it("never reports stale evidence as met", () => {
    const lines = requirementLines(view({ freshness: "stale" }));
    expect(lines.every((l) => l.state !== "met")).toBe(true);
    expect(lines[0]?.detail).toMatch(/not current/);
  });

  it("keeps failing facts failing even when not current", () => {
    const lines = requirementLines(
      view(
        { freshness: "aging" },
        { conflicts: { status: "known", value: "conflicting" } },
      ),
    );
    expect(lines.find((l) => l.id === "conflicts")?.state).toBe("failing");
  });

  it("is unknown when nothing was observed", () => {
    expect(
      requirementLines(undefined).every((l) => l.state === "unknown"),
    ).toBe(true);
  });

  it("treats computing facts as unknown", () => {
    const lines = requirementLines(
      view({}, { review: { status: "computing" } }),
    );
    expect(lines.find((l) => l.id === "review")?.state).toBe("unknown");
  });
});

describe("display helpers", () => {
  it("maps readiness keys to reference tones", () => {
    expect(toneForKey("ready")).toBe("ready");
    expect(toneForKey("waiting")).toBe("review");
    expect(toneForKey("stale")).toBe("unknown");
    expect(toneForKey("merged")).toBe("merged");
  });

  it("derives initials, basenames, ages, and counts", () => {
    expect(initialsFor("tysonthomas9")).toBe("TY");
    expect(initialsFor("sonal.bangera@example.com")).toBe("SB");
    expect(repoBasename("acme/loomcli")).toBe("loomcli");
    expect(
      relativeAge("2026-09-26T00:00:00Z", Date.parse("2026-09-26T00:12:00Z")),
    ).toBe("12m ago");
    expect(checkCountLabel(view())).toBe("8/8");
    expect(changeSummary(undefined)).toBeNull();
    expect(
      changeSummary({
        number: 1,
        title: "t",
        url: "",
        state: "OPEN",
        is_draft: false,
        head_ref_name: "",
        base_ref_name: "",
        repo_name: "a/b",
        changed_files: 1,
        additions: 8,
        deletions: 2,
      })?.files,
    ).toBe("1 file");
  });
});
