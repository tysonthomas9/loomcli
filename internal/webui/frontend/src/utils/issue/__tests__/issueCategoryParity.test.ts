import { describe, it, expect } from "vitest";
import { getOpenStatus, hasNeedsRevision } from "../issueCategory";
import fixture from "../../../../testdata/plan_status_parity_cases.json";

describe("issueCategory parity with Go taskfilter.go", () => {
  for (const c of fixture) {
    it(`case: ${c.id}`, () => {
      const issue = {
        design: c.issue.design,
        has_design: c.issue.has_design,
        design_artifact_id: c.issue.design_artifact_id,
        labels: c.issue.labels,
      };
      expect(hasNeedsRevision(issue)).toBe(c.expected.has_needs_revision);
      expect(getOpenStatus(issue)).toBe(c.expected.ts_open_status);
      const tsIsReady = getOpenStatus(issue) === "ready";
      expect(tsIsReady).toBe(c.expected.ready_to_implement);
    });
  }
});
