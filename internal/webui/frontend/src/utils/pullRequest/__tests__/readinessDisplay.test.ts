import { describe, expect, it } from "vitest";

import type { PullRequestReadinessView } from "@/api/workspace/pullRequests";
import {
  formatAgeSeconds,
  formatObservedAtLine,
  readinessDisplay,
  summarizeMergePreview,
} from "../readinessDisplay";

function view(
  overrides: Partial<PullRequestReadinessView>,
): PullRequestReadinessView {
  return {
    pr_key: "github:octocat/hello#7",
    freshness: "fresh",
    age_seconds: 12,
    current_verdict: "ready",
    current_reasons: [],
    ...overrides,
  };
}

describe("readinessDisplay", () => {
  it("labels fresh ready as Ready", () => {
    const d = readinessDisplay(view({}));
    expect(d.key).toBe("ready");
    expect(d.label).toBe("Ready");
    expect(d.isHistorical).toBe(false);
  });

  it("never labels stale evidence as Ready", () => {
    const d = readinessDisplay(
      view({
        freshness: "stale",
        age_seconds: 900,
        current_verdict: "unknown",
        current_reasons: ["readiness_aging"],
        snapshot: {
          pr_key: "github:octocat/hello#7",
          head_sha: "abc",
          head_ref: "feat",
          base_ref: "main",
          base_sha: "def",
          observed_at: "2026-09-24T12:00:00Z",
          facts: {
            lifecycle: { status: "known", value: "open" },
            conflicts: { status: "known", value: "none" },
            merge_state: { status: "known", value: "clean" },
            review: { status: "known", value: "approved" },
            required_checks: { status: "known", value: "passing" },
            required_check_counts: {
              passed: 1,
              pending: 0,
              failed: 0,
              total: 1,
            },
            optional_checks: { status: "known", value: "none" },
            optional_check_counts: {
              passed: 0,
              pending: 0,
              failed: 0,
              total: 0,
            },
            queue: { status: "known", value: "not_queued" },
          },
          verdict: "ready",
          reasons: [],
          fingerprint: "fp",
        },
      }),
    );
    expect(d.key).toBe("stale");
    expect(d.label).not.toMatch(/^Ready$/i);
    expect(d.label).toMatch(/Stale/i);
    expect(d.isHistorical).toBe(true);
  });

  it("never labels aging ready as currently Ready", () => {
    const d = readinessDisplay(
      view({
        freshness: "aging",
        current_verdict: "ready",
        age_seconds: 70,
      }),
    );
    expect(d.label).not.toBe("Ready");
    expect(d.key).toBe("unknown");
  });

  it("surfaces rate limit and timeout honestly", () => {
    expect(
      readinessDisplay(
        view({
          freshness: "unknown",
          current_verdict: "unknown",
          last_error: {
            code: "rate_limited",
            retry_after_s: 30,
            at: "2026-09-24T12:00:00Z",
          },
        }),
      ).label,
    ).toBe("Rate limited");

    expect(
      readinessDisplay(
        view({
          freshness: "unknown",
          current_verdict: "unknown",
          last_error: { code: "timeout", at: "2026-09-24T12:00:00Z" },
        }),
      ).label,
    ).toBe("Timed out");
  });
});

describe("formatObservedAtLine", () => {
  it("marks stale observations as not current", () => {
    const line = formatObservedAtLine(
      readinessDisplay(
        view({
          freshness: "stale",
          age_seconds: 600,
          current_verdict: "unknown",
          snapshot: {
            pr_key: "github:octocat/hello#7",
            head_sha: "a",
            head_ref: "f",
            base_ref: "main",
            base_sha: "b",
            observed_at: "2026-09-24T12:00:00Z",
            facts: {
              lifecycle: { status: "known" },
              conflicts: { status: "known" },
              merge_state: { status: "known" },
              review: { status: "known" },
              required_checks: { status: "known" },
              required_check_counts: {
                passed: 0,
                pending: 0,
                failed: 0,
                total: 0,
              },
              optional_checks: { status: "known" },
              optional_check_counts: {
                passed: 0,
                pending: 0,
                failed: 0,
                total: 0,
              },
              queue: { status: "known" },
            },
            verdict: "ready",
            reasons: [],
            fingerprint: "fp",
          },
        }),
      ),
    );
    expect(line).toMatch(/Not current/i);
  });
});

describe("formatAgeSeconds", () => {
  it("formats short ages", () => {
    expect(formatAgeSeconds(12)).toBe("12s ago");
    expect(formatAgeSeconds(120)).toBe("2m ago");
  });
});

describe("summarizeMergePreview", () => {
  it("reports ready_count and first blocker without merge action", () => {
    const summary = summarizeMergePreview({
      ready_count: 1,
      fingerprint: "fp",
      members: [
        {
          index: 0,
          position: "in_prefix",
          reasons: [],
          readiness: view({ pr_key: "github:a/b#1" }),
        },
        {
          index: 1,
          position: "stop",
          reasons: ["checks_failing"],
          readiness: view({
            pr_key: "github:a/b#2",
            current_verdict: "blocked",
            freshness: "fresh",
          }),
        },
      ],
      stopped_by: {
        pr_key: "github:a/b#2",
        verdict: "blocked",
        reasons: ["checks_failing"],
      },
    });
    expect(summary.readyCount).toBe(1);
    expect(summary.stopLabel).toMatch(/First blocker/i);
    expect(summary.cannotEstablish).toBe(false);
  });

  it("flags cannot-establish for unknown stop", () => {
    const summary = summarizeMergePreview({
      ready_count: 0,
      fingerprint: "fp",
      members: [],
      stopped_by: {
        pr_key: "github:a/b#2",
        verdict: "unknown",
        reasons: ["rate_limited"],
      },
    });
    expect(summary.cannotEstablish).toBe(true);
    expect(summary.stopLabel).toMatch(/Cannot establish/i);
  });
});
