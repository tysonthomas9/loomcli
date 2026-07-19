import { describe, expect, it } from "vitest";

import { getTruncationBannerText } from "../TracesView";

describe("getTruncationBannerText", () => {
  it("returns the mandated newest-of-total banner when results are truncated", () => {
    expect(getTruncationBannerText(275, 200, 200)).toBe(
      "showing newest 200 of 275 in this range — narrow the time range",
    );
  });

  it("returns null when all matching sessions are shown", () => {
    expect(getTruncationBannerText(12, 12, 200)).toBeNull();
  });
});
